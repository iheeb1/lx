package engine_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

const claudeCap = 26800

func allCaptures(t *testing.T) []fixture.Case {
	t.Helper()
	return raceSample(fullCaptures(t))
}

// fullCaptures is every capture, also under -race, for the cheap tests
// that check how many captures they reached.
func fullCaptures(t *testing.T) []fixture.Case {
	t.Helper()
	cases := fixture.All(t)
	root := fixture.Root()
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "internal", "filters"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "fuzz" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".txt") && strings.Contains(p, string(filepath.Separator)+"testdata"+string(filepath.Separator)) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, f := range files {
		dir := filepath.Dir(f)
		c, err := fixture.Read(filepath.Dir(dir), filepath.Base(dir), strings.TrimSuffix(filepath.Base(f), ".txt"))
		if err != nil {
			t.Fatal(err)
		}
		c.Category = strings.TrimPrefix(dir, root+string(filepath.Separator))
		cases = append(cases, c)
	}
	if len(cases) < 500 {
		t.Fatalf("only %d captures found", len(cases))
	}
	return cases
}

// raceSample keeps every 4th capture under -race: the corpus sweeps are
// single-threaded, and the full set runs without -race.
func raceSample(cases []fixture.Case) []fixture.Case {
	if !testenv.Race {
		return cases
	}
	var out []fixture.Case
	for i, c := range cases {
		if i%4 == 0 {
			out = append(out, c)
		}
	}
	return out
}

func errorBytes(s string) int {
	n := 0
	seen := map[string]bool{}
	for _, ln := range strings.Split(s, "\n") {
		t := strings.Join(strings.Fields(ln), " ")
		if t == "" || seen[t] || !engine.IsError(ln) {
			continue
		}
		seen[t] = true
		n += len(ln) + 1
	}
	return n
}

func contentView(c *engine.Context, clean string, res engine.Result) bool {
	if f := engine.Find(c); f != nil && f.Name() == res.Filter {
		if ct, ok := f.(engine.Content); ok && ct.IsContent() {
			return true
		}
	}
	if res.Filter == "generic" {
		_, shape := engine.GenericShape(c, clean)
		return shape == "json" || shape == "paths"
	}
	return false
}

func faithful(c *engine.Context, res engine.Result) bool {
	f := engine.Find(c)
	if f == nil || f.Name() != res.Filter {
		return false
	}
	fa, ok := f.(engine.Faithful)
	return ok && fa.Faithful(c)
}

func TestCharCapCorpus(t *testing.T) {
	over := map[int]int{}
	n := 0
	for _, c := range allCaptures(t) {
		name := c.Category + "/" + c.Name
		clean := c.Clean()
		if testenv.Race && len(clean) <= claudeCap {

			continue
		}
		base := engine.Process(c.Context(), c.Raw, engine.Options{})
		n++
		if len(base.Output) > claudeCap {
			over[0]++
		}
		var missBase map[string]bool
		eb := -1
		caps := []int{claudeCap, 9800}
		if testenv.Race {
			caps = caps[:1]
		}
		for _, maxChars := range caps {
			res := engine.Process(c.Context(), c.Raw, engine.Options{MaxChars: maxChars})
			if res.Filter == "passthrough" && engine.MachineReadable(c.Context()) {
				continue
			}
			if len(res.Output) > maxChars {
				t.Errorf("%s: cap %d: view is %d bytes", name, maxChars, len(res.Output))
				over[maxChars]++
			}
			if res.Output == base.Output {
				continue
			}
			if len(clean) <= maxChars && len(base.Output) <= maxChars {
				t.Errorf("%s: cap %d changed a view that already fit", name, maxChars)
			}
			if res.Output != clean && !res.Lossy && !faithful(c.Context(), res) {
				t.Errorf("%s: cap %d: a reduced view must be lossy", name, maxChars)
			}
			if maxChars != claudeCap && contentView(c.Context(), clean, res) {
				continue
			}
			if eb < 0 {
				eb = errorBytes(clean)
			}
			if eb >= maxChars/2 {
				continue
			}
			if missBase == nil {
				missBase = map[string]bool{}
				for _, ln := range engine.MissingErrorLines(clean, base.Output) {
					missBase[ln] = true
				}
			}
			for _, ln := range engine.MissingErrorLines(clean, res.Output) {
				if !missBase[ln] {
					t.Errorf("%s: cap %d (error lines %d bytes) lost %q", name, maxChars, eb, ln)
				}
			}
		}
	}
	t.Logf("%d captures; views over %d bytes: %d uncapped, %d with MaxChars %d",
		n, claudeCap, over[0], over[claudeCap], claudeCap)
}

type synthShape struct {
	name string
	argv []string
	line func(r *rand.Rand, i int) string
	err  func(k int) string
}

var synthWords = strings.Fields(`the of and to in is for that with on as by this from are be or a it we
	can use not all new one may has was but if its also each when you your into see run set
	configuration repository dependency implementation documentation performance compatibility
	initialization authentication serialization transformation environment deployment middleware
	subscription notification specification optimization parallelism infrastructure observability`)

var synthLong = synthWords[len(synthWords)-18:]

func synthWord(r *rand.Rand) string     { return synthWords[r.Intn(len(synthWords))] }
func synthLongWord(r *rand.Rand) string { return synthLong[r.Intn(len(synthLong))] }

var synthShapes = []synthShape{
	{"prose", []string{"./gen-docs"}, func(r *rand.Rand, i int) string {
		var ws []string
		for j := 0; j < 8+r.Intn(10); j++ {
			ws = append(ws, synthWord(r))
		}
		return strings.Join(ws, " ") + "."
	}, func(k int) string { return fmt.Sprintf("ERROR: chapter %d failed to render: template not found", k) }},
	{"ls-R", []string{"ls", "-R"}, func(r *rand.Rand, i int) string {
		if i%12 == 0 {
			return ""
		}
		if i%12 == 1 {
			return fmt.Sprintf("./%s/%s%d/%s:", synthWord(r), synthWord(r), r.Intn(1000), synthWord(r))
		}
		return fmt.Sprintf("%s_%s%d.%s", synthWord(r), synthWord(r), r.Intn(100000), []string{"go", "md", "ts", "json"}[r.Intn(4)])
	}, func(k int) string { return fmt.Sprintf("ls: ./private/vault%d: Permission denied", k) }},
	{"csv", []string{"./export.sh"}, func(r *rand.Rand, i int) string {
		return fmt.Sprintf("%d,%s,%s,%d.%02d,%s %s", 100000+i, synthLongWord(r), synthLongWord(r), r.Intn(10000), r.Intn(100), synthWord(r), synthLongWord(r))
	}, func(k int) string { return fmt.Sprintf("export: row %d failed: could not encode field", k) }},
	{"log", []string{"./server"}, func(r *rand.Rand, i int) string {
		return fmt.Sprintf("2026-09-%02dT%02d:%02d:%02dZ worker-%d %s %s %s %s took=%dms",
			1+r.Intn(28), r.Intn(24), r.Intn(60), r.Intn(60), r.Intn(64), synthWord(r), synthWord(r), synthWord(r), synthWord(r), r.Intn(900))
	}, func(k int) string {
		return fmt.Sprintf("2026-09-01T00:00:%02dZ worker-1 FATAL connection %d refused by upstream", k, k)
	}},
}

func synth(sh synthShape, size, errs int, seed int64) (string, []string) {
	r := rand.New(rand.NewSource(seed))
	var lines []string
	n, tok := 0, 0
	for i := 0; (size > 0 && n < size) || (size < 0 && tok < -size); i++ {
		ln := sh.line(r, i)
		lines = append(lines, ln)
		n += len(ln) + 1
		tok += tokens.Count(ln) + 1
	}
	var inserted []string
	for k := 0; k < errs; k++ {
		e := sh.err(k)
		at := (k + 1) * len(lines) / (errs + 1)
		for at < len(lines) && sh.name == "ls-R" && lines[at] == "" {
			at++
		}
		lines = append(lines[:at], append([]string{e}, lines[at:]...)...)
		inserted = append(inserted, e)
	}
	return strings.Join(lines, "\n"), inserted
}

func TestCharCapSynthetic(t *testing.T) {
	sizes := []int{25000, 30000, 36000, 42000, 50000, 60000, -8000, -8300, -8600, -8850}
	if testenv.Race {
		sizes = []int{30000, -8600}
	}
	spills, over := map[string]int{}, map[string]int{}
	total := 0
	for si, sh := range synthShapes {
		for zi, size := range sizes {
			in, errs := synth(sh, size, 5, int64(100*si+zi+1))
			ctx := &engine.Context{Argv: sh.argv, Exit: 1, Cwd: "/w", Home: "/home/u"}
			clean := textutil.Clean(in)
			ct := tokens.Count(clean)
			ratio := float64(len(clean)) / float64(ct)
			if ratio < 2.2 || ratio > 4.8 {
				t.Errorf("%s: %.2f chars/token is outside the range this test models", sh.name, ratio)
			}
			base := engine.Process(ctx, in, engine.Options{})
			whole := base.Output == clean && !base.Lossy
			spill := whole && len(clean) > claudeCap
			if spill {
				spills[sh.name]++
			}
			if len(base.Output) > claudeCap {
				over[sh.name]++
			}
			total++
			res := engine.Process(ctx, in, engine.Options{MaxChars: claudeCap})
			t.Logf("%-5s %6d bytes %5d tokens (%.2f c/t) uncapped: %-11s %6d bytes whole=%-5v capped: %-8s %6d bytes",
				sh.name, len(clean), ct, ratio, base.Filter, len(base.Output), whole, res.Filter, len(res.Output))
			if len(res.Output) > claudeCap {
				t.Errorf("%s/%d: capped view is %d bytes", sh.name, size, len(res.Output))
			}
			if res.Output != clean && !res.Lossy {
				t.Errorf("%s/%d: a reduced view must be lossy", sh.name, size)
			}
			if spill && res.Output == clean {
				t.Errorf("%s/%d: over the cap the view must be reduced", sh.name, size)
			}
			for _, e := range errs {
				if !strings.Contains(res.Output, e) && (!strings.HasPrefix(res.Output, "[log: ") || len(engine.MissingErrorKinds(e, res.Output)) > 0) {
					t.Errorf("%s/%d: capped view lost %q", sh.name, size, e)
				}
			}
		}
	}
	for _, name := range []string{"prose", "csv"} {
		if spills[name] == 0 {
			t.Errorf("%s: no input passed through whole over the cap; the test lost its point", name)
		}
	}
	t.Logf("%d inputs; uncapped views over %d bytes by shape: %v (of which passed through whole: %v); capped: none",
		total, claudeCap, over, spills)
}

func TestCharCapProcessEdges(t *testing.T) {
	ctx := func(argv ...string) *engine.Context {
		return &engine.Context{Argv: argv, Exit: 1, Cwd: "/w", Home: "/home/u"}
	}

	var b strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "line %d of a short report\n", i)
	}
	b.WriteString("ERROR: the last step failed\n")
	small := b.String()
	if tokens.Count(small) > engine.SmallOutput {
		t.Fatalf("fixture has %d tokens; it must be a small output", tokens.Count(small))
	}
	res := engine.Process(ctx("./report"), small, engine.Options{MaxChars: 160})
	if len(res.Output) > 160 || !res.Lossy || !strings.Contains(res.Output, "ERROR: the last step failed") {
		t.Errorf("small output over the cap: %d bytes lossy=%v\n%s", len(res.Output), res.Lossy, res.Output)
	}
	if got := engine.Process(ctx("./report"), small, engine.Options{}); got.Output != strings.TrimRight(small, "\n") || got.Lossy {
		t.Errorf("uncapped small output must pass through: %+v", got)
	}

	r := rand.New(rand.NewSource(3))
	var pad strings.Builder
	for i := 0; i < 200; i++ {
		pad.WriteString(synthShapes[0].line(r, i) + strings.Repeat(" ", 40) + "\n")
	}
	raw := pad.String()
	clean := textutil.Clean(raw)
	limit := len(clean) + 100
	if len(raw) <= limit {
		t.Fatalf("fixture sizes: clean %d raw %d", len(clean), len(raw))
	}
	if res = engine.Process(ctx("./docs"), raw, engine.Options{}); res.Filter != "passthrough" {
		t.Fatalf("uncapped, this output replays raw; got %s", res.Filter)
	}
	res = engine.Process(ctx("./docs"), raw, engine.Options{MaxChars: limit})
	if res.Filter == "passthrough" || res.Output != clean || res.Lossy {
		t.Errorf("raw over the cap must not be replayed; the clean text fits: filter %s, %d bytes", res.Filter, len(res.Output))
	}

	var por strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&por, " M internal/pkg%d/file%d.go\n", i%40, i)
	}
	res = engine.Process(ctx("git", "status", "--porcelain"), por.String(), engine.Options{MaxChars: 5000})
	if res.Filter != "passthrough" || res.Output != strings.TrimRight(por.String(), "\n") {
		t.Errorf("machine-readable output must pass through byte-exact: filter %s", res.Filter)
	}

	in, _ := synth(synthShapes[0], 30000, 0, 7)
	res = engine.Process(ctx("./gen-docs"), in, engine.Options{MaxChars: 10})
	if len(res.Output) > engine.MinMaxChars || !res.Lossy {
		t.Errorf("tiny cap: %d bytes %q", len(res.Output), res.Output)
	}
}

func TestCharCapRandom(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	checked, iters := 0, 40
	if testenv.Race {
		iters = 6
	}
	for iter := 0; iter < iters; iter++ {
		var lines, errs []string
		n := 50 + r.Intn(2000)
		for i := 0; i < n; i++ {
			switch k := r.Intn(40); {
			case k == 0:
				e := fmt.Sprintf("error: step %d failed: %s", i, synthWord(r))

				lines = append(lines, e, synthShapes[0].line(r, i))
				errs = append(errs, e)
			case k == 1:
				lines = append(lines, strings.Repeat("ü€", 50+r.Intn(500)))
			case k == 2:
				lines = append(lines, "")
			default:
				lines = append(lines, synthShapes[r.Intn(len(synthShapes))].line(r, i))
			}
		}
		in := strings.Join(lines, "\n")
		maxChars := 200 + r.Intn(40000)
		c := &engine.Context{Argv: []string{"./job"}, Exit: r.Intn(2), Cwd: "/w", Home: "/home/u"}
		budget := 500 + r.Intn(12000)
		res := engine.Process(c, in, engine.Options{MaxChars: maxChars, Budget: budget})
		if len(res.Output) > maxChars {
			t.Fatalf("iter %d: cap %d, view %d bytes", iter, maxChars, len(res.Output))
		}
		errBytes, errTokens := 0, 0
		for _, e := range errs {

			errBytes += len(e) + 200 + 30
			errTokens += tokens.Count(e) + 50 + 8
		}
		if errBytes < maxChars/3 && errTokens < budget/3 && len(errs) > 0 {
			checked++
			for _, e := range errs {
				if !strings.Contains(res.Output, e) {
					t.Fatalf("iter %d: cap %d (errors ~%d bytes) lost %q", iter, maxChars, errBytes, e)
				}
			}
		}
	}
	if checked < iters/6 {
		t.Errorf("only %d cases checked error lines", checked)
	}
}
