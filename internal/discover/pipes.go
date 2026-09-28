package discover

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/hook"
)

type sliceStage struct {
	cat  bool
	tail bool
	n    int
	ok   bool
}

func downstream(in hook.Inspection, target []string) []sliceStage {
	i := commandIndex(in.Commands, target)
	if i < 0 {
		return nil
	}
	var out []sliceStage
	for _, argv := range in.Commands[i+1:] {
		st, ok := parseStage(argv)
		if !ok {
			break
		}
		out = append(out, st)
	}
	return out
}

func commandIndex(cmds [][]string, target []string) int {
	if len(target) == 0 {
		return -1
	}
	for i, c := range cmds {
		if len(c) > 0 && &c[0] == &target[0] {
			return i
		}
	}
	for i, c := range cmds {
		if slices.Equal(c, target) {
			return i
		}
	}
	return -1
}

func hasSlice(stages []sliceStage) bool {
	for _, st := range stages {
		if !st.cat {
			return true
		}
	}
	return false
}

func parseStage(argv []string) (sliceStage, bool) {
	if len(argv) == 0 {
		return sliceStage{}, false
	}
	switch filepath.Base(argv[0]) {
	case "cat":
		for _, a := range argv[1:] {
			if a == "-" || strings.HasPrefix(a, "-") && len(a) > 1 && a != "--" {
				continue
			}
			return sliceStage{}, false
		}
		return sliceStage{cat: true, ok: true}, true
	case "head":
	case "tail":
	default:
		return sliceStage{}, false
	}
	st := sliceStage{tail: filepath.Base(argv[0]) == "tail", n: 10, ok: true}
	count := func(v string) {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") {
			st.ok = false
			return
		}
		st.n = n
	}
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				return sliceStage{}, false
			}
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				st.ok = false
				continue
			}
			i++
			count(args[i])
		case strings.HasPrefix(a, "--lines="):
			count(a[len("--lines="):])
		case strings.HasPrefix(a, "-n"):
			count(a[2:])
		case a == "-c" || a == "--bytes":
			i++
			st.ok = false
		case a == "-q" || a == "--quiet" || a == "--silent" || a == "-v" || a == "--verbose":
		case len(a) > 1 && a[0] == '-' && isDigits(a[1:]):
			count(a[1:])
		case a == "-":
		case strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+"):
			st.ok = false
		default:
			return sliceStage{}, false
		}
	}
	return st, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func mergesStderr(command string) bool {
	return strings.Contains(command, "2>&1") || strings.Contains(command, "|&")
}

func lineCount(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

type slicing struct {
	sliced bool

	replayable bool
	stages     []sliceStage
}

func sliceOf(in hook.Inspection, command string, o recording) slicing {
	var sl slicing
	for i, t := range in.Targets {
		st := downstream(in, t)
		if hasSlice(st) {
			sl.sliced = true
			if i == 0 {
				sl.stages = st
			}
		}
	}

	if !sl.sliced || len(in.Targets) != 1 || !o.complete {
		return sl
	}
	limit := -1
	for _, st := range sl.stages {
		if st.cat {
			continue
		}
		if !st.ok {
			return sl
		}
		if limit < 0 || st.n < limit {
			limit = st.n
		}
	}

	stream := o.full
	if o.split && !mergesStderr(command) {
		stream = o.stdout
	}

	sl.replayable = lineCount(stream) < limit
	return sl
}

const replayLimit = 1 << 20

func streamsKept(v *view) bool {
	return v.res.Filter == "passthrough" && len(v.raw) <= replayLimit
}

func (sl slicing) shown(v *view) (lines []string, nView, lo, hi int) {
	if v.res.Output != "" {
		lines = strings.Split(strings.TrimRight(v.res.Output, "\n"), "\n")
	}
	nView = len(lines)
	if v.receipt != "" {
		lines = append(lines, v.receipt)
	}
	lo, hi = 0, len(lines)
	for _, st := range sl.stages {
		if st.cat || st.n >= hi-lo {
			continue
		}
		if st.tail {
			lo = hi - st.n
		} else {
			hi = lo + st.n
		}
	}
	return lines, nView, lo, hi
}

func sliceStats(sl slicing, v *view, r *Report) {
	if !sl.sliced {
		return
	}
	r.Sliced++
	if !sl.replayable {
		return
	}
	r.SlicedReplayable++
	if streamsKept(v) {
		return
	}
	lines, nView, lo, hi := sl.shown(v)
	if lo > 0 || hi < nView {
		r.SlicedViewCut++
	}
	if v.receipt != "" && (nView < lo || nView >= hi) {
		r.SlicedReceiptLost++
	}
	for i, ln := range lines[:nView] {
		if i >= lo && i < hi {
			continue
		}
		t := strings.TrimSpace(ln)
		if t != "" && engine.IsError(ln) && strings.Contains(v.cleanRaw(), t) {
			r.SlicedErrorsCut++
			break
		}
	}
}
