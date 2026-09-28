package fs

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

var corpusCases = []struct {
	name, filter string

	process string
}{
	{"du-sh-node-modules-sorted", "du", "passthrough"},
	{"du-sh-star", "du", "passthrough"},
	{"find-js-node-modules-noise", "find", "find"},
	{"find-type-f-gin", "find", "find"},
	{"find-type-f-vite", "find", "find"},
	{"ls-R-vite-src", "ls", "ls"},
	{"ls-la-node-modules", "ls", "ls"},
	{"ls-la-repo-root", "ls", "ls"},
}

var extraCases = []struct{ name, filter, process string }{
	{"du-h-recursive", "du", "du"},
	{"du-sk-many", "du", "du"},
	{"find-missing-root", "find", "passthrough"},
	{"find-perm-denied", "find", "passthrough"},
	{"find-type-d", "find", "find"},
	{"ls-R-dot", "ls", "passthrough"},
	{"ls-l-multi-operand", "ls", "ls"},
	{"ls-la-luxon-bin", "ls", "ls"},
	{"ls-la-perms-symlink", "ls", "ls"},
	{"ls-lR-large", "ls", "ls"},
	{"ls-lR-small", "ls", "ls"},
	{"ls-missing", "ls", "passthrough"},

	{"ls-plain", "ls", "passthrough"},

	{"find-maxdepth-2", "find", "find"},

	{"find-name-matches-dirs", "find", "find"},

	{"find-tricky", "find", "passthrough"},

	{"find-abs-argv0", "find", "find"},
	{"ls-R-tricky", "ls", "passthrough"},

	{"ls-la-playground", "ls", "ls"},

	{"ls-lt-head", "ls", "ls"},

	{"du-sort-head", "du", "passthrough"},

	{"ls-la-color", "ls", "ls"},
	{"ls-R-color", "ls", "passthrough"},
}

var bailCases = []struct{ name, filter string }{
	{"ls-la-fr", "ls"},
	{"ls-la-de", "ls"},
	{"ls-la-ja", "ls"},
}

func TestBailCorpus(t *testing.T) {
	for _, tc := range bailCases {
		t.Run(tc.name, func(t *testing.T) {
			fc, err := fixture.Read("testdata/corpus", "fs", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			c := fc.Context()
			f := engine.Find(c)
			if f == nil || f.Name() != tc.filter {
				t.Fatalf("engine.Find = %v, want %s", f, tc.filter)
			}
			if out, ok := f.Apply(c, fc.Clean()); ok {
				t.Fatalf("must bail on unrecognized output, got\n%s", out)
			}
			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 || res.Filter == tc.filter {
				t.Errorf("Process: filter %q, guard added %d", res.Filter, res.GuardAdded)
			}
			t.Logf("bail %-12s process: %s %d → %d tokens", fc.Name, res.Filter, res.RawTokens, res.OutTokens)
		})
	}
}

func TestCorpus(t *testing.T) {
	for _, tc := range corpusCases {
		t.Run(tc.name, func(t *testing.T) {
			runCase(t, fixture.Load(t, "fs", tc.name), tc.filter, tc.process)
		})
	}
}

func TestExtraCorpus(t *testing.T) {
	for _, tc := range extraCases {
		t.Run(tc.name, func(t *testing.T) {
			fc, err := fixture.Read("testdata/corpus", "fs", tc.name)
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
	if !ok {
		t.Fatal("filter bailed on real output")
	}
	fixture.Golden(t, "fs", fc.Name, got)
	got2, _ := f.Apply(c, clean)
	if got2 != got {
		t.Error("not deterministic")
	}

	checkFidelity(t, Effective(c), clean, got)
	if c.Failed() {
		if miss := fixture.LocationsMissing(clean, got); len(miss) > 0 {
			t.Errorf("locations missing: %q", miss)
		}
	}

	res := engine.Process(c, fc.Raw, engine.Options{})
	if res.GuardAdded != 0 {
		t.Errorf("guard re-added %d lines", res.GuardAdded)
	}
	if res.Filter != process {
		t.Errorf("Process used %q, want %q", res.Filter, process)
	}
	raw, out := tokens.Count(fc.Raw), tokens.Count(got)
	t.Logf("savings %-28s %7d → %6d tokens (%5.1f%%)  process: %s %d → %d", fc.Name, raw, out,
		100*(1-float64(out)/float64(max(raw, 1))), res.Filter, res.RawTokens, res.OutTokens)
}

func checkFidelity(t *testing.T, e *engine.Context, clean, got string) {
	t.Helper()
	tool := e.Name()
	for _, ln := range strings.Split(clean, "\n") {
		if NoteHasPrefix(e, ln, tool) && !strings.Contains(got, ln) {
			t.Errorf("diagnostic dropped: %q", ln)
		}
	}
	missing := fixture.ErrorLinesMissing(clean, got)
	switch tool {
	case "find", "fd":
		paths := nonDiag(e, clean)
		if _, dirsOnly := findArgs(e.Args()); dirsOnly {
			for i := range paths {
				paths[i] += "/"
			}
		}
		checkTree(t, got, paths, missing)
	case "ls":
		f := parseLsFlags(e.Args())
		if f.recursive && !f.long {
			paths, _ := lsPathsForTest(clean, f)
			checkTree(t, got, paths, missing)
			return
		}
		if f.recursive && strings.Contains(got, "names only") {
			var rest []string
			for _, m := range missing {
				if !lsRowRe.MatchString(m) {
					rest = append(rest, m)
				}
			}
			checkTree(t, got, lsLongPathsForTest(clean, f), rest)
			return
		}

		for _, ln := range strings.Split(clean, "\n") {
			m := lsRowRe.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			name := m[7]
			if name == "." || name == ".." {
				continue
			}
			if !strings.Contains(got, name) {
				t.Errorf("entry %q dropped", name)
			}
		}
		for _, m := range missing {
			if lsRowRe.MatchString(m) {
				continue
			}
			t.Errorf("error line missing: %q", m)
		}
	case "du":

		rows, kept := 0, 0
		for _, ln := range strings.Split(clean, "\n") {
			if duLineRe.MatchString(ln) {
				rows++
				if strings.Contains("\n"+got+"\n", "\n"+ln+"\n") {
					kept++
				}
			}
		}
		omitted := 0
		if m := regexp.MustCompile(`… (\d+) smaller entr`).FindStringSubmatch(got); m != nil {
			omitted = atoi(m[1])
		}
		if kept+omitted != rows {
			t.Errorf("du rows: %d kept + %d counted != %d", kept, omitted, rows)
		}
		for _, m := range missing {
			if !duLineRe.MatchString(m) {
				t.Errorf("error line missing: %q", m)
			}
		}
	}
}

func nonDiag(e *engine.Context, clean string) []string {
	var out []string
	for _, ln := range strings.Split(clean, "\n") {
		if ln == "" || NoteHasPrefix(e, ln, e.Name()) {
			continue
		}
		out = append(out, ln)
	}
	return out
}

var (
	foldedRe = regexp.MustCompile(`^(.*)/ \[([\d,]+) (files?|entry|entries|director(?:y|ies))(?:: (.*))?\]$`)
	moreRe   = regexp.MustCompile(`^… \+([\d,]+) more (?:files?|entry|entries): (.*)$`)

	moreDirs = regexp.MustCompile(`^… \+[\d,]+ more director(?:y|ies)(?: \[([\d,]+) (?:files?|entry|entries)(?:: .*)?\])?:$`)
)

type parsedTree struct {
	files map[string]bool
	dirs  map[string]bool

	counted map[string]int

	countedDirs map[string]int
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
	return n
}

func parseTree(lines []string) parsedTree {
	pt := parsedTree{files: map[string]bool{}, dirs: map[string]bool{}, counted: map[string]int{}, countedDirs: map[string]int{}}
	type frame struct {
		indent int
		dir    string
	}
	var stack []frame
	dirAt := func(indent int) string {
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].dir
	}
	join := func(dir, name string) string {
		if dir == "" {
			return name
		}
		if dir == "/" {
			return "/" + name
		}
		return dir + "/" + name
	}

	addDir := func(parent, label string) string {
		d := parent
		for _, seg := range strings.Split(label, "/") {
			d = join(d, seg)
			pt.dirs[d] = true
		}
		return d
	}
	listing := 0
	for _, ln := range lines {
		if ln == "" || strings.HasPrefix(ln, "[") {
			continue
		}
		body := strings.TrimLeft(ln, " ")
		indent := len(ln) - len(body)
		if listing > 0 && indent != listing {
			listing = 0
		}
		parent := dirAt(indent)
		if listing > 0 {
			parent = dirAt(indent - 2)
		}
		switch {
		case foldedRe.MatchString(body):
			m := foldedRe.FindStringSubmatch(body)
			d := addDir(parent, m[1])
			if !strings.HasPrefix(m[3], "director") {
				pt.counted[d] += atoi(m[2])
			} else {
				pt.countedDirs[d] += atoi(m[2])
				pt.counted[d] += 0
			}
		case moreDirs.MatchString(body):
			m := moreDirs.FindStringSubmatch(body)
			pt.counted[parent] += atoi(m[1])
			listing = indent + 2
		case listing > 0 && indent == listing:

			for _, nm := range strings.Split(body, "  ") {
				addDir(parent, strings.TrimSuffix(nm, "/"))
			}
			continue
		case moreRe.MatchString(body):
			m := moreRe.FindStringSubmatch(body)
			pt.counted[parent] += atoi(m[1])
		case strings.HasSuffix(body, "/") && !strings.Contains(body, "  "):
			var d string
			switch {
			case body == "/":
				d = "/"
				pt.dirs[d] = true
			case strings.HasPrefix(body, "/"):
				d = "/" + addDir("", strings.Trim(body, "/"))
				pt.dirs[d] = true
			default:
				d = addDir(parent, strings.TrimSuffix(body, "/"))
			}
			stack = append(stack, frame{indent, d})
		default:
			for _, nm := range strings.Split(body, "  ") {
				pt.files[join(parent, nm)] = true
			}
		}
	}
	return pt
}

func normPath(p string) (string, bool) {
	segs, _, ok := splitPath(p)
	if !ok {
		return "", false
	}
	if len(segs) == 1 && segs[0] == "" {
		return "/", true
	}
	return strings.Join(segs, "/"), true
}

func checkTree(t *testing.T, got string, paths, missing []string) {
	t.Helper()
	pt := parseTree(strings.Split(got, "\n"))
	all := map[string]bool{}
	marked := map[string]bool{}
	for _, p := range paths {
		if n, ok := normPath(p); ok {
			all[n] = true
			if strings.HasSuffix(p, "/") {
				marked[n] = true
			}
		}
	}

	isDir := map[string]bool{}
	for p := range all {
		if marked[p] {
			isDir[p] = true
		}
		for d := path.Dir(p); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			isDir[d] = true
		}
	}
	ancestorCount := func(p string) (string, bool) {
		for d := path.Dir(p); ; d = path.Dir(d) {
			if _, ok := pt.counted[d]; ok {
				return d, true
			}
			if d == "." || d == "/" || d == "" {
				return "", false
			}
		}
	}
	counted := map[string]int{}
	countedDirs := map[string]int{}
	for p := range all {
		if isDir[p] {
			if pt.dirs[p] {
				continue
			}
			if d, ok := ancestorCount(p); ok {
				countedDirs[d]++
				continue
			}
			t.Errorf("directory %q not reconstructable from output", p)
			continue
		}
		if pt.files[p] {
			continue
		}
		if d, ok := ancestorCount(p); ok {
			counted[d]++
			continue
		}
		if !pt.dirs[p] {
			t.Errorf("path %q not reconstructable from output", p)
		}
	}
	for d, n := range pt.counted {
		if counted[d] != n {
			t.Errorf("file count for %q says %d, input has %d uncovered files there", d, n, counted[d])
		}
	}
	for d, n := range pt.countedDirs {
		if countedDirs[d] != n {
			t.Errorf("directory count for %q says %d, input has %d there", d, n, countedDirs[d])
		}
	}
	for _, m := range missing {
		if n, ok := normPath(m); ok && (all[n] || isDir[n]) {
			continue
		}
		if n, ok := normPath(strings.TrimSuffix(m, ":")); ok && strings.HasSuffix(m, ":") && (isDir[n] || pt.dirs[n]) {
			continue
		}
		t.Errorf("error line missing: %q", m)
	}
}

func lsPathsForTest(clean string, f lsFlags) ([]string, error) {
	dir := "."
	if len(f.operands) == 1 {
		dir = f.operands[0]
	}
	var out []string
	blank := false
	for i, ln := range strings.Split(clean, "\n") {
		if ln == "" {
			blank = true
			continue
		}

		first := i == 0 && strings.HasSuffix(ln, ":") && (len(f.operands) > 1 || ln == ".:")
		if (blank || first) && strings.HasSuffix(ln, ":") {
			dir = strings.TrimSuffix(ln, ":")
			blank = false
			continue
		}
		blank = false
		out = append(out, joinDir(dir, ln))
	}
	return out, nil
}

func TestTreeRoundTrip(t *testing.T) {
	paths := []string{"./a.go", "./b/c.go", "./b/d/e.go", "./node_modules/x/y.js", "./node_modules/z.js", "/abs/p.txt"}
	pt := NewPathTree(paths, nil)
	lines, capped := pt.Render(0)
	if capped != "" {
		t.Fatalf("capped at target 0: %s", capped)
	}
	got := strings.Join(lines, "\n")
	want := "a.go\n/abs/\n  p.txt\nb/\n  c.go\n  d/\n    e.go\nnode_modules/ [2 files]"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	checkTree(t, got, paths, nil)
}

func TestTreeRootInsideHeavy(t *testing.T) {
	paths := []string{"node_modules/express/index.js", "node_modules/express/lib/router.js",
		"node_modules/express/node_modules/qs/index.js", "src/a.js"}
	pt := NewPathTree(paths, []string{"node_modules/express", "src"})
	lines, _ := pt.Render(0)
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "node_modules/express/\n  index.js") || !strings.Contains(got, "node_modules/ [1 file]") {
		t.Fatalf("root inside heavy dir must not be pruned, nested heavy must:\n%s", got)
	}
}

func TestTreeCaps(t *testing.T) {
	var paths []string
	for i := range 3000 {
		paths = append(paths, fmt.Sprintf("./pkg%d/sub%d/deep/er/file%d.ts", i%30, i%7, i))
	}
	pt := NewPathTree(paths, nil)
	lines, capped := pt.Render(DefaultTreeTarget)
	got := strings.Join(lines, "\n")
	if capped == "" {
		t.Fatal("expected caps on 3000 paths")
	}
	if n := tokens.Count(got); n > DefaultTreeTarget {
		t.Errorf("capped tree still %d tokens", n)
	}
	checkTree(t, got, paths, nil)
}

func TestHugeFind(t *testing.T) {
	var b strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&b, "./src/mod%d/part%d/file_%d.go\n", i%97, i%13, i)
	}
	c := &engine.Context{Argv: []string{"find", ".", "-type", "f"}}
	start := time.Now()
	got, ok := find{}.Apply(c, strings.TrimSpace(b.String()))
	if !ok {
		t.Fatal("bailed")
	}
	if el := time.Since(start); el > testenv.Scale(3*time.Second) {
		t.Errorf("50k paths took %v", el)
	}
	if n := tokens.Count(got); n > 2*DefaultTreeTarget {
		t.Errorf("output %d tokens", n)
	}
	if !strings.HasPrefix(got, "[50,000 paths · large tree:") {
		t.Errorf("header: %q", strings.SplitN(got, "\n", 2)[0])
	}
}

func lsLongPathsForTest(clean string, f lsFlags) []string {
	dir := "."
	if len(f.operands) == 1 {
		dir = f.operands[0]
	}
	var out []string
	for _, ln := range strings.Split(clean, "\n") {
		if strings.HasSuffix(ln, ":") && !lsRowRe.MatchString(ln) {
			dir = strings.TrimSuffix(ln, ":")
			continue
		}
		m := lsRowRe.FindStringSubmatch(ln)
		if m == nil || m[7] == "." || m[7] == ".." {
			continue
		}
		p := joinDir(dir, m[7])
		if m[1] == "d" {
			p += "/"
		}
		out = append(out, p)
	}
	return out
}
