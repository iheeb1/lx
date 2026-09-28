package engine_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/tokens"
)

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func words(line string) []string {
	var out []string
	for i := 0; i < len(line); {
		if !isIdentByte(line[i]) {
			i++
			continue
		}
		j := i
		for j < len(line) && isIdentByte(line[j]) {
			j++
		}
		out = append(out, line[i:j])
		i = j
	}
	return out
}

func identLike(w string) bool {
	if len(w) < 6 || len(w) > 60 {
		return false
	}
	upper, lower, other := false, false, false
	for i := 0; i < len(w); i++ {
		switch c := w[i]; {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		default:
			other = true
		}
	}
	return lower && (upper || other)
}

func hasWord(s, w string) bool {
	for off := 0; ; {
		i := strings.Index(s[off:], w)
		if i < 0 {
			return false
		}
		i += off
		j := i + len(w)
		if (i == 0 || !isIdentByte(s[i-1])) && (j == len(s) || !isIdentByte(s[j])) {
			return true
		}
		off = i + 1
	}
}

func dropped(pre, view string) []string {
	have := map[string]bool{}
	for _, ln := range strings.Split(view, "\n") {
		have[strings.TrimSpace(ln)] = true
	}
	var out []string
	for _, ln := range strings.Split(pre, "\n") {
		if t := strings.TrimSpace(ln); t != "" && !have[t] && !engine.IsError(ln) && len(ln) < 400 {
			out = append(out, ln)
		}
	}
	return out
}

func rareDroppedWord(pre, view string) string {
	lines := strings.Split(pre, "\n")
	cands := dropped(pre, view)
	for k := len(cands) / 2; k < len(cands)+len(cands)/2; k++ {
		for _, w := range words(cands[k%len(cands)]) {
			if !identLike(w) || hasWord(view, w) {
				continue
			}
			n := 0
			for _, ln := range lines {
				if hasWord(ln, w) {
					n++
				}
			}
			if n <= 3 {
				return w
			}
		}
	}
	return ""
}

func focusNote(r engine.Result) string {
	for _, n := range r.Notes {
		if strings.HasPrefix(n, "focus: ") {
			return n
		}
	}
	return ""
}

func lostVsBase(clean, base, view string) []string {
	errs := errorLinesOf(clean)
	missBase := map[string]bool{}
	for _, ln := range missingFrom(errs, base) {
		missBase[ln] = true
	}
	var lost []string
	for _, ln := range missingFrom(errs, view) {
		if !missBase[ln] && !longLine(ln) {
			lost = append(lost, ln)
		}
	}
	return lost
}

func TestFocusCorpus(t *testing.T) {
	const budget = 1200
	checked, kept := 0, 0
	for _, c := range allCaptures(t) {
		if testenv.Race && len(c.Raw)%5 != 0 {
			continue
		}
		if engine.MachineReadableAny(c.Context()) {
			continue
		}
		name := c.Category + "/" + c.Name
		base := engine.Process(c.Context(), c.Raw, engine.Options{Budget: budget})
		if !base.Lossy {
			continue
		}
		pre := engine.Process(c.Context(), c.Raw, engine.Options{Budget: 1 << 30}).Output
		w := rareDroppedWord(pre, base.Output)
		if w == "" {
			continue
		}
		f := &engine.Focus{Terms: []engine.FocusTerm{{Text: w, Weight: 1}}}
		res := engine.Process(c.Context(), c.Raw, engine.Options{Budget: budget, Focus: f})
		checked++
		if lost := lostVsBase(c.Clean(), base.Output, res.Output); len(lost) > 0 {
			t.Errorf("%s (focus %s): error line lost: %q", name, w, lost[0])
		}
		if hasWord(res.Output, w) {
			kept++
			if n := focusNote(res); !strings.HasPrefix("focus: "+w, strings.TrimSuffix(n, "…")) || n == "" {
				t.Errorf("%s: focus %s kept its line but the notes are %q", name, w, res.Notes)
			}
		} else if n := focusNote(res); n != "" {
			t.Errorf("%s: note %q, but %s is not in the view", name, n, w)
		}
		if res.Output == base.Output && len(res.Notes) > 0 {
			t.Errorf("%s: notes %q on an unchanged view", name, res.Notes)
		}
	}
	t.Logf("%d captures checked, a dropped line kept by its focus term in %d", checked, kept)
	if !testenv.Race && (checked < 100 || kept*10 < checked*7) {
		t.Errorf("focus kept the dropped line in only %d of %d captures", kept, checked)
	}
}

func failingNames(clean string) (names, files []string) {
	seen := map[string]bool{}
	for _, ln := range strings.Split(clean, "\n") {
		t := strings.TrimSpace(ln)
		if rest, ok := strings.CutPrefix(t, "--- FAIL: "); ok {
			if n := strings.Fields(rest); len(n) > 0 && !seen[n[0]] {
				seen[n[0]] = true
				names = append(names, n[0])
			}
		}
		for _, w := range strings.Fields(t) {
			w = strings.Trim(w, `"'(),`)
			dot := strings.LastIndexByte(w, '.')
			colon := strings.IndexByte(w, ':')
			if dot > 0 && colon > dot && colon+1 < len(w) && w[colon+1] >= '0' && w[colon+1] <= '9' {
				file := w[:colon]
				if !seen[file] && !strings.Contains(file, "://") {
					seen[file] = true
					files = append(files, file)
				}
			}
		}
	}
	return names, files
}

func TestFocusCorpusFailures(t *testing.T) {
	checked, changed := 0, 0
	for _, c := range allCaptures(t) {
		if c.Meta.ExitCode == 0 || testenv.Race && len(c.Raw)%3 != 0 {
			continue
		}
		clean := c.Clean()
		names, files := failingNames(clean)
		if len(names) == 0 && len(files) == 0 {
			continue
		}
		if len(files) > 3 {
			files = files[len(files)-3:]
		}
		f := &engine.Focus{Files: files}
		for i, n := range names {
			f.Terms = append(f.Terms, engine.FocusTerm{Text: n, Weight: float64(len(names) - i)})
		}
		for _, budget := range []int{800, 0} {
			name := fmt.Sprintf("%s/%s (budget %d)", c.Category, c.Name, budget)
			base := engine.Process(c.Context(), c.Raw, engine.Options{Budget: budget})
			res := engine.Process(c.Context(), c.Raw, engine.Options{Budget: budget, Focus: f})
			checked++
			if lost := lostVsBase(clean, base.Output, res.Output); len(lost) > 0 {
				t.Errorf("%s: error line lost to focus: %q", name, lost[0])
			}
			if res.Output != base.Output {
				changed++
			} else if len(res.Notes) > 0 {
				t.Errorf("%s: notes %q on an unchanged view", name, res.Notes)
			}
			if n := focusNote(res); n != "" && !res.Lossy {
				t.Errorf("%s: note %q without a receipt", name, n)
			}
			if limit := engine.ModeAuto.Budget(budget, true); res.Lossy && res.OutTokens > limit {
				t.Errorf("%s: %d tokens over the budget %d", name, res.OutTokens, limit)
			}
		}
	}
	t.Logf("%d views with the failing tests and stack files as focus, %d changed", checked, changed)
	if !testenv.Race && checked < 100 {
		t.Errorf("only %d views checked", checked)
	}
}

func randomFocus(r *rand.Rand, clean string) *engine.Focus {
	lines := strings.Split(clean, "\n")
	f := &engine.Focus{}
	for k := r.Intn(5); k > 0; k-- {
		ws := words(lines[r.Intn(len(lines))])
		if len(ws) > 0 {
			f.Terms = append(f.Terms, engine.FocusTerm{Text: ws[r.Intn(len(ws))], Weight: r.Float64() * 4})
		}
	}
	_, files := failingNames(clean)
	for k := r.Intn(3); k > 0 && len(files) > 0; k-- {
		f.Files = append(f.Files, files[r.Intn(len(files))])
	}
	if r.Intn(6) == 0 {
		return nil
	}
	return f
}

func TestFocusPressureProperty(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	modes := []engine.Mode{engine.ModeAuto, engine.ModeAuto, engine.ModeError, engine.ModeVerify, engine.ModeMinimal, engine.ModeDebug}
	checked, pressed, focused := 0, 0, 0
	for _, c := range allCaptures(t) {
		if testenv.Race && len(c.Raw)%7 != 0 {
			continue
		}
		clean := c.Clean()
		for round := 0; round < 1+len(c.Raw)%2; round++ {
			opt := engine.Options{Mode: modes[r.Intn(len(modes))]}
			switch r.Intn(4) {
			case 0:
				opt.Budget = 500 + r.Intn(4000)
			case 1:
				opt.MaxChars = claudeCap
			case 2:
				opt.MaxLines, opt.Cut = 5+r.Intn(60), engine.Cut(r.Intn(3))
			}
			base := engine.Process(c.Context(), c.Raw, opt)
			opt.Focus = randomFocus(r, clean)
			opt.Pressure = engine.Pressure{Used: r.Intn(220000), Window: 200000}
			res := engine.Process(c.Context(), c.Raw, opt)
			checked++
			tag := fmt.Sprintf("%s/%s (%+v)", c.Category, c.Name, opt)
			if !contentView(c.Context(), clean, base) && !contentView(c.Context(), clean, res) {
				if len(engine.MissingErrorLines(clean, base.Output)) == 0 {
					for _, ln := range engine.MissingErrorLines(clean, res.Output) {
						if !longLine(ln) {
							t.Errorf("%s: error line missing although every error fits: %q", tag, ln)
							break
						}
					}
				}
				if lost := lostVsBase(clean, base.Output, res.Output); len(lost) > 0 {
					t.Errorf("%s: error line lost: %q", tag, lost[0])
				}
			}
			if opt.Focus.Empty() && opt.Pressure.Budget(1<<20) == 1<<20 && !reflect.DeepEqual(res, base) {
				t.Errorf("%s: no focus and no pressure, yet the result changed", tag)
			}
			if opt.MaxChars > 0 && len(res.Output) > opt.MaxChars {
				t.Errorf("%s: %d bytes over the cap", tag, len(res.Output))
			}
			for _, n := range res.Notes {
				switch {
				case strings.HasPrefix(n, "context "):
					pressed++
					if opt.Pressure.Budget(1<<20) == 1<<20 {
						t.Errorf("%s: pressure note %q below the first tier", tag, n)
					}
				case strings.HasPrefix(n, "focus: "):
					focused++
				default:
					t.Errorf("%s: unknown note %q", tag, n)
				}
			}
		}
	}
	t.Logf("%d views, %d with a pressure note, %d with a focus note", checked, pressed, focused)
}

func TestProcessPressure(t *testing.T) {
	var lines []string
	for i := 0; i < 3000; i++ {
		lines = append(lines, fmt.Sprintf("worker %d: synced shard %d of table events in %dms", i%7, i, 10+i%90))
	}
	raw := strings.Join(lines, "\n")
	ctx := func() *engine.Context { return &engine.Context{Argv: []string{"sync-tool"}} }
	base := engine.Process(ctx(), raw, engine.Options{})
	if !base.Lossy || len(base.Notes) > 0 {
		t.Fatalf("base view: lossy %v, notes %q", base.Lossy, base.Notes)
	}
	for _, c := range []struct {
		used  int
		limit int
		note  string
	}{
		{80000, 0, ""},
		{100000, 6400, "context 50% full"},
		{164000, 4800, "context 82% full"},
		{190000, 3200, "context 95% full"},
	} {
		res := engine.Process(ctx(), raw, engine.Options{Pressure: engine.Pressure{Used: c.used, Window: 200000}})
		if c.limit == 0 {
			if !reflect.DeepEqual(res, base) {
				t.Errorf("%d used: pressure below 50%% changed the view", c.used)
			}
			continue
		}
		if res.OutTokens > c.limit || res.OutTokens < c.limit*3/4 {
			t.Errorf("%d used: %d tokens, want about %d", c.used, res.OutTokens, c.limit)
		}
		if !reflect.DeepEqual(res.Notes, []string{c.note}) {
			t.Errorf("%d used: notes %q", c.used, res.Notes)
		}
		if r := engine.WithNotes(engine.Receipt(res, "3"), res.Notes, 200); !strings.HasSuffix(r, " · full output: lx show 3 · "+c.note+"]") {
			t.Errorf("receipt %q", r)
		}
	}
	small := strings.Join(lines[:80], "\n")
	a := engine.Process(ctx(), small, engine.Options{})
	b := engine.Process(ctx(), small, engine.Options{Pressure: engine.Pressure{Used: 199000, Window: 200000}})
	if tokens.Count(small) > 1500 || !reflect.DeepEqual(a, b) {
		t.Errorf("an output under the smallest budget changed under pressure: %q", b.Notes)
	}
}

func TestProcessPressureNoteOnlyWhenChanged(t *testing.T) {
	var lines []string
	for i := 0; i < 3000; i++ {
		lines = append(lines, fmt.Sprintf("worker %d: synced shard %d of table events in %dms with a long trailing description", i%7, i, 10+i%90))
	}
	raw := strings.Join(lines, "\n")
	p := engine.Pressure{Used: 110000, Window: 200000}
	for _, maxChars := range []int{4000, 12000, 20000, 0} {
		ctx := &engine.Context{Argv: []string{"sync-tool"}}
		base := engine.Process(ctx, raw, engine.Options{MaxChars: maxChars})
		res := engine.Process(ctx, raw, engine.Options{MaxChars: maxChars, Pressure: p})
		switch {
		case res.Output == base.Output && len(res.Notes) > 0:
			t.Errorf("cap %d: notes %q, yet pressure left the view as it was", maxChars, res.Notes)
		case res.Output != base.Output && !reflect.DeepEqual(res.Notes, []string{"context 55% full"}):
			t.Errorf("cap %d: pressure shrank the view, notes %q", maxChars, res.Notes)
		case maxChars == 0 && res.Output == base.Output:
			t.Errorf("fixture: pressure no longer shrinks the uncapped view")
		}
	}
}

func TestProcessPressureKeepsErrors(t *testing.T) {
	var lines []string
	for i := 0; i < 3000; i++ {
		if i%40 == 7 {
			lines = append(lines, fmt.Sprintf("ERROR shard %d: checksum mismatch in segment %d, replica refused the write", i, i*3))
			continue
		}
		lines = append(lines, fmt.Sprintf("worker %d: synced shard %d of table events in %dms", i%7, i, 10+i%90))
	}
	raw := strings.Join(lines, "\n")
	c := &engine.Context{Argv: []string{"sync-tool"}, Exit: 1}
	base := engine.Process(c, raw, engine.Options{})
	res := engine.Process(c, raw, engine.Options{Pressure: engine.Pressure{Used: 195000, Window: 200000}})
	if miss := engine.MissingErrorLines(raw, base.Output); len(miss) > 0 {
		t.Fatalf("fixture: the base view already misses %d error lines", len(miss))
	}
	if miss := engine.MissingErrorLines(raw, res.Output); len(miss) > 0 {
		t.Errorf("pressure dropped %d error lines, e.g. %q", len(miss), miss[0])
	}
	if res.OutTokens >= base.OutTokens {
		t.Errorf("pressure did not shrink the view around the errors: %d vs %d tokens", res.OutTokens, base.OutTokens)
	}
}
