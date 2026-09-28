package engine

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/tokens"
)

func terms(ts ...string) *Focus {
	f := &Focus{}
	for _, t := range ts {
		f.Terms = append(f.Terms, FocusTerm{Text: t, Weight: 1})
	}
	return f
}

func TestFocusMatch(t *testing.T) {
	cases := []struct {
		focus *Focus
		line  string
		want  bool
	}{
		{terms("TestParseConfig"), "--- FAIL: TestParseConfig (0.00s)", true},
		{terms("TestParseConfig"), "--- FAIL: TestParseConfig/empty_file (0.00s)", true},
		{terms("TestParseConfig"), "--- FAIL: TestParseConfigEmpty (0.00s)", false},
		{terms("TestParseConfig"), "testparseconfig failed", false},
		{terms("parse_config"), "  File \"x.py\", line 3, in parse_config", true},
		{terms("parse_config"), "reparse_config()", false},
		{terms("http.Client"), "net/http.Client.Do(...)", true},
		{terms("timeout"), "Timeout exceeded after 5s", true},
		{terms("timeout"), "READ TIMEOUT", true},
		{terms("timeout"), "timeouts: 3", false},
		{terms("Timeout"), "connection timeout", true},
		{terms("4711"), "GET /api/items/4711 200", true},
		{terms("4711"), "GET /api/items/47110 200", false},
		{terms("ab"), "ab ab ab", false},
		{terms("  "), "anything", false},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "internal/cli/run.go:12: undefined: x", true},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "run.go:12:3: expected ';'", true},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "/home/u/src/lx/internal/cli/run.go:40 +0x1d", true},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "./cli/run.go:40", true},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "internal/engine/run.go:40", false},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "rerun.go:1", false},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "pre-run.go:1", false},
		{&Focus{Files: []string{"internal/cli/run.go"}}, "run.gopher", false},
		{&Focus{Files: []string{"/Users/me/app/pkg/config.py"}}, `  File "/Users/me/app/pkg/config.py", line 3, in load`, true},
		{&Focus{Files: []string{"/Users/me/app/pkg/config.py"}}, `  File "/Users/me/other/pkg/config.py", line 3, in load`, false},
		{&Focus{Files: []string{"/Users/me/app/pkg/config.py"}}, "config.py:3: error", true},
		{&Focus{Files: []string{`C:\proj\src\app.ts`}}, `src\app.ts(3,1): error TS2304`, true},
	}
	for _, c := range cases {
		m := newFocusMatcher(c.focus)
		got := len(m.rank(c.line)) > 0
		if got != c.want {
			t.Errorf("%+v in %q: got %v, want %v", *c.focus, c.line, got, c.want)
		}
	}
	if newFocusMatcher(nil) != nil || newFocusMatcher(&Focus{}) != nil || newFocusMatcher(terms("a", "")) != nil {
		t.Error("an empty focus must compile to nil")
	}
}

func TestFocusRank(t *testing.T) {
	m := newFocusMatcher(&Focus{
		Terms: []FocusTerm{{Text: "alpha", Weight: 1}, {Text: "Beta", Weight: 3}, {Text: "common", Weight: 5}},
		Files: []string{"x.go"},
	})
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("common line %d", i))
	}
	lines[3] += " alpha"
	lines[10] += " Beta"
	lines[20] += " x.go:3 alpha"
	var got []int
	for _, f := range m.rank(strings.Join(lines, "\n")) {
		got = append(got, f.i)
	}
	if want := []int{10, 20, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("rank = %v, want %v (weight order, a term on every line ignored)", got, want)
	}
	var nilM *focusMatcher
	if nilM.rank(strings.Join(lines, "\n")) != nil || nilM.note("x", nil) != "" {
		t.Error("a nil matcher must rank and note nothing")
	}
}

func TestPressureBudget(t *testing.T) {
	cases := []struct {
		used, window, budget, want int
	}{
		{0, 200000, 8000, 8000},
		{100000, 0, 8000, 8000},
		{98000, 200000, 8000, 8000},
		{100000, 200000, 8000, 6400},
		{150000, 200000, 8000, 4800},
		{150000, 200000, 3000, 2500},
		{150000, 200000, 2000, 2000},
		{180000, 200000, 8000, 3200},
		{180000, 200000, 12000, 4800},
		{180000, 200000, 3000, 1500},
		{180000, 200000, 1000, 1000},
		{900000, 1000000, 16000, 6400},
		{250000, 200000, 8000, 3200},
	}
	for _, c := range cases {
		p := Pressure{Used: c.used, Window: c.window}
		if got := p.Budget(c.budget); got != c.want {
			t.Errorf("%d/%d of %d: got %d, want %d", c.used, c.window, c.budget, got, c.want)
		}
	}
	if n := (Pressure{Used: 164000, Window: 200000}).note(); n != "context 82% full" {
		t.Errorf("note %q", n)
	}
	if n := (Pressure{Used: 250000, Window: 200000}).note(); n != "context 100% full" {
		t.Errorf("note %q", n)
	}
}

func fillerLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("step %d: processed record batch with checksum %x and status nominal", i, i*7919)
	}
	return lines
}

func TestBudgetFocusKeepsFocusLines(t *testing.T) {
	lines := fillerLines(2000)
	lines[500] = "    TestParseConfig/nested: loading fixture testdata/nested.toml"
	lines[501] = "        config_test.go:88: want 3 sections, got 2"
	lines[1000] = "panic: runtime error: index out of range [3] with length 3"
	lines[1500] = "loading defaults from internal/config/config.go"
	in := strings.Join(lines, "\n")
	f := &Focus{Terms: []FocusTerm{{Text: "TestParseConfig", Weight: 2}}, Files: []string{"internal/config/config.go"}}

	base := budgetFitLines(in, 1500, 0, 0, CutEither, true, true, errKeep{context: 1})
	fm := newFocusMatcher(f)
	got, hits := budgetFocus(in, 1500, 0, 0, CutEither, true, true, errKeep{context: 1}, fm)
	for _, ln := range []string{lines[500], lines[1500]} {
		if strings.Contains(base, ln) {
			t.Fatalf("the fixture no longer drops %q without focus", ln)
		}
		if !strings.Contains(got, ln) {
			t.Errorf("focused line dropped: %q", ln)
		}
	}
	if !strings.Contains(got, lines[501]) {
		t.Error("the line after a focus line is its context and must be kept")
	}
	if miss := MissingErrorLines(base, got); len(miss) > 0 {
		t.Errorf("focus displaced error lines: %q", miss)
	}
	if n := tokens.Count(got); n > 1500 {
		t.Errorf("%d tokens over the budget", n)
	}
	if note := fm.note(got, hits); note != "focus: TestParseConfig, config.go" {
		t.Errorf("note %q", note)
	}
	if again, _ := budgetFocus(in, 1500, 0, 0, CutEither, true, true, errKeep{context: 1}, newFocusMatcher(terms("nowhere_to_be_found"))); again != base {
		t.Error("a focus that matches nothing changed the view")
	}
}

func TestBudgetFocusNeverDisplacesErrors(t *testing.T) {
	lines := fillerLines(600)
	for i := 0; i < 600; i += 4 {
		lines[i] = fmt.Sprintf("error: request %d failed: connection refused by upstream", i)
	}
	lines[301] = "note: retry policy from settings.go applies to TestRetry"
	in := strings.Join(lines, "\n")
	fm := newFocusMatcher(&Focus{Terms: []FocusTerm{{Text: "TestRetry", Weight: 1}}, Files: []string{"settings.go"}})
	for _, budget := range []int{400, 1500, 3000, 6000} {
		base := budgetFitLines(in, budget, 0, 0, CutEither, true, true, errKeep{context: 1})
		got, _ := budgetFocus(in, budget, 0, 0, CutEither, true, true, errKeep{context: 1}, fm)
		if miss := MissingErrorLines(base, got); len(miss) > 0 {
			t.Errorf("budget %d: focus displaced %d error lines, e.g. %q", budget, len(miss), miss[0])
		}
	}
}

func TestBudgetFocusContentKeepsErrors(t *testing.T) {
	lines := fillerLines(3000)
	for i := 0; i < 60; i += 3 {
		lines[i] = fmt.Sprintf("error: head problem %d could not open socket", i)
	}
	for i := 1000; i < 1400; i += 2 {
		lines[i] += " Widget"
	}
	in := strings.Join(lines, "\n")
	base := budgetFitLines(in, 1500, 0, 0, CutEither, false, false, errKeep{context: 1})
	got, hits := budgetFocus(in, 1500, 0, 0, CutEither, false, false, errKeep{context: 1}, newFocusMatcher(terms("Widget")))
	if !strings.Contains(base, lines[0]) || strings.Contains(base, lines[1000]) {
		t.Fatal("fixture: the content view must keep the head's error lines and drop the focus lines")
	}
	if miss := MissingErrorLines(base, got); len(miss) > 0 {
		t.Errorf("focus displaced %d error lines of a content view, e.g. %q", len(miss), miss[0])
	}
	if !strings.Contains(got, lines[1000]) || len(hits) == 0 {
		t.Error("focus lines no longer come back in a content view")
	}
}

func TestFocusNoteIgnoresDiscardedViews(t *testing.T) {
	stack := []string{"main.handler(0x1)", "\thandler.go:12 +0x1a"}
	for i := 0; i < 6; i++ {
		file := fmt.Sprintf("/usr/local/go/src/net/http/server%d.go", i)
		if i == 3 {
			file = "/root/go/pkg/mod/github.com/lib/pq@v1.10.9/conn.go"
		}
		stack = append(stack, fmt.Sprintf("net/http.serve%d(0x1)", i), "\t"+file+":100 +0x2b")
	}
	withDetectors(t,
		Detector{Name: "bails", Detect: marker, Argv: []string{"bails"},
			Filter: detFake{"bails", func(c *Context, s string) (string, bool) {
				FoldStacks(c, strings.Split(s, "\n"))
				return "", false
			}}},
		Detector{Name: "renders", Detect: marker, Argv: []string{"tool"},
			Filter: detFake{"renders", func(c *Context, s string) (string, bool) {
				var keep []string
				for _, ln := range strings.Split(s, "\n") {
					if strings.Contains(ln, "conn.go") || strings.Contains(ln, "handler") {
						keep = append(keep, ln)
					}
				}
				return strings.Join(keep, "\n"), true
			}}},
	)
	raw := strings.Join(append(append([]string{"@@lx-detect-test@@"}, fillerLines(40)...), stack...), "\n")
	focus := &Focus{Files: []string{"github.com/lib/pq@v1.10.9/conn.go"}}
	res := Process(&Context{Argv: []string{"just", "x"}, Exit: 1}, raw, Options{Budget: 4000, Focus: focus})
	if res.Filter != DetectedPrefix+"renders" || !res.Lossy || !strings.Contains(res.Output, "conn.go:100") {
		t.Fatalf("fixture: filter %s, lossy %v:\n%s", res.Filter, res.Lossy, res.Output)
	}
	if len(res.Notes) > 0 {
		t.Errorf("notes %q for a line the view keeps without focus", res.Notes)
	}
}

func TestBudgetFocusRandom(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	iters := 300
	if testenv.Race {
		iters = 40
	}
	for iter := 0; iter < iters; iter++ {
		n := 50 + r.Intn(800)
		lines := fillerLines(n)
		var words []string
		for i := range lines {
			switch r.Intn(12) {
			case 0:
				lines[i] = fmt.Sprintf("FAIL: case %d errored with code %d", i, r.Intn(9))
			case 1:
				w := fmt.Sprintf("Ident%d", r.Intn(40))
				lines[i] += " " + w
				words = append(words, w)
			case 2:
				lines[i] = ""
			}
		}
		f := &Focus{}
		for k := 0; k < 1+r.Intn(4) && len(words) > 0; k++ {
			f.Terms = append(f.Terms, FocusTerm{Text: words[r.Intn(len(words))], Weight: r.Float64() * 3})
		}
		in := strings.Join(lines, "\n")
		budget, chars, maxLines := 200+r.Intn(3000), 0, 0
		if r.Intn(3) == 0 {
			chars = 2000 + r.Intn(20000)
		}
		if r.Intn(3) == 0 {
			maxLines = 5 + r.Intn(80)
		}
		cut := Cut(r.Intn(3))
		ek := errKeep{context: 1 + r.Intn(3), all: r.Intn(4) == 0}
		base := budgetFitLines(in, budget, chars, maxLines, cut, true, true, ek)
		got, _ := budgetFocus(in, budget, chars, maxLines, cut, true, true, ek, newFocusMatcher(f))
		if miss := MissingErrorLines(base, got); len(miss) > 0 {
			t.Fatalf("iter %d: focus displaced %d error lines, e.g. %q", iter, len(miss), miss[0])
		}
		if tokens.Count(got) > budget || chars > 0 && len(got) > chars || maxLines > 0 && countLines(got) > maxLines {
			t.Fatalf("iter %d: over a cap: %d tokens/%d, %d bytes/%d, %d lines/%d", iter, tokens.Count(got), budget, len(got), chars, countLines(got), maxLines)
		}
	}
}

func TestCollapseSimilarFocus(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("GET /api/items/%d 200 %dms", 4700+i, 3+i%5))
	}
	base := collapseSimilar(lines, minSimilarRun, nil)
	if len(base) != 3 {
		t.Fatalf("fixture no longer folds: %q", base)
	}
	fm := newFocusMatcher(terms("4711", "4712", "4720"))
	got := collapseSimilar(lines, minSimilarRun, fm)
	want := []string{
		lines[0],
		"  … 10 similar lines …",
		lines[11], lines[12],
		"  … 7 similar lines …",
		lines[20],
		"  … 8 similar lines …",
		lines[29],
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := fm.note(strings.Join(got, "\n"), nil); n != "focus: 4711, 4712, 4720" {
		t.Errorf("note %q", n)
	}
	edge := newFocusMatcher(terms("4700", "4729"))
	if got := collapseSimilar(lines, minSimilarRun, edge); !reflect.DeepEqual(got, base) {
		t.Errorf("focus on lines a fold already shows changed it: %q", got)
	}
	if edge.note(strings.Join(base, "\n"), nil) != "" {
		t.Error("a note for a fold focus did not change")
	}
}

func TestFoldStacksFocusFile(t *testing.T) {
	in := []string{"main.handler(0x1)", "\thandler.go:12 +0x1a"}
	for i := 0; i < 6; i++ {
		file := fmt.Sprintf("/usr/local/go/src/net/http/server%d.go", i)
		if i == 3 {
			file = "/root/go/pkg/mod/github.com/lib/pq@v1.10.9/conn.go"
		}
		in = append(in, fmt.Sprintf("net/http.serve%d(0x1)", i), "\t"+file+":100 +0x2b")
	}
	base := FoldStacks(&Context{Cwd: "/w"}, in)
	c := &Context{Cwd: "/w", Focus: &Focus{Files: []string{"github.com/lib/pq@v1.10.9/conn.go"}}}
	got := strings.Join(FoldStacks(c, in), "\n")
	if strings.Contains(strings.Join(base, "\n"), "conn.go") {
		t.Fatalf("fixture no longer folds the library frame: %q", base)
	}
	for _, must := range []string{"conn.go:100", "net/http.serve2(0x1)", "net/http.serve4(0x1)"} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q (a focus-file frame is kept with its neighbours):\n%s", must, got)
		}
	}
	if n := c.fm.note(got, nil); n != "focus: conn.go" {
		t.Errorf("note %q", n)
	}
	other := &Context{Cwd: "/w", Focus: &Focus{Files: []string{"elsewhere/conn.go"}}}
	if got := FoldStacks(other, in); !reflect.DeepEqual(got, base) {
		t.Errorf("a focus file in another directory changed the fold:\n%s", strings.Join(got, "\n"))
	}
}

func TestReceiptNotes(t *testing.T) {
	r := Result{RawLines: 300, OutLines: 30, RawTokens: 1000, OutTokens: 100, Mode: ModeError, ViewMode: ModeError,
		Notes: []string{"focus: TestParseConfig, config.go", "context 82% full"}}
	base := "[lx: 300→30 lines (−90%) · mode error · full output: lx show 7]"
	if got := Receipt(r, "7"); got != base {
		t.Errorf("Receipt leaves notes to WithNotes: %s", got)
	}
	want := "[lx: 300→30 lines (−90%) · mode error · full output: lx show 7 · focus: TestParseConfig, config.go · context 82% full]"
	if got := WithNotes(Receipt(r, "7"), r.Notes, 0); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := WithNotes(base, r.Notes, len(base)+40); got != "[lx: 300→30 lines (−90%) · mode error · full output: lx show 7 · focus: TestParseConfig, config.go]" {
		t.Errorf("a note that does not fit the room must be left out: %s", got)
	}
	if got := WithNotes(base, r.Notes, len(base)); got != base {
		t.Errorf("no room: %s", got)
	}
	if got := WithNotes(base, r.Notes, len(base)+22); got != "[lx: 300→30 lines (−90%) · mode error · full output: lx show 7 · context 82% full]" {
		t.Errorf("a later note that fits must follow one that does not: %s", got)
	}
	m := newFocusMatcher(terms("TestAVeryLongIdentifierNameForTheParser", "TestSecondRatherLongName", "TestThird", "TestFourth"))
	var all uint64 = 1<<len(m.pats) - 1
	if n := m.note("x", []focusHit{{probe: "x", mask: all}}); len(n) > 7+maxFocusNote+4 || !strings.HasSuffix(n, " +2") {
		t.Errorf("long focus note %q (%d bytes)", n, len(n))
	}
	dup := newFocusMatcher(&Focus{Terms: []FocusTerm{{Text: "config.go", Weight: 1}}, Files: []string{"internal/config/config.go"}})
	if n := dup.note("x", []focusHit{{probe: "x", mask: 3}}); n != "focus: config.go" {
		t.Errorf("duplicate names: %q", n)
	}
}

func bigOutput() string {
	lines := fillerLines(50000)
	for i := 3; i < len(lines); i += 500 {
		lines[i] = fmt.Sprintf("ERROR shard %d: checksum mismatch in segment %d", i, i*3)
	}
	lines[20000] += " events_42"
	return strings.Join(lines, "\n")
}

var benchFocus = &Focus{
	Terms: []FocusTerm{{Text: "events_42", Weight: 2}, {Text: "TestSync", Weight: 1}, {Text: "checksum", Weight: 1}},
	Files: []string{"internal/sync/shard.go", "cmd/sync/main.go"},
}

func BenchmarkBudget(b *testing.B) {
	in := bigOutput()
	for b.Loop() {
		budgetFitLines(in, DefaultBudget, 0, 0, CutEither, true, true, errKeep{context: 1})
	}
}

func BenchmarkBudgetFocus(b *testing.B) {
	in := bigOutput()
	for b.Loop() {
		budgetFocus(in, DefaultBudget, 0, 0, CutEither, true, true, errKeep{context: 1}, newFocusMatcher(benchFocus))
	}
}

func BenchmarkErrorNeed(b *testing.B) {
	in := bigOutput()
	for b.Loop() {
		errorNeed(in, errKeep{context: 1})
	}
}
