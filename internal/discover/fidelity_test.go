package discover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

const fidelityDir = "testdata/fidelity"

const (
	appCwd   = "/home/user/src/app"
	cobraCwd = "/home/user/src/cobra"
)

type transcript struct {
	b   bytes.Buffer
	cwd string
}

func (t *transcript) line(v map[string]any) {
	v["cwd"] = t.cwd
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	t.b.Write(b)
	t.b.WriteByte('\n')
}

func (t *transcript) use(id, name string, input map[string]any) {
	t.line(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}})
}

func (t *transcript) bash(id, command string) { t.use(id, "Bash", map[string]any{"command": command}) }

func (t *transcript) result(id, content string, isError bool, tur map[string]any) {
	v := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": isError}}}}
	if tur != nil {
		v["toolUseResult"] = tur
	}
	t.line(v)
}

func (t *transcript) ran(id, command, stdout, stderr string, isError bool) {
	t.bash(id, command)
	t.result(id, "(output)", isError, map[string]any{"stdout": stdout, "stderr": stderr, "interrupted": false})
}

func (t *transcript) other(id string) { t.use(id, "TodoWrite", map[string]any{"todos": []any{}}) }

func corpus(tb testing.TB, cat, name string) string {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", cat, name+".txt"))
	if err != nil {
		tb.Fatal(err)
	}
	return string(b)
}

func grepOutput(files, perFile, first int, pattern string) string {
	var b strings.Builder
	for i := range files {
		n := perFile
		if i == 0 {
			n = first
		}
		for j := range n {
			fmt.Fprintf(&b, "src/pkg%d/file%02d.go:%d:\tvalue := %s(x, %d) // TODO handle\n", i%5, i, 10+j*7, pattern, j)
		}
	}
	return b.String()
}

func manyFiles(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "src/mod%d/unit%03d.go:%d:\tregisterHandler(mux, \"/v1/item%d\")\n", i/50, i, 12+i, i)
	}
	return b.String()
}

func verboseGoTest(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "=== RUN   TestCase%04d\n--- PASS: TestCase%04d (0.00s)\n", i, i)
	}
	b.WriteString("=== RUN   TestBroken\n    broken_test.go:42: want 3, got 4\n--- FAIL: TestBroken (0.00s)\nFAIL\nFAIL\texample.com/app\t0.321s\nFAIL\n")
	return b.String()
}

func fidelityScenarios(tb testing.TB) map[string]map[string][]byte {
	goFail := corpus(tb, "go", "go-test-fail")
	vet := corpus(tb, "go", "go-vet-findings")
	out := map[string]map[string][]byte{}
	add := func(scenario, file string, t *transcript) {
		if out[scenario] == nil {
			out[scenario] = map[string][]byte{}
		}
		out[scenario][file] = append([]byte(nil), t.b.Bytes()...)
	}

	t := &transcript{cwd: appCwd}
	t.ran("a1", "grep -rn computeThing src", grepOutput(30, 8, 20, "computeThing"), "", false)
	t.use("a2", "Read", map[string]any{"file_path": appCwd + "/src/pkg0/file00.go", "offset": 140, "limit": 10})
	add("a-grep-per-file-cap", "session.jsonl", t)

	t = &transcript{cwd: appCwd}
	t.ran("k1", "grep -rn registerHandler src", manyFiles(200), "", false)
	t.use("k2", "Read", map[string]any{"file_path": appCwd + "/src/mod3/unit170.go"})
	add("a-grep-file-cap", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("b1", "go test ./...", goFail, "", true)
	t.use("b2", "Edit", map[string]any{"file_path": cobraCwd + "/args_test.go", "old_string": "x", "new_string": "y"})
	add("b-go-test-edit", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("c1", "go test ./...", goFail, "", true)
	t.use("c2", "Read", map[string]any{"file_path": cobraCwd + "/args.go"})
	t.use("c3", "Read", map[string]any{"file_path": cobraCwd + "/README.md"})
	add("c-unrelated", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("d1", "go test ./...", goFail, "", true)
	t.other("d2")
	t.use("d3", "Glob", map[string]any{"pattern": "**/*.go"})
	t.use("d4", "Read", map[string]any{"file_path": cobraCwd + "/cobra.go"})
	t.ran("d5", "git status", "On branch main\nnothing to commit, working tree clean\n", "", false)
	t.use("d6", "Edit", map[string]any{"file_path": cobraCwd + "/args_test.go", "old_string": "x", "new_string": "y"})
	add("d-outside-window", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("w1", "go test ./...", goFail, "", true)
	t.other("w2")
	t.use("w3", "Glob", map[string]any{"pattern": "**/*.go"})
	t.use("w4", "Read", map[string]any{"file_path": cobraCwd + "/args_test.go"})
	add("d-window-edge", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("e1", "go test ./...", goFail, "", true)
	t.use("e2", "Edit", map[string]any{"file_path": cobraCwd + "/args_test.go", "old_string": "x", "new_string": "y"})
	add("e-resumed", "first.jsonl", t)
	t.use("e3", "Read", map[string]any{"file_path": cobraCwd + "/args_test.go"})
	add("e-resumed", "resumed.jsonl", t)

	t = &transcript{cwd: appCwd}
	preview := "<persisted-output>\nOutput too large (45.0KB). Full output saved to: /nonexistent/s1/tool-results/b1.txt\n\nPreview (first 2KB):\nok  \texample.com/app\t0.1s\n...\n</persisted-output>"
	t.bash("f1", "go test ./...")
	t.result("f1", preview, false, nil)
	t.bash("f2", "cat build.log")
	t.result("f2", "Output too large (80.0KB). Full output saved to: /nonexistent/s1/tool-results/b2.txt", false, nil)
	big := verboseGoTest(700)
	t.bash("f3", "go test -v ./...")
	t.result("f3", preview, true, map[string]any{"stdout": big, "stderr": "", "persistedOutputPath": "/nonexistent/s1/tool-results/b3.txt", "persistedOutputSize": len(big)})
	t.bash("f4", "go test -v ./...")
	t.result("f4", preview, true, map[string]any{"stdout": big[:30000], "stderr": "", "persistedOutputPath": "/nonexistent/s1/tool-results/b4.txt", "persistedOutputSize": len(big) + 50000})
	add("f-spills", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}

	t.ran("g1", "go test ./... 2>&1 | head -5", goFail, "", false)

	t.ran("g2", "go test ./... | head -5", "", goFail, false)

	t.ran("g3", "go vet ./... | tail -3", "", vet, false)
	t.ran("g4", "go test ./... 2>&1 | tail -3", "ok  \texample.com/a\t0.1s\nok  \texample.com/b\t0.2s\n", "", false)
	t.ran("g5", "go test ./... ; head -3 notes.txt", "ok  \texample.com/a\t0.1s\n", "", false)
	t.ran("g6", "go test ./... 2>&1 | head -c 100", "ok  \texample.com/a\t0.1s\n", "", false)

	t.ran("g7", "grep -rn computeThing src 2>&1 | head -40", grepOutput(12, 3, 3, "computeThing"), "", false)

	t.ran("g8", "grep -rn computeThing src |& tail -40", "grep: src/private: Permission denied\n"+grepOutput(12, 3, 3, "computeThing"), "", false)
	add("g-head-tail", "session.jsonl", t)

	t = &transcript{cwd: appCwd}
	panicOut := verboseGoTest(40) + "panic: boom [recovered]\n\ngoroutine 7 [running]:\n" +
		"example.com/app/pkg.Do()\n\t" + appCwd + "/pkg/util.go:20 +0x1d\n" +
		"    util.go:31: unexpected state\n"
	t.ran("h1", "go test ./...", panicOut, "", true)
	t.use("h2", "Edit", map[string]any{"file_path": appCwd + "/pkg/util.go", "old_string": "x", "new_string": "y"})
	add("h-ambiguous", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("i1", "go test ./...", "--- FAIL: TestX (0.00s)\n    x_test.go:9: bad\n"+strings.Repeat("noise line for padding\n", 60)+"[lx: 307→29 lines (−71%) · full output: lx show 3]\n", "", true)
	t.use("i2", "Read", map[string]any{"file_path": cobraCwd + "/x_test.go"})
	add("i-lx-view", "session.jsonl", t)

	t = &transcript{cwd: "/home/user"}
	t.bash("j0", "cd src/cobra && go test ./...")
	t.result("j0", "(output)", true, map[string]any{"stdout": goFail, "stderr": ""})
	t.bash("j1", "cd src/cobra && sed -n '60,80p' args_test.go && sed -n '1,20p' command_test.go")
	t.result("j1", "...", false, map[string]any{"stdout": "...", "stderr": ""})
	add("j-bash-reads", "session.jsonl", t)

	t = &transcript{cwd: appCwd}
	first20 := strings.Join(strings.SplitAfter(grepOutput(30, 8, 20, "computeThing"), "\n")[:20], "")
	t.ran("s1", "grep -rn computeThing src 2>&1 | head -20", first20, "", false)
	t.use("s2", "Read", map[string]any{"file_path": appCwd + "/src/pkg0/file00.go", "offset": 140, "limit": 5})
	add("k-head-partial", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("l1", "go test ./... | head -5", "", goFail, false)
	t.use("l2", "Read", map[string]any{"file_path": cobraCwd + "/command_test.go", "offset": 2870, "limit": 20})
	add("l-head-cuts-view", "session.jsonl", t)

	t = &transcript{cwd: appCwd}
	t.ran("q1", "grep -rn computeThing src 2>&1 | head -40", grepOutput(12, 3, 3, "computeThing"), "", false)
	t.use("q2", "Read", map[string]any{"file_path": appCwd + "/src/pkg1/file11.go"})
	add("l-grep-head-cut", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("m1", "go test ./...", goFail, "", true)
	t.ran("m2", "go test -run TestMinimumNArgs_WithLessArgs .", "--- FAIL: TestMinimumNArgs_WithLessArgs (0.00s)\n    args_test.go:73: bad\nFAIL\n", "", true)
	t.use("m3", "Edit", map[string]any{"file_path": cobraCwd + "/args_test.go", "old_string": "x", "new_string": "y"})
	add("m-newest-output-wins", "session.jsonl", t)

	t = &transcript{cwd: cobraCwd}
	t.ran("n1", "go vet ./... && go test ./...", goFail, "", true)
	t.use("n2", "Edit", map[string]any{"file_path": cobraCwd + "/args_test.go", "old_string": "x", "new_string": "y"})
	add("n-two-commands", "session.jsonl", t)
	return out
}

func TestFidelityTranscriptsCurrent(t *testing.T) {
	update := os.Getenv("LX_UPDATE_GOLDEN") == "1"
	for scenario, files := range fidelityScenarios(t) {
		for name, want := range files {
			p := filepath.Join(fidelityDir, scenario, name)
			if update {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, want, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			got, err := os.ReadFile(p)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s is stale (LX_UPDATE_GOLDEN=1 regenerates it): %v", p, err)
			}
		}
	}
}

func TestFidelityScenarioIDsUnique(t *testing.T) {
	owner := map[string]string{}
	for scenario, files := range fidelityScenarios(t) {
		for _, data := range files {
			for _, ln := range bytes.Split(data, []byte("\n")) {
				var rec struct {
					Message struct {
						Content []struct {
							Type string `json:"type"`
							ID   string `json:"id"`
						} `json:"content"`
					} `json:"message"`
				}
				if len(ln) == 0 || json.Unmarshal(ln, &rec) != nil {
					continue
				}
				for _, b := range rec.Message.Content {
					if b.Type != "tool_use" {
						continue
					}
					if o, ok := owner[b.ID]; ok && o != scenario {
						t.Errorf("tool_use id %q is used by %s and %s", b.ID, o, scenario)
					}
					owner[b.ID] = scenario
				}
			}
		}
	}
}

func scanFid(t *testing.T, dir string, examples bool) Report {
	t.Helper()
	r, err := Scan(Options{Dirs: []string{dir}, Fidelity: true, Examples: examples})
	if err != nil {
		t.Fatal(err)
	}
	if r.ActedOn == nil {
		t.Fatal("no acted_on section with Fidelity")
	}
	return r
}

func TestFidelityScenarios(t *testing.T) {
	type want struct {
		refs, fileIn, locIn, fileHT, locHT, budget, filter, cut, ambiguous, unmeasured, lxViews int
	}
	for _, tc := range []struct {
		scenario string
		want     want
	}{
		{"a-grep-per-file-cap", want{refs: 1, fileIn: 1, fileHT: 1, locHT: 1, filter: 1}},

		{"a-grep-file-cap", want{refs: 1, fileHT: 1, locHT: 1, filter: 1}},

		{"b-go-test-edit", want{refs: 1, fileIn: 1, locIn: 1, fileHT: 1, locHT: 1}},

		{"c-unrelated", want{}},

		{"d-outside-window", want{}},
		{"d-window-edge", want{refs: 1, fileIn: 1, locIn: 1, fileHT: 1, locHT: 1}},

		{"e-resumed", want{refs: 1, fileIn: 1, locIn: 1, fileHT: 1, locHT: 1}},
		{"h-ambiguous", want{ambiguous: 1}},

		{"i-lx-view", want{unmeasured: 1, lxViews: 1}},

		{"j-bash-reads", want{refs: 1, fileIn: 1, locIn: 1, fileHT: 1, locHT: 1}},

		{"k-head-partial", want{unmeasured: 1}},

		{"l-head-cuts-view", want{}},

		{"l-grep-head-cut", want{}},

		{"m-newest-output-wins", want{}},

		{"n-two-commands", want{unmeasured: 1}},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			r := scanFid(t, filepath.Join(fidelityDir, tc.scenario), false)
			a := r.ActedOn
			got := want{a.Total.Refs, a.Total.FileInView, a.Total.LocInView, a.Total.FileInHT, a.Total.LocInHT,
				a.Total.Misses.Budget, a.Total.Misses.Filter, a.Total.Misses.Cut, a.Ambiguous, a.Unmeasured, a.LxViews}
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
			if a.Window != actWindow {
				t.Errorf("window = %d", a.Window)
			}
			if len(a.Examples) != 0 {
				t.Errorf("examples without Options.Examples: %+v", a.Examples)
			}
		})
	}
}

func TestFidelityUnmeasuredCauses(t *testing.T) {
	for scenario, want := range map[string]Unreplayable{
		"k-head-partial": {HeadTail: 1},
		"n-two-commands": {Several: 1},
		"i-lx-view":      {LxView: 1},
	} {
		if got := scanFid(t, filepath.Join(fidelityDir, scenario), false).ActedOn.UnmeasuredBy; got != want {
			t.Errorf("%s: %+v, want %+v", scenario, got, want)
		}
	}

	dir := t.TempDir()
	tr := &transcript{cwd: cobraCwd}
	tr.bash("us1", "go test ./...")
	tr.result("us1", "<persisted-output>\nOutput too large (45.0KB). Full output saved to: /gone/tool-results/x.txt\n\n"+
		"Preview (first 2KB):\n--- FAIL: TestX (0.00s)\n    x_test.go:9: bad\n</persisted-output>", true, nil)
	tr.use("us2", "Edit", map[string]any{"file_path": cobraCwd + "/x_test.go", "old_string": "x", "new_string": "y"})
	writeTranscript(t, dir, "s.jsonl", tr)
	a := scanFid(t, dir, false).ActedOn
	if a.UnmeasuredBy != (Unreplayable{Spill: 1}) || a.Unmeasured != 1 || a.Total.Refs != 0 {
		t.Errorf("spill: %+v", a)
	}
}

func TestFidelityResumedCountsOnce(t *testing.T) {
	r := scanFid(t, filepath.Join(fidelityDir, "e-resumed"), false)
	if r.Files != 2 || r.BashCalls != 1 || r.Candidates != 1 {
		t.Errorf("files=%d bash=%d candidates=%d", r.Files, r.BashCalls, r.Candidates)
	}
}

func TestFidelityExamples(t *testing.T) {
	r := scanFid(t, filepath.Join(fidelityDir, "a-grep-file-cap"), true)
	ex := r.ActedOn.Examples
	if len(ex) != 1 || ex[0] != (ActedMiss{Command: "grep", Location: "src/mod3/unit170.go:182", Reason: "filter"}) {
		t.Fatalf("examples = %+v", ex)
	}
	var b bytes.Buffer
	r.Text(&b)
	if !strings.Contains(b.String(), "  grep  src/mod3/unit170.go:182  (filter)\n") {
		t.Errorf("text lacks the example:\n%s", b.String())
	}
	r = scanFid(t, filepath.Join(fidelityDir, "a-grep-per-file-cap"), true)
	if ex := r.ActedOn.Examples; len(ex) != 1 || !ex[0].FileInView || ex[0].Location != "src/pkg0/file00.go:143" {
		t.Errorf("examples = %+v", ex)
	}
}

func TestSpillCounters(t *testing.T) {
	r, err := Scan(Options{Dirs: []string{filepath.Join(fidelityDir, "f-spills")}})
	if err != nil {
		t.Fatal(err)
	}
	if r.HostSpills != 4 || r.HostSpillsRewritable != 1 || r.HostSpillsAvoided != 1 {
		t.Errorf("spills=%d rewritable=%d avoided=%d", r.HostSpills, r.HostSpillsRewritable, r.HostSpillsAvoided)
	}
	if r.Candidates != 3 || r.Measured != 4 {
		t.Errorf("candidates=%d measured=%d", r.Candidates, r.Measured)
	}
}

func TestSliceCounters(t *testing.T) {
	r, err := Scan(Options{Dirs: []string{filepath.Join(fidelityDir, "g-head-tail")}})
	if err != nil {
		t.Fatal(err)
	}

	if u := statMap(r.Unsupported); r.Candidates != 6 || u["go test"].Count != 1 || u["go vet"].Count != 1 {
		t.Errorf("candidates = %d, not rewritten = %+v; want 6, and g2 (go test) and g3 (go vet)", r.Candidates, r.Unsupported)
	}
	got := [5]int{r.Sliced, r.SlicedReplayable, r.SlicedViewCut, r.SlicedReceiptLost, r.SlicedErrorsCut}
	if want := [5]int{5, 3, 0, 0, 0}; got != want {
		t.Errorf("sliced, replayable, view cut, receipt lost, errors cut = %v, want %v", got, want)
	}
}

func TestSliceHeadOfLongView(t *testing.T) {
	goFail := corpus(t, "go", "go-test-fail")
	hits := grepOutput(12, 3, 3, "computeThing")
	res := engine.Process(&engine.Context{Argv: []string{"grep", "-rn", "computeThing", "src"}, Cwd: appCwd}, hits, engine.Options{})
	if lineCount(hits) != 36 || res.OutLines != 59 || !res.Lossy {
		t.Fatalf("raw %d lines, view %d lines (lossy %v); the fixture assumes 36 and 59", lineCount(hits), res.OutLines, res.Lossy)
	}
	dir := t.TempDir()
	tr := &transcript{cwd: appCwd}
	tr.ran("g2", "go test ./... | head -5", "", goFail, false)
	tr.ran("g7", "grep -rn computeThing src 2>&1 | head -40", hits, "", false)
	writeTranscript(t, dir, "s.jsonl", tr)
	r, err := Scan(Options{Dirs: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if st := statMap(r.Unsupported)["go test"]; r.Candidates != 1 || st.Count != 1 {
		t.Errorf("candidates = %d, go test not rewritten %d times; want the grep only", r.Candidates, st.Count)
	}
	if r.Sliced != 1 || r.SlicedReplayable != 1 || r.SlicedViewCut != 0 || r.SlicedReceiptLost != 0 || r.SlicedErrorsCut != 0 {
		t.Errorf("%+v", r)
	}
	fitted := engine.Process(&engine.Context{Argv: []string{"grep", "-rn", "computeThing", "src"}, Cwd: appCwd}, hits, engine.Options{MaxLines: 40})
	if fitted.Output != strings.TrimRight(hits, "\n") || fitted.Lossy {
		t.Errorf("lx --fit 40 must print the 36 hits as grep did:\n%s", fitted.Output)
	}
}

func writeTranscript(t *testing.T, dir, name string, tr *transcript) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, tr.b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseStage(t *testing.T) {
	for in, want := range map[string]string{
		"head":              "head 10 ok",
		"head -5":           "head 5 ok",
		"head -n 7":         "head 7 ok",
		"head -n7":          "head 7 ok",
		"head --lines=12":   "head 12 ok",
		"head --lines 12":   "head 12 ok",
		"tail -n 3":         "tail 3 ok",
		"tail -q -n 3":      "tail 3 ok",
		"tail -n +3":        "tail 10 unmodeled",
		"head -n -3":        "head 10 unmodeled",
		"head -c 100":       "head 10 unmodeled",
		"head -c100":        "head 10 unmodeled",
		"tail -r":           "tail 10 unmodeled",
		"head -n 1k":        "head 10 unmodeled",
		"cat":               "cat",
		"cat -n":            "cat",
		"head notes.txt":    "file",
		"head -3 notes.txt": "file",
		"head -- x":         "file",
		"cat notes.txt":     "file",
		"grep x":            "file",
	} {
		st, ok := parseStage(strings.Fields(in))
		got := "file"
		switch {
		case ok && st.cat:
			got = "cat"
		case ok:
			kind := "head"
			if st.tail {
				kind = "tail"
			}
			mode := "ok"
			if !st.ok {
				mode = "unmodeled"
			}
			got = fmt.Sprintf("%s %d %s", kind, st.n, mode)
		}
		if got != want {
			t.Errorf("parseStage(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestTouchedPaths(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	for _, tc := range []struct {
		name, input string
		want        []touch
	}{
		{"Read", `{"file_path":"/r/a.go"}`, []touch{{path: "/r/a.go"}}},
		{"Read", `{"file_path":"/r/a.go","offset":100,"limit":20}`, []touch{{path: "/r/a.go", from: 99, to: 120}}},
		{"Read", `{"file_path":"/r/a.go","offset":"100"}`, []touch{{path: "/r/a.go", from: 99, to: 2100}}},
		{"Read", `{"file_path":"rel/a.go"}`, []touch{{path: "/cwd/rel/a.go"}}},
		{"Edit", `{"file_path":"/r/./x/../a.go"}`, []touch{{path: "/r/a.go"}}},
		{"MultiEdit", `{"file_path":"/r/a.go","edits":[]}`, []touch{{path: "/r/a.go"}}},
		{"Write", `{"file_path":"/r/a.go","content":"x"}`, []touch{{path: "/r/a.go"}}},
		{"NotebookEdit", `{"notebook_path":"/r/n.ipynb"}`, []touch{{path: "/r/n.ipynb"}}},
		{"Grep", `{"pattern":"x","path":"/r/a.go"}`, []touch{{path: "/r/a.go"}}},
		{"Grep", `{"pattern":"x","path":"/r/src"}`, nil},
		{"Glob", `{"pattern":"*.go"}`, nil},
		{"TodoWrite", `{"todos":[]}`, nil},
		{"Read", `{"file_path":42}`, nil},
		{"Read", `not json`, nil},
	} {
		got := touchedPaths(tc.name, raw(tc.input), "/cwd")
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s %s = %+v, want %+v", tc.name, tc.input, got, tc.want)
		}
	}
}

func TestBashPaths(t *testing.T) {
	for cmd, want := range map[string]string{
		"cat a.go b.txt":                           "[{/c/a.go 0 0} {/c/b.txt 0 0}]",
		"head -n 20 /abs/x.py":                     "[{/abs/x.py 0 0}]",
		"sed -n '60,80p' pkg/a.go":                 "[{/c/pkg/a.go 60 80}]",
		"sed -n 73p a.go":                          "[{/c/a.go 73 73}]",
		"sed -i 's/a/b/' a.go":                     "[{/c/a.go 0 0}]",
		"cd sub && cat a.go":                       "[{/c/sub/a.go 0 0}]",
		"cd /other && nl -ba a.go | sed -n '5,9p'": "[{/other/a.go 0 0}]",
		"wc -l *.go":                               "[]",
		"cat $F.go":                                "[]",
		"grep -n foo a.go":                         "[]",
		"go test ./...":                            "[]",
		"bat --style=plain README":                 "[]",
		"tail -f log.txt":                          "[{/c/log.txt 0 0}]",
		"cd - && cat a.go":                         "[{a.go 0 0}]",
		`cd "$D" && cat a.go`:                      "[{a.go 0 0}]",
		"cd ~/x && cat a.go ../b.go":               "[{a.go 0 0} {../b.go 0 0}]",
		"cd && cat a.go":                           "[{a.go 0 0}]",
		"pushd /x && cat a.go":                     "[{a.go 0 0}]",
		"cd /abs && cd sub && cat a.go":            "[{/abs/sub/a.go 0 0}]",
		"cd $D && cd /abs && cat a.go":             "[{/abs/a.go 0 0}]",
		"sed -n '80,60p' a.go":                     "[{/c/a.go 0 0}]",
		"cat ./a.go ../b.go":                       "[{/c/a.go 0 0} {/b.go 0 0}]",
		"head -3 a.go 2>/dev/null; cat \"q r.go\"": "[{/c/a.go 0 0} {/c/q r.go 0 0}]",
	} {
		in := inspect(cmd)
		got := fmt.Sprint(bashPaths(in, "/c"))
		if got == "[]" && want == "[]" {
			continue
		}
		if got != want {
			t.Errorf("bashPaths(%q) = %s, want %s", cmd, got, want)
		}
	}
}

func TestRefMatches(t *testing.T) {
	for _, tc := range []struct {
		ref, touched, cwd string
		line, from, to    int
		want              bool
	}{
		{ref: "args_test.go", touched: "/r/cobra/args_test.go", want: true},
		{ref: "cobra/args_test.go", touched: "/r/cobra/args_test.go", want: true},
		{ref: "args_test.go", touched: "/r/cobra/xargs_test.go", want: false},
		{ref: "/r/a.go", touched: "/r/a.go", want: true},
		{ref: "/r/a.go", touched: "/s/r/a.go", want: false},
		{ref: "../lib/a.go", touched: "/r/lib/a.go", cwd: "/r/app", want: true},
		{ref: "../lib/a.go", touched: "/r/lib/a.go", want: false},
		{ref: "a.go", touched: "a.go", want: true},
		{ref: "a.go", touched: "/r/a.go", line: 50, from: 40, to: 60, want: true},
		{ref: "a.go", touched: "/r/a.go", line: 70, from: 40, to: 60, want: false},
	} {
		r := &actRef{path: tc.ref, line: tc.line}
		if got := refMatches(r, touch{path: tc.touched, from: tc.from, to: tc.to}, tc.cwd); got != tc.want {
			t.Errorf("refMatches(%+v) = %v", tc, got)
		}
	}
}

func hasName(text, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i <= len(text)-len(name); {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		j += i
		k := j + len(name)
		if (j == 0 || !nameByte(text[j-1])) && (k == len(text) || !wordByte(text[k])) {
			return true
		}
		i = j + 1
	}
	return false
}

func TestNames(t *testing.T) {
	for _, tc := range []struct {
		text, name string
		want       bool
	}{
		{"pkg/a.go:12: bad", "a.go", true},
		{"data.go:12", "a.go", false},
		{"see a.gox", "a.go", false},
		{"a.go", "a.go", true},
		{"(a.go)", "a.go", true},
		{"x.a.go", "a.go", false},
		{"a.go-backup a.go:1", "a.go", true},
		{"a.go-backup", "a.go", false},
		{"a.go.orig", "a.go", true},
		{"end of a.go.", "a.go", true},
		{"", "a.go", false},
		{"anything", "", false},
	} {
		if got := names(tc.text)[tc.name]; got != tc.want {
			t.Errorf("names(%q)[%q] = %v", tc.text, tc.name, got)
		}
		if got := hasName(tc.text, tc.name); got != tc.want {
			t.Errorf("hasName(%q, %q) = %v", tc.text, tc.name, got)
		}
	}
}

func TestNamesMatchesReference(t *testing.T) {
	const alphabet = "ab./-_: \n(x1@"
	seed := uint32(7)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	for range 300 {
		var b strings.Builder
		for range next(40) {
			b.WriteByte(alphabet[next(len(alphabet))])
		}
		text := b.String()
		set := names(text)
		for i := 0; i < len(text); i++ {
			for j := i + 1; j <= len(text) && j-i <= 8; j++ {
				n := text[i:j]
				if strings.IndexFunc(n, func(r rune) bool { return r > 127 || !nameByte(byte(r)) }) >= 0 {
					continue
				}
				if set[n] != hasName(text, n) {
					t.Fatalf("names(%q)[%q] = %v, reference %v", text, n, set[n], hasName(text, n))
				}
			}
		}
	}
}

func TestIsLxView(t *testing.T) {
	for s, want := range map[string]bool{
		"x\n[lx: 307→29 lines (−71%) · full output: lx show 3]\n": true,
		"[lx: 1,204→38 lines (−94%)]":                             true,
		"[lx: 307→29 lines (−71%)]\nmore output":                  false,
		"plain output": false,
		"":             false,
	} {
		if got := isLxView(s); got != want {
			t.Errorf("isLxView(%q) = %v", s, got)
		}
	}
}

func TestRefsViewEqualsRaw(t *testing.T) {
	n := 0
	for _, c := range fixture.All(t) {
		clean := textutil.Clean(c.Raw)
		locs := engine.AppLocations(clean)
		refs := refsOf(locs, clean, "", clean, false)
		if len(refs) != len(locs) {
			t.Errorf("%s/%s: %d refs for %d locations", c.Category, c.Name, len(refs), len(locs))
		}
		for _, r := range refs {
			n++
			if !r.fileInView || !r.locInView || !r.fileInHT || !r.locInHT {
				t.Errorf("%s/%s: %+v not in an identical view", c.Category, c.Name, r)
			}
		}
	}
	if n < 100 {
		t.Errorf("only %d references across the corpus", n)
	}
}

func TestFidelityPrivacy(t *testing.T) {
	r := scanFid(t, fidelityDir, false)
	var text bytes.Buffer
	r.Text(&text)
	js, _ := json.Marshal(r)
	for _, leak := range []string{"computeThing", "registerHandler", "src", "pkg0", "file00", "unit170", "args_test",
		"command_test", "cobra", "/home", "user", "notes", "build.log", "README", "util.go", "x_test", ".go:", "nonexistent",
		"TestMinimum", "Expected", "requires", "TodoWrite", "Edit", "Read"} {
		if strings.Contains(text.String(), leak) || strings.Contains(string(js), leak) {
			t.Errorf("report leaks %q", leak)
		}
	}
	if !strings.Contains(text.String(), "--examples lists them") {
		t.Errorf("text should point at --examples:\n%s", text.String())
	}
}

func TestFidelityGolden(t *testing.T) {
	r := scanFid(t, fidelityDir, false)
	js, _ := json.MarshalIndent(r, "", "  ")
	golden(t, filepath.Join("testdata", "fidelity.golden.json"), string(js)+"\n")
	var b bytes.Buffer
	r.Text(&b)
	golden(t, filepath.Join("testdata", "fidelity.golden.txt"), b.String())

	p, err := Scan(Options{Dirs: []string{fixtures}})
	if err != nil {
		t.Fatal(err)
	}
	b.Reset()
	p.Text(&b)
	golden(t, filepath.Join("testdata", "projects.golden.txt"), b.String())
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv("LX_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (LX_UPDATE_GOLDEN=1 creates it): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("%s differs (LX_UPDATE_GOLDEN=1 to accept):\n%s", path, got)
	}
}

func TestNoFidelityByDefault(t *testing.T) {
	r, err := Scan(Options{Dirs: []string{fidelityDir}})
	if err != nil {
		t.Fatal(err)
	}
	if r.ActedOn != nil {
		t.Error("acted_on without Options.Fidelity")
	}
	js, _ := json.Marshal(r)
	if strings.Contains(string(js), "acted_on") {
		t.Error("acted_on in JSON without Options.Fidelity")
	}
}

func TestPersistedOutput(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	full := verboseGoTest(900)
	write := func(p, s string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write(filepath.Join(root, "proj", "sess", "tool-results", "ok.txt"), full)
	wrongDir := write(filepath.Join(root, "proj", "sess", "notes", "x.txt"), full)
	out := write(filepath.Join(outside, "tool-results", "secret.txt"), full)
	link := filepath.Join(root, "proj", "sess", "tool-results", "link.txt")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(root, "proj", "sess", "tool-results", "fifo.txt")
	haveFifo := runtime.GOOS != "windows" && mkfifo(fifo) == nil

	tr := &transcript{cwd: appCwd}
	spill := func(id, path string) {
		tr.bash(id, "go test -v ./...")
		tr.result(id, "<persisted-output>\nOutput too large (60.0KB). Full output saved to: "+path+"\n\nPreview (first 2KB):\n=== RUN\n</persisted-output>", true, nil)
	}
	spill("p1", good)
	spill("p2", wrongDir)
	spill("p3", out)
	spill("p4", link)
	spill("p5", "relative/tool-results/ok.txt")
	if haveFifo {
		spill("p6", fifo)
	}
	writeTranscript(t, filepath.Join(root, "proj"), "sess.jsonl", tr)

	s := &scanner{roots: resolvedRoots([]string{root})}
	if got, ok := s.readPersisted(good); !ok || got != full {
		t.Errorf("good file not read (ok=%v, %d bytes)", ok, len(got))
	}
	for _, p := range []string{wrongDir, out, link, fifo, "relative/tool-results/ok.txt", ""} {
		if _, ok := s.readPersisted(p); ok {
			t.Errorf("read %q", p)
		}
	}
	old := maxLine
	maxLine = len(full) - 1
	if _, ok := s.readPersisted(good); ok {
		t.Error("read a file over maxLine")
	}
	maxLine = old

	done := make(chan Report, 1)
	go func() {
		r, _ := Scan(Options{Dirs: []string{root}})
		done <- r
	}()
	var r Report
	select {
	case r = <-done:
	case <-timeAfter(10):
		t.Fatal("scan blocked on a persisted path")
	}
	spills := 5
	if haveFifo {
		spills = 6
	}
	if r.HostSpills != spills || r.HostSpillsRewritable != 1 || r.HostSpillsAvoided != 1 {
		t.Errorf("spills=%d rewritable=%d avoided=%d", r.HostSpills, r.HostSpillsRewritable, r.HostSpillsAvoided)
	}

	if st := statMap(r.Top)["go test"]; st.Tokens > 2000 {
		t.Errorf("go test measured %d tokens: the savings counted the persisted output", st.Tokens)
	}
}

func TestFidelityBudgetMiss(t *testing.T) {
	var b strings.Builder
	for i := range 1500 {
		fmt.Fprintf(&b, "--- FAIL: TestCase%04d (0.00s)\n    case%02d_test.go:%d: expected value %d to equal the reference output for this input\n", i, i%40, 100+i, i)
	}
	b.WriteString("FAIL\nFAIL\texample.com/app\t1.234s\nFAIL\n")
	dir := t.TempDir()
	tr := &transcript{cwd: appCwd}
	tr.ran("m1", "go test ./...", b.String(), "", true)
	tr.use("m2", "Read", map[string]any{"file_path": appCwd + "/case20_test.go", "offset": 795, "limit": 10})
	writeTranscript(t, dir, "s.jsonl", tr)
	r := scanFid(t, dir, true)
	a := r.ActedOn
	if a.Total.Refs != 1 || a.Total.FileInView != 1 || a.Total.LocInView != 0 || a.Total.Misses.Budget != 1 {
		t.Fatalf("%+v", a.Total)
	}
	if len(a.Examples) != 1 || a.Examples[0].Reason != "budget" || a.Examples[0].Location != "case20_test.go:800" {
		t.Errorf("examples = %+v", a.Examples)
	}
}

func TestScanBigFidelity(t *testing.T) {
	mb := 0
	if _, err := fmt.Sscan(os.Getenv("LX_DISCOVER_BIG_MB"), &mb); err != nil || mb <= 0 {
		t.Skip("set LX_DISCOVER_BIG_MB to run")
	}
	dense := os.Getenv("LX_DISCOVER_DENSE") == "1"
	dir := t.TempDir()
	goFail := corpus(t, "go", "go-test-fail")
	grep := grepOutput(30, 8, 20, "computeThing")
	listing := manyFiles(400)
	source := strings.Repeat("func handler(w http.ResponseWriter, r *http.Request) {\n\tif err := decode(r, &req); err != nil {\n\t\treturn\n\t}\n}\n", 300)
	prose := strings.Repeat("The failing assertion compares the formatted message with the expected one. ", 60)
	written, file, id := 0, 0, 0
	for written < mb<<20 {
		tr := &transcript{cwd: cobraCwd}
		for tr.b.Len() < 8<<20 {
			id++
			n := fmt.Sprint(id)
			k := id % 6
			if !dense && id%4 != 0 {
				k = 6 + id%3
			}
			switch k {
			case 0:
				tr.ran("t"+n, "go test ./...", goFail, "", true)
				tr.use("e"+n, "Edit", map[string]any{"file_path": cobraCwd + "/args_test.go", "old_string": "x", "new_string": "y"})
			case 1:
				tr.ran("t"+n, "grep -rn computeThing src", grep, "", false)
				tr.use("e"+n, "Read", map[string]any{"file_path": cobraCwd + "/src/pkg1/file01.go"})
			case 2:
				tr.ran("t"+n, "cat src/big.txt", listing, "", false)
			case 3:
				tr.ran("t"+n, "git status", "On branch main\nnothing to commit, working tree clean\n", "", false)
			case 4:
				tr.ran("t"+n, "go test ./... 2>&1 | tail -20", "ok  \texample.com/a\t0.1s\n", "", false)
			case 5:
				tr.use("e"+n, "Read", map[string]any{"file_path": cobraCwd + "/command.go", "offset": 10, "limit": 50})
			case 6:
				tr.use("r"+n, "Read", map[string]any{"file_path": cobraCwd + "/server.go"})
				tr.result("r"+n, source, false, map[string]any{"type": "text", "file": map[string]any{"filePath": cobraCwd + "/server.go", "content": source}})
			case 7:
				tr.line(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "text", "text": prose}}}})
			case 8:
				tr.ran("t"+n, "ls -la", "total 8\n-rw-r--r--  1 u  staff  120 Sep  1 10:00 go.mod\n", "", false)
			}
		}
		file++
		writeTranscript(t, dir, fmt.Sprintf("p%d/s%d.jsonl", file%7, file), tr)
		written += tr.b.Len()
	}
	start := timeNow()
	if _, err := Scan(Options{Dirs: []string{dir}}); err != nil {
		t.Fatal(err)
	}
	plain := timeSince(start)
	start = timeNow()
	r, err := Scan(Options{Dirs: []string{dir}, Fidelity: true})
	elapsed := timeSince(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d MB (dense %v), %d files, %d Bash calls, %d candidates, %d refs: %v with --fidelity, %v without",
		written>>20, dense, r.Files, r.BashCalls, r.Candidates, r.ActedOn.Total.Refs, elapsed, plain)
	if !dense && elapsed.Seconds() > 10 {
		t.Errorf("scan took %v", elapsed)
	}
}

func TestFidelityManyRefs(t *testing.T) {
	var out, files strings.Builder
	for i := range 40000 {
		fmt.Fprintf(&out, "src/d%d/f%05d.go:%d:\tneedle := %d\n", i%100, i, i%900+1, i)
	}
	for i := range 3000 {
		fmt.Fprintf(&files, " src/d%d/f%05d.go", i%100, i)
	}
	dir := t.TempDir()
	tr := &transcript{cwd: appCwd}
	tr.ran("n1", "grep -rn needle src", out.String(), "", false)
	for k := range 3 {
		tr.ran(fmt.Sprint("n", k+2), "wc -l"+files.String(), "", "", false)
	}
	writeTranscript(t, dir, "s.jsonl", tr)
	start := timeNow()
	r := scanFid(t, dir, false)
	if el := timeSince(start); el.Seconds() > 5*raceSlowdown {
		t.Errorf("scan took %v", el)
	}

	if r.ActedOn.Total.Refs != 3000 {
		t.Errorf("refs = %d", r.ActedOn.Total.Refs)
	}
}

func TestSpillMentionIsNotASpill(t *testing.T) {
	root := t.TempDir()
	saved := filepath.Join(root, "proj", "sess", "tool-results", "b9.txt")
	if err := os.MkdirAll(filepath.Dir(saved), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(saved, []byte(verboseGoTest(900)+"    other_test.go:12: boom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mention := func(i int) string {
		return fmt.Sprintf("internal/discover/spill.go:%d:\t// <persisted-output> Output too large (45.0KB). Full output saved to: %s\n", i, saved)
	}
	var grep strings.Builder
	for i := range 60 {
		grep.WriteString(mention(20 + i))
	}
	tr := &transcript{cwd: appCwd}

	tr.bash("sm1", "grep -rn 'Output too large' internal")
	tr.result("sm1", grep.String(), false, map[string]any{"stdout": grep.String(), "stderr": ""})
	tr.use("sm2", "Read", map[string]any{"file_path": appCwd + "/other_test.go"})
	tr.bash("sm3", "grep -rn persisted-output internal")
	tr.result("sm3", "Found it:\n"+mention(7)+mention(8), false, nil)
	writeTranscript(t, filepath.Join(root, "proj"), "sess.jsonl", tr)

	r := scanFid(t, root, false)
	if r.HostSpills != 0 || r.HostSpillsRewritable != 0 || r.HostSpillsAvoided != 0 {
		t.Errorf("spills=%d rewritable=%d avoided=%d, want 0", r.HostSpills, r.HostSpillsRewritable, r.HostSpillsAvoided)
	}
	if r.ActedOn.Total.Refs != 0 || r.ActedOn.Unmeasured != 0 {
		t.Errorf("the saved file of another command was read as the grep's output: %+v", r.ActedOn)
	}
	for text, want := range map[string]bool{
		"<persisted-output>\nOutput too large (45.0KB). Full output saved to: /x\n\nPreview:\n…\n</persisted-output>": true,
		"\n  <persisted-output>\n…":                           true,
		"Output too large (80.0KB). Full output saved to: /x": true,
		"x.go:1: // <persisted-output>":                       false,
		"Error: Output too large":                             false,
		"":                                                    false,
	} {
		if got := isSpill(text); got != want {
			t.Errorf("isSpill(%q) = %v", text, got)
		}
	}
	for text, want := range map[string]string{
		"<persisted-output>\nOutput too large (1KB). Full output saved to: /a/tool-results/b.txt\n\nPreview (first 2KB):\nFull output saved to: /evil\n": "/a/tool-results/b.txt",
		"<persisted-output>\nOutput too large (1KB).\n\nPreview:\nFull output saved to: /evil\n":                                                         "",
	} {
		if got := savedPath(text); got != want {
			t.Errorf("savedPath(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestNamesLongRun(t *testing.T) {
	start := timeNow()
	for _, s := range []string{strings.Repeat(".", 2<<20), strings.Repeat("a.", 1<<20), strings.Repeat("x@", 1<<20)} {
		set := names(s)
		for n := range set {
			if len(n) > maxName {
				t.Fatalf("names kept a %d-byte name", len(n))
			}
		}
	}
	if el := timeSince(start); el.Seconds() > 3*raceSlowdown {
		t.Errorf("names took %v", el)
	}

	long := strings.Repeat("n", maxName-3) + ".go"
	if !names("see " + long + ":12 here")[long] {
		t.Error("a maxName-byte name is not found")
	}
}

func TestFidelityLongDottedLine(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("--- FAIL: TestBroken (0.00s)\n    broken_test.go:42: want 3, got 4\n")
	raw.WriteString("    " + strings.Repeat("y", 300) + ".go:7: impossible name\n")
	for i := range 1000 {
		fmt.Fprintf(&raw, "=== RUN   TestCase%04d\n--- PASS: TestCase%04d (0.00s)\n", i, i)
	}
	raw.WriteString(strings.Repeat(".", 512<<10) + "\nFAIL\nFAIL\texample.com/app\t0.321s\nFAIL\n")
	dir := t.TempDir()
	tr := &transcript{cwd: appCwd}
	tr.ran("ld1", "go test ./...", raw.String(), "", true)
	tr.use("ld2", "Edit", map[string]any{"file_path": appCwd + "/broken_test.go", "old_string": "x", "new_string": "y"})
	writeTranscript(t, dir, "s.jsonl", tr)
	start := timeNow()
	r := scanFid(t, dir, false)
	if el := timeSince(start); el.Seconds() > 2*raceSlowdown {
		t.Errorf("scan took %v", el)
	}
	if a := r.ActedOn; a.Total.Refs != 1 || a.Total.LocInView != 1 {
		t.Errorf("%+v", a.Total)
	}
	for _, ref := range plainRefs(engine.AppLocations(raw.String())) {
		if len(filepath.Base(ref.path)) > maxName {
			t.Errorf("a reference with a %d-byte file name", len(filepath.Base(ref.path)))
		}
	}
}
