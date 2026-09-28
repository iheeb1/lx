package jstest

import (
	"strings"

	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	npmEchoRe = lazyre.New(`^> (?:@[^/\s]+/)?[^@\s]+@\S+ \S+(?: /\S.*)?$`)

	yarnRunRe  = lazyre.New(`^yarn run v\d+\.\d+\.\d+$`)
	yarnCmdRe  = lazyre.New(`^\$ \S`)
	yarnInfoRe = lazyre.New(`^info Visit https://yarnpkg\.com/\S+ for documentation about this command\.$`)
)

type wrapper struct {
	seen     map[string]bool
	consumed map[int]bool
}

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
			d.sep()
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
