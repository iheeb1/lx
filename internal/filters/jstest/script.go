package jstest

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"
)

// Package-manager wrapper noise around a test script's output.

var (
	// npm / pnpm lifecycle echo: "> luxon@3.7.2 test" (pnpm adds the path),
	// followed by "> jest --coverage".
	npmEchoRe = lazyre.New(`^> (?:@[^/\s]+/)?[^@\s]+@\S+ \S+(?: /\S.*)?$`)
	// yarn v1: "yarn run v1.22.19" then "$ jest --ci"; the trailing hint.
	yarnRunRe  = lazyre.New(`^yarn run v\d+\.\d+\.\d+$`)
	yarnCmdRe  = lazyre.New(`^\$ \S`)
	yarnInfoRe = lazyre.New(`^info Visit https://yarnpkg\.com/\S+ for documentation about this command\.$`)
)

// wrapper tracks the "> pkg@x script" / "> command" pairs already shown so
// each is kept once.
type wrapper struct {
	seen     map[string]bool
	consumed map[int]bool // "> command" lines already handled with their echo
}

// handle consumes wrapper noise at line i, reporting how many lines it used
// (0 when line i is not wrapper output).
func (w *wrapper) handle(d *doc, i int) int {
	ln := d.in[i]
	if w.seen == nil {
		w.seen = map[string]bool{}
		w.consumed = map[int]bool{}
	}
	if w.consumed[i] {
		return 1
	}
	switch {
	case npmEchoRe.MatchString(ln):
		n := 1
		if i+1 < len(d.in) && strings.HasPrefix(d.in[i+1], "> ") {
			n = 2
			w.consumed[i+1] = true
		}
		key := strings.Join(d.in[i:i+n], "\n")
		if !w.seen[key] {
			w.seen[key] = true
			d.sep() // npm workspaces: one echo per package run
			for k := i; k < i+n; k++ {
				d.keep(k)
			}
			d.sep()
		}
		return n
	case yarnRunRe.MatchString(ln), yarnInfoRe.MatchString(ln):
		return 1
	case yarnCmdRe.MatchString(ln) && i > 0 && yarnRunRe.MatchString(d.in[i-1]):
		d.keep(i)
		return 1
	}
	return 0
}
