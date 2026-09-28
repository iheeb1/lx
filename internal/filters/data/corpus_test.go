package data

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

type corpusCase struct {
	name    string
	local   bool
	filter  string
	process string
	why     string

	drops func(t *testing.T, clean, got string, missing []string)
}

const (
	small   = "engine: at most SmallOutput (150) tokens after normalization — the filter is not called"
	notWith = "engine never-worse gate: the view saves under 10%"
	same    = "the filter returns the data unchanged (within its budget), so the gate passes it through"
)

var corpusCases = []corpusCase{

	{name: "cat-package-json-large", filter: "cat", process: "passthrough", why: same + ": a 1.8k-token package.json is shown exactly"},
	{name: "cat-source-1500-lines", filter: "cat", drops: windowDrops},
	{name: "cat-tsconfig", filter: "cat", process: "passthrough", why: small + "; unchanged anyway"},
	{name: "curl-github-issues-json", filter: "curl", drops: jsonDrops},
	{name: "curl-jq-filter", filter: "jq", process: "passthrough", why: same + ": jq output the user shaped (1.5k tokens) is kept"},
	{name: "curl-progress-meter", filter: "curl", process: "normalize", why: notWith + ": only the progress meter goes, the 1.1k-token JSON body is under the 1500-token threshold and kept; the gate shows the normalized text"},

	{local: true, name: "cat-c-source", filter: "cat", drops: windowDrops},
	{local: true, name: "cat-go-source", filter: "cat", drops: windowDrops},
	{local: true, name: "cat-go-sum", filter: "cat", drops: lockDrops},
	{local: true, name: "cat-json-fixture", filter: "cat", drops: jsonDrops},
	{local: true, name: "cat-minified-js", filter: "cat", drops: windowDrops},
	{local: true, name: "cat-missing-then-source", filter: "cat", drops: windowDrops},
	{local: true, name: "cat-package-lock", filter: "cat", drops: lockDrops},
	{local: true, name: "cat-pnpm-lock", filter: "cat", drops: lockDrops},
	{local: true, name: "curl-D-languages", filter: "curl"},
	{local: true, name: "curl-f-404", filter: "curl", process: "passthrough", why: small},
	{local: true, name: "curl-head-github", filter: "curl", drops: meterDrops},
	{local: true, name: "curl-html-blog", filter: "curl", drops: htmlDrops},
	{local: true, name: "curl-i-404-json", filter: "curl"},
	{local: true, name: "curl-i-html-404", filter: "curl", drops: htmlDrops},
	{local: true, name: "curl-i-issues", filter: "curl", process: "normalize", why: notWith + ": curl's progress meter was written into the middle of the JSON body (see meterNote), so the body cannot be parsed and is kept verbatim; only 18 headers go"},
	{local: true, name: "curl-post-401", filter: "curl", process: "normalize", why: notWith + ": a 401 keeps every header and its small JSON error body"},
	{local: true, name: "curl-progress-bar", filter: "curl", process: "normalize", why: small},
	{local: true, name: "curl-raw-go-source", filter: "curl", drops: windowDrops},
	{local: true, name: "curl-refused", filter: "curl", drops: meterDrops},
	{local: true, name: "curl-resolve-fail", filter: "curl", process: "normalize", why: small, drops: meterDrops},
	{local: true, name: "curl-timeout", filter: "curl", process: "passthrough", why: small},
	{local: true, name: "curl-v-json", filter: "curl"},
	{local: true, name: "curl-vL-redirect", filter: "curl", drops: htmlDrops},
	{local: true, name: "head-200-lvm", filter: "cat", process: "passthrough", why: same},
	{local: true, name: "httpie-issues-body", filter: "httpie", drops: jsonDrops},
	{local: true, name: "httpie-v-post-422", filter: "httpie"},
	{local: true, name: "jq-error", filter: "jq", process: "passthrough", why: small},
	{local: true, name: "jq-packages", filter: "jq", drops: jsonDrops},
	{local: true, name: "jq-raw-keys", filter: "jq", process: "passthrough", why: same},
	{local: true, name: "jq-stream-objects", filter: "jq", process: "passthrough", why: same},
	{local: true, name: "tail-3000-cjson", filter: "cat", drops: windowDrops},
	{local: true, name: "wget-S-json-stdout", filter: "wget", drops: jsonDrops},
	{local: true, name: "wget-download-dots", filter: "wget"},
	{local: true, name: "wget-errors", filter: "wget"},

	{local: true, name: "cat-glued-missing", filter: "cat", drops: windowDrops},
	{local: true, name: "cat-lua-latin1", filter: "cat", process: "passthrough", why: same + ": a 6.9k-token Latin-1 source file is text, shown exactly"},
	{local: true, name: "cat-go-conflict", filter: "cat", drops: windowDrops},
	{local: true, name: "cat-yarn-lock", filter: "cat", drops: lockDrops},
	{local: true, name: "cat-yarn-lock-conflict", filter: "cat", drops: windowDrops},
	{local: true, name: "curl-partial-meter", filter: "curl", drops: windowDrops},
	{local: true, name: "curl-s-partial", filter: "curl", drops: windowDrops},
	{local: true, name: "curl-sS-partial", filter: "curl", drops: windowDrops},
	{local: true, name: "curl-sv-nonewline", filter: "curl", drops: jsonDrops},
	{local: true, name: "curl-v-nonewline", filter: "curl", drops: meterDrops},
	{local: true, name: "httpie-check-status-404", filter: "httpie", process: "normalize", why: small + " (CRLF header lines are normalized)"},
	{local: true, name: "httpie-issues", filter: "httpie", drops: jsonDrops},
	{local: true, name: "httpie-refused", filter: "httpie", process: "passthrough", why: small},
	{local: true, name: "httpie-usage-error", filter: "httpie", process: "passthrough", why: small},
	{local: true, name: "httpie-v-404", filter: "httpie", process: "normalize", why: notWith + ": a 404 keeps every response header and its small JSON body; only 6 request header lines go"},
	{local: true, name: "httpie-v-post-401", filter: "httpie", process: "normalize", why: notWith + ": a 401 keeps every response header and its small JSON body; only 7 request header lines go"},
	{local: true, name: "jq-ndjson-corrupt", filter: "jq", drops: windowDrops},
}

func loadCase(t testing.TB, cc corpusCase) fixture.Case {
	t.Helper()
	if !cc.local {
		return fixture.Load(t, "data", cc.name)
	}
	c, err := fixture.Read("testdata", "data", cc.name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCorpusCoversEveryCapture(t *testing.T) {
	have := map[string]bool{}
	for _, cc := range corpusCases {
		have[cc.name] = true
	}
	local, err := fixture.ReadAll("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append(fixture.All(t), local...) {
		if c.Category == "data" && !have[c.Name] {
			t.Errorf("capture data/%s has no corpus case", c.Name)
		}
	}
}

var diagRe = regexp.MustCompile(`^(?:curl: \(\d+\) |cat: |head: |tail: |jq: |wget: |http: error|\d{4}-\d\d-\d\d \d\d:\d\d:\d\d ERROR )`)

func TestCorpus(t *testing.T) {
	var table strings.Builder
	for _, cc := range corpusCases {
		t.Run(cc.name, func(t *testing.T) {
			fc := loadCase(t, cc)
			c := fc.Context()
			clean := fc.Clean()

			f := engine.Find(c)
			if f == nil || f.Name() != cc.filter {
				t.Fatalf("filter = %v, want %s", f, cc.filter)
			}
			got, ok := f.Apply(c, clean)
			if !ok {
				t.Fatal("filter bailed on real output")
			}
			if again, _ := f.Apply(c, clean); again != got {
				t.Fatal("Apply is not deterministic")
			}
			fixture.Golden(t, "data", cc.name, got)

			if missing := fixture.ErrorLinesMissing(clean, got); len(missing) > 0 {
				if cc.drops == nil {
					t.Errorf("%d error lines missing, e.g. %q", len(missing), missing[0])
				} else {
					cc.drops(t, clean, got, missing)
				}
			}
			if fc.Meta.ExitCode != 0 {
				if lm := fixture.LocationsMissing(clean, got); len(lm) > 0 {
					t.Errorf("failing run lost locations: %v", lm)
				}
			}

			for _, ln := range strings.Split(clean, "\n") {
				ln = stripFrame(ln)
				if diagRe.MatchString(ln) && !strings.Contains(got, ln) {
					t.Errorf("diagnostic dropped: %q", ln)
				}
			}

			if fc.Meta.ExitCode != 0 && strings.Contains(got, "[lx:") && regexp.MustCompile(`(?i)\bsuccess|\bpassed|\bok\b`).MatchString(lxNotes(got)) {
				t.Errorf("pass-like note on a failed run:\n%s", lxNotes(got))
			}

			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("guard re-added %d error lines", res.GuardAdded)
			}
			want := cc.process
			if want == "" {
				want = cc.filter
			}
			if res.Filter != want {
				t.Errorf("pipeline filter = %s, want %s (%s)", res.Filter, want, cc.why)
			}
			if res.OutTokens > engine.DefaultBudget {
				t.Errorf("output of %d tokens is over the budget", res.OutTokens)
			}
			raw, out := tokens.Count(fc.Raw), res.OutTokens
			line := fmt.Sprintf("%-26s %7d → %6d tokens %6.1f%%  %s", cc.name, raw, out, 100*(1-float64(out)/float64(max(raw, 1))), res.Filter)
			t.Log(line)
			table.WriteString(line + "\n")
		})
	}
	t.Log("\n" + table.String())
}

func lxNotes(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, "[lx:") {
			b.WriteString(ln + "\n")
		}
	}
	return b.String()
}

var testFrameRe = regexp.MustCompile(`^(?: *[\d.]+[kMGTPE]?){8} +(?:(?:--:--:--|\d+:\d\d:\d\d) +){3}[\d.]+[kMGTPE]?`)

func stripFrame(ln string) string {
	if m := testFrameRe.FindStringIndex(ln); m != nil && len(ln) >= 78 {
		return ln[min(78, len(ln)):]
	}
	return ln
}

var omitRe = regexp.MustCompile(`^… lines (\d+)-(\d+) omitted \(`)

func windowDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	lines := strings.Split(clean, "\n")
	type span struct{ a, b int }
	var spans []span
	for _, ln := range strings.Split(got, "\n") {
		if m := omitRe.FindStringSubmatch(ln); m != nil {
			a, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[2])
			spans = append(spans, span{a, b})
		}
	}
	if len(spans) == 0 {
		t.Fatalf("lines missing but no omission marker")
	}
	inSpan := func(n int) bool {
		for _, s := range spans {
			if n >= s.a && n <= s.b {
				return true
			}
		}
		return false
	}
	allowed := map[string]bool{}
	for i, ln := range lines {
		if inSpan(i + 1) {
			allowed[strings.TrimSpace(ln)] = true
		}
	}
	for _, m := range missing {
		if !allowed[m] {
			t.Errorf("line outside the omitted ranges dropped: %q", m)
		}
	}
	checkMarkerBoundaries(t, lines, strings.Split(got, "\n"))

	out := strings.Split(got, "\n")
	j := 0
	for i, ln := range lines {
		if inSpan(i+1) || ln == "" {
			continue
		}
		want := ln
		if len(want) > maxShownLine {
			want = want[:100]
		}
		for j < len(out) && !strings.HasPrefix(out[j], want) {
			j++
		}
		if j == len(out) {
			t.Fatalf("line %d not shown in order: %q", i+1, cutRunes(ln, 100))
		}
		j++
	}
}

var outlineLineRe = regexp.MustCompile(`^  (?:outline of the omitted lines:|L\d+: |… \+\d+ more declaration)`)

func checkMarkerBoundaries(t *testing.T, clean, out []string) {
	t.Helper()
	skippable := func(ln string) bool {
		return testFrameRe.MatchString(ln) && strings.TrimSpace(stripFrame(ln)) == "" || diagRe.MatchString(ln)
	}
	shown := func(o string, want string) bool {
		if len(want) > maxShownLine {
			return strings.HasPrefix(o, want[:100])
		}
		return o == want
	}
	for m, ln := range out {
		sm := omitRe.FindStringSubmatch(ln)
		if sm == nil {
			continue
		}
		a, _ := strconv.Atoi(sm[1])
		b, _ := strconv.Atoi(sm[2])
		if m > 0 {
			k := a - 2
			for k >= 0 && skippable(clean[k]) {
				k--
			}
			if k >= 0 && !shown(out[m-1], clean[k]) {
				t.Errorf("line before %q is %q, want line %d %q", ln, cutRunes(out[m-1], 80), k+1, cutRunes(clean[k], 80))
			}
		}
		n := m + 1
		for n < len(out) && outlineLineRe.MatchString(out[n]) {
			n++
		}
		k := b
		for k < len(clean) && skippable(clean[k]) {
			k++
		}
		if n < len(out) && k < len(clean) && !shown(out[n], clean[k]) {
			t.Errorf("line after %q is %q, want line %d %q", ln, cutRunes(out[n], 80), k+1, cutRunes(clean[k], 80))
		}
	}
}

var jsonLineRe = regexp.MustCompile(`^\s*(?:"(?:[^"\\]|\\.)*"\s*:\s*)?(?:"(?:[^"\\]|\\.)*"|-?[\d.eE+-]+|true|false|null|[\[{\]}])?,?\s*$`)

func jsonDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	if !strings.Contains(got, "condensed from ~") {
		t.Fatalf("JSON lines missing but no condensed-JSON note")
	}
	for _, m := range missing {

		if !jsonLineRe.MatchString(m) && !json.Valid([]byte(m)) {
			t.Errorf("non-JSON line dropped: %q", cutRunes(m, 200))
		}
	}
}

func htmlDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	if !strings.Contains(got, "[lx: HTML body") {
		t.Fatalf("lines missing but no HTML note")
	}
	for _, m := range missing {
		if diagRe.MatchString(m) || strings.HasPrefix(m, "* ") || strings.HasPrefix(m, "< ") {
			t.Errorf("non-body line dropped: %q", m)
		}
	}
}

func lockDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	if !strings.HasPrefix(got, "[lx: lockfile ") {
		t.Fatalf("lines missing but no lockfile summary")
	}
}

func meterDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	for _, m := range missing {
		rest := strings.TrimSpace(stripFrame(m))
		if rest == m || rest != "" && !strings.Contains(got, rest) {
			t.Errorf("line dropped: %q", m)
		}
	}
}
