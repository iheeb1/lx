package agentctx

import (
	"fmt"
	"strings"
	"testing"
)

func termMap(s *Snapshot) map[string]float64 {
	m := map[string]float64{}
	if f := s.Focus(); f != nil {
		for _, t := range f.Terms {
			m[t.Text] = t.Weight
		}
	}
	return m
}

func TestFocusKeepsSpecificTerms(t *testing.T) {
	s := &Snapshot{
		Assistant: []string{
			"The failure is in `TestParseMode/verify_case`: parseMode returns ModeAuto. See internal/engine/modes.go:42 " +
				"and pkg.ParseMode; the error is TS2322 (and E0308, CVE-2024-12345). Check it('renders the header') and " +
				"describe(\"Header component\"). Also test_parse_mode in tests/test_modes.py, snake_case_name, HTTP_PROXY. " +
				"Visit https://github.com/foo/bar.go for details. SHA256 mismatch, ISO8601 dates, RFC3339 times. " +
				"The message was \"connection refused by upstream\" and 'invalid token'. It's `--fit` that matters, not `true`.",
			"Earlier I looked at BenchmarkFit and os.Getenv in main.go.",
		},
		Prompt: "please fix TestFoo, and the lint rule no-unused-vars in Button.tsx",
	}
	got := termMap(s)
	for _, want := range []string{"TestParseMode", "parseMode", "ModeAuto", "modes.go", "pkg.ParseMode", "TS2322", "E0308",
		"CVE-2024-12345", "renders the header", "Header component", "test_parse_mode", "test_modes.py", "snake_case_name",
		"HTTP_PROXY", "connection refused by upstream", "invalid token", "BenchmarkFit", "os.Getenv", "main.go",
		"TestFoo", "Button.tsx"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	for _, noise := range []string{"github.com", "bar.go", "foo", "SHA256", "ISO8601", "RFC3339", "The", "the", "error",
		"Check", "Visit", "details", "failure", "true", "--fit", "please", "fix", "verify_case", "internal", "engine",
		"TestParseMode/verify_case", "It's", "no-unused-vars", "unused"} {
		if _, ok := got[noise]; ok {
			t.Errorf("noise %q in %v", noise, got)
		}
	}
	if got["ModeAuto"] != 1.0 || got["BenchmarkFit"] != 0.8 || got["TestFoo"] != promptWeight {
		t.Errorf("weights: %v", got)
	}
}

func TestFocusWeightsDecayWithAge(t *testing.T) {
	s := &Snapshot{Assistant: []string{"about AlphaOne", "about BetaTwo", "about GammaThree and AlphaOne"}, Prompt: "fix GammaThree"}
	got := termMap(s)
	if got["AlphaOne"] != 1.0 || got["BetaTwo"] != 0.8 || got["GammaThree"] != promptWeight {
		t.Fatalf("%v", got)
	}
	f := s.Focus()
	for i := 1; i < len(f.Terms); i++ {
		if f.Terms[i].Weight > f.Terms[i-1].Weight {
			t.Fatalf("terms not sorted by weight: %+v", f.Terms)
		}
	}
}

func TestFocusCaps(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "HandlerNumber%c%c ", 'A'+i%26, 'a'+i/26)
	}
	s := &Snapshot{Assistant: []string{b.String()}}
	for i := 0; i < 25; i++ {
		s.Files = append(s.Files, File{Path: fmt.Sprintf("/repo/f%d.go", i), Op: "edit"})
	}
	f := s.Focus()
	if len(f.Terms) != maxTerms || len(f.Files) != maxFocusFiles || f.Files[0] != "/repo/f0.go" {
		t.Fatalf("terms %d files %d", len(f.Terms), len(f.Files))
	}
}

func TestFocusEmpty(t *testing.T) {
	for _, s := range []*Snapshot{
		{},
		{Assistant: []string{"Let me check the status and run the tests now."}},
		{Prompt: "ok, do it"},
	} {
		if f := s.Focus(); f != nil {
			t.Errorf("%+v: focus %+v, want nil", s, f)
		}
	}
	if f := (&Snapshot{Files: []File{{Path: "/a.go"}}}).Focus(); f == nil || len(f.Files) != 1 {
		t.Errorf("files alone make a focus: %+v", f)
	}
}

func TestFocusIgnoresLongProseQuotes(t *testing.T) {
	s := &Snapshot{Prompt: `Say "let me make sure the tests pass now" or "// Package tokens estimates token counts." but keep "exit status 2"`}
	got := termMap(s)
	if len(got) != 1 || got["exit status 2"] == 0 {
		t.Fatalf("%v", got)
	}
}

func TestFocusFromFixture(t *testing.T) {
	s := load(t, Source{Path: "testdata/claude/main.jsonl"})
	got := termMap(s)
	for _, want := range []string{"TestParseMode", "ParseMode", "parseMode", "ModeAuto", "modes.go", "modes_test.go", "E0308", "parser.rs"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	f := s.Focus()
	if len(f.Files) != 2 || f.Files[0] != "/repo/internal/engine/modes_test.go" {
		t.Errorf("files %v", f.Files)
	}
}

func TestFocusQuotedTermsHaveNoQuotes(t *testing.T) {
	got := termMap(&Snapshot{Prompt: `the message 'said "no" twice' and 'plain words here'`})
	for term := range got {
		if strings.Contains(term, `"`) {
			t.Fatalf("term %q carries a quote into receipts", term)
		}
	}
	if _, ok := got["plain words here"]; !ok {
		t.Fatalf("%v", got)
	}
}
