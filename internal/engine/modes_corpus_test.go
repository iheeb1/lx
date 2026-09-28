package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/tokens"
)

func keptLines(clean, view string) int {
	have := map[string]bool{}
	for _, ln := range strings.Split(view, "\n") {
		have[strings.Join(strings.Fields(ln), " ")] = true
	}
	seen := map[string]bool{}
	n := 0
	for _, ln := range strings.Split(clean, "\n") {
		t := strings.Join(strings.Fields(ln), " ")
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		if have[t] {
			n++
		}
	}
	return n
}

func errorLinesOf(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(s, "\n") {
		t := strings.Join(strings.Fields(ln), " ")
		if t == "" || seen[t] || !engine.IsError(ln) {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func missingFrom(errs []string, view string) []string {
	lines := strings.Split(view, "\n")
	have := make(map[string]bool, len(lines))
	for i, ln := range lines {
		lines[i] = strings.Join(strings.Fields(ln), " ")
		have[lines[i]] = true
	}
	squashed := strings.Join(lines, "\n")
	var miss []string
	for _, e := range errs {
		if !have[e] && !strings.Contains(squashed, e) {
			miss = append(miss, e)
		}
	}
	return miss
}

func errorTokens(s string) int {
	n := 0
	seen := map[string]bool{}
	for _, ln := range strings.Split(s, "\n") {
		t := strings.Join(strings.Fields(ln), " ")
		if t == "" || seen[t] || !engine.IsError(ln) {
			continue
		}
		seen[t] = true
		n += tokens.Count(ln) + 1
	}
	return n
}

func TestModesCorpus(t *testing.T) {
	type stat struct{ checked, differs, errChecked int }
	stats := map[engine.Mode]*stat{}
	modes := []engine.Mode{engine.ModeError, engine.ModeDebug, engine.ModeVerify, engine.ModeMinimal}
	for _, m := range modes {
		stats[m] = &stat{}
	}
	failures := 0
	for _, c := range allCaptures(t) {
		name := c.Category + "/" + c.Name
		if testenv.Race && len(c.Raw)%9 != 0 {
			continue
		}
		clean := c.Clean()
		failed := c.Meta.ExitCode != 0
		if failed {
			failures++
		}
		base := engine.Process(c.Context(), c.Raw, engine.Options{})
		if len(clean)%8 == 0 {
			if a := engine.Process(c.Context(), c.Raw, engine.Options{Mode: engine.ModeAuto}); !reflect.DeepEqual(a, base) {
				t.Errorf("%s: ModeAuto differs from the zero Options", name)
			}
		}
		errs := errorLinesOf(clean)
		missBase := map[string]bool{}
		for _, ln := range missingFrom(errs, base.Output) {
			missBase[ln] = true
		}
		baseKept := keptLines(clean, base.Output)
		baseContent := contentView(c.Context(), clean, base)
		baseErrTokens := -1
		machine := base.Filter == "passthrough" && engine.MachineReadableAny(c.Context())
		views := map[engine.Mode]engine.Result{}
		for _, m := range modes {
			tag := name + " (" + m.String() + ")"
			res := engine.Process(c.Context(), c.Raw, engine.Options{Mode: m})
			views[m] = res
			st := stats[m]
			st.checked++
			if res.Output != base.Output {
				st.differs++
			}
			if res.Mode != m || res.ViewMode != m.View(failed) {
				t.Errorf("%s: result mode %v/%v", tag, res.Mode, res.ViewMode)
			}
			if machine && res.Output != base.Output {
				t.Errorf("%s: machine-readable output changed", tag)
			}
			if res.Lossy {
				if r := engine.Receipt(res, "7"); !strings.Contains(r, " · mode "+m.String()) {
					t.Errorf("%s: receipt %q does not name the mode", tag, r)
				}
			}

			smaller := m.Budget(0, failed) < engine.DefaultBudget
			content := baseContent
			if !content && res.Filter != base.Filter {
				content = contentView(c.Context(), clean, res)
			}
			if !content {
				if baseErrTokens < 0 {
					baseErrTokens = errorTokens(base.Output)
				}
				if !smaller || baseErrTokens <= m.Budget(0, failed) {
					st.errChecked++
					for _, ln := range missingFrom(errs, res.Output) {
						if !missBase[ln] && !longLine(ln) {
							t.Errorf("%s: error line lost (auto keeps it): %q", tag, ln)
						}
					}
				}
			}

			if !smaller {
				if k := keptLines(clean, res.Output); k < baseKept {
					t.Errorf("%s: keeps %d lines of the output, auto %d", tag, k, baseKept)
				}
			}
			switch {
			case m == engine.ModeVerify && !failed && res.OutTokens > base.OutTokens:
				t.Errorf("%s: %d tokens on success, auto %d", tag, res.OutTokens, base.OutTokens)
			case m == engine.ModeMinimal && !machine:
				rc := tokens.Count(engine.Receipt(res, "1000")) + 1
				if res.OutTokens > engine.MinimalBudget+rc {
					t.Errorf("%s: %d tokens (receipt %d)", tag, res.OutTokens, rc)
				}
				if res.Lossy && res.OutTokens+rc >= tokens.Count(clean) {
					t.Errorf("%s: a view of %d tokens and its receipt (%d) cost more than the output (%d)", tag, res.OutTokens, rc, tokens.Count(clean))
				}
			}
		}
		if failed {
			v, e := views[engine.ModeVerify], views[engine.ModeError]
			v.Mode, e.Mode = 0, 0
			if !reflect.DeepEqual(v, e) {
				t.Errorf("%s: verify on a failed run differs from error:\n%s\n--- error:\n%s", name, v.Output, e.Output)
			}
		}
	}
	for _, m := range modes {
		st := stats[m]
		t.Logf("%-7s %d views, %d differ from auto, %d checked for error lines", m, st.checked, st.differs, st.errChecked)
	}
	if !testenv.Race {
		if failures < 100 || stats[engine.ModeMinimal].differs < 50 || stats[engine.ModeVerify].differs < 10 || stats[engine.ModeMinimal].errChecked < 300 {
			t.Errorf("the corpus no longer exercises the modes: %d failures, minimal differs on %d, verify on %d, %d minimal views checked for error lines",
				failures, stats[engine.ModeMinimal].differs, stats[engine.ModeVerify].differs, stats[engine.ModeMinimal].errChecked)
		}
	}
}

func TestModesKeepCaps(t *testing.T) {
	capped, cut := 0, 0
	for _, c := range allCaptures(t) {
		if testenv.Race && len(c.Raw)%13 != 0 {
			continue
		}
		clean := c.Clean()
		overCap, sample := len(clean) > claudeCap, len(clean)%6 == 0
		if !overCap && !sample {
			continue
		}
		if engine.MachineReadableAny(c.Context()) {
			continue
		}

		var base engine.Result
		var errs []string
		var missBase map[string]bool
		baseKept, failed, content := 0, c.Meta.ExitCode != 0, false
		if overCap {
			base = engine.Process(c.Context(), c.Raw, engine.Options{MaxChars: claudeCap})
			content = contentView(c.Context(), clean, base)
			errs, missBase = errorLinesOf(clean), map[string]bool{}
			for _, ln := range missingFrom(errs, base.Output) {
				missBase[ln] = true
			}
			baseKept = keptLines(clean, base.Output)
		}
		for _, m := range []engine.Mode{engine.ModeError, engine.ModeDebug, engine.ModeVerify, engine.ModeMinimal} {
			tag := c.Category + "/" + c.Name + " (" + m.String() + ")"
			if overCap {
				capped++
				res := engine.Process(c.Context(), c.Raw, engine.Options{Mode: m, MaxChars: claudeCap})
				if len(res.Output) > claudeCap {
					t.Errorf("%s: %d bytes over the cap %d", tag, len(res.Output), claudeCap)
				}
				if m.Budget(0, failed) >= engine.DefaultBudget && !content {
					for _, ln := range missingFrom(errs, res.Output) {
						if !missBase[ln] && !longLine(ln) {
							t.Errorf("%s: under the cap, error line lost (auto keeps it): %q", tag, ln)
						}
					}
					if k := keptLines(clean, res.Output); k < baseKept {
						t.Errorf("%s: under the cap, keeps %d lines of the output, auto %d", tag, k, baseKept)
					}
				}
			}
			if sample {
				cut++
				if got := printedLines(engine.Process(c.Context(), c.Raw, engine.Options{Mode: m, MaxLines: 20, Cut: engine.CutTail})); got > 20 {
					t.Errorf("%s: %d lines under a 20-line cut", tag, got)
				}
			}
		}
	}
	t.Logf("%d capped views, %d under a line cut", capped, cut)
	if (capped < 100 || cut < 100) && !testenv.Race {
		t.Errorf("only %d capped views and %d under a line cut checked", capped, cut)
	}
}
