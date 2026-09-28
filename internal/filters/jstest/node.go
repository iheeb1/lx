package jstest

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type nodeFilter struct{}

func (nodeFilter) Name() string { return "node-crash" }

func (nodeFilter) Match(c *engine.Context) bool { return parseInvocation(c).runner == "node" }

var (
	nodeFooterRe = lazyre.New(`^Node\.js v\d+\.\d+\.\d+$`)

	nodeThrowLocRe = lazyre.New(`^(?:node:[\w/]+|file:///\S+|/\S+|[A-Za-z]:\\\S+|\S+\.[cm]?[jt]sx?):\d+$`)
	nodeCaretRe    = lazyre.New(`^\s*\^+\s*$`)
)

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

	var b []string
	for _, ln := range res {
		if strings.TrimSpace(ln) == "" && (len(b) == 0 || b[len(b)-1] == "") {
			continue
		}
		b = append(b, ln)
	}
	return relativize(c, strings.Join(b, "\n")), true
}
