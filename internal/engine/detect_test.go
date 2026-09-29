package engine_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

var wantDetectors = []string{"cargo-test", "monorepo", "go-test", "jest", "vitest", "mocha", "tsc", "eslint", "pytest"}

func TestDetectorRegistry(t *testing.T) {
	var got []string
	for _, d := range engine.Detectors() {
		got = append(got, d.Name)
		if f := engine.DetectedFilter(engine.DetectedPrefix + d.Name); f != d.Filter {
			t.Errorf("DetectedFilter(%q) = %v, want %v", d.Name, f, d.Filter)
		}
		if _, ok := d.Filter.(engine.Content); ok {
			t.Errorf("%s: a detector's filter must not be a Content filter (its output is status, not data)", d.Name)
		}
		if g, ok := d.Filter.(engine.Guarded); !ok || !g.GuardsErrors() {
			t.Errorf("%s: filter %s is not Guarded; the engine guard would re-add the lines it regroups", d.Name, d.Filter.Name())
		}
	}
	if strings.Join(got, " ") != strings.Join(wantDetectors, " ") {
		t.Errorf("detectors = %q, want %q", got, wantDetectors)
	}
}

func captureTool(t *testing.T, c fixture.Case) (tool string, ok bool) {
	t.Helper()
	p := filepath.Join(fixture.Root(), "testdata", "corpus", c.Category, c.Name+".meta.json")
	if strings.HasPrefix(c.Category, "internal"+string(filepath.Separator)) {
		p = filepath.Join(fixture.Root(), c.Category, c.Name+".meta.json")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	var m struct {
		Tool *string `json:"tool"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	if m.Tool == nil {
		return "", false
	}
	return *m.Tool, true
}

var owners = map[string][]string{
	"go-test": {"go-test"}, "cargo-test": {"cargo-test"}, "jest": {"jest"}, "vitest": {"vitest"},
	"mocha": {"mocha"}, "pytest": {"pytest"}, "tsc": {"tsc"}, "eslint": {"eslint"},
	"npm-test": {"jest", "vitest", "mocha"},
	"make":     {"go-test"},
	"npm-run":  {"tsc", "eslint", "monorepo"},
}

func TestDetectorsZeroFalsePositives(t *testing.T) {
	type input struct {
		name  string
		clean string
		allow map[string]bool
		multi bool
	}
	var inputs []input
	for _, c := range fullCaptures(t) {
		allow, multi := map[string]bool{}, false
		if tool, ok := captureTool(t, c); ok {
			for _, x := range strings.Split(tool, ",") {
				if x != "" {
					allow[x] = true
				}
			}
			multi = len(allow) > 1
		} else if f, _ := engine.Resolve(c.Context()); f != nil {
			for _, d := range owners[f.Name()] {
				allow[d] = true
			}
		}
		inputs = append(inputs, input{c.Category + "/" + c.Name, c.Clean(), allow, multi})
	}
	for _, sh := range synthShapes {
		s, _ := synth(sh, 60000, 5, 1)
		inputs = append(inputs, input{"synthetic/" + sh.name, s, nil, false})
	}
	hits := map[string]int{}
	var bad []string
	relayed := 0
	for _, in := range inputs {
		var fired []string
		for _, d := range engine.Detectors() {
			if !d.Detect(in.clean) {
				continue
			}
			fired = append(fired, d.Name)
			if !in.allow[d.Name] {
				bad = append(bad, fmt.Sprintf("%s: detector %s fired", in.name, d.Name))
				continue
			}
			hits[d.Name]++
		}

		if len(fired) > 1 && !in.multi {
			bad = append(bad, fmt.Sprintf("%s: %d detectors fired: %q", in.name, len(fired), fired))
		}
		if len(fired) == 0 {
			continue
		}

		for _, r := range relays {
			s := r.apply(in.clean)
			relayed++
			for _, d := range engine.Detectors() {
				if d.Detect(s) {
					bad = append(bad, fmt.Sprintf("%s relayed by %s: detector %s fired", in.name, r.name, d.Name))
				}
			}
		}
	}
	if relayed < 100 {
		t.Errorf("only %d relayed variants checked", relayed)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
	for _, name := range wantDetectors {
		if hits[name] < 3 {
			t.Errorf("detector %s recognized only %d captures of its tool", name, hits[name])
		}
	}
	t.Logf("%d inputs, %d relayed variants; hits on own tool: %v", len(inputs), relayed, hits)
}

var relays = []struct {
	name  string
	apply func(string) string
}{
	{"turbo prefix", prefixLines(func(int) string { return "web:test: " })},
	{"docker compose prefix", prefixLines(func(int) string { return "app-1  | " })},
	{"CI timestamp", prefixLines(func(int) string { return "2026-09-01T12:00:00.1234567Z " })},
	{"grep -n", prefixLines(func(i int) string { return fmt.Sprintf("ci.log:%d:", i+1) })},
	{"diff +", prefixLines(func(int) string { return "+" })},
	{"Markdown quote", prefixLines(func(int) string { return "> " })},
	{"indent", prefixLines(func(int) string { return "    " })},
}

func prefixLines(p func(i int) string) func(string) string {
	return func(s string) string {
		lines := strings.Split(s, "\n")
		for i := range lines {
			lines[i] = p(i) + lines[i]
		}
		return strings.Join(lines, "\n")
	}
}

func TestDetectorsAgreeWithNpmTest(t *testing.T) {
	n := 0
	for _, c := range fullCaptures(t) {
		ctx := c.Context()
		f, fc := engine.Resolve(ctx)
		if f == nil || f.Name() != "npm-test" {
			continue
		}
		clean := c.Clean()
		want, ok := f.Apply(fc, clean)
		if !ok {
			continue
		}
		for _, d := range engine.Detectors() {
			if !d.Detect(clean) {
				continue
			}
			dc := *ctx
			dc.Argv = d.Argv
			got, ok := d.Filter.Apply(&dc, clean)
			if !ok || got != want {
				t.Errorf("%s/%s: detector %s disagrees with the npm-test filter", c.Category, c.Name, d.Name)
			}
			n++
			break
		}
	}
	if n < 5 {
		t.Errorf("only %d npm-test captures compared", n)
	}
}

func TestDetectOnlyForUnknownCommands(t *testing.T) {
	runners := 0
	for _, c := range fullCaptures(t) {
		ctx := c.Context()
		clean := c.Clean()
		_, name, ok := engine.Detect(ctx, clean)
		id := c.Category + "/" + c.Name
		if f, _ := engine.Resolve(ctx); f != nil {
			if ok || engine.Detectable(ctx) {
				t.Errorf("%s: filter %s handles %q, but detector %q ran", id, f.Name(), c.Meta.Argv, name)
			}
			continue
		}
		tool, explicit := captureTool(t, c)
		tool, _, _ = strings.Cut(tool, ",")
		if !explicit || !engine.Detectable(ctx) {
			if ok {
				t.Errorf("%s: %q is not detectable, but detector %s ran", id, c.Meta.Argv, name)
			}
			continue
		}
		switch {
		case tool == "" && ok:
			t.Errorf("%s: detector %s fired on output no detector may claim", id, name)
		case tool != "" && (!ok || name != tool):
			t.Errorf("%s: detected %q (ok=%v), want %s", id, name, ok, tool)
		case tool != "":
			runners++
		}
	}
	if runners < 10 {
		t.Errorf("only %d runner captures detected", runners)
	}
}

func TestDetectable(t *testing.T) {
	for cmd, want := range map[string]bool{

		"just test": true, "task lint": true, "rake test": true, "composer test": true, "mise run test": true,
		"turbo run test": true, "nx test app": true, "npx nx run app:test": true, "cargo nextest run": true,
		"deno test": true, "npm run ci": true, "npm run testonly": true, "sh scripts/ci.sh": true,
		"./scripts/test.sh": true, "timeout 600 just test": true, "docker compose run app go test ./...": true,

		"go test ./...": false, "npm test": false, "npm run test:unit": false, "bun test": false, "make test": false,
		"pytest -x": false, "uv run pytest": false, "npx jest": false, "node scripts/test.js": false,
		"python run_tests.py": false, "cargo test": false, "npm run build": false,

		"pytest --co": false, "pytest --fixtures": false, "python -m pytest --setup-plan": false,
		"go test -list .": false, "go test -bench=. ./...": false, "jest --listTests": false,
		"npx jest --listTests": false, "vitest bench": false, "npx vitest list": true,
		"mocha --dry-run": false, "npx mocha --dry-run test/": false, "cargo test --no-run": false,
		"tsc --listFiles": false, "uv run pytest --co": false,
	} {
		if got := engine.Detectable(&engine.Context{Argv: strings.Fields(cmd)}); got != want {
			t.Errorf("Detectable(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestDetectCheap(t *testing.T) {
	big := fixture.Load(t, "search", "grep-rn-minified-lines").Clean()
	if len(big) < 1<<20 {
		t.Fatalf("capture is only %d bytes", len(big))
	}
	var near strings.Builder
	for i := 0; i < 50000; i++ {
		switch i % 10 {
		case 0:
			fmt.Fprintf(&near, "Tests: %d of something\n", i)
		case 1:
			fmt.Fprintf(&near, "  %d passing tests (not mocha)\n", i)
		case 2:
			fmt.Fprintf(&near, "src/a%d.ts(%d,3): warning TS%d is not a tsc line\n", i, i, i)
		case 3:
			fmt.Fprintf(&near, "ok  \tpkg%d\n", i)
		case 4:
			fmt.Fprintf(&near, "=== test session starts %d\n", i)
		case 5:
			fmt.Fprintf(&near, " Test Files  %d\n", i)
		case 6:
			fmt.Fprintf(&near, "running %d tests from x\n", i)
		default:
			fmt.Fprintf(&near, "✖ %d problems  error  in line %d\n", i, i)
		}
	}
	ctx := &engine.Context{Argv: []string{"just", "test"}}
	for name, s := range map[string]string{"minified grep": big, "near misses": near.String()} {
		best := time.Hour
		for range 3 {
			start := time.Now()
			for _, d := range engine.Detectors() {
				if d.Detect(s) {
					t.Errorf("%s: detector %s fired", name, d.Name)
				}
			}
			if _, _, ok := engine.Detect(ctx, s); ok {
				t.Errorf("%s: Detect fired", name)
			}
			best = min(best, time.Since(start))
		}
		limit := testenv.Scale(20 * time.Millisecond)
		if name == "near misses" {
			limit = testenv.Scale(100 * time.Millisecond)
		}
		if best > limit {
			t.Errorf("%s (%d bytes): detection took %v, want < %v", name, len(s), best, limit)
		}
		t.Logf("%s: %v", name, best)
	}
}

func TestProcessHonorsDetectedFilter(t *testing.T) {
	const pending = " (pending LEAD EDIT from detectors? engine.Process must honor engine.DetectedFilter(shape))"
	n := 0
	for _, c := range fullCaptures(t) {
		tool, explicit := captureTool(t, c)
		tool, _, _ = strings.Cut(tool, ",")
		ctx := c.Context()
		if !explicit || tool == "" || !engine.Detectable(ctx) {
			continue
		}
		n++
		id := c.Category + "/" + c.Name
		res := engine.Process(ctx, c.Raw, engine.Options{})
		if res.Filter == "passthrough" || res.Filter == "normalize" {
			continue
		}
		if res.Filter != engine.DetectedPrefix+tool {
			t.Errorf("%s: filter %q, want %q%s", id, res.Filter, engine.DetectedPrefix+tool, pending)
			continue
		}
		if res.GuardAdded != 0 {
			t.Errorf("%s: the engine guard re-added %d lines to a Guarded filter's view%s", id, res.GuardAdded, pending)
		}
		if !res.Lossy {
			t.Errorf("%s: a detected view must be stored (lossy)", id)
		}
		if tool == "monorepo" {
			continue // prefixes removed: internal/filters/ci checks the regrouped lines
		}
		clean := c.Clean()
		if miss := engine.ErrorMessagesMissing(clean, res.Output); len(miss) > 0 && !benignMissing(miss) {
			t.Errorf("%s: error messages missing: %q", id, miss)
		}
	}
	if n < 10 {
		t.Errorf("only %d runner captures", n)
	}
}

func benignMissing(miss []string) bool {
	for _, m := range miss {
		switch m {
		case "Summary of all failing tests", "assert not result.exception", "error handling", "error-pages",
			"on failure", "when an error occurs", "when error occurs in response handler",
			"at error (node_modules/supertest/lib/test.js:335:15)":
		default:
			return false
		}
	}
	return true
}

func FuzzDetect(f *testing.F) {
	for _, name := range []string{"just-test-cargo-fail"} {
		c, err := fixture.Read(filepath.Join(fixture.Root(), "internal", "filters", "build", "testdata"), "detect", name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(c.Clean())
	}
	for _, s := range []string{
		"ok  \tpkg\t0.1s\n--- FAIL: TestX (0.00s)\nFAIL\tpkg\t0.2s",
		"Test Suites: 1 failed, 1 total\nTests:       1 failed, 1 total",
		" Test Files  1 failed (1)\n   Duration  1.2s",
		"  3 passing (4ms)\n  1 failing",
		"a.ts(1,2): error TS2322: x",
		"/w/a.js\n  1:2  error  x  rule\n\n✖ 1 problem (1 error, 0 warnings)",
		"=== test session starts ===\n=== 1 failed in 0.1s ===",
		"running 1 test\ntest result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out",
		"", "\n\n", "ok  \t", "✖ ",
	} {
		f.Add(s)
	}
	ctx := &engine.Context{Argv: []string{"just", "test"}, Exit: 1, Cwd: "/w", Home: "/h"}
	f.Fuzz(func(t *testing.T, s string) {
		for _, d := range engine.Detectors() {
			if d.Detect(s) != d.Detect(s) {
				t.Fatalf("%s: Detect is not deterministic", d.Name)
			}
		}
		clean := textutil.Clean(s)
		out, name, ok := engine.Detect(ctx, clean)
		out2, name2, ok2 := engine.Detect(ctx, clean)
		if out != out2 || name != name2 || ok != ok2 {
			t.Fatal("Detect is not deterministic")
		}
		g, shape := engine.GenericShape(ctx, s)
		if tokens.Count(g) > tokens.Count(s) {
			t.Fatalf("GenericShape grew the output (%s)", shape)
		}
		if ok && shape != engine.DetectedPrefix+name && tokens.Count(out) <= tokens.Count(s) {
			t.Fatalf("detected view of %s not used (shape %q)", name, shape)
		}
	})
}

func ownToolCaptures(t *testing.T) (ids, tools, cleans []string) {
	t.Helper()
	for _, c := range fullCaptures(t) {
		clean := c.Clean()
		for _, d := range engine.Detectors() {
			if d.Detect(clean) {
				ids, tools, cleans = append(ids, c.Category+"/"+c.Name), append(tools, d.Name), append(cleans, clean)
				break
			}
		}
	}
	if len(ids) < 100 {
		t.Fatalf("only %d captures recognized", len(ids))
	}
	return ids, tools, cleans
}

func TestDetectedViewKeepsRunnerLines(t *testing.T) {
	before := []string{
		"==> lint", "internal/db/conn.go", "[warn] Code style issues found in 2 files. Run Prettier with --write to fix.",
		"Would reformat: src/app/models.py", "make: *** [check] Error 1", "M  package-lock.json",
	}
	after := []string{
		"==> coverage", "src/unformatted.ts", "error: Recipe `test` failed on line 3 with exit code 1",
		"2 files would be reformatted, 14 files would be left unchanged.",
		"Coverage for lines (71.2%) does not meet global threshold (80%)", "diff --git a/x b/x",
		"FAILED: step three", "rake aborted!", "Command failed with exit code 1.",
	}
	ids, _, cleans := ownToolCaptures(t)
	views, step := 0, 1
	if testenv.Race {
		step = 5
	}
	for i := 0; i < len(cleans); i += step {
		clean := cleans[i]
		for _, mode := range []string{"before", "after", "both"} {
			in, want := clean, []string(nil)
			if mode != "after" {
				in, want = strings.Join(before, "\n")+"\n"+in, append(want, before...)
			}
			if mode != "before" {
				in, want = in+"\n"+strings.Join(after, "\n"), append(want, after...)
			}
			for _, exit := range []int{0, 1} {
				ctx := &engine.Context{Argv: []string{"sh", "ci.sh"}, Exit: exit, Cwd: "/home/user/src/app", Home: "/home/user"}
				out, name, ok := engine.Detect(ctx, in)
				if !ok {
					continue
				}
				views++
				have := map[string]bool{}
				for _, ln := range strings.Split(out, "\n") {
					have[strings.Join(strings.Fields(ln), " ")] = true
				}
				for _, w := range want {
					if !have[strings.Join(strings.Fields(w), " ")] {
						t.Errorf("%s (%s, exit %d) via %s: runner line %q dropped", ids[i], mode, exit, name, w)
					}
				}
			}
		}
	}
	if views < 500/step {
		t.Errorf("only %d detected views checked", views)
	}
}

func TestDetectedConcatenatedReports(t *testing.T) {
	ids, tools, cleans := ownToolCaptures(t)
	ctx := &engine.Context{Argv: []string{"nx", "run-many", "-t", "test"}, Exit: 1, Cwd: "/home/user/src/app", Home: "/home/user"}

	cache := map[int]map[string]bool{}
	alone := func(i int) map[string]bool {
		if m, ok := cache[i]; ok {
			return m
		}
		s := cleans[i]
		out, _, ok := engine.Detect(ctx, s)
		if !ok {
			out = ""
		}
		m := map[string]bool{}
		for _, x := range engine.ErrorMessagesMissing(s, out) {
			m[x] = true
		}
		for _, x := range engine.AppLocationsMissing(s, out) {
			m[x] = true
		}
		cache[i] = m
		return m
	}
	var pairs [][2]int
	step := 2
	if testenv.Race {
		step = 8
	}

	richest, richLocs := map[string]int{}, map[string]int{}
	for i := range cleans {
		if n := len(engine.AppLocations(cleans[i])); n > richLocs[tools[i]] || richLocs[tools[i]] == 0 {
			richest[tools[i]], richLocs[tools[i]] = i, max(n, 1)
		}
		if i%step != 0 {
			continue
		}
		for j := i + 1; j < len(cleans); j++ {
			if tools[j] == tools[i] {
				pairs = append(pairs, [2]int{i, j}, [2]int{j, i})
				break
			}
		}
	}
	for ta, a := range richest {
		for tb, b := range richest {
			if ta != tb {
				pairs = append(pairs, [2]int{a, b})
			}
		}
	}
	checked, cross := 0, map[string]bool{}
	for _, p := range pairs {
		a, b := p[0], p[1]
		in := "> nx run a:test\n" + cleans[a] + "\n\n> nx run b:test\n" + cleans[b]
		out, name, ok := engine.Detect(ctx, in)
		if !ok {
			continue
		}
		checked++
		if tools[a] != tools[b] {
			cross[tools[a]+"+"+tools[b]] = true
		}
		ok1, ok2 := alone(a), alone(b)
		lost := engine.AppLocationsMissing(in, out)
		check := engine.ErrorMessagesMissing(in, out)
		if tools[a] == tools[b] {
			check = append(check, lost...)
		}
		for _, m := range check {
			if !ok1[m] && !ok2[m] {
				t.Errorf("%s + %s via %s: %q lost", ids[a], ids[b], name, m)
			}
		}
		budgeted := func(v string) int {
			return len(engine.AppLocationsMissing(in, engine.Budget(v, engine.DefaultBudget, true, true)))
		}
		for _, d := range engine.Detectors() {
			if d.Name == name || !d.Detect(in) {
				continue
			}
			dc := *ctx
			dc.Argv = d.Argv
			v, ok := d.Filter.Apply(&dc, in)
			if !ok {
				continue
			}
			if n := len(engine.AppLocationsMissing(in, v)); n < len(lost) && budgeted(v) < budgeted(out) {
				t.Errorf("%s + %s: the %s view drops %d app locations (%d after the budget), the %s view %d (%d): the wrong view won",
					ids[a], ids[b], name, len(lost), budgeted(out), d.Name, n, budgeted(v))
			}
		}
	}
	if checked < 150/step || len(cross) < len(wantDetectors)*(len(wantDetectors)-1)*3/4 {
		t.Errorf("only %d concatenated reports checked (%d pairs of different tools)", checked, len(cross))
	}
}

func TestDetectedExitNoteNamesCommand(t *testing.T) {
	ids, tools, cleans := ownToolCaptures(t)
	relabeled := 0
	for i, clean := range cleans {
		for k, argv := range [][]string{{"just", "test"}, {"timeout", "600", "just", "test"}} {

			in := clean + "\nerror: Recipe `test` failed on line 4 with exit code 3"
			if k == 1 {
				in = clean + "\n==> coverage gate: lines 71.2% < 80%"
			}
			ctx := &engine.Context{Argv: argv, Exit: 3, Cwd: "/home/user/src/app", Home: "/home/user"}
			out, name, ok := engine.Detect(ctx, in)
			if !ok {
				continue
			}
			var d engine.Detector
			for _, x := range engine.Detectors() {
				if x.Name == name {
					d = x
				}
			}
			tool := filepath.Base(d.Argv[0])
			direct, ok := d.Filter.Apply(&engine.Context{Argv: d.Argv, Exit: 3, Cwd: ctx.Cwd, Home: ctx.Home}, in)
			if !ok {
				t.Fatalf("%s: the %s filter rendered the output through Detect but bails on it", ids[i], name)
			}
			for _, ln := range strings.Split(out, "\n") {
				if strings.HasPrefix(ln, "[lx: "+tool+" exit") || strings.HasPrefix(ln, "[lx: "+strings.Join(d.Argv, " ")+" exit") {
					t.Errorf("%s via %s under %q: note names the tool: %q", ids[i], name, argv, ln)
				}
			}
			if strings.Contains(direct, "[lx: "+tool+" exit") {
				relabeled++
				if !strings.Contains(out, "[lx: just exit") {
					t.Errorf("%s via %s under %q: the note does not name just", ids[i], tools[i], argv)
				}
			}
		}
	}
	if relabeled < 10 {
		t.Errorf("only %d views had an exit note to relabel", relabeled)
	}
}
