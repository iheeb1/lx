package golang

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

type corpusCase struct {
	cat, name string
	filter    string

	process string
	why     string
}

var corpus = []corpusCase{
	{"go", "go-build-errors", "go-build", "passthrough",
		"compiler diagnostics are kept verbatim and there are no downloads to condense, so the view equals the input and the never-worse gate passes it through"},
	{"go", "go-get-u", "go-mod", "go-mod", ""},
	{"go", "go-list-m-all", "go-list", "passthrough",
		"go list is content shown as is; nothing to condense, so the gate passes it through"},
	{"go", "go-mod-download-x", "go-mod", "go-mod", ""},
	{"go", "go-mod-tidy-fresh-cache", "go-mod", "go-mod", ""},
	{"go", "go-test-build-failed", "go-test", "passthrough",
		"compiler errors and FAIL lines are all kept; collapsing four cached ok lines saves only ~10%, under the never-worse gate's threshold"},
	{"go", "go-test-cover", "go-test", "passthrough",
		"every ok line carries a coverage figure, which the filter keeps; the output is below the 150-token small-output threshold anyway"},
	{"go", "go-test-fail-json", "go-test-json", "go-test-json", ""},
	{"go", "go-test-fail-v", "go-test", "go-test", ""},
	{"go", "go-test-fail", "go-test", "go-test", ""},
	{"go", "go-test-panic", "go-test", "go-test", ""},
	{"go", "go-test-pass-multi-pkg", "go-test", "passthrough",
		"below the 150-token small-output threshold: Process never calls a filter"},
	{"go", "go-test-pass", "go-test", "passthrough",
		"two lines, below the small-output threshold"},
	{"go", "go-test-timeout-goroutine-dump", "go-test", "go-test", ""},
	{"go", "go-test-v-pass-gin", "go-test", "go-test", ""},
	{"go", "go-test-v-pass", "go-test", "go-test", ""},
	{"go", "go-vet-findings", "go-build", "passthrough",
		"vet diagnostics are kept verbatim; nothing to condense, so the view equals the input"},

	{"misc", "make-go-test-gin", "", "", "make is not a go command; see TestTextDetect"},
	{"misc", "make-go-vet-gin", "", "", "one echoed command line, below the small-output threshold"},
}

var captures = []corpusCase{
	{"go", "lxcap-test", "go-test", "go-test", ""},
	{"go", "lxcap-test-v", "go-test", "go-test", ""},
	{"go", "lxcap-test-json", "go-test-json", "go-test-json", ""},
	{"go", "lxcap-test-race", "go-test", "go-test", ""},
	{"go", "lxcap-vet", "go-build", "passthrough", "vet diagnostics verbatim, below the small-output threshold"},
	{"go", "lxcap-test-run-logs", "go-test", "passthrough", "-run with logs: everything is shown, below the small-output threshold"},
	{"go", "lxcap-test-v-par", "go-test", "go-test", ""},
	{"go", "lxcap-test-v-panic-sub", "go-test", "go-test", ""},
	{"go", "lxcap-test-v-timeout", "go-test", "go-test", ""},
	{"go", "lxcap-test-c", "go-build", "passthrough", "three compiler lines, below the small-output threshold"},

	{"go", "lxcap2-test", "go-test", "go-test", ""},
	{"go", "lxcap2-test-v", "go-test", "go-test", ""},
	{"go", "lxcap2-test-json", "go-test-json", "go-test-json", ""},
	{"go", "lxcap2-test-race-post", "go-test", "go-test", ""},
	{"go", "lxcap2-test-cover-fail", "go-test", "go-test", ""},
	{"go", "lxcap2-test-run-norun", "go-test", "go-test", ""},

	{"go", "lxcap2-example", "go-test", "passthrough", "small output (13 lines): the view keeps got:/want: and saves under 10%"},
	{"go", "lxcap2-example-v", "go-test", "passthrough", "small output (19 lines), the view saves under 10%"},
	{"go", "lxcap2-example-json", "go-test-json", "go-test-json", ""},

	{"go", "lxcap2-flaky-count-v", "go-test", "passthrough", "small output (13 lines), below the small-output threshold"},
	{"go", "lxcap2-flaky-count-json", "go-test-json", "go-test-json", ""},

	{"go", "lxcap2-count-sub-json", "go-test-json", "go-test-json", ""},
	{"go", "lxcap2-vet-json", "go-machine", "passthrough", "go vet -json is machine output, two lines"},
	{"go", "lxcap2-list-json", "go-machine", "passthrough",
		"go list -json is machine output: kept verbatim (the generic reducer used to restructure it)"},
}

func loadCapture(t *testing.T, cat, name string) fixture.Case {
	t.Helper()
	c, err := fixture.Read(filepath.Join("testdata", "captures"), cat, name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var libLocRe = regexp.MustCompile(`^(?:/opt/homebrew/Cellar/go/[^/]+/libexec/src/|/home/user/gopath/pkg/mod/|_testmain\.go:)`)

var locExempt = map[string]*regexp.Regexp{}

func passingOnlyLocs(ref string) map[string]bool {
	owner := map[string][]string{}
	status := map[string]string{}
	cur := ""
	for _, ln := range strings.Split(ref, "\n") {
		if m := markerRe.FindStringSubmatch(ln); m != nil {
			cur = m[2]
			if m[1] == "PAUSE" {
				cur = ""
			}
			continue
		}
		if m := resultRe.FindStringSubmatch(ln); m != nil {
			status[m[3]] = m[2]
			cur = ""
			continue
		}
		for _, loc := range fixture.LocRe.FindAllString(ln, -1) {
			owner[loc] = append(owner[loc], cur)
		}
	}
	out := map[string]bool{}
	for loc, tests := range owner {
		ok := true
		for _, tn := range tests {
			if tn == "" || status[tn] == "FAIL" || status[tn] == "" {
				ok = false
			}
		}
		if ok {
			out[loc] = true
		}
	}
	return out
}

func TestCorpus(t *testing.T) {
	all := append(append([]corpusCase(nil), corpus...), captures...)
	for i, cc := range all {
		t.Run(cc.name, func(t *testing.T) {
			var fc fixture.Case
			if i < len(corpus) {
				fc = fixture.Load(t, cc.cat, cc.name)
			} else {
				fc = loadCapture(t, cc.cat, cc.name)
			}
			c := fc.Context()
			clean := fc.Clean()
			f := engine.Find(c)
			if cc.filter == "" {
				if f != nil && strings.HasPrefix(f.Name(), "go-") {
					t.Fatalf("filter = %s, want none of ours (%s)", f.Name(), cc.why)
				}
				if cc.name == "make-go-test-gin" {
					got, ok := TestTextDetect(c, clean)
					if !ok {
						t.Fatal("detector bailed on make test output")
					}
					fixture.Golden(t, "golang", cc.name, got)
					checkFidelity(t, cc, fc, clean, got)
					logSavings(t, fc.Raw, got)
				}
				return
			}
			if f == nil || f.Name() != cc.filter {
				t.Fatalf("filter = %v, want %s", f, cc.filter)
			}
			got, ok := f.Apply(c, clean)
			if !ok {
				t.Fatal("filter bailed on real output")
			}
			fixture.Golden(t, "golang", cc.name, got)
			ref := clean
			if cc.filter == "go-test-json" {
				ref = decodeEvents(clean)
				if _, okd := DecodeTestJSON(clean); !okd {
					t.Fatal("decode failed")
				}
			}
			checkFidelity(t, cc, fc, ref, got)

			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("guard re-added %d error lines:\n%s", res.GuardAdded, res.Output)
			}
			if res.FilterPanic != "" {
				t.Errorf("filter panicked: %s", res.FilterPanic)
			}
			if res.Filter != cc.process {
				t.Errorf("Process used %q, want %q (%s)", res.Filter, cc.process, cc.why)
			}
			if res.Filter == cc.filter && res.Output != got && !strings.Contains(res.Output, "lines omitted") {
				t.Errorf("Process changed the filter output unexpectedly")
			}
			logSavings(t, fc.Raw, got)
		})
	}
}

func checkFidelity(t *testing.T, cc corpusCase, fc fixture.Case, ref, got string) {
	t.Helper()
	gref := ref
	if strings.HasPrefix(cc.filter, "go-test") || cc.filter == "" {
		gref = stripMarkers(ref)
		for _, ln := range strings.Split(ref, "\n") {
			if m := resultRe.FindStringSubmatch(ln); m != nil && m[2] == "FAIL" && !strings.Contains(got, "--- FAIL: "+m[3]+" (") {
				t.Errorf("failing test not shown: %s", strings.TrimSpace(ln))
			}
		}
	}
	if m := fixture.ErrorLinesMissing(gref, got); len(m) > 0 {
		t.Errorf("error lines dropped:\n%s", strings.Join(m, "\n"))
	}
	if fc.Meta.ExitCode == 0 {
		return
	}
	exempt := locExempt[cc.name]
	passing := passingOnlyLocs(ref)
	for _, loc := range fixture.LocationsMissing(ref, got) {
		if libLocRe.MatchString(loc) || passing[loc] || exempt != nil && exempt.MatchString(loc) {
			continue
		}
		t.Errorf("location dropped: %s", loc)
	}

	if strings.HasPrefix(cc.filter, "go-test") && !strings.Contains(got, "FAIL") {
		t.Errorf("failing run rendered without a FAIL line")
	}
}

func stripMarkers(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		if !markerRe.MatchString(ln) {
			b.WriteString(ln + "\n")
		}
	}
	return b.String()
}

func logSavings(t *testing.T, raw, got string) {
	t.Helper()
	rt, ot := tokens.Count(raw), tokens.Count(got)
	pct := 0.0
	if rt > 0 {
		pct = 100 * (1 - float64(ot)/float64(rt))
	}
	t.Logf("savings: %d → %d tokens (%.0f%%)", rt, ot, pct)
}

func decodeEvents(raw string) string {
	type key struct{ pkg, test string }
	buf := map[key]*strings.Builder{}
	var order []key
	var b strings.Builder
	for _, ln := range strings.Split(raw, "\n") {
		var ev struct{ Action, Package, Test, Output, ImportPath string }
		if json.Unmarshal([]byte(ln), &ev) != nil || ev.Action == "" {
			b.WriteString(ln + "\n")
			continue
		}
		k := key{ev.Package, ev.Test}
		switch ev.Action {
		case "output":
		case "build-output":
			k = key{"build " + ev.ImportPath, ""}
		default:
			continue
		}
		w := buf[k]
		if w == nil {
			w = &strings.Builder{}
			buf[k] = w
			order = append(order, k)
		}
		if s := w.String(); s != "" && !strings.HasSuffix(s, "\n") {
			o := ev.Output
			if strings.HasPrefix(strings.TrimLeft(o, " "), "--- ") || strings.HasPrefix(o, "=== ") ||
				strings.HasPrefix(o, "PASS\n") || strings.HasPrefix(o, "FAIL") || strings.HasPrefix(o, "ok  ") {
				w.WriteByte('\n')
			}
		}
		w.WriteString(ev.Output)
	}
	for _, k := range order {
		b.WriteString(buf[k].String())
		b.WriteByte('\n')
	}
	return b.String()
}
