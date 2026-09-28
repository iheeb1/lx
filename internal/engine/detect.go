package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type Detector struct {
	Name   string
	Detect func(out string) bool
	Filter Filter
	Argv   []string
}

const DetectedPrefix = "detected:"

var (
	detMu     sync.RWMutex
	detectors []Detector
)

func RegisterDetector(d Detector) {
	if d.Name == "" || strings.ContainsAny(d.Name, " \t\n") || d.Detect == nil || d.Filter == nil || len(d.Argv) == 0 {
		panic(fmt.Sprintf("engine.RegisterDetector: incomplete detector %q", d.Name))
	}
	detMu.Lock()
	defer detMu.Unlock()
	for _, x := range detectors {
		if x.Name == d.Name {
			panic(fmt.Sprintf("engine.RegisterDetector: duplicate detector %q", d.Name))
		}
	}
	d.Argv = append([]string(nil), d.Argv...)
	detectors = append(detectors, d)
}

func Detectors() []Detector {
	detMu.RLock()
	defer detMu.RUnlock()
	return append([]Detector(nil), detectors...)
}

func DetectedFilter(shape string) Filter {
	name, ok := strings.CutPrefix(shape, DetectedPrefix)
	if !ok {
		return nil
	}
	detMu.RLock()
	defer detMu.RUnlock()
	for _, d := range detectors {
		if d.Name == name {
			return d.Filter
		}
	}
	return nil
}

func Detect(c *Context, clean string) (out, name string, ok bool) {
	if !Detectable(c) {
		return "", "", false
	}
	var locs []string
	for _, d := range Detectors() {
		if !safeDetect(d, clean) {
			continue
		}
		dc := *c
		dc.Argv = append([]string(nil), d.Argv...)
		r, rok, perr := safeApply(d.Filter, &dc, clean)
		if !rok || perr != "" {
			continue
		}
		r = relabelExitNotes(r, clean, d.Argv, commandLabel(c))
		if !ok {
			out, name, ok = r, d.Name, true
			continue
		}
		if locs == nil {
			locs = append(AppLocations(clean), "")
			locs = locs[:len(locs)-1]
		}
		if keepsMoreLocs(c, locs, r, out) {
			out, name = r, d.Name
		}
	}
	return out, name, ok
}

func keepsMoreLocs(c *Context, locs []string, a, b string) bool {
	if len(locs) == 0 || locsLost(locs, a) >= locsLost(locs, b) {
		return false
	}
	budget := c.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	return locsLost(locs, Budget(a, budget, c.Failed(), true)) < locsLost(locs, Budget(b, budget, c.Failed(), true))
}

func locsLost(locs []string, view string) int {
	if len(locs) == 0 {
		return 0
	}
	have := make(map[string]bool)
	for _, m := range FindLocs(view) {
		have[LocKey(m)] = true
	}
	n := 0
	for _, m := range locs {
		if !have[LocKey(m)] {
			n++
		}
	}
	return n
}

func relabelExitNotes(view, clean string, tool []string, cmd string) string {
	if cmd == "" || len(tool) == 0 || !strings.Contains(view, "[lx: ") {
		return view
	}
	labels := []string{strings.Join(tool, " "), filepath.Base(tool[0])}
	lines := strings.Split(view, "\n")
	changed := false
	for i, ln := range lines {
		rest, ok := strings.CutPrefix(ln, "[lx: ")
		if !ok {
			continue
		}
		for _, l := range labels {
			tail, ok := strings.CutPrefix(rest, l+" exit")
			if !ok || l == cmd || !strings.HasPrefix(tail, "ed ") && !strings.HasPrefix(tail, " ") {
				continue
			}
			if !strings.Contains(clean, ln) {
				lines[i] = "[lx: " + cmd + " exit" + tail
				changed = true
			}
			break
		}
	}
	if !changed {
		return view
	}
	return strings.Join(lines, "\n")
}

func commandLabel(c *Context) string {
	argv := c.Argv
	for range MaxPeel {
		inner, _ := Peel(argv)
		if len(inner) == 0 {
			break
		}
		argv = inner
	}
	if len(argv) == 0 {
		return ""
	}
	return filepath.Base(argv[0])
}

func safeDetect(d Detector, s string) (hit bool) {
	defer func() {
		if recover() != nil {
			hit = false
		}
	}()
	return d.Detect(s)
}

func Detectable(c *Context) bool {
	if c == nil || len(c.Argv) == 0 {
		return false
	}
	if f, _ := Resolve(c); f != nil {
		return false
	}
	argv := c.Argv
	for range MaxPeel + 1 {
		if Find(&Context{Argv: argv[:1]}) != nil {
			return false
		}
		if bare := positionalWords(argv); len(bare) > 0 && len(bare) < len(argv) && Find(&Context{Argv: bare}) != nil {
			return false
		}
		inner, _ := Peel(argv)
		if inner == nil {
			break
		}
		argv = inner
	}
	return true
}

func positionalWords(argv []string) []string {
	out := make([]string, 0, len(argv))
	for i, a := range argv {
		if i > 0 && len(a) > 1 && a[0] == '-' {
			continue
		}
		out = append(out, a)
	}
	return out
}

const MaxSignatureLine = 4096

func ScanLines(s, sub string, fn func(line string) bool) {
	for off := 0; off <= len(s); {
		k := off
		if sub != "" {
			i := strings.Index(s[off:], sub)
			if i < 0 {
				return
			}
			k = off + i
		}
		start := strings.LastIndexByte(s[off:k], '\n') + 1 + off
		end := strings.IndexByte(s[k:], '\n')
		if end < 0 {
			end = len(s)
		} else {
			end += k
		}
		if end-start <= MaxSignatureLine && !fn(s[start:end]) {
			return
		}
		off = end + 1
	}
}

func HasLine(s, sub string, match func(line string) bool) bool {
	found := false
	ScanLines(s, sub, func(ln string) bool {
		found = match(ln)
		return !found
	})
	return found
}

func HasLinePrefix(s, prefix string, match func(line string) bool) bool {
	return CountLinePrefix(s, prefix, match, 1) > 0
}

func CountLinePrefix(s, prefix string, match func(line string) bool, limit int) int {
	if prefix == "" {
		return 0
	}
	n := 0
	visit := func(start int) (next int) {
		end := strings.IndexByte(s[start:], '\n')
		if end < 0 {
			end = len(s)
		} else {
			end += start
		}
		if ln := s[start:end]; end-start <= MaxSignatureLine && (match == nil || match(ln)) {
			n++
		}
		return end
	}
	off := 0
	if strings.HasPrefix(s, prefix) {
		off = visit(0)
	}
	needle := "\n" + prefix
	for (limit <= 0 || n < limit) && off < len(s) {
		i := strings.Index(s[off:], needle)
		if i < 0 {
			break
		}
		off = visit(off + i + 1)
	}
	return n
}
