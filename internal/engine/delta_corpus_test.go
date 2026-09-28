package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/testenv"
)

func TestDeltaCorpus(t *testing.T) {
	checked, used := 0, 0
	for _, fc := range fullCaptures(t) {
		if testenv.Race && len(fc.Raw)%5 != 0 {
			continue
		}
		c := fc.Context()
		f, fctx := engine.Resolve(c)
		id, ok := f.(engine.Identities)
		if !ok {
			continue
		}
		name := fc.Category + "/" + fc.Name
		clean := fc.Clean()
		for _, n := range []int{len(clean) / 3, len(clean) / 2, len(clean) - 1} {
			id.Items(fctx, clean[:max(n, 0)])
		}
		checked++

		view := engine.Process(c, fc.Raw, engine.Options{}).Output
		prev := engine.Prev{Ref: "lx show 1", TurnsAgo: 1, Exit: c.Exit, Raw: fc.Raw}
		if out, ok := engine.Rerun(c, prev, fc.Raw, view); ok && !strings.HasPrefix(out, "[lx: output identical to lx show 1 (1 turn ago") {
			t.Errorf("%s: identical rerun gave:\n%s", name, out)
		}

		lines := strings.Split(fc.Raw, "\n")
		prev.Raw = strings.Join(lines[:len(lines)/2], "\n")
		out, ok := engine.Rerun(c, prev, fc.Raw, view)
		if !ok {
			continue
		}
		used++
		pc := *c
		old, _ := engine.ItemsOf(&pc, strings.Join(strings.Split(clean, "\n")[:len(lines)/2], "\n"))
		had := map[string]bool{}
		for _, it := range old {
			had[it.Key] = true
		}
		cur, _ := engine.ItemsOf(c, clean)
		for _, it := range cur {
			if !had[it.Key] && !strings.Contains(out, strings.TrimRight(it.Block, "\n")) {
				t.Errorf("%s: new %q is not shown in full:\n%s", name, it.Key, out)
			}
		}
	}
	t.Logf("%d captures with identities, %d half-run deltas", checked, used)
	want := 60
	if testenv.Race {
		want /= 5
	}
	if checked < want {
		t.Errorf("only %d captures reached a filter with identities", checked)
	}
}

func TestDeltaCorpusOneNewFailure(t *testing.T) {
	tried, used := 0, 0
	for _, fc := range fullCaptures(t) {
		c := fc.Context()
		clean := fc.Clean()
		items, ok := engine.ItemsOf(c, clean)
		if !ok || len(items) < 3 || c.Exit == 0 {
			continue
		}
		lines := strings.Split(clean, "\n")
		at := func(it engine.Item, from int) int {
			name := ""
			for _, ln := range strings.Split(it.Block, "\n") {
				if name = strings.TrimSpace(ln); !strings.HasPrefix(name, "FAIL ") {
					break
				}
			}
			for i := from; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) == name {
					return i
				}
			}
			return -1
		}
		k := len(items) / 2
		i := at(items[k], 0)
		j := at(items[k+1], i+1)
		if i < 0 || j < 0 || j-i > 300 {
			continue
		}
		prev := strings.Join(append(append([]string(nil), lines[:i]...), lines[j:]...), "\n")
		if old, _ := engine.ItemsOf(c, prev); slices.ContainsFunc(old, func(it engine.Item) bool { return it.Key == items[k].Key }) {
			continue
		}
		tried++
		view := engine.Process(c, fc.Raw, engine.Options{}).Output
		out, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 1", TurnsAgo: 1, Exit: c.Exit, Raw: prev}, fc.Raw, view)
		if !ok {
			continue
		}
		used++
		if !strings.Contains(out, strings.TrimRight(items[k].Block, "\n")) {
			t.Errorf("%s/%s: the new %q is not shown in full:\n%s", fc.Category, fc.Name, items[k].Key, out)
		}
		fixed := engine.Process(c, prev, engine.Options{}).Output
		if out, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 1", TurnsAgo: 1, Exit: c.Exit, Raw: fc.Raw}, prev, fixed); ok &&
			!strings.Contains(out, "\nfixed: ") && !strings.Contains(out, "\nchanged:\n") {
			t.Errorf("%s/%s: %q is gone, yet the delta does not say so:\n%s", fc.Category, fc.Name, items[k].Key, out)
		}
	}
	t.Logf("%d captures with one failure taken out of the earlier run, %d deltas", tried, used)
	if used < tried/3 {
		t.Errorf("only %d of %d reruns with one new failure got a delta", used, tried)
	}
}
