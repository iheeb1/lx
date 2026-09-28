package jstools

import (
	"regexp"
	"strings"
	"testing"
)

var (
	refTSCPlainRe  = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (error|warning|message) (TS\d+): (.*)$`)
	refTSCPrettyRe = regexp.MustCompile(`^(.+?):(\d+):(\d+) - (error|warning|message) (TS\d+): (.*)$`)
	refTSCGlobalRe = regexp.MustCompile(`^(error|warning|message) (TS\d+): (.*)$`)
	refESMsgRe     = regexp.MustCompile(`^(\s+)(\d+):(\d+)\s+(error|warning)\s+(.*)$`)
	refESRuleRe    = regexp.MustCompile(`^(.*?)\s{2,}(\S+)$`)
)

func checkScanners(t *testing.T, ln string) {
	t.Helper()
	cmp := func(name string, re *regexp.Regexp, got []string, ok bool) {
		t.Helper()
		m := re.FindStringSubmatch(ln)
		if (m != nil) != ok {
			t.Fatalf("%s(%q): ok=%v, regexp matched=%v", name, ln, ok, m != nil)
		}
		if m != nil && strings.Join(m[1:], "\x00") != strings.Join(got, "\x00") {
			t.Fatalf("%s(%q): got %q, regexp %q", name, ln, got, m[1:])
		}
	}
	p, ok := scanTSCPlain(ln)
	cmp("scanTSCPlain", refTSCPlainRe, p[:], ok)
	p, ok = scanTSCPretty(ln)
	cmp("scanTSCPretty", refTSCPrettyRe, p[:], ok)
	if got, want := scanTSCGlobal(ln), refTSCGlobalRe.MatchString(ln); got != want {
		t.Fatalf("scanTSCGlobal(%q) = %v, regexp %v", ln, got, want)
	}
	l, c, sev, rest, ok := scanESMsg(ln)
	m := refESMsgRe.FindStringSubmatch(ln)
	if (m != nil) != ok || m != nil && (m[2] != l || m[3] != c || m[4] != sev || m[5] != rest) {
		t.Fatalf("scanESMsg(%q) = %q %q %q %q %v, regexp %q", ln, l, c, sev, rest, ok, m)
	}
	text, rule, ok := scanESRule(ln)
	cmp("scanESRule", refESRuleRe, []string{text, rule}, ok)
}

func TestScannersMatchRegexps(t *testing.T) {
	edge := []string{
		"", " ", "a(1,2): error TS1: x", "(1,2): error TS1: x", "a(1,2): error TS: x", "a(1,): error TS1: x",
		"a(1,2): error TS1:x", "a(1,2): errors TS1: x", "a(b(1,2): error TS1: x(3,4): error TS2: y",
		"a(1,2): note (3,4): error TS2: y", "src/x.ts:1:2 - error TS2322: m", ":1:2 - error TS1: m",
		"a:1:2:3 - error TS1: m", "a:1:2 - warning TS6133: m - n:3:4 - error TS2: k", "a:x:2 - error TS1: m",
		"error TS5023: Unknown compiler option 'x'.", "error TS5023:", "error TS5023: ", "message TS6194: Found 0 errors.",
		"  1:2  error  msg  rule", " 1:2 error x", "\t10:5\twarning\tm\tr", "  1:2  errors  x", "  1:2  error", "  1:2  error  ",
		"  1:2  error  Parsing error: Unexpected token", "  a  b", "a  b", "a b", "a   ", "  ", "x\n  y", "x  \ny",
		"a(1,2): error TS1: x\nb", "a\n(1,2): error TS1: x", "  1:2\n error\tx", "é(1,2): error TS9: ü  r",
		"  1:2  warning  Unused eslint-disable directive (no problems were reported from 'no-debugger')",
	}
	for _, ln := range edge {
		checkScanners(t, ln)
	}
	for _, tc := range corpus {
		fc := tc.load(t)
		for _, ln := range strings.Split(fc.Clean(), "\n") {
			checkScanners(t, ln)
		}
	}
}

func FuzzScanners(f *testing.F) {
	for _, s := range []string{"a(1,2): error TS1: x", "src/x.ts:1:2 - error TS2322: m", "  1:2  error  msg  rule", "error TS1: x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ln string) { checkScanners(t, ln) })
}
