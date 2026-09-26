package golang

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// testJSON renders `go test -json` event streams as the go-test text view:
// events are decoded, attributed exactly by their Package and Test fields
// (so parallel tests never mix), and rendered like go test output without
// -v. Build output events (Go 1.24+) and any non-JSON stderr lines are kept
// verbatim.
//
// It implements engine.Guarded because the error lines it keeps are the
// decoded Output text, never the raw JSON lines; Apply runs the guard
// (guardTest) against the decoded text itself, so the error-line invariant
// still holds.
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

// event is one test2json record (cmd/test2json, plus build events).
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

// DecodeTestJSON returns the text a go test -json stream carries: every
// non-JSON line, and each test's Output assembled into lines (a partial line
// ends where test2json starts a framing line), in the order the lines were
// completed. It is what go test -v would have printed, without the
// interleaving of parallel tests. ok is false when the input holds no
// test2json events.
func DecodeTestJSON(out string) (string, bool) {
	_, decoded, ok := parseJSON(out)
	return decoded, ok
}

type jsonPkg struct {
	seg     *segment
	partial map[string]string // unterminated Output per test
	done    bool
	dec     *strings.Builder
}

// parseJSON builds the run model from a test2json stream. decoded is the
// plain text the stream carries, line by line as the parser saw it (output
// is assembled per test, so parallel tests never mix), for the error guard.
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
	build := map[string]string{} // unterminated build output per ImportPath
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
			// stderr of the go command (build errors before Go 1.24,
			// "go: downloading", …).
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
				// test2json starts every framing line (=== RUN, --- FAIL,
				// the final PASS / FAIL) in a new event, even when the
				// test's previous output did not end in a newline.
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
			continue // only a "start" event
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

// framing reports whether an Output event starts with a line test2json
// frames on its own: a === marker, a --- result, or (package output) the
// final PASS / FAIL or the verdict.
func framing(out string, pkgLevel bool) bool {
	first, _, _ := strings.Cut(out, "\n")
	first = strings.TrimRight(first, " \t\r")
	if matchMarker(first) != nil || matchResult(first) != nil {
		return true
	}
	return pkgLevel && (isBare(first) || isVerdict(first))
}

// feed passes one decoded line to the package's segment and records it in
// the decoded text.
func (p *jsonPkg) feed(ln, test string) {
	ln = strings.TrimRight(ln, " \t\r")
	p.dec.WriteString(ln)
	p.dec.WriteByte('\n')
	p.seg.feedJSON(ln, test)
}

// flush feeds unterminated output, tests in first-seen order.
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

// testDone applies a test's pass / fail / skip event. Its "--- FAIL" line
// normally came first; when none was seen (older test2json, output cut
// short), the line go test would have printed is added, so the outcome is
// never lost or mistaken for a test that was still running.
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

// feedJSON adds one decoded output line whose test is known exactly.
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
