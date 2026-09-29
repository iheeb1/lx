package hook_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/hook"
	"github.com/iheeb1/lx/internal/testenv"
)

// as hostCharCapFor(1) in internal/cli
const (
	failView  = 8800
	failTotal = 9000
)

var capGaps = map[string]string{}

var fileDiag = regexp.MustCompile(`(?:cat|head|tail|bat)(?:\]|:) .*(?:No such file or directory|Is a directory|Permission denied|error)`)

func failingCaptures(t *testing.T) []fixture.Case {
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
		c.Category = filepath.ToSlash(strings.TrimPrefix(dir, root+string(filepath.Separator)))
		cases = append(cases, c)
	}
	var out []fixture.Case
	for _, c := range cases {
		if c.Meta.ExitCode != 0 {
			out = append(out, c)
		}
		if c.Name == "cat-glued-missing" {
			mid := c
			mid.Name, mid.Raw = "cat-missing-mid", strings.Replace(c.Raw, "}cat: missing.h", "}\ncat: missing.h", 1)
			out = append(out, mid)
		}
	}
	return out
}

func errorBytes(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if engine.IsError(ln) {
			n += len(ln) + 1
		}
	}
	return n
}

func isContent(c *engine.Context) bool {
	ct, ok := engine.Find(c).(engine.Content)
	return ok && ct.IsContent()
}

func TestFailingViewsFitClaudeCode(t *testing.T) {
	l := hook.OutputLimitsFrom("", "", "", "", "")
	if l.Fail*9/10-200 != failView || l.Fail*9/10 != failTotal {
		t.Fatalf("failing limit %d no longer gives a %d-char view", l.Fail, failView)
	}
	cases := failingCaptures(t)
	if len(cases) < 250 {
		t.Fatalf("only %d failing captures found", len(cases))
	}
	checked, machine, over10k := 0, 0, 0
	gapsSeen := map[string]bool{}
	for i, c := range cases {
		if testenv.Race && i%4 != 0 {
			continue
		}
		name := c.Category + "/" + c.Name
		ctx := c.Context()
		if engine.MachineReadableAny(ctx) {
			machine++
			continue
		}
		checked++
		full := engine.Process(ctx, c.Raw, engine.Options{})
		res := engine.Process(c.Context(), c.Raw, engine.Options{MaxChars: failView})
		if len(full.Output) > 10000 {
			over10k++
		}
		total := len(res.Output)
		if res.Lossy {
			total += 1 + len(engine.WithNotes(engine.Receipt(res, "1073741823"), res.Notes, 200))
		}
		if len(res.Output) > failView || total > failTotal {
			t.Errorf("%s: view %d chars, %d with its receipt; the cap is %d, the limit %d", name, len(res.Output), total, failView, failTotal)
		}

		var lost []string
		if isContent(ctx) {
			for _, ln := range engine.MissingErrorLines(full.Output, res.Output) {
				if fileDiag.MatchString(ln) {
					lost = append(lost, ln)
				}
			}
		} else if eb := errorBytes(full.Output); eb <= failView/2 {
			lost = engine.MissingErrorLines(full.Output, res.Output)
		} else if kept := errorBytes(res.Output); kept < failView/2 {
			t.Errorf("%s: %d bytes of error lines to show, the capped view keeps only %d", name, eb, kept)
		}
		if missing := engine.MissingErrorLines(full.Output, res.Output); len(missing) > 0 && !strings.Contains(res.Output, "omitted") {
			t.Errorf("%s: the cap dropped %d error lines without an omission marker, first %q", name, len(missing), missing[0])
		}
		if why, ok := capGaps[name]; ok {
			gapsSeen[name] = true
			if len(lost) == 0 {
				t.Errorf("%s: fixed (%s); remove it from capGaps", name, why)
			}
			continue
		}
		for _, ln := range lost {
			t.Errorf("%s: the %d-char cap lost %q", name, failView, ln)
		}
	}
	for name := range capGaps {
		if !gapsSeen[name] && !testenv.Race {
			t.Errorf("capGaps names %s, which is not a failing capture", name)
		}
	}
	t.Logf("%d failing captures checked (%d machine-readable, passed through); %d uncapped views over 10,000 chars", checked, machine, over10k)
}
