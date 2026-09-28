package jstools

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/testenv"
)

var tscHeadRe = regexp.MustCompile(`^(\S+?)[(:]\d+[,:]\d+\)?:? - error|^(\S+?)\(\d+,\d+\): error`)

func withFocus(t *testing.T, tc corpusCase, files ...string) (string, *engine.Context, string) {
	t.Helper()
	fc := tc.load(t)
	c := fc.Context()
	if len(files) > 0 {
		c.Focus = &engine.Focus{Files: files}
	}
	got, ok := engine.Find(c).Apply(c, fc.Clean())
	if !ok {
		t.Fatal("bailed")
	}
	return got, c, fc.Clean()
}

func firstMatch(s string, keep func(string) bool) string {
	for _, ln := range strings.Split(s, "\n") {
		if keep(ln) {
			return ln
		}
	}
	return ""
}

func TestFocusTscFileFirst(t *testing.T) {
	tc := corpusCase{"node", "tsc-noemit-errors", false, "tsc", ""}
	base, _, _ := withFocus(t, tc)
	if first := firstMatch(base, tscHeadRe.MatchString); !strings.HasPrefix(first, "src/array.ts(") {
		t.Fatalf("unexpected first error: %s", first)
	}
	got, c, clean := withFocus(t, tc, "/home/user/src/yup/src/string.ts")
	if first := firstMatch(got, tscHeadRe.MatchString); !strings.HasPrefix(first, "src/string.ts(") {
		t.Errorf("focused file not first: %s", first)
	}
	if strings.Count(got, ": error TS") != strings.Count(base, ": error TS") {
		t.Error("an error went missing")
	}
	checkFidelity(t, "tsc", c, clean, got)
}

func TestFocusEslintFileFirst(t *testing.T) {
	tc := corpusCase{"node", "eslint-many-problems", false, "eslint", ""}
	isFile := func(ln string) bool { return strings.HasPrefix(ln, "examples/") || strings.HasPrefix(ln, "lib/") }
	base, _, _ := withFocus(t, tc)
	got, c, clean := withFocus(t, tc, "/home/user/src/express/examples/error/index.js")
	if first := firstMatch(got, isFile); first != "examples/error/index.js" {
		t.Errorf("focused file not first: %q (was %q)", first, firstMatch(base, isFile))
	}
	if strings.Count(got, "\n") != strings.Count(base, "\n") {
		t.Error("focus changed the size of the view")
	}
	checkFidelity(t, "eslint", c, clean, got)
}

func TestFocusJSToolsKeepsEverything(t *testing.T) {
	fileRe := regexp.MustCompile(`(?m)^(?:/home/user/src/\w+/)?((?:src|lib|examples|app|packages)/\S+?\.[jt]sx?)\b`)
	for _, tc := range corpus {
		if tc.filter != "tsc" && tc.filter != "eslint" && tc.filter != "npm-run" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			base, _, clean := withFocus(t, tc)
			if got, _, _ := withFocus(t, tc, "src/nothing.ts"); got != base {
				t.Error("an unmatched focus changed the view")
			}
			all := fileRe.FindAllStringSubmatch(clean, -1)
			if len(all) == 0 {
				return
			}
			got, c, _ := withFocus(t, tc, "/somewhere/"+all[len(all)-1][1])
			if strings.Count(got, "\n") != strings.Count(base, "\n") {
				t.Errorf("focus changed the size of the view:\n%s", got)
			}
			checkFidelity(t, tc.filter, c, clean, got)
		})
	}
}

func TestFocusHugeIsFast(t *testing.T) {
	var b strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&b, "src/f%d.ts(%d,5): error TS2322: Type 'string' is not assignable to type 'number'.\n", i%700, i)
	}
	b.WriteString("\nFound 50000 errors in 700 files.\n")
	var files []string
	for i := range 20 {
		files = append(files, fmt.Sprintf("/home/user/src/app/src/f%d.ts", i*31))
	}
	c := ctx(2, "tsc", "--noEmit")
	c.Focus = &engine.Focus{Files: files}
	start := time.Now()
	if _, ok := (tsc{}).Apply(c, b.String()); !ok {
		t.Fatal("bailed")
	}
	if d := time.Since(start); d > testenv.Scale(3*time.Second) {
		t.Errorf("took %v", d)
	}
}
