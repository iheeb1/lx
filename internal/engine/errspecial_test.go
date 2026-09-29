package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var errSpecialRef = regexp.MustCompile(`(?i)^\s*E\s{2,}\S|^\s*[✗✘✕×]\s|\bTS\d{4}\b|\berror\[E\d+\]|^\s*npm (?:ERR!|error)|` +
	`^--- FAIL|^FAIL\b|^\s*FAILED\b|^\s*!\s+\[rejected\]|` +
	`^\s*g?make(?:\[\d+\])?: \*\*\*|\bundefined symbols?\b|\bunknown (?:options?|flags?|arguments?|commands?)\b|^\s*e: `)

func TestErrSpecialMatchesOneRegex(t *testing.T) {
	lines := strings.Split(`E       assert 1 == 2
  ✗ renders the header
 × fails
error TS2345: Argument
src/a.ts(3,1): error ts1005: ';' expected
xts12 TS12345 TS123
error[E0308]: mismatched types
ERROR[e12]
npm ERR! code 1
npm error missing script
--- FAIL: TestX (0.00s)
FAIL	github.com/x
FAILED tests/test_a.py::test_b
 ! [rejected]        main -> main (fetch first)
make[2]: *** [all] Error 2
gmake: *** No rule
Undefined symbols for architecture arm64:
unknown option --frobnicate
Unknown commands: x
e: file.kt: Unresolved
  e: x
failed
e:x
E x
unknownoption
TSX1234
ts1234`, "\n")
	lines = append(lines, "un\u212anown option", "T\u017f1234", "error ts1234")
	for _, dir := range []string{"../../testdata/corpus", "../../testdata/golden"} {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if b, err := os.ReadFile(p); err == nil {
				lines = append(lines, strings.Split(string(b), "\n")...)
			}
			return nil
		})
	}
	if len(lines) < 10000 {
		t.Fatalf("only %d lines", len(lines))
	}
	for _, ln := range lines {
		if got, want := errSpecial(ln), errSpecialRef.MatchString(ln); got != want {
			t.Errorf("%q: %v, want %v", ln, got, want)
		}
	}
}

func FuzzErrSpecial(f *testing.F) {
	for _, s := range []string{"un\u212anown option", "T\u017f1234", "error TS2345: x", "  E   y", "make[1]: *** z", "Unknown flags: -q", "--- FAIL: T", "✗ a"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := errSpecial(s), errSpecialRef.MatchString(s); got != want {
			t.Fatalf("%q: %v, want %v", s, got, want)
		}
	})
}
