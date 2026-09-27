package discover

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/hook"
)

// sliceStage is one head/tail/cat stage reading a rewritten command's
// output (lx rewrites a pipeline only when every later stage is one of
// these).
type sliceStage struct {
	cat  bool // passes everything through
	tail bool
	n    int  // lines kept
	ok   bool // a plain line count (not -c bytes, tail +N, head -n -N, …)
}

// downstream returns the stages that read target's output: the commands
// right after it that are head, tail or cat without a file operand.
// (Inspection.Commands is flat; a head that names a file reads the file,
// so it belongs to another pipeline or list.)
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

// commandIndex finds target (one of Inspection.Targets) in cmds.
func commandIndex(cmds [][]string, target []string) int {
	if len(target) == 0 {
		return -1
	}
	for i, c := range cmds {
		if len(c) > 0 && &c[0] == &target[0] { // the same segment
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

// parseStage parses head/tail/cat reading stdin. ok is false for any other
// command and for one with a file operand.
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
			return sliceStage{}, false // cat FILE
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
			st.ok = false // +N (from line N), -N (all but), 1k, …: not modeled
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
				return sliceStage{}, false // operands follow: reads files
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
			st.ok = false // -c5, -z, -r, +5, …
		default:
			return sliceStage{}, false // head FILE
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

// mergesStderr: stderr goes down the pipe too (2>&1 or |&). Without it,
// stderr bypassed the agent's head/tail — but lx prints the whole view,
// stderr included, on stdout, so with lx the cut applies to it as well.
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

// slicing is how an agent's head/tail stages cut a rewritten command's
// output (cmd | head -N): with lx they cut lx's view and receipt instead.
type slicing struct {
	sliced bool // some rewritten command's output goes through head or tail
	// replayable: the command line runs one rewritten command and the
	// transcript shows the cut kept its whole output (fewer lines than N),
	// so replaying lx and the same cut is exact.
	replayable bool
	stages     []sliceStage // the stages reading the (first) target
}

// sliceOf finds the head/tail stages after in's targets and whether the
// cut can be replayed exactly from what o recorded.
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
	// Replay only a single command's cut: the recording is that command's
	// output alone.
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
	// The lines the cut received: all of them with 2>&1, else stdout (when
	// the transcript recorded the streams apart; if not, the total is an
	// upper bound, which only makes fewer runs replayable).
	stream := o.full
	if o.split && !mergesStderr(command) {
		stream = o.stdout
	}
	// Otherwise the cut may have dropped lines: the whole output is unknown.
	sl.replayable = lineCount(stream) < limit
	return sl
}

// replayLimit mirrors internal/runner: lx records a command's stdout and
// stderr apart, and replays them as written, for outputs up to 1 MiB.
const replayLimit = 1 << 20

// streamsKept: lx prints this view the way the command wrote it — a
// passthrough view is replayed to stdout and stderr as recorded — so a
// head/tail after lx cuts exactly what it cut without lx. Every other view
// is printed on stdout, stderr included, where the cut applies to all of it.
func streamsKept(v *view) bool {
	return v.res.Filter == "passthrough" && len(v.raw) <= replayLimit
}

// shown returns what the agent reads of lx's output through the cut: the
// view's lines plus the receipt line, the lines [lo, hi) of them kept.
// nView is the number of view lines (the receipt, when there is one, is
// line nView).
func (sl slicing) shown(v *view) (lines []string, nView, lo, hi int) {
	if v.res.Output != "" {
		lines = strings.Split(strings.TrimRight(v.res.Output, "\n"), "\n")
	}
	nView = len(lines)
	if v.receipt != "" {
		lines = append(lines, v.receipt) // the last line lx prints
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

// sliceStats counts a rewritten command whose output an agent's head/tail
// cut, and — when replaying lx is exact — what the same cut would do to
// lx's view: cut view lines, drop the receipt (head), drop an error line
// the agent saw.
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
		return // the cut sees what it saw without lx
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
