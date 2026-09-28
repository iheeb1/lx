package track

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var gainNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func day(d int) int64 { return gainNow.AddDate(0, 0, -d).Unix() }

func gainRecords() []Record {
	return []Record{
		{Time: day(2), Cmd: "go test", Filter: "go-test", Raw: 12000, Out: 900, Ms: 2100, Exit: 1, Lossy: true},
		{Time: day(2), Cmd: "go test", Filter: "go-test", Raw: 8000, Out: 600, Ms: 1900, Lossy: true},
		{Time: day(1), Cmd: "git log", Filter: "git-log", Raw: 40000, Out: 3000, Ms: 40, Lossy: true},
		{Time: day(0), Cmd: "git status", Filter: "git-status", Raw: 300, Out: 120, Ms: 10},
		{Time: day(1), Cmd: "go test", Kind: KindShow, Of: 7, Mode: "errors", Out: 450},
		{Time: day(0), Cmd: "git log", Kind: KindShow, Of: 9, Mode: "full", Out: 6200},
	}
}

func TestRecallsReduceNetNotSaved(t *testing.T) {
	recs := gainRecords()
	s := Summarize(recs, 7, gainNow)
	withoutRecalls := Summarize(recs[:4], 7, gainNow)
	if s.Commands != 4 || s.Condensed != 4 || s.Failures != 1 {
		t.Fatalf("recalls must not count as commands: %+v", s)
	}
	if s.Raw != withoutRecalls.Raw || s.Out != withoutRecalls.Out || s.Saved != withoutRecalls.Saved || s.Pct != withoutRecalls.Pct {
		t.Fatalf("recalls changed raw/out/saved: %+v vs %+v", s, withoutRecalls)
	}
	for i := range s.Daily {
		if s.Daily[i] != withoutRecalls.Daily[i] {
			t.Fatalf("recalls changed the daily series: %+v", s.Daily)
		}
	}
	if s.Recalls != 2 || s.RecallTokens != 6650 {
		t.Fatalf("recalls %d / %d tokens", s.Recalls, s.RecallTokens)
	}
	if s.Saved != 55680 || s.NetSaved != 55680-6650 {
		t.Fatalf("saved %d net %d", s.Saved, s.NetSaved)
	}
	var gl, gt *CmdStat
	for i := range s.ByCmd {
		switch s.ByCmd[i].Cmd {
		case "git log":
			gl = &s.ByCmd[i]
		case "go test":
			gt = &s.ByCmd[i]
		}
	}
	if gl == nil || gl.Count != 1 || gl.Recalls != 1 || gl.RecallTokens != 6200 || gl.Saved != 37000 {
		t.Fatalf("git log stat: %+v", gl)
	}
	if gt == nil || gt.Count != 2 || gt.Recalls != 1 || gt.AvgMs != 2000 {
		t.Fatalf("go test stat: %+v", gt)
	}
	b, _ := json.Marshal(s)
	for _, k := range []string{`"recalls":2`, `"recall_tokens":6650`, `"net_saved_tokens":49030`, `"saved_tokens":55680`} {
		if !bytes.Contains(b, []byte(k)) {
			t.Errorf("JSON lacks %s: %s", k, b)
		}
	}
}

func TestNegativeNetShowsNegative(t *testing.T) {
	recs := []Record{
		{Time: day(0), Cmd: "cat", Raw: 5000, Out: 1000, Lossy: true},
		{Time: day(0), Cmd: "cat", Kind: KindShow, Of: 3, Mode: "full", Out: 5200},
	}
	s := Summarize(recs, 3, gainNow)
	if s.NetSaved != -1200 || s.NetPct >= 0 {
		t.Fatalf("net %d (%.1f%%): not clamped, must be negative", s.NetSaved, s.NetPct)
	}
	var b bytes.Buffer
	s.Text(&b, 10)
	if !strings.Contains(b.String(), "  net saved                             -1200  (-24.0%)\n") {
		t.Fatalf("negative net not shown as negative:\n%s", b.String())
	}
}

func TestRecallOnlyCommand(t *testing.T) {
	s := Summarize([]Record{{Time: day(0), Cmd: "pytest", Kind: KindShow, Of: 1, Mode: "tail", Out: 80}}, 3, gainNow)
	if s.Commands != 0 || s.Recalls != 1 || len(s.ByCmd) != 1 || s.ByCmd[0].Count != 0 || s.ByCmd[0].AvgMs != 0 {
		t.Fatalf("%+v", s)
	}
	var b bytes.Buffer
	s.Text(&b, 10)
	if !strings.Contains(b.String(), "(1 recall)") || !strings.Contains(b.String(), "net saved") {
		t.Fatalf("recall-only report:\n%s", b.String())
	}
}

func TestOldHistoryLoads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LX_DATA_DIR", dir)
	t.Setenv("LX_TRACK", "")
	old := `{"t":1790000000,"cmd":"git status","filter":"git-status","raw":300,"out":120,"ms":10,"exit":0}
{"t":1790000100,"cmd":"go test","filter":"go-test","raw":9000,"out":700,"ms":1500,"exit":1,"lossy":true}
not json at all
`
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(Record{Time: 1790000200, Cmd: "go test", Kind: KindShow, Of: 4, Mode: "errors", Out: 321}); err != nil {
		t.Fatal(err)
	}
	recs, err := Load(time.Time{})
	if err != nil || len(recs) != 3 {
		t.Fatalf("load: %v, %d records", err, len(recs))
	}
	if recs[0].Kind != "" || recs[1].Kind != "" || !recs[1].Lossy || recs[2].Kind != KindShow || recs[2].Of != 4 || recs[2].Mode != "errors" {
		t.Fatalf("records: %+v", recs)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	last := strings.TrimSpace(string(b))
	last = last[strings.LastIndexByte(last, '\n')+1:]
	if last != `{"t":1790000200,"cmd":"go test","filter":"","raw":0,"out":321,"ms":0,"exit":0,"kind":"show","of":4,"mode":"errors"}` {
		t.Fatalf("recall line: %s", last)
	}

	r, _ := json.Marshal(Record{Time: 1, Cmd: "ls", Raw: 2, Out: 1})
	if string(r) != `{"t":1,"cmd":"ls","filter":"","raw":2,"out":1,"ms":0,"exit":0}` {
		t.Fatalf("run record: %s", r)
	}
	if st, err := os.Stat(filepath.Join(dir, "history.jsonl")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("history mode: %v %v", st.Mode(), err)
	}
}

func TestGainTextGolden(t *testing.T) {
	loc := time.Local
	time.Local = time.UTC
	defer func() { time.Local = loc }()

	var b bytes.Buffer
	Summarize(gainRecords(), 5, gainNow).Text(&b, 10)
	want := "lx gain — 4 commands through lx, 4 views condensed\n" +
		"\n" +
		"  tokens the agent would have read      60.3k\n" +
		"  tokens it actually read                4620\n" +
		"  saved                                 55.7k  (92.3%)\n" +
		"  read back with lx show                 6650  (2 recalls)\n" +
		"  net saved                             49.0k  (81.3%)\n" +
		"  [█████████████████████████████████░░░░░░░]\n" +
		"\n" +
		"  command                  runs        raw      saved  saved% recalls  \n" +
		"  git log                     1      40.0k      37.0k   92.5%       1  ████████████████████████\n" +
		"  go test                     2      20.0k      18.5k   92.5%       1  ████████████\n" +
		"  git status                  1        300        180   60.0%       0  \n" +
		"\n" +
		"  saved per day (last 5 days)\n" +
		"  ··▄█▁\n" +
		"  2026-09-22 2026-09-26\n"
	if got := b.String(); got != want {
		t.Errorf("gain text:\n%q\nwant:\n%q", got, want)
	}

	b.Reset()
	Summarize(gainRecords()[:4], 5, gainNow).Text(&b, 10)
	if strings.Contains(b.String(), "recall") || strings.Contains(b.String(), "net saved") {
		t.Errorf("no recalls, no recall lines:\n%s", b.String())
	}
}
