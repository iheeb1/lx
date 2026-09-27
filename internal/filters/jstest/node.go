package jstest

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// nodeFilter condenses `node script.js` output that ends in an uncaught
// exception. Node prints the throw location, the source line and a caret,
// the error, its V8 stack, the error's own properties, and a
// "Node.js vX.Y.Z" footer:
//
//	node:events:487
//	      throw er; // Unhandled 'error' event
//	      ^
//
//	Error: listen EADDRINUSE: address already in use :::3000
//	    at Server.setupListenHandle [as _listen2] (node:net:2009:16)
//	    …
//	Node.js v24.18.0
//
// The view keeps everything except the middle of long stacks: runs of
// node:internal / node_modules frames are folded by engine.FoldStacks (the
// throw site, application frames and the frames next to them stay).
// Program output printed before the crash is kept as is, or reduced by the
// generic reducer when it is long. Output without the footer is not a crash
// this filter knows: it bails.
type nodeFilter struct{}

func (nodeFilter) Name() string { return "node-crash" }

func (nodeFilter) Match(c *engine.Context) bool { return parseInvocation(c).runner == "node" }

var (
	nodeFooterRe = lazyre.New(`^Node\.js v\d+\.\d+\.\d+$`)
	// "node:events:487", "/abs/app.js:13", "file:///abs/app.mjs:4"
	nodeThrowLocRe = lazyre.New(`^(?:node:[\w/]+|file:///\S+|/\S+|[A-Za-z]:\\\S+|\S+\.[cm]?[jt]sx?):\d+$`)
	nodeCaretRe    = lazyre.New(`^\s*\^+\s*$`)
)

// longPreamble: program output before the crash longer than this goes
// through the generic reducer.
const longPreamble = 50

func (nodeFilter) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 0 || !nodeFooterRe.MatchString(lines[last]) {
		return "", false
	}
	// The crash report starts at the throw location (a location line with
	// a caret line at most two lines below), else at the footer's block.
	start := -1
	for i := 0; i < last; i++ {
		if nodeThrowLocRe.MatchString(lines[i]) {
			for k := i + 1; k <= min(i+3, last); k++ {
				if nodeCaretRe.MatchString(lines[k]) {
					start = i
					break
				}
			}
			if start >= 0 {
				break
			}
		}
	}
	if start < 0 {
		start = 0
	}
	var res []string
	if start > longPreamble {
		res = append(res, strings.Split(engine.Generic(c, strings.Join(lines[:start], "\n")), "\n")...)
	} else {
		res = append(res, lines[:start]...)
	}
	res = append(res, engine.FoldStacks(c, lines[start:last+1])...)
	// Collapse blank runs.
	var b []string
	for _, ln := range res {
		if strings.TrimSpace(ln) == "" && (len(b) == 0 || b[len(b)-1] == "") {
			continue
		}
		b = append(b, ln)
	}
	return relativize(c, strings.Join(b, "\n")), true
}
