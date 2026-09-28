package golang

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type testJSON struct{}

func (testJSON) Name() string { return "go-test-json" }

func (testJSON) Match(c *engine.Context) bool {
	if !isGo(c) {
		return false
	}
	sub, args := goArgs(c)
	return sub == "test" && flagSet(args, "json") && testShapeFlagsOK(args)
}

func (testJSON) GuardsErrors() bool { return true }

type event struct {
	Action      string
	Package     string
	Test        string
	Output      string
	ImportPath  string
	FailedBuild string
	Elapsed     float64
}

func (testJSON) Apply(c *engine.Context, out string) (string, bool) {
	r, decoded, ok := parseJSON(out)
	if !ok {
		return "", false
	}
	res, ok := r.render(c, runOpts(c))
	if !ok {
		return "", false
	}
	return guardTest(splitLines(decoded), res), true
}

func DecodeTestJSON(out string) (string, bool) {
	_, decoded, ok := parseJSON(out)
	return decoded, ok
}

type jsonPkg struct {
	seg     *segment
	partial map[string]string
	done    bool
	dec     *strings.Builder
}

func parseJSON(out string) (*run, string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return nil, "", false
	}
	r := &run{json: true}
	var dec strings.Builder
	addPre := func(ln string) {
		r.pre = append(r.pre, ln)
		dec.WriteString(ln)
		dec.WriteByte('\n')
	}
	pkgs := map[string]*jsonPkg{}
	var order []string
	build := map[string]string{}
	var buildOrder []string
	events := 0
	get := func(name string) *jsonPkg {
		p := pkgs[name]
		if p == nil {
			p = &jsonPkg{seg: newSegment(), partial: map[string]string{}, dec: &dec}
			p.seg.pkg = name
			p.seg.json = true
			pkgs[name] = p
			order = append(order, name)
		}
		return p
	}
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		var ev event
		if !strings.HasPrefix(t, "{") || json.Unmarshal([]byte(t), &ev) != nil || ev.Action == "" {
			addPre(ln)
			continue
		}
		events++
		switch ev.Action {
		case "build-output":
			if _, ok := build[ev.ImportPath]; !ok {
				buildOrder = append(buildOrder, ev.ImportPath)
			}
			text := build[ev.ImportPath] + ev.Output
			for {
				i := strings.IndexByte(text, '\n')
				if i < 0 {
					break
				}
				addPre(text[:i])
				text = text[i+1:]
			}
			build[ev.ImportPath] = text
			continue
		case "build-fail":
			if rest := build[ev.ImportPath]; rest != "" {
				addPre(rest)
				build[ev.ImportPath] = ""
			}
			continue
		}
		if ev.Package == "" {
			continue
		}
		p := get(ev.Package)
		switch ev.Action {
		case "output":
			text := p.partial[ev.Test]
			if text != "" && framing(ev.Output, ev.Test == "") {
				p.feed(text, ev.Test)
				text = ""
			}
			text += ev.Output
			for {
				i := strings.IndexByte(text, '\n')
				if i < 0 {
					break
				}
				p.feed(text[:i], ev.Test)
				text = text[i+1:]
			}
			p.partial[ev.Test] = text
		case "run":
			if ev.Test != "" {
				p.seg.get(ev.Test).ran = true
			}
		case "pass", "fail", "skip":
			if ev.Test != "" {
				if text := p.partial[ev.Test]; text != "" {
					p.feed(text, ev.Test)
					p.partial[ev.Test] = ""
				}
				p.seg.testDone(ev.Test, ev.Action, ev.Elapsed)
				continue
			}
			p.flush()
			p.done = true
			if ev.Action == "fail" && !strings.HasPrefix(p.seg.verdict, "FAIL") {
				p.seg.jsonFail = true
			}
		}
	}
	if events == 0 {
		return nil, "", false
	}
	for _, name := range order {
		p := pkgs[name]
		p.flush()
		if p.seg.verdict == "" && len(p.seg.items) == 0 && len(p.seg.crash) == 0 && p.seg.markers == 0 && !p.seg.jsonFail {
			continue
		}
		r.segs = append(r.segs, p.seg)
	}
	for _, ip := range buildOrder {
		if rest := build[ip]; rest != "" {
			addPre(rest)
		}
	}
	r.pre, _ = condenseFetch(r.pre)
	return r, dec.String(), true
}

func framing(out string, pkgLevel bool) bool {
	first, _, _ := strings.Cut(out, "\n")
	first = strings.TrimRight(first, " \t\r")
	if matchMarker(first) != nil || matchResult(first) != nil {
		return true
	}
	return pkgLevel && (isBare(first) || isVerdict(first))
}

func (p *jsonPkg) feed(ln, test string) {
	ln = strings.TrimRight(ln, " \t\r")
	p.dec.WriteString(ln)
	p.dec.WriteByte('\n')
	p.seg.feedJSON(ln, test)
}

func (p *jsonPkg) flush() {
	if text := p.partial[""]; text != "" {
		p.feed(text, "")
		p.partial[""] = ""
	}
	for _, t := range p.seg.order {
		if text := p.partial[t.name]; text != "" {
			p.feed(text, t.name)
			p.partial[t.name] = ""
		}
	}
}

func (s *segment) testDone(name, action string, elapsed float64) {
	t := s.get(name)
	t.ran = true
	if t.status != 0 || s.crashing {
		return
	}
	ln := fmt.Sprintf("%s--- %s: %s (%.2fs)", strings.Repeat("    ", strings.Count(name, "/")), strings.ToUpper(action), name, elapsed)
	if m := matchResult(ln); m != nil {
		s.result(ln, m)
	}
}

func (s *segment) feedJSON(ln, test string) {
	if test == "" {
		switch {
		case isVerdict(ln):
			s.setVerdict(ln)
			return
		case isBare(ln):
			s.bare(ln)
			return
		}
	}
	if s.crashing {
		s.addCrash(ln, test)
		return
	}
	if strings.TrimSpace(ln) == "" {
		s.blanks++
		return
	}
	if !s.noCrash && isCrash(ln) {
		s.startCrash(ln, test)
		return
	}
	if m := matchMarker(ln); m != nil {
		s.marker(m[1], m[2])
		s.current = nil
		return
	}
	if m := matchResult(ln); m != nil {
		s.result(ln, m)
		return
	}
	if m := matchGlued(ln); m != nil && test != "" && m[3] == test {
		s.result(ln, m)
		return
	}
	if test == "" || buildLine(ln) {
		s.stray(ln)
		return
	}
	s.activity = true
	t := s.get(test)
	s.addBlanks(&t.out, false)
	t.out = append(t.out, outLine{text: ln, reindent: true})
}
