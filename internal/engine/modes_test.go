package engine

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/tokens"
)

var allModes = []Mode{ModeAuto, ModeError, ModeDebug, ModeVerify, ModeMinimal}

func TestParseMode(t *testing.T) {
	for _, m := range allModes {
		got, ok := ParseMode(m.String())
		if !ok || got != m {
			t.Errorf("ParseMode(%q) = %v, %v", m.String(), got, ok)
		}
		if !strings.Contains(ModeList, m.String()) {
			t.Errorf("ModeList %q lacks %q", ModeList, m)
		}
	}
	for _, s := range []string{"", "Auto", "VERIFY", " verify", "verify ", "err", "errors", "debugging", "min", "mode(9)", "0", "auto,debug"} {
		if m, ok := ParseMode(s); ok || m != ModeAuto {
			t.Errorf("ParseMode(%q) = %v, %v: want an error", s, m, ok)
		}
	}
	if s := Mode(9).String(); s != "mode(9)" {
		t.Errorf("Mode(9) = %q", s)
	}
}

func TestModeBudget(t *testing.T) {
	for _, c := range []struct {
		m      Mode
		base   int
		failed bool
		want   int
	}{
		{ModeAuto, 0, false, DefaultBudget},
		{ModeAuto, 3000, true, 3000},
		{ModeError, 0, true, 12000},
		{ModeError, 3000, false, 4500},
		{ModeDebug, 0, false, 16000},
		{ModeDebug, 500, true, 1000},
		{ModeVerify, 0, false, 4000},
		{ModeVerify, 0, true, 12000},
		{ModeVerify, 1, false, 1},
		{ModeMinimal, 0, true, MinimalBudget},
		{ModeMinimal, 50000, false, MinimalBudget},
		{ModeMinimal, 1200, true, 1200},
	} {
		if got := c.m.Budget(c.base, c.failed); got != c.want {
			t.Errorf("%v.Budget(%d, failed %v) = %d, want %d", c.m, c.base, c.failed, got, c.want)
		}
	}
	for _, m := range allModes {
		if m.View(true) != m && !(m == ModeVerify && m.View(true) == ModeError) {
			t.Errorf("%v on a failure is %v", m, m.View(true))
		}
		if m.View(false) != m {
			t.Errorf("%v on success is %v", m, m.View(false))
		}
	}
	if k := ModeAuto.knobs(); k != (modeKnobs{num: 1, den: 1, errs: errKeep{context: 1}, boundary: 1, similarRun: minSimilarRun}) {
		t.Errorf("auto's knobs changed: %+v", k)
	}
}

func TestModeBudgetOverflow(t *testing.T) {
	for _, b := range []int{math.MaxInt, math.MaxInt/2 + 1, math.MaxInt/3 + 1, math.MaxInt - 1} {
		for _, m := range []Mode{ModeError, ModeDebug} {
			for _, failed := range []bool{false, true} {
				if got := m.Budget(b, failed); got < b {
					t.Errorf("%v.Budget(%d) = %d: smaller than auto's", m, b, got)
				}
			}
		}
		if got := ModeVerify.Budget(b, false); got != b/2 {
			t.Errorf("verify.Budget(%d) = %d", b, got)
		}
	}
	in := plainReport(2000)
	for _, m := range allModes[:3] {
		res := Process(&Context{Argv: []string{"some-tool"}, Exit: 1}, in, Options{Budget: math.MaxInt, Mode: m})
		if res.Lossy || res.Output != in {
			t.Errorf("%v with budget MaxInt: lossy %v, %d of %d lines", m, res.Lossy, res.OutLines, res.RawLines)
		}
	}
}

func TestProcessSetsModeOnContext(t *testing.T) {
	for _, c := range []struct {
		m          Mode
		exit       int
		view       Mode
		budget     int
		receiptHas string
	}{
		{ModeAuto, 1, ModeAuto, DefaultBudget, ""},
		{ModeVerify, 1, ModeError, 12000, " · mode verify→error · "},
		{ModeVerify, 0, ModeVerify, 4000, " · mode verify · "},
		{ModeDebug, 0, ModeDebug, 16000, " · mode debug · "},
		{ModeMinimal, 2, ModeMinimal, MinimalBudget, " · mode minimal · "},
	} {
		ctx := &Context{Argv: []string{"some-tool"}, Exit: c.exit}
		res := Process(ctx, strings.Repeat("a line of output that is not an error at all\n", 3000), Options{Mode: c.m})
		if ctx.Mode != c.view || ctx.Budget != c.budget || res.Mode != c.m || res.ViewMode != c.view {
			t.Errorf("%v exit %d: context mode %v budget %d, result %v/%v", c.m, c.exit, ctx.Mode, ctx.Budget, res.Mode, res.ViewMode)
		}
		r := Receipt(res, "7")
		if c.receiptHas == "" && strings.Contains(r, "mode") || c.receiptHas != "" && !strings.Contains(r, c.receiptHas) {
			t.Errorf("%v exit %d: receipt %q", c.m, c.exit, r)
		}
	}
}

func TestReceiptNamesMode(t *testing.T) {
	r := Result{RawLines: 307, OutLines: 29, RawTokens: 1000, OutTokens: 290}
	for _, c := range []struct {
		mode, view Mode
		want       string
	}{
		{ModeAuto, ModeAuto, "[lx: 307→29 lines (−71%) · full output: lx show 1]"},
		{ModeVerify, ModeVerify, "[lx: 307→29 lines (−71%) · mode verify · full output: lx show 1]"},
		{ModeVerify, ModeError, "[lx: 307→29 lines (−71%) · mode verify→error · full output: lx show 1]"},
		{ModeMinimal, ModeMinimal, "[lx: 307→29 lines (−71%) · mode minimal · full output: lx show 1]"},
		{ModeDebug, ModeAuto, "[lx: 307→29 lines (−71%) · mode debug · full output: lx show 1]"},
	} {
		r.Mode, r.ViewMode = c.mode, c.view
		if got := Receipt(r, "1"); got != c.want {
			t.Errorf("%v/%v: %q, want %q", c.mode, c.view, got, c.want)
		}
	}
	r.Mode, r.ViewMode = ModeError, ModeError
	if got := Receipt(r, ""); got != "[lx: 307→29 lines (−71%) · mode error]" {
		t.Errorf("without an id: %q", got)
	}
}

func errContextOutput(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i%40 == 20 {
			fmt.Fprintf(&b, "ERROR: job %d failed: timeout\n", i)
			for k := 1; k <= 4; k++ {
				fmt.Fprintf(&b, "  context %d of job %d: retried against shard %d\n", k, i, i*k%17)
			}
			continue
		}
		fmt.Fprintf(&b, "job %d finished in %dms, %d records written\n", i, i*7%900, i*31)
	}
	return strings.TrimRight(b.String(), "\n")
}

func TestBudgetErrContext(t *testing.T) {
	for _, lines := range []int{0, 60} {
		n := 2000
		if lines > 0 {
			n = 200
		}
		out := errContextOutput(n)
		for _, c := range []struct{ ctx, want int }{{1, 1}, {3, 3}} {
			got := budgetFitLines(out, 6000, 0, lines, CutEither, true, true, errKeep{context: c.ctx})
			if lines > 0 && countLines(got) > lines {
				t.Fatalf("context %d: %d lines under a %d-line cut", c.ctx, countLines(got), lines)
			}
			for i := 20; i < n; i += 40 {
				if !strings.Contains(got, fmt.Sprintf("ERROR: job %d failed", i)) {
					t.Fatalf("context %d: error line of job %d dropped", c.ctx, i)
				}

				inGap := !strings.Contains(got, fmt.Sprintf("job %d finished", i+5))
				for k := 1; k <= 4; k++ {
					has := strings.Contains(got, fmt.Sprintf("  context %d of job %d:", k, i))
					if k <= c.want && !has || inGap && k > c.want && has {
						t.Fatalf("context %d: job %d's context line %d kept %v", c.ctx, i, k, has)
					}
				}
			}
		}
	}

	filler := strings.Repeat("filler line with some words in it\n", 1000)
	blank := filler + "ERROR: build failed\n  cause: disk full\n\n  unrelated trailer\n" + filler
	got := budgetFitLines(blank, 300, 0, 0, CutEither, true, true, errKeep{context: 3})
	if !strings.Contains(got, "cause: disk full") || strings.Contains(got, "unrelated trailer") {
		t.Errorf("context past a blank line:\n%s", got)
	}
}

func TestBudgetErrContextKeepsEveryErrorLine(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	trials := 30
	if testenv.Race {
		trials = 3
	}
	for trial := 0; trial < trials; trial++ {
		var b strings.Builder
		n := 200 + rng.Intn(3000)
		every := 2 + rng.Intn(30)
		for i := 0; i < n; i++ {
			switch {
			case i%every == 0:
				fmt.Fprintf(&b, "error: step %d failed: %s\n", i, strings.Repeat("x", rng.Intn(120)))
			case rng.Intn(9) == 0:
				b.WriteString("\n")
			default:
				fmt.Fprintf(&b, "  step %d detail %s\n", i, strings.Repeat("y", rng.Intn(80)))
			}
		}
		out := b.String()
		budget := 200 + rng.Intn(4000)
		lines := []int{0, 0, 20, 60}[rng.Intn(4)]
		chars := []int{0, 0, 3000}[rng.Intn(3)]
		cut := Cut(rng.Intn(3))
		a := budgetFitLines(out, budget, chars, lines, cut, true, true, errKeep{context: 1})
		e := budgetFitLines(out, budget, chars, lines, cut, true, true, errKeep{context: 3})
		if ea, ee := countErr(a), countErr(e); ee < ea {
			t.Errorf("trial %d (budget %d, lines %d, chars %d): %d error lines with 3 lines of context, %d with 1", trial, budget, lines, chars, ee, ea)
		}
		if tokens.Count(e) > budget {
			t.Errorf("trial %d: %d tokens over a budget of %d", trial, tokens.Count(e), budget)
		}
		if lines > 0 && countLines(e) > lines || chars > 0 && len(e) > chars {
			t.Errorf("trial %d: %d lines / %d bytes over %d / %d", trial, countLines(e), len(e), lines, chars)
		}
	}
}

func TestBudgetErrKeepAll(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	trials := 40
	if testenv.Race {
		trials = 4
	}
	more := 0
	for trial := 0; trial < trials; trial++ {
		var b strings.Builder
		n := 200 + rng.Intn(3000)
		every := 2 + rng.Intn(30)
		for i := 0; i < n; i++ {
			switch {
			case i%every == 0:
				fmt.Fprintf(&b, "error: step %d failed: %s\n", i, strings.Repeat("x", rng.Intn(120)))
			case rng.Intn(9) == 0:
				b.WriteString("\n")
			default:
				fmt.Fprintf(&b, "  step %d detail %s\n", i, strings.Repeat("y", rng.Intn(80)))
			}
		}
		out := b.String()
		budget := 200 + rng.Intn(4000)
		lines := []int{0, 0, 20, 60}[rng.Intn(4)]
		chars := []int{0, 0, 3000}[rng.Intn(3)]
		cut := Cut(rng.Intn(3))
		failed := rng.Intn(2) == 0
		a := budgetFitLines(out, budget, chars, lines, cut, failed, true, errKeep{context: 1})
		e := budgetFitLines(out, budget, chars, lines, cut, failed, true, errKeep{context: 1, all: true})
		ea, ee := countErr(a), countErr(e)
		if ee < ea {
			t.Errorf("trial %d (budget %d, lines %d, chars %d): %d error lines with all, %d without", trial, budget, lines, chars, ee, ea)
		}
		if ee > ea {
			more++
		}
		if tokens.Count(e) > budget {
			t.Errorf("trial %d: %d tokens over a budget of %d", trial, tokens.Count(e), budget)
		}
		if lines > 0 && countLines(e) > lines || chars > 0 && len(e) > chars {
			t.Errorf("trial %d: %d lines / %d bytes over %d / %d", trial, countLines(e), len(e), lines, chars)
		}
	}
	if more == 0 && !testenv.Race {
		t.Error("errKeep.all never kept more error lines: the test no longer exercises it")
	}
}

func reducedOutput(errTokens int, verdict string) (string, []string) {
	words := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima"}
	var lines, errs []string
	cost := 0
	for i := 0; cost < errTokens; i++ {
		w := func(k int) string { return words[(i/k)%12] }
		if i%60 >= 30 && i%60 < 33 {
			e := fmt.Sprintf("error: step %s %s %s failed: quota exceeded on volume %s", w(1), w(12), w(144), w(1728))
			cost += tokens.Count(e) + 1
			lines, errs = append(lines, e), append(errs, e)
			continue
		}
		lines = append(lines, fmt.Sprintf("the report says %s %s %s %s went fine today", w(1), w(12), w(144), w(1728)))
	}
	for i := 0; i < 400; i++ {
		lines = append(lines, fmt.Sprintf("then the %s %s %s step went fine too", words[i%12], words[i/12%12], words[i/144%12]))
	}
	return strings.Join(append(lines, verdict), "\n"), errs
}

func TestReducedModesKeepErrorsThatFit(t *testing.T) {
	for _, c := range []struct {
		m       Mode
		exit    int
		verdict string
	}{
		{ModeMinimal, 1, "FAILED: 9 steps"},
		{ModeMinimal, 0, "done: every step finished"},
		{ModeVerify, 0, "done: every step finished"},
	} {
		budget := c.m.Budget(0, c.exit != 0)
		in, errs := reducedOutput(budget*2/3, c.verdict)

		if old := budgetFitLines(in, budget, 0, 0, CutEither, c.exit != 0, true, errKeep{context: 1}); countErr(old) >= len(errs) {
			t.Fatalf("%v: auto's order keeps all %d error lines at %d tokens: the test needs more", c.m, len(errs), budget)
		}
		res := Process(&Context{Argv: []string{"some-tool"}, Exit: c.exit}, in, Options{Mode: c.m})
		if miss := MissingErrorLines(in, res.Output); len(miss) > 0 {
			t.Errorf("%v exit %d: %d of %d error lines lost (they cost %d of %d tokens), e.g. %q", c.m, c.exit, len(miss), len(errs), budget*2/3, budget, miss[0])
		}
		if !strings.HasSuffix(res.Output, c.verdict) {
			t.Errorf("%v exit %d: the verdict is gone:\n…%s", c.m, c.exit, res.Output[max(0, len(res.Output)-300):])
		}
		if res.OutTokens > budget || !res.Lossy {
			t.Errorf("%v exit %d: %d tokens (budget %d), lossy %v", c.m, c.exit, res.OutTokens, budget, res.Lossy)
		}
	}
}

func countErr(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if IsError(ln) {
			n++
		}
	}
	return n
}

func TestMinimalReceiptGate(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&b, "record %d: value=%d status=ok region=eu-west-%d\n", i, i*37%1000, i%3)
	}
	big := b.String()
	ctx := func() *Context { return &Context{Argv: []string{"some-tool"}} }
	res := Process(ctx(), big, Options{Mode: ModeMinimal})
	if !res.Lossy || res.OutTokens > MinimalBudget {
		t.Fatalf("minimal: lossy %v, %d tokens", res.Lossy, res.OutTokens)
	}
	if whole := Process(ctx(), big, Options{Mode: ModeMinimal, MinSavings: 2}); whole.Lossy || whole.Output != strings.TrimRight(big, "\n") {
		t.Fatalf("an explicit MinSavings must win over minimal's gate: %q", whole.Filter)
	}

	for _, over := range []int{4, 300} {
		in := plainReport(MinimalBudget + over)
		clean := tokens.Count(in)
		res := Process(ctx(), in, Options{Mode: ModeMinimal})
		rc := receiptTokens(res, res.Output, res.OutTokens)
		switch {
		case res.Lossy && res.OutTokens+rc >= clean:
			t.Errorf("%d over: a view of %d tokens and its receipt (%d) cost more than the output (%d)", over, res.OutTokens, rc, clean)
		case !res.Lossy && res.Output != in:
			t.Errorf("%d over: not lossy, yet not the output", over)
		case over == 4 && res.Lossy, over == 300 && !res.Lossy:
			t.Errorf("%d over: lossy %v (view %d tokens, receipt %d, output %d)", over, res.Lossy, res.OutTokens, rc, clean)
		}
		if res.OutTokens > MinimalBudget+rc {
			t.Errorf("%d over: %d tokens printed", over, res.OutTokens)
		}
	}
}

func plainReport(n int) string {
	words := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima"}
	var lines []string
	for i := 0; ; i++ {
		ln := fmt.Sprintf("the report says %s %s %s went fine today", words[i%12], words[i/12%12], words[i/144%12])
		if tokens.Count(strings.Join(append(lines, ln), "\n")) > n {
			return strings.Join(lines, "\n")
		}
		lines = append(lines, ln)
	}
}

func modeKnobsWired(t *testing.T) {
	t.Helper()
	trace := strings.Split(nodeTrace, "\n")
	fold := len(FoldStacks(&Context{Cwd: "/w", Mode: ModeError}, trace)) != len(FoldStacks(&Context{Cwd: "/w"}, trace))
	_, shape := genericShape(&Context{Mode: ModeDebug}, logOutput(200))
	logs := shape != "log"
	sim, _ := genericShape(&Context{Mode: ModeDebug}, similarRun(6))
	similar := !strings.Contains(sim, "similar lines")
	switch n := btoi(fold) + btoi(logs) + btoi(similar); n {
	case 0:
		t.Skip("pending LEAD EDIT from modes: stack.go, similar.go and generic.go do not read Context.Mode yet")
	case 3:
	default:
		t.Fatalf("the modes LEAD EDITs are only partly applied: stack traces %v, log templates %v, similar runs %v", fold, logs, similar)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

const nodeTrace = `Error: boom
    at throwIt (node_modules/lib/a.js:1:1)
    at b (node_modules/lib/b.js:2:2)
    at c (node_modules/lib/c.js:3:3)
    at d (node_modules/lib/d.js:4:4)
    at handler (src/app.js:10:5)
    at e (node_modules/express/router.js:5:5)
    at f (node:internal/process/task_queues:90:21)
    at g (node:internal/timers:504:21)
    at h (node:events:630:28)
after`

func TestFoldStacksBoundaryByMode(t *testing.T) {
	modeKnobsWired(t)
	in := strings.Split(nodeTrace, "\n")
	auto := `Error: boom
    at throwIt (node_modules/lib/a.js:1:1)
    … 2 library frames (lib)
    at d (node_modules/lib/d.js:4:4)
    at handler (src/app.js:10:5)
    at e (node_modules/express/router.js:5:5)
    … 3 library frames (node:internal, node:events)
after`
	two := `Error: boom
    at throwIt (node_modules/lib/a.js:1:1)
    at b (node_modules/lib/b.js:2:2)
    at c (node_modules/lib/c.js:3:3)
    at d (node_modules/lib/d.js:4:4)
    at handler (src/app.js:10:5)
    at e (node_modules/express/router.js:5:5)
    at f (node:internal/process/task_queues:90:21)
    … 2 library frames (node:internal, node:events)
after`
	for _, m := range allModes {
		want := auto
		if m == ModeError || m == ModeDebug {
			want = two
		}
		if got := strings.Join(FoldStacks(&Context{Cwd: "/w", Mode: m}, in), "\n"); got != want {
			t.Errorf("%v:\n%s\nwant:\n%s", m, got, want)
		}
	}
	if got := strings.Join(FoldStacks(nil, in), "\n"); got != auto {
		t.Errorf("nil context:\n%s", got)
	}
}

func logOutput(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		lvl, msg := "INFO", fmt.Sprintf("served GET /api/items/%d in %dms", i*7%97, i*13%400)
		switch {
		case i == n/2:
			lvl, msg = "ERROR", "database connection lost: connection reset by peer"
		case i%10 == 3:
			lvl, msg = "DEBUG", fmt.Sprintf("cache miss for key user:%d", i*31%1000)
		}
		fmt.Fprintf(&b, "2026-09-27T10:%02d:%02d.%03dZ %s %s\n", i/60%60, i%60, i*7%1000, lvl, msg)
	}
	return strings.TrimRight(b.String(), "\n")
}

func similarRun(n int) string {
	var lines []string
	lines = append(lines, "starting the batch")
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("processed item %d of the batch (%d bytes)", 100+i, 2048+i*17))
	}
	return strings.Join(append(lines, "batch done"), "\n")
}

func TestGenericDebugKeepsLogLines(t *testing.T) {
	modeKnobsWired(t)
	in := logOutput(400)
	auto, shape := genericShape(&Context{}, in)
	if shape != "log" {
		t.Fatalf("auto shape %q: the test needs a templated log", shape)
	}
	debug, dshape := genericShape(&Context{Mode: ModeDebug}, in)
	if dshape != "lines" {
		t.Fatalf("debug shape %q", dshape)
	}
	for _, ln := range strings.Split(in, "\n") {
		if strings.Contains(ln, " ERROR ") && (!strings.Contains(debug, ln) || !strings.Contains(auto, ln)) {
			t.Fatalf("error line lost: %q", ln)
		}
		if !strings.Contains(debug, ln) {
			t.Fatalf("debug lost the log line %q", ln)
		}
	}

	res := Process(&Context{Argv: []string{"my-server"}, Exit: 1}, in, Options{Mode: ModeDebug})
	if res.Output != in {
		t.Errorf("debug view of a %d-token log is not the log (filter %s, %d tokens)", tokens.Count(in), res.Filter, res.OutTokens)
	}
	if a := Process(&Context{Argv: []string{"my-server"}, Exit: 1}, in, Options{}); a.OutLines >= res.OutLines {
		t.Errorf("auto keeps %d lines, debug %d", a.OutLines, res.OutLines)
	}
}

func TestGenericDebugSimilarRuns(t *testing.T) {
	modeKnobsWired(t)
	for _, c := range []struct {
		n             int
		m             Mode
		wantCollapsed bool
	}{
		{4, ModeAuto, true}, {7, ModeAuto, true}, {7, ModeError, true},
		{4, ModeDebug, false}, {7, ModeDebug, false}, {8, ModeDebug, true}, {40, ModeDebug, true},
	} {
		out, _ := genericShape(&Context{Mode: c.m}, similarRun(c.n))
		if got := strings.Contains(out, "similar lines"); got != c.wantCollapsed {
			t.Errorf("%d similar lines in %v: collapsed %v\n%s", c.n, c.m, got, out)
		}
	}
}
