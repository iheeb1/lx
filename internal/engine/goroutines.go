package engine

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"slices"
	"strings"
)

var (
	goroutineHdrRe = lazyre.New(`^goroutine (\d+)(?: [^\[]*)? \[([^\]]*)\]:$`)
	goOffsetRe     = lazyre.New(` \+0x[0-9a-f]+$`)
	goCreatedInRe  = lazyre.New(` in goroutine \d+$`)
)

type goroutineBlock struct {
	header string
	state  string
	frames []string
	sig    string
	lib    bool
	top    string
	create string
}

func GroupGoroutines(lines []string) ([]string, bool) {
	var hdrs []int
	for i, ln := range lines {
		if goroutineHdrRe.MatchString(ln) {
			hdrs = append(hdrs, i)
		}
	}
	if len(hdrs) < 2 {
		return nil, false
	}
	var (
		blocks []*goroutineBlock
		stray  []string
		end    int
	)
	for k, h := range hdrs {
		m := goroutineHdrRe.FindStringSubmatch(lines[h])
		b := &goroutineBlock{header: lines[h], state: m[2]}
		j := h + 1
		for j < len(lines) {
			ln := lines[j]
			if j+1 < len(lines) && ln != "" && ln[0] != '\t' && ln[0] != ' ' && goFileRe.MatchString(lines[j+1]) {
				b.frames = append(b.frames, ln, lines[j+1])
				j += 2
				continue
			}
			if strings.TrimSpace(ln) == "...additional frames elided..." {
				b.frames = append(b.frames, ln)
				j++
				continue
			}
			break
		}
		blocks = append(blocks, b)
		end = j

		if k+1 < len(hdrs) {
			for _, ln := range lines[j:hdrs[k+1]] {
				if strings.TrimSpace(ln) != "" {
					stray = append(stray, ln)
				}
			}
		}
	}
	for _, b := range blocks {
		b.summarize()
	}

	out := make([]string, 0, len(lines)/4)
	out = append(out, lines[:hdrs[0]]...)
	first := blocks[0]
	out = append(out, first.header)
	out = append(out, first.frames...)

	type group struct {
		members []*goroutineBlock
		states  []string
	}
	var order []*group
	bySig := map[string]*group{}
	for _, b := range blocks[1:] {
		g := bySig[b.sig]
		if g == nil {
			g = &group{}
			bySig[b.sig] = g
			order = append(order, g)
		}
		g.members = append(g.members, b)
		if !slices.Contains(g.states, b.state) {
			g.states = append(g.states, b.state)
		}
	}
	count := func(g *group) string {
		if len(g.members) == 1 {
			return "1 goroutine"
		}
		return fmt.Sprintf("%d goroutines", len(g.members))
	}
	for _, g := range order {
		rep := g.members[0]
		if rep.lib {
			continue
		}
		out = append(out, "")
		if len(g.members) == 1 {
			out = append(out, rep.header)
		} else {
			out = append(out, fmt.Sprintf("%s [%s]:", count(g), strings.Join(g.states, ", ")))
		}
		out = append(out, FoldStacks(nil, rep.frames)...)
	}

	var libOrder []*group
	byTop := map[string]*group{}
	for _, g := range order {
		rep := g.members[0]
		if !rep.lib {
			continue
		}
		k := rep.top + "\x00" + rep.create
		m := byTop[k]
		if m == nil {
			m = &group{}
			byTop[k] = m
			libOrder = append(libOrder, m)
		}
		m.members = append(m.members, g.members...)
		for _, st := range g.states {
			if !slices.Contains(m.states, st) {
				m.states = append(m.states, st)
			}
		}
	}
	if len(libOrder) > 0 {
		out = append(out, "")
	}
	for _, g := range libOrder {
		rep := g.members[0]
		s := fmt.Sprintf("%s [%s] in library code: %s", count(g), strings.Join(g.states, ", "), rep.top)
		if rep.create != "" {
			s += " … created by " + rep.create
		}
		out = append(out, s)
	}
	if len(stray) > 0 {
		out = append(out, "")
		out = append(out, stray...)
	}
	out = append(out, lines[end:]...)
	return out, true
}

func (b *goroutineBlock) summarize() {
	var sig strings.Builder
	b.lib = true
	for i := 0; i < len(b.frames); i++ {
		fn := b.frames[i]
		if i+1 >= len(b.frames) || !goFileRe.MatchString(b.frames[i+1]) {
			sig.WriteString(fn)
			sig.WriteByte('\n')
			continue
		}
		file := b.frames[i+1]
		i++
		name := goCreatedInRe.ReplaceAllString(goFuncName(fn), "")
		sig.WriteString(name)
		sig.WriteByte('@')
		sig.WriteString(goOffsetRe.ReplaceAllString(file, ""))
		sig.WriteByte('\n')
		if strings.HasPrefix(name, "created by ") {
			b.create = strings.TrimPrefix(name, "created by ")
		} else if b.top == "" {
			b.top = name
		}
		f := stackFrame{start: 0, end: 2}
		classifyFrame([]string{fn, file}, &f, "")
		if !f.lib {
			b.lib = false
		}
	}
	b.sig = sig.String()
}

func goFuncName(s string) string {
	if strings.HasPrefix(s, "created by ") {
		return s
	}
	if !strings.HasSuffix(s, ")") {
		return s
	}
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}
