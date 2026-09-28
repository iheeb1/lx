package engine_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/textutil"
)

var fitCuts = []int{5, 20, 40, 100}

func printedLines(res engine.Result) int {
	n := 0
	if res.Output != "" {
		n = strings.Count(res.Output, "\n") + 1
	}
	if res.Lossy {
		n++
	}
	return n
}

func nLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func errorsAloneLines(s string) int {
	need, inGap := 0, false
	for _, ln := range strings.Split(s, "\n") {
		switch {
		case engine.IsError(ln):
			need++
			inGap = false
		case !inGap:
			need++
			inGap = true
		}
	}
	return need
}

func longLine(ln string) bool { return utf8.RuneCountInString(ln) > 1200 }

func TestFitCorpus(t *testing.T) {
	checked, fitWhole, fitView, cut, errChecked := 0, 0, 0, 0, 0
	ends, headMore, tailMore := 0, 0, 0
	for _, c := range allCaptures(t) {
		name := c.Category + "/" + c.Name
		clean := c.Clean()
		if testenv.Race && len(clean)%7 != 0 {
			continue
		}
		cl := nLines(clean)
		base := engine.Process(c.Context(), c.Raw, engine.Options{})
		var missBase map[string]bool
		need := -1
		var capped *engine.Result
		check := func(tag string, res engine.Result, n, maxChars int) {
			checked++
			if got := printedLines(res); got > n {
				t.Errorf("%s: lx prints %d lines (lossy %v)", tag, got, res.Lossy)
			}
			if maxChars > 0 && len(res.Output) > maxChars {
				t.Errorf("%s: view is %d bytes", tag, len(res.Output))
			}
			overCap := maxChars > 0 && len(clean) > maxChars
			switch {
			case cl <= n && !overCap:
				ref := base
				if maxChars > 0 {
					if capped == nil {
						r := engine.Process(c.Context(), c.Raw, engine.Options{MaxChars: maxChars})
						capped = &r
					}
					ref = *capped
				}
				switch res.Output {
				case clean:
					fitWhole++
					if res.Lossy {
						t.Errorf("%s: the whole output marked lossy", tag)
					}
				case ref.Output:
					fitView++
				default:
					t.Errorf("%s: the output fits the cut (%d lines), yet the view differs from both it and the uncut view", tag, cl)
				}
			case cl > n:
				cut++
				if !res.Lossy && !faithful(c.Context(), res) {
					t.Errorf("%s: a view of %d of %d lines must be lossy", tag, nLines(res.Output), cl)
				}
			}
			if contentView(c.Context(), clean, res) || contentView(c.Context(), clean, base) {
				return
			}
			if need < 0 {
				need = max(errorsAloneLines(clean), errorsAloneLines(base.Output))
			}
			if need > n-1 {
				return
			}
			if missBase == nil {
				missBase = map[string]bool{}
				for _, ln := range engine.MissingErrorLines(clean, base.Output) {
					missBase[ln] = true
				}
			}
			errChecked++
			for _, ln := range engine.MissingErrorLines(clean, res.Output) {
				if !missBase[ln] && !longLine(ln) {
					t.Errorf("%s (error lines alone: %d lines) lost %q", tag, need, ln)
				}
			}
		}
		for _, maxChars := range []int{0, claudeCap} {
			for _, n := range fitCuts {
				opt := engine.Options{MaxLines: n, MaxChars: maxChars}
				res := engine.Process(c.Context(), c.Raw, opt)
				if res.Filter == "passthrough" && engine.MachineReadable(c.Context()) {
					continue
				}
				tag := fmt.Sprintf("%s: cut %d, cap %d", name, n, maxChars)
				check(tag, res, n, maxChars)
				if maxChars > 0 || cl <= n {
					continue
				}
				if testenv.Race && n != 40 {
					continue
				}

				ends++
				uncut := strings.Split(base.Output, "\n")
				either := strings.Split(res.Output, "\n")
				opt.Cut = engine.CutHead
				head := engine.Process(c.Context(), c.Raw, opt)
				check(tag+", head", head, n, 0)
				hl := strings.Split(head.Output, "\n")
				switch ph, pe := commonPrefix(hl, uncut), commonPrefix(either, uncut); {
				case ph > pe:
					headMore++
				case ph < pe && (n > engine.MinFitLines || errorLines(head.Output) < errorLines(res.Output)):
					t.Errorf("%s, head: the view starts with %d of the uncut view's first lines, %d for either end:\n%s", tag, ph, pe, head.Output)
				}
				opt.Cut = engine.CutTail
				tail := engine.Process(c.Context(), c.Raw, opt)
				check(tag+", tail", tail, n, 0)
				tl := strings.Split(tail.Output, "\n")
				switch st, se := commonSuffix(tl, uncut), commonSuffix(either, uncut); {
				case st > se:
					tailMore++
				case st < se && (n > engine.MinFitLines || errorLines(tail.Output) < errorLines(res.Output)):
					t.Errorf("%s, tail: the view ends with %d of the uncut view's last lines, %d for either end:\n%s", tag, st, se, tail.Output)
				}
			}
		}
	}
	t.Logf("%d fitted views: %d whole output, %d uncut view kept, %d over the cut; %d checked for error lines; "+
		"of %d over the cut, the head's view has more first lines in %d, the tail's more last lines in %d",
		checked, fitWhole, fitView, cut, errChecked, ends, headMore, tailMore)
	if !testenv.Race && (fitWhole == 0 || fitView == 0 || cut == 0 || errChecked < 100 || headMore < ends/3 || tailMore < ends/3) {
		t.Errorf("the corpus no longer exercises every case")
	}
}

func commonPrefix(a, b []string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

func commonSuffix(a, b []string) int {
	i := 0
	for i < len(a) && i < len(b) && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}

func errorLines(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if engine.IsError(ln) {
			n++
		}
	}
	return n
}

func fitShape(n int, errAt map[int]bool) (string, []string) {
	var lines, errs []string
	for i := 0; i < n; i++ {
		if errAt[i] {
			e := fmt.Sprintf("ERROR: step %d failed: connection refused", i)
			lines = append(lines, e)
			errs = append(errs, e)
			continue
		}
		lines = append(lines, fmt.Sprintf("step %d finished in %dms, %d records written to shard %d", i, i*7%900, i*31, i%13))
	}
	return strings.Join(lines, "\n"), errs
}

func TestFitProcess(t *testing.T) {
	ctx := func(argv ...string) *engine.Context {
		return &engine.Context{Argv: argv, Exit: 1, Cwd: "/w", Home: "/home/u"}
	}

	var hits strings.Builder
	hits.WriteString("grep: src/private: Permission denied\n")
	for f := 0; f < 12; f++ {
		for k := 0; k < 3; k++ {
			fmt.Fprintf(&hits, "src/pkg%d/file%02d.go:%d:\tresult := computeThing(ctx, %d) // call site number %d in this file\n", f%3, f, 10+k*40, k, k)
		}
	}
	grep := ctx("grep", "-rn", "computeThing", "src")
	uncut := engine.Process(grep, hits.String(), engine.Options{})
	if nLines(uncut.Output) <= 40 {
		t.Fatalf("fixture: the uncut view has %d lines; it must be longer than the cut", nLines(uncut.Output))
	}
	res := engine.Process(grep, hits.String(), engine.Options{MaxLines: 40})
	if res.Output != textutil.Clean(hits.String()) || res.Lossy {
		t.Fatalf("37 lines fit tail -n 40: the output itself must be printed, got %d lines (%s)", nLines(res.Output), res.Filter)
	}

	for n := range 5 {
		for f := 0; f < 12; f++ {
			fmt.Fprintf(&hits, "src/more%d/file%02d.go:%d:\tcomputeThing(%d)\n", n, f, n*10+f, f)
		}
	}
	res = engine.Process(grep, hits.String(), engine.Options{MaxLines: 40})
	if printedLines(res) > 40 || !res.Lossy || !strings.Contains(res.Output, "grep: src/private: Permission denied") {
		t.Fatalf("over the cut: %d lines, lossy %v:\n%s", printedLines(res), res.Lossy, res.Output)
	}

	errAt := map[int]bool{}
	for i := 30; i < 400; i += 60 {
		errAt[i] = true
	}
	in, errs := fitShape(400, errAt)
	in += "\nsummary: 7 of 7 shards failed"
	job := ctx("./job")
	for _, n := range []int{20, 40, 100} {
		res := engine.Process(job, in, engine.Options{MaxLines: n})
		if printedLines(res) > n || !res.Lossy {
			t.Fatalf("cut %d: %d lines, lossy %v", n, printedLines(res), res.Lossy)
		}
		for _, e := range errs {
			if !strings.Contains(res.Output, e) {
				t.Errorf("cut %d lost %q:\n%s", n, e, res.Output)
			}
		}
		if !strings.Contains(res.Output, "summary: 7 of 7 shards failed") {
			t.Errorf("cut %d lost the verdict:\n%s", n, res.Output)
		}
		if !strings.Contains(res.Output, "lines omitted") {
			t.Errorf("cut %d: gaps must be marked:\n%s", n, res.Output)
		}
	}

	small, _ := fitShape(12, map[int]bool{4: true})
	res = engine.Process(job, small, engine.Options{MaxLines: 6})
	if printedLines(res) > 6 || !res.Lossy || !strings.Contains(res.Output, "ERROR: step 4 failed") {
		t.Errorf("small output over the cut: %d lines, lossy %v:\n%s", printedLines(res), res.Lossy, res.Output)
	}

	if res := engine.Process(job, small, engine.Options{MaxLines: 12}); res.Output != small || res.Lossy {
		t.Errorf("an output that fits the cut changed:\n%s", res.Output)
	}

	for n := 1; n < engine.MinFitLines; n++ {
		if res := engine.Process(job, in, engine.Options{MaxLines: n}); res.Output != in || res.Lossy {
			t.Errorf("cut %d: want the output itself, got %d lines (%s)", n, nLines(res.Output), res.Filter)
		}
	}

	res = engine.Process(job, in, engine.Options{MaxLines: 3, MaxChars: 2000})
	if len(res.Output) > 2000 || !res.Lossy {
		t.Errorf("tiny cut over the cap: %d bytes, lossy %v", len(res.Output), res.Lossy)
	}

	var por strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&por, " M internal/pkg%d/file%d.go\n", i%40, i)
	}
	res = engine.Process(ctx("git", "status", "--porcelain"), por.String(), engine.Options{MaxLines: 10})
	if res.Filter != "passthrough" || res.Output != strings.TrimRight(por.String(), "\n") {
		t.Errorf("machine-readable output must pass through: %s", res.Filter)
	}

	a := engine.Process(job, in, engine.Options{})
	b := engine.Process(job, in, engine.Options{MaxLines: 0})
	if !reflect.DeepEqual(a, b) {
		t.Error("MaxLines 0 must be the default")
	}
}

func TestFitHeadCutKeepsTheHead(t *testing.T) {
	c := fixture.Load(t, "git", "git-log-oneline-color")
	uncut := strings.Split(engine.Process(c.Context(), c.Raw, engine.Options{}).Output, "\n")
	head := engine.Process(c.Context(), c.Raw, engine.Options{MaxLines: 20, Cut: engine.CutHead})
	if want := strings.Join(uncut[:18], "\n") + "\n… "; printedLines(head) != 20 || !strings.HasPrefix(head.Output, want) {
		t.Errorf("head -20 of git log: want the 18 newest commits and a gap marker, got:\n%s", head.Output)
	}
	tail := engine.Process(c.Context(), c.Raw, engine.Options{MaxLines: 20, Cut: engine.CutTail})
	if want := " …\n" + strings.Join(uncut[len(uncut)-18:], "\n"); printedLines(tail) != 20 || !strings.HasSuffix(tail.Output, want) {
		t.Errorf("tail -20 of git log: want a gap marker and the 18 oldest commits, got:\n%s", tail.Output)
	}
}

func TestFitOverCapWithinCut(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("row %d: %s", i, strings.Repeat("payload ", 150)))
	}
	lines[12] = "error: row 12 could not be decoded"
	in := strings.Join(lines, "\n")
	res := engine.Process(&engine.Context{Argv: []string{"./dump"}, Exit: 1}, in, engine.Options{MaxLines: 40, MaxChars: 8000})
	if len(res.Output) > 8000 || printedLines(res) > 40 || !res.Lossy || !strings.Contains(res.Output, "error: row 12 could not be decoded") {
		t.Fatalf("%d bytes, %d lines, lossy %v", len(res.Output), printedLines(res), res.Lossy)
	}
}

func TestFitRandom(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	iters := 300
	if testenv.Race {
		iters = 30
	}
	checked := 0
	for iter := 0; iter < iters; iter++ {
		var lines, errs []string
		n := 1 + r.Intn(600)
		for i := 0; i < n; i++ {
			switch k := r.Intn(30); {
			case k == 0:
				e := fmt.Sprintf("error: step %d failed: %s", i, synthWord(r))
				lines = append(lines, e)
				errs = append(errs, e)
			case k == 1:
				lines = append(lines, "")
			case k == 2:
				lines = append(lines, strings.Repeat("ü€", 20+r.Intn(300)))
			default:
				lines = append(lines, synthShapes[r.Intn(len(synthShapes))].line(r, i))
			}
		}
		in := strings.Join(lines, "\n")
		cut := 1 + r.Intn(120)
		opt := engine.Options{MaxLines: cut, Cut: engine.Cut(r.Intn(3))}
		if r.Intn(3) == 0 {
			opt.MaxChars = 2000 + r.Intn(30000)
		}
		c := &engine.Context{Argv: []string{"./job"}, Exit: r.Intn(2), Cwd: "/w", Home: "/home/u"}
		res := engine.Process(c, in, opt)
		clean := textutil.Clean(in)
		overCap := opt.MaxChars > 0 && len(clean) > opt.MaxChars
		if opt.MaxChars > 0 && len(res.Output) > opt.MaxChars {
			t.Fatalf("iter %d: cap %d, view %d bytes", iter, opt.MaxChars, len(res.Output))
		}
		switch {
		case cut < engine.MinFitLines && !overCap:
			if res.Output != clean || res.Lossy {
				t.Fatalf("iter %d: cut %d below MinFitLines must print the output", iter, cut)
			}
			continue
		case cut < engine.MinFitLines:
			continue
		}
		if got := printedLines(res); got > cut {
			t.Fatalf("iter %d: cut %d, lx prints %d lines", iter, cut, got)
		}
		if nLines(clean) <= cut && !overCap && res.Output != clean {
			if uncut := engine.Process(c, in, engine.Options{}); res.Output != uncut.Output {
				t.Fatalf("iter %d: the output fits the cut, yet a different view was printed", iter)
			}
		}
		errBytes := 0
		for _, e := range errs {
			errBytes += len(e) + 30
		}
		if len(errs) > 0 && errorsAloneLines(clean) <= cut-1 && (opt.MaxChars == 0 || errBytes < opt.MaxChars/3) {
			checked++
			for _, e := range errs {
				if !strings.Contains(res.Output, e) {
					t.Fatalf("iter %d: cut %d (errors alone %d lines) lost %q:\n%s", iter, cut, errorsAloneLines(clean), e, res.Output)
				}
			}
		}
	}
	if checked < iters/10 {
		t.Errorf("only %d cases checked error lines", checked)
	}
}
