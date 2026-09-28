package search

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/fs"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

var corpusCases = []struct {
	name, filter string

	process string
}{
	{"grep-no-match", "search", "passthrough"},
	{"grep-rn-go-identifier", "search", "search"},
	{"grep-rn-luxon-duration", "search", "search"},
	{"grep-rn-minified-lines", "search", "search"},
	{"grep-rn-node-modules-noise", "search", "search"},
	{"rg-context", "search", "search"},
	{"rg-files", "rg-files", "rg-files"},
	{"rg-identifier", "search", "search"},
}

func TestCorpus(t *testing.T) {
	for _, tc := range corpusCases {
		t.Run(tc.name, func(t *testing.T) {
			runCase(t, fixture.Load(t, "search", tc.name), tc.filter, tc.process)
		})
	}
}

var extraCases = []struct{ name, filter, process string }{
	{"git-grep-n", "search", "search"},
	{"grep-E-alternation", "search", "search"},
	{"grep-binary", "search", "passthrough"},
	{"grep-explicit-node-modules", "search", "search"},
	{"grep-missing-dir", "search", "search"},
	{"grep-n-single-file", "search", "passthrough"},
	{"grep-r-no-numbers", "search", "search"},
	{"grep-rn-context", "search", "search"},
	{"grep-rni-panic", "search", "search"},
	{"rg-C-no-n", "search", "search"},
	{"rg-missing-path", "search", "search"},
	{"rg-n-single-file", "search", "passthrough"},
	{"rg-no-n", "search", "search"},
	{"rg-vimgrep", "search", "search"},

	{"git-grep-rev", "search", "search"},
	{"grep-color-always", "search", "search"},
	{"grep-o", "search", "search"},
	{"grep-rn-sorted", "search", "search"},
	{"rg-column", "search", "search"},

	{"grep-rn-crlf", "search", "normalize"},
	{"grep-v-conf", "search", "passthrough"},

	{"grep-abs-argv0-missing", "search", "passthrough"},
	{"rg-abs-argv0", "search", "passthrough"},
	{"rg-color", "search", "normalize"},
}

var bailCases = []struct{ name, filter string }{
	{"grep-rn-colon-path", "search"},
}

func TestBailCorpus(t *testing.T) {
	for _, tc := range bailCases {
		t.Run(tc.name, func(t *testing.T) {
			fc, err := fixture.Read("testdata/corpus", "search", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			c := fc.Context()
			f := engine.Find(c)
			if f == nil || f.Name() != tc.filter {
				t.Fatalf("engine.Find = %v, want %s", f, tc.filter)
			}
			if out, ok := f.Apply(c, fc.Clean()); ok {
				t.Fatalf("must bail, got\n%s", out)
			}
			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 || res.Filter == tc.filter {
				t.Errorf("Process: filter %q, guard added %d", res.Filter, res.GuardAdded)
			}
		})
	}
}

func TestExtraCorpus(t *testing.T) {
	for _, tc := range extraCases {
		t.Run(tc.name, func(t *testing.T) {
			fc, err := fixture.Read("testdata/corpus", "search", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			runCase(t, fc, tc.filter, tc.process)
		})
	}
}

func runCase(t *testing.T, fc fixture.Case, filter, process string) {
	t.Helper()
	c := fc.Context()
	f := engine.Find(c)
	if f == nil || f.Name() != filter {
		t.Fatalf("engine.Find = %v, want %s (argv %q)", f, filter, c.Argv)
	}
	clean := fc.Clean()
	got, ok := f.Apply(c, clean)
	res := engine.Process(c, fc.Raw, engine.Options{})
	if res.GuardAdded != 0 {
		t.Errorf("guard re-added %d lines", res.GuardAdded)
	}
	if res.Filter != process {
		t.Errorf("Process used %q, want %q", res.Filter, process)
	}
	if strings.TrimSpace(clean) == "" {
		if ok {
			t.Fatalf("empty output must bail, got %q", got)
		}
		return
	}
	if !ok {
		t.Fatal("filter bailed on real output")
	}
	fixture.Golden(t, "search", fc.Name, got)
	if got2, _ := f.Apply(c, clean); got2 != got {
		t.Error("not deterministic")
	}
	e := fs.Effective(c)
	if filter == "rg-files" {
		checkFiles(t, clean, got)
	} else {
		checkMatches(t, e, clean, got)
	}

	if c.Failed() {
		if miss := fixture.LocationsMissing(clean, ungroup(got)); len(miss) > 0 {
			t.Errorf("locations missing: %q", miss)
		}
	}
	raw, out := tokens.Count(fc.Raw), tokens.Count(got)
	t.Logf("savings %-28s %7d → %6d tokens (%5.1f%%)  process: %s %d → %d", fc.Name, raw, out,
		100*(1-float64(out)/float64(max(raw, 1))), res.Filter, res.RawTokens, res.OutTokens)
}

type inMatch struct {
	path, num, text string
	match           bool
}

var (
	testMatchRe   = regexp.MustCompile(`^(.+?):(\d+):(.*)$`)
	testContextRe = regexp.MustCompile(`^(.+?)-(\d+)-(.*)$`)
	testBareRe    = regexp.MustCompile(`^(\d+)([:-])(.*)$`)
	testNoteRe    = regexp.MustCompile(`^(?:grep|rg|fatal|error): |^Binary file .* matches$`)
)

type testShape struct{ withFile, numbered, column, context bool }

func shapeOf(e *engine.Context) testShape {
	t, args := tool(e)
	o := parseOpts(t, e.Name(), args)
	return testShape{withFile: o.withFile >= 0, numbered: o.numbered, column: o.column, context: o.context}
}

func parseInput(clean string, sh testShape) (ms []inMatch, notes []string) {
	lines := strings.Split(clean, "\n")
	if !sh.withFile {
		for _, ln := range lines {
			switch m := testBareRe.FindStringSubmatch(ln); {
			case ln == "" || ln == "--":
			case testNoteRe.MatchString(ln):
				notes = append(notes, ln)
			case sh.numbered && m != nil:
				ms = append(ms, inMatch{"", m[1], m[3], m[2] == ":"})
			default:
				ms = append(ms, inMatch{"", "", ln, true})
			}
		}
		return ms, notes
	}
	matchRe := testMatchRe
	if !sh.numbered {
		matchRe = regexp.MustCompile(`^([^:]+):(\s*)(.*)$`)
	}
	cands := map[string]bool{}
	for _, ln := range lines {
		if testNoteRe.MatchString(ln) {
			continue
		}
		if m := matchRe.FindStringSubmatch(ln); m != nil {
			cands[m[1]] = true
		}
	}

	known := map[string]bool{}
	for p := range cands {
		bogus := false
		for k := range cands {
			if k != p && strings.HasPrefix(p, k+"-") {
				bogus = true
			}
		}
		if !bogus {
			known[p] = true
		}
	}
	for _, ln := range lines {
		if ln == "" || ln == "--" {
			continue
		}
		if testNoteRe.MatchString(ln) {
			notes = append(notes, ln)
			continue
		}
		if m := matchRe.FindStringSubmatch(ln); m != nil && known[m[1]] {
			if sh.numbered {
				text := m[3]
				if sh.column {
					text = regexp.MustCompile(`^\d+:`).ReplaceAllString(text, "")
				}
				ms = append(ms, inMatch{m[1], m[2], text, true})
			} else {
				ms = append(ms, inMatch{m[1], "", m[2] + m[3], true})
			}
			continue
		}
		found := false
		for k := range known {
			if !strings.HasPrefix(ln, k+"-") {
				continue
			}
			rest := ln[len(k)+1:]
			if !sh.numbered {
				ms = append(ms, inMatch{k, "", rest, false})
				found = true
				break
			}
			if i := strings.Index(rest, "-"); i > 0 {
				if _, err := strconv.Atoi(rest[:i]); err == nil {
					ms = append(ms, inMatch{k, rest[:i], rest[i+1:], false})
					found = true
					break
				}
			}
		}
		if !found {
			notes = append(notes, "UNPARSED "+ln)
		}
	}
	return ms, notes
}

var (
	outEntryRe = regexp.MustCompile(`^(\d+)([:-])(.*)$`)
	outMoreRe  = regexp.MustCompile(`^(?:  )?… \+(\d+) more in this file$`)
	outHistRe  = regexp.MustCompile(`^(.+): ([\d,]+) match(?:es)?$`)
	outHeavyRe = regexp.MustCompile(`^(.+/): ([\d,]+) match(?:es)? in ([\d,]+) files?: (.*)$`)
	winRe      = regexp.MustCompile(`^(?:…\[\+(\d+) chars\]… )?(.*?)(?: …\[\+(\d+) chars\]…)?$`)
)

func atoiT(s string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
	return n
}

func checkMatches(t *testing.T, e *engine.Context, clean, got string) {
	t.Helper()
	sh := shapeOf(e)
	in, notes := parseInput(clean, sh)
	for _, n := range notes {
		if strings.HasPrefix(n, "UNPARSED ") {
			t.Fatalf("test could not parse input line %q", n)
		}
	}
	byKey := map[string][]inMatch{}
	count := map[string]int{}
	for _, m := range in {
		k := m.path + "\x00" + m.num
		byKey[k] = append(byKey[k], m)
		if m.match {
			count[m.path]++
		}
	}
	binary := 0
	for _, n := range notes {
		if strings.HasPrefix(n, "Binary file ") {
			binary++
			if binary > maxNotes {
				continue
			}
		}
		if !strings.Contains(got, n) {
			t.Errorf("diagnostic dropped: %q", n)
		}
	}
	shown := map[string]int{}
	counted := map[string]int{}
	heavyCounted := map[string]int{}
	cur := ""
	colPrefix := regexp.MustCompile(`^\d+:`)

	check := func(num, marker, text string) {
		if sh.column && marker == ":" {
			if !colPrefix.MatchString(text) {
				t.Errorf("column dropped from %q (line %s)", text, num)
			}
			text = colPrefix.ReplaceAllString(text, "")
		}
		w := winRe.FindStringSubmatch(text)
		body := strings.TrimSpace(w[2])
		for _, orig := range byKey[cur+"\x00"+num] {
			if !strings.Contains(orig.text, body) {
				continue
			}
			if marker != "" && (marker == ":") != orig.match {
				continue
			}
			if w[1] == "" && w[3] == "" && strings.TrimSpace(orig.text) != body {
				continue
			}
			if orig.match {
				shown[cur]++
			}
			return
		}
		t.Errorf("shown entry %q (file %q, line %q) does not match an input line", text, cur, num)
	}
	inHist := false
	for _, ln := range strings.Split(got, "\n") {
		_, isPath := count[ln]
		switch {
		case strings.HasPrefix(ln, "[") && strings.HasSuffix(ln, "by match count]"):
			inHist = true
		case inHist && outHistRe.MatchString(ln):
			m := outHistRe.FindStringSubmatch(ln)
			counted[m[1]] += atoiT(m[2])
		case ln == "" || strings.HasPrefix(ln, "[") || ln == "  --" || ln == "--":
		case testNoteRe.MatchString(ln) || strings.HasPrefix(ln, "… +") && strings.Contains(ln, "binary files matched"):
		case sh.withFile && isPath:
			cur = ln
		case sh.withFile && nativeLine(ln, count) != "":
			p := nativeLine(ln, count)
			rest := ln[len(p)+1:]
			cur = p
			if sh.numbered {
				m := outEntryRe.FindStringSubmatch(rest)
				if m == nil {
					t.Errorf("bad one-line hit %q", ln)
					continue
				}
				check(m[1], m[2], m[3])
			} else {
				check("", ":", rest)
			}
		case sh.numbered && outEntryRe.MatchString(ln):
			m := outEntryRe.FindStringSubmatch(ln)
			check(m[1], m[2], m[3])
		case outMoreRe.MatchString(ln):
			counted[cur] += atoiT(outMoreRe.FindStringSubmatch(ln)[1])
		case outHeavyRe.MatchString(ln):
			m := outHeavyRe.FindStringSubmatch(ln)
			heavyCounted[m[1]] += atoiT(m[2])
		case !sh.numbered:
			text := strings.TrimPrefix(ln, "  ")
			marker := ""
			if sh.context && (strings.HasPrefix(text, ":") || strings.HasPrefix(text, "-")) {
				marker, text = text[:1], text[1:]
			}
			check("", marker, text)
		default:
			t.Errorf("unparsed line %q", ln)
		}
	}
	heavyIn := map[string]int{}
	for p, n := range count {
		if root := heavyRoot(p); root != "" {
			if _, ok := heavyCounted[root]; ok {
				heavyIn[root] += n
				continue
			}
		}
		if shown[p]+counted[p] != n {
			t.Errorf("%q: %d shown + %d counted != %d matches", p, shown[p], counted[p], n)
		}
	}
	for root, n := range heavyCounted {
		if heavyIn[root] != n {
			t.Errorf("%s: summary says %d matches, input has %d", root, n, heavyIn[root])
		}
	}
	for _, m := range fixture.ErrorLinesMissing(clean, got) {
		if !testNoteRe.MatchString(m) {
			continue
		}
		t.Errorf("error line missing: %q", m)
	}
}

func checkFiles(t *testing.T, clean, got string) {
	t.Helper()
	for _, m := range fixture.ErrorLinesMissing(clean, got) {
		if strings.HasPrefix(m, "rg: ") {
			t.Errorf("diagnostic dropped: %q", m)
		}
	}
	if !strings.HasPrefix(got, "[") {
		t.Errorf("missing count header")
	}
}

func nativeLine(ln string, count map[string]int) string {
	best := ""
	for p := range count {
		if len(p) > len(best) && strings.HasPrefix(ln, p+":") {
			best = p
		}
	}
	return best
}

func ungroup(got string) string {
	var b strings.Builder
	cur := ""
	for _, ln := range strings.Split(got, "\n") {
		switch m := outEntryRe.FindStringSubmatch(ln); {
		case ln == "" || strings.HasPrefix(ln, "[") || strings.HasPrefix(ln, "…"):
			cur = ""
		case m != nil && cur != "":
			b.WriteString(cur + ":" + m[1] + m[2] + m[3] + "\n")
			continue
		case !strings.Contains(ln, ": ") && !strings.HasPrefix(ln, " "):
			cur = ln
		}
		b.WriteString(ln + "\n")
	}
	return b.String()
}
