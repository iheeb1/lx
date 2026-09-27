package engine

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/tokens"
)

func TestBudgetUsesTheBudget(t *testing.T) {
	var lines []string
	for i := 0; i < 5000; i++ {
		lines = append(lines, fmt.Sprintf("commit %04d: refactor the widget factory module", i))
	}
	out := Budget(strings.Join(lines, "\n"), 4000, false, false)
	got := tokens.Count(out)
	if got > 4000 || got < 3000 {
		t.Fatalf("budget 4000 produced %d tokens; should fill most of it", got)
	}
	if !strings.HasPrefix(out, "commit 0000") || !strings.Contains(out, "commit 4999") {
		t.Fatal("head and tail must both survive")
	}
	if !strings.Contains(out, "lines omitted") {
		t.Fatal("gap must be marked")
	}
}

func TestBudgetKeepsErrorsFirst(t *testing.T) {
	var lines []string
	for i := 0; i < 4000; i++ {
		lines = append(lines, fmt.Sprintf("step %d ok, moving on to the next stage of the pipeline", i))
		if i%500 == 250 {
			lines = append(lines, fmt.Sprintf("ERROR: stage %d exploded: disk full", i))
		}
	}
	out := Budget(strings.Join(lines, "\n"), 2000, true, true)
	for i := 250; i < 4000; i += 500 {
		if !strings.Contains(out, fmt.Sprintf("ERROR: stage %d exploded", i)) {
			t.Fatalf("error line for stage %d dropped", i)
		}
	}
}

func TestBudgetHugeLineAndSpeed(t *testing.T) {
	huge := strings.Repeat("abcdefghij", 200000)
	out := Budget("start\n"+huge+"\nend", 500, false, true)
	if !strings.Contains(out, "start") || !strings.Contains(out, "end") || tokens.Count(out) > 500 {
		t.Fatalf("huge line not capped: %d tokens", tokens.Count(out))
	}
	var lines []string
	for i := 0; i < 50000; i++ {
		lines = append(lines, fmt.Sprintf("error_count=%d warning: something deprecated found", i))
	}
	start := time.Now()
	Budget(strings.Join(lines, "\n"), 8000, true, true)
	// 50k lines that all hold warning words is an extreme case; each still
	// needs the benign-span pass (~30µs). Guard against regressions only.
	if d := time.Since(start); d > testenv.Scale(6*time.Second) {
		t.Fatalf("Budget on 50k classified lines took %v", d)
	}
}

func TestGuardScales(t *testing.T) {
	var in []string
	for i := 0; i < 50000; i++ {
		in = append(in, fmt.Sprintf("FAIL: case %d broke", i))
	}
	s := strings.Join(in, "\n")
	start := time.Now()
	out, added := Guard(s, s[:len(s)/2])
	if d := time.Since(start); d > testenv.Scale(3*time.Second) {
		t.Fatalf("Guard took %v", d)
	}
	if added != maxGuardLines || !strings.Contains(out, "more error lines") {
		t.Fatalf("added=%d", added)
	}
}

func TestBudgetFitHonoursBothCaps(t *testing.T) {
	var lines []string
	for i := 0; i < 5000; i++ {
		lines = append(lines, fmt.Sprintf("commit %04d: refactor the widget factory module so it reads well", i))
	}
	in := strings.Join(lines, "\n")
	// Characters bind: plenty of tokens, few bytes.
	out := BudgetFit(in, 1_000_000, 5000, false, false)
	if len(out) > 5000 || len(out) < 4000 {
		t.Fatalf("char cap 5000 produced %d bytes; should fill most of it", len(out))
	}
	if !strings.HasPrefix(out, "commit 0000") || !strings.Contains(out, "commit 4999") || !strings.Contains(out, "lines omitted") {
		t.Fatalf("head, tail and a counted gap must survive:\n%s", out)
	}
	// Tokens bind: plenty of bytes, few tokens.
	out = BudgetFit(in, 1000, 1_000_000, false, false)
	if tokens.Count(out) > 1000 {
		t.Fatalf("token budget 1000 produced %d tokens", tokens.Count(out))
	}
	// Both given: the tighter one wins.
	for _, tc := range []struct{ tok, chars int }{{800, 20000}, {20000, 3000}, {2000, 7000}} {
		out := BudgetFit(in, tc.tok, tc.chars, true, true)
		if tokens.Count(out) > tc.tok || len(out) > tc.chars {
			t.Errorf("caps %d tokens / %d chars: got %d tokens, %d bytes", tc.tok, tc.chars, tokens.Count(out), len(out))
		}
	}
	// Within both caps: untouched.
	if got := BudgetFit("a\nb\nc", 100, 100, true, true); got != "a\nb\nc" {
		t.Fatalf("fitting input changed: %q", got)
	}
	// No char cap: exactly Budget.
	if BudgetFit(in, 3000, 0, true, true) != Budget(in, 3000, true, true) {
		t.Fatal("BudgetFit with maxChars 0 must equal Budget")
	}
}

// Scattered error lines make a gap marker each. The markers must count
// toward the character cap, or a cap that the kept lines alone meet would
// be blown by "… N lines omitted …" lines.
func TestBudgetFitCountsGapMarkers(t *testing.T) {
	var lines []string
	n := 20000
	if testenv.Race {
		n = 4000
	}
	for i := 0; i < n; i++ {
		if i%7 == 3 {
			lines = append(lines, fmt.Sprintf("FAIL %d", i)) // short lines, long markers
			continue
		}
		lines = append(lines, fmt.Sprintf("x%d", i))
	}
	in := strings.Join(lines, "\n")
	caps := []int{64, 100, 500, 1000, 5000, 12345, 26800, 60000}
	if testenv.Race {
		caps = []int{64, 5000, 26800}
	}
	for _, maxChars := range caps {
		for _, errorsFirst := range []bool{false, true} {
			out := BudgetFit(in, 1_000_000, maxChars, true, errorsFirst)
			if len(out) > maxChars {
				t.Errorf("cap %d errorsFirst=%v: %d bytes", maxChars, errorsFirst, len(out))
			}
			markers := strings.Count(out, "lines omitted")
			if errorsFirst && maxChars >= 5000 && len(in) > maxChars && markers < 20 {
				t.Errorf("cap %d: expected several gaps, got %d markers", maxChars, markers)
			}
		}
	}
	// Scattered real errors, many gaps: every error kept while the errors,
	// the line after each and a marker per gap fit the errors' share (the
	// tail's quarter comes first, the errors stop at 60%).
	lines = lines[:0]
	for i := 0; i < 6000; i++ {
		if i%150 == 75 {
			lines = append(lines, fmt.Sprintf("ERROR: shard %d failed: connection refused", i))
			continue
		}
		lines = append(lines, fmt.Sprintf("shard %d processed %d records in %dms without trouble", i, i*31, i%97))
	}
	out := BudgetFit(strings.Join(lines, "\n"), 1_000_000, 16000, true, true)
	if len(out) > 16000 {
		t.Fatalf("%d bytes over the 16000 cap", len(out))
	}
	for i := 75; i < 6000; i += 150 {
		if !strings.Contains(out, fmt.Sprintf("ERROR: shard %d failed", i)) {
			t.Fatalf("error line %d dropped under the char cap", i)
		}
	}
}

func TestBudgetFitAdversarial(t *testing.T) {
	cases := map[string]string{
		"one huge line":       strings.Repeat("abcdefghij", 50000),
		"huge multibyte line": strings.Repeat("€ü", 40000),
		"huge error line":     "fatal: " + strings.Repeat("bad ", 20000),
		"blank lines":         strings.Repeat("\n", 30000),
		"many huge lines":     strings.Repeat(strings.Repeat("z", 5000)+"\n", 40),
		"no newline tail":     strings.Repeat("line\n", 10000) + "last",
	}
	caps := []int{1, 64, 300, 2000, 26800}
	if testenv.Race {
		caps = []int{64, 2000}
	}
	for name, in := range cases {
		for _, maxChars := range caps {
			out := BudgetFit(in, 8000, maxChars, true, true)
			if lim := max(maxChars, MinMaxChars); len(out) > lim {
				t.Errorf("%s, cap %d: %d bytes", name, maxChars, len(out))
			}
		}
	}
}

// Under a small character cap a long error line is shortened, never
// dropped whole: ShortenLine keeps error lines up to 1,200 characters,
// which caps of a few thousand bytes could otherwise only replace with an
// "… N lines omitted …" marker.
func TestBudgetFitShortensLongErrorLine(t *testing.T) {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, fmt.Sprintf("step %d ok, moving on to the next stage", i))
		if i == 200 {
			lines = append(lines, "error: 构建失败 "+strings.Repeat("依赖解析错误 ", 180)+"(exit 2)")
		}
	}
	in := strings.Join(lines, "\n")
	for _, maxChars := range []int{1000, 1500, 2000, 3000, 5000, 8000} {
		out := BudgetFit(in, 8000, maxChars, true, true)
		if len(out) > maxChars {
			t.Errorf("cap %d: %d bytes", maxChars, len(out))
		}
		if !strings.Contains(out, "error: 构建失败 依赖解析错误") || !strings.Contains(out, "(exit 2)") || !utf8.ValidString(out) {
			t.Errorf("cap %d: the error line (start and end) must survive, shortened:\n%s", maxChars, out)
		}
	}
	// Claude Code's default cap leaves ShortenLine's result alone, even
	// for its longest (1,200 four-byte runes of an error line).
	huge := "error: " + strings.Repeat("😀", 5000)
	in = strings.Repeat("filler line\n", 3000) + huge + "\n" + strings.Repeat("more filler\n", 3000)
	if out := BudgetFit(in, 1_000_000, 26800, true, true); !strings.Contains(out, ShortenLine(huge, 400)) {
		t.Fatal("at the default cap an error line must be exactly ShortenLine's")
	}
}

func TestShortenBytes(t *testing.T) {
	if got := shortenBytes("short", 64); got != "short" {
		t.Fatalf("fitting line changed: %q", got)
	}
	r := rand.New(rand.NewSource(5))
	alphabet := []string{"a", "é", "€", "😀", " ", "\t"}
	for iter := 0; iter < 2000; iter++ {
		var b strings.Builder
		for n := r.Intn(3000); n > 0; n-- {
			b.WriteString(alphabet[r.Intn(len(alphabet))])
		}
		line := b.String()
		lim := minLineChars + r.Intn(600)
		got := shortenBytes(line, lim)
		if len(got) > lim || !utf8.ValidString(got) {
			t.Fatalf("limit %d: %d bytes, valid=%v", lim, len(got), utf8.ValidString(got))
		}
		if len(line) <= lim {
			if got != line {
				t.Fatalf("limit %d: a fitting line changed", lim)
			}
			continue
		}
		head, rest, ok := strings.Cut(got, " …[+")
		if !ok || !strings.HasPrefix(line, head) {
			t.Fatalf("limit %d: head lost: %q", lim, got)
		}
		num, tail, ok := strings.Cut(rest, " chars]… ")
		n, err := strconv.Atoi(num)
		if !ok || err != nil || !strings.HasSuffix(line, tail) || utf8.RuneCountInString(line) != utf8.RuneCountInString(head)+n+utf8.RuneCountInString(tail) {
			t.Fatalf("limit %d: marker or tail wrong: %q", lim, got)
		}
	}
}
