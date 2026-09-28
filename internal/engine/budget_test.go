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

	out := BudgetFit(in, 1_000_000, 5000, false, false)
	if len(out) > 5000 || len(out) < 4000 {
		t.Fatalf("char cap 5000 produced %d bytes; should fill most of it", len(out))
	}
	if !strings.HasPrefix(out, "commit 0000") || !strings.Contains(out, "commit 4999") || !strings.Contains(out, "lines omitted") {
		t.Fatalf("head, tail and a counted gap must survive:\n%s", out)
	}

	out = BudgetFit(in, 1000, 1_000_000, false, false)
	if tokens.Count(out) > 1000 {
		t.Fatalf("token budget 1000 produced %d tokens", tokens.Count(out))
	}

	for _, tc := range []struct{ tok, chars int }{{800, 20000}, {20000, 3000}, {2000, 7000}} {
		out := BudgetFit(in, tc.tok, tc.chars, true, true)
		if tokens.Count(out) > tc.tok || len(out) > tc.chars {
			t.Errorf("caps %d tokens / %d chars: got %d tokens, %d bytes", tc.tok, tc.chars, tokens.Count(out), len(out))
		}
	}

	if got := BudgetFit("a\nb\nc", 100, 100, true, true); got != "a\nb\nc" {
		t.Fatalf("fitting input changed: %q", got)
	}

	if BudgetFit(in, 3000, 0, true, true) != Budget(in, 3000, true, true) {
		t.Fatal("BudgetFit with maxChars 0 must equal Budget")
	}
}

func TestBudgetFitCountsGapMarkers(t *testing.T) {
	var lines []string
	n := 20000
	if testenv.Race {
		n = 4000
	}
	for i := 0; i < n; i++ {
		if i%7 == 3 {
			lines = append(lines, fmt.Sprintf("FAIL %d", i))
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

func TestBudgetFitLines(t *testing.T) {
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, fmt.Sprintf("commit %04d: refactor the widget factory", i))
	}
	in := strings.Join(lines, "\n")
	for _, maxLines := range []int{1, 2, 3, 4, 5, 10, 39, 99, 499, 500} {
		for _, errorsFirst := range []bool{false, true} {
			out := BudgetFitLines(in, 1_000_000, 0, maxLines, CutEither, true, errorsFirst)
			got := countLines(out)
			if got > maxLines {
				t.Errorf("cap %d: %d lines", maxLines, got)
			}
			if maxLines >= 3 && got < maxLines {
				t.Errorf("cap %d: only %d lines used", maxLines, got)
			}
			if maxLines == 500 && out != in {
				t.Errorf("cap %d: a fitting output changed", maxLines)
			}
			if maxLines >= 5 && maxLines < 500 {
				if !strings.HasPrefix(out, "commit 0000") && errorsFirst == false {
					t.Errorf("cap %d: the head must survive:\n%s", maxLines, out)
				}
				if !strings.HasSuffix(out, "commit 0499: refactor the widget factory") || !strings.Contains(out, "lines omitted") {
					t.Errorf("cap %d: the tail and a counted gap must survive:\n%s", maxLines, out)
				}
			}
		}
	}

	if BudgetFitLines(in, 3000, 5000, 0, CutHead, true, true) != BudgetFit(in, 3000, 5000, true, true) {
		t.Fatal("BudgetFitLines with maxLines 0 must equal BudgetFit")
	}

	for _, tc := range []struct{ tok, chars, lines int }{{300, 20000, 100}, {20000, 900, 100}, {20000, 20000, 12}} {
		out := BudgetFitLines(in, tc.tok, tc.chars, tc.lines, CutEither, true, true)
		if tokens.Count(out) > tc.tok || len(out) > tc.chars || countLines(out) > tc.lines {
			t.Errorf("caps %+v: %d tokens, %d bytes, %d lines", tc, tokens.Count(out), len(out), countLines(out))
		}
	}
}

func TestBudgetFitLinesErrorsFirst(t *testing.T) {
	var lines []string
	for i := 0; i < 2000; i++ {
		if i%200 == 100 {
			lines = append(lines, fmt.Sprintf("ERROR: shard %d failed", i))
			continue
		}
		lines = append(lines, fmt.Sprintf("shard %d processed %d records", i, i*31))
	}
	lines = append(lines, "done: 10 shards failed")
	in := strings.Join(lines, "\n")

	for _, maxLines := range []int{22, 25, 39, 60} {
		out := BudgetFitLines(in, 1_000_000, 0, maxLines, CutEither, true, true)
		if countLines(out) > maxLines {
			t.Fatalf("cap %d: %d lines", maxLines, countLines(out))
		}
		for i := 100; i < 2000; i += 200 {
			if !strings.Contains(out, fmt.Sprintf("ERROR: shard %d failed", i)) {
				t.Errorf("cap %d: error %d dropped:\n%s", maxLines, i, out)
			}
		}
	}

	for maxLines, want := range map[int]int{10: 3, 21: 7} {
		out := BudgetFitLines(in, 1_000_000, 0, maxLines, CutEither, true, true)
		kept := strings.Count(out, "ERROR: shard")
		if countLines(out) > maxLines || !strings.HasSuffix(out, "done: 10 shards failed") || kept != want {
			t.Errorf("cap %d: %d error lines kept, want %d:\n%s", maxLines, kept, want, out)
		}
	}

	lines = lines[:0]
	for i := 0; i < 300; i++ {
		switch i % 100 {
		case 50:
			lines = append(lines, fmt.Sprintf("--- FAIL: TestShard%d (0.00s)", i))
		case 51:
			lines = append(lines, fmt.Sprintf("    shard_test.go:%d: want 1 record, got 0", i))
		default:
			lines = append(lines, fmt.Sprintf("=== RUN   TestOther%d", i))
		}
	}
	out := BudgetFitLines(strings.Join(lines, "\n")+"\nFAIL", 1_000_000, 0, 19, CutEither, true, true)
	for _, i := range []int{51, 151, 251} {
		if !strings.Contains(out, fmt.Sprintf("shard_test.go:%d: want 1 record", i)) {
			t.Errorf("the message after failure %d was dropped:\n%s", i-1, out)
		}
	}
}

func TestBudgetFitLinesRandom(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	iters := 3000
	if testenv.Race {
		iters = 300
	}
	for iter := 0; iter < iters; iter++ {
		n := r.Intn(80)
		var lines []string
		for i := 0; i < n; i++ {
			switch r.Intn(6) {
			case 0:
				lines = append(lines, "")
			case 1:
				lines = append(lines, fmt.Sprintf("error: %d broke", i))
			case 2:
				lines = append(lines, "warning: x")
			default:
				lines = append(lines, strings.Repeat("w", r.Intn(90)))
			}
		}
		in := strings.Join(lines, "\n")
		maxLines := 1 + r.Intn(40)
		maxChars := 0
		if r.Intn(3) == 0 {
			maxChars = 64 + r.Intn(3000)
		}
		out := BudgetFitLines(in, 50+r.Intn(3000), maxChars, maxLines, Cut(r.Intn(3)), r.Intn(2) == 0, r.Intn(2) == 0)
		if countLines(out) > maxLines {
			t.Fatalf("iter %d: cap %d, %d lines:\n%q", iter, maxLines, countLines(out), out)
		}
		if maxChars > 0 && len(out) > maxChars {
			t.Fatalf("iter %d: cap %d bytes, %d", iter, maxChars, len(out))
		}
	}
}

func FuzzBudgetFitLines(f *testing.F) {
	f.Add("a\nb\nerror: c\n\nd", 3, 100, 64, true, uint8(0))
	f.Add(strings.Repeat("x\n", 50)+"FAIL", 5, 1000, 0, false, uint8(1))
	f.Add("", 1, 1, 0, true, uint8(2))
	f.Add("\n\n\n", 2, 10, 70, true, uint8(0))
	f.Fuzz(func(t *testing.T, in string, maxLines, maxTokens, maxChars int, errorsFirst bool, cutN uint8) {
		if len(in) > 1<<16 {
			return
		}
		maxLines = 1 + fitAbs(maxLines)%200
		maxTokens = fitAbs(maxTokens) % 20000
		maxChars = fitAbs(maxChars) % 50000
		cut := Cut(cutN % 3)
		out := BudgetFitLines(in, maxTokens, maxChars, maxLines, cut, true, errorsFirst)
		if countLines(out) > maxLines {
			t.Fatalf("cap %d lines: %d", maxLines, countLines(out))
		}
		if maxChars > 0 && len(out) > max(maxChars, MinMaxChars) {
			t.Fatalf("cap %d bytes: %d", maxChars, len(out))
		}
		if again := BudgetFitLines(in, maxTokens, maxChars, maxLines, cut, true, errorsFirst); again != out {
			t.Fatal("not deterministic")
		}
	})
}

func fitAbs(n int) int {
	if n < 0 {
		if n == -n {
			return 0
		}
		return -n
	}
	return n
}

func TestBudgetFitLinesCut(t *testing.T) {
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, fmt.Sprintf("commit %04d: refactor the widget factory", i))
	}
	in := strings.Join(lines, "\n")
	for _, errorsFirst := range []bool{true, false} {
		head := BudgetFitLines(in, 1_000_000, 0, 20, CutHead, false, errorsFirst)
		if want := strings.Join(lines[:19], "\n") + "\n… 481 lines omitted …"; head != want {
			t.Errorf("head cut (errorsFirst %v):\n%s", errorsFirst, head)
		}
		tail := BudgetFitLines(in, 1_000_000, 0, 20, CutTail, false, errorsFirst)
		if want := "… 481 lines omitted …\n" + strings.Join(lines[481:], "\n"); tail != want {
			t.Errorf("tail cut (errorsFirst %v):\n%s", errorsFirst, tail)
		}
		either := BudgetFitLines(in, 1_000_000, 0, 20, CutEither, false, errorsFirst)
		if !strings.HasPrefix(either, lines[0]+"\n") || !strings.HasSuffix(either, "\n"+lines[499]) || countLines(either) != 20 {
			t.Errorf("either end (errorsFirst %v): both ends:\n%s", errorsFirst, either)
		}
	}

	lines[250] = "ERROR: commit 0250 broke the build"
	lines[400] = "ERROR: commit 0400 broke the build"
	in = strings.Join(lines, "\n")
	for _, cut := range []Cut{CutHead, CutTail, CutEither} {
		out := BudgetFitLines(in, 1_000_000, 0, 20, cut, true, true)
		if countLines(out) > 20 || !strings.Contains(out, lines[250]) || !strings.Contains(out, lines[400]) {
			t.Errorf("cut %d: the error lines must come first:\n%s", cut, out)
		}
		switch cut {
		case CutHead:
			if !strings.HasPrefix(out, strings.Join(lines[:10], "\n")) || strings.Contains(out, lines[499]) {
				t.Errorf("head cut: the head, not the tail:\n%s", out)
			}
		case CutTail:
			if !strings.HasSuffix(out, strings.Join(lines[491:], "\n")) || strings.Contains(out, lines[0]) {
				t.Errorf("tail cut: the tail, not the head:\n%s", out)
			}
		}
	}

	for _, cut := range []Cut{CutHead, CutTail} {
		if BudgetFitLines(in, 300, 2000, 0, cut, true, true) != BudgetFit(in, 300, 2000, true, true) {
			t.Errorf("cut %d without a line cap must equal BudgetFit", cut)
		}
	}
}

func TestParseFit(t *testing.T) {
	for s, want := range map[string]struct {
		n   int
		cut Cut
	}{
		"1": {1, CutEither}, "40": {40, CutEither}, "007": {7, CutEither},
		"head:40": {40, CutHead}, "tail:40": {40, CutTail}, "tail:1": {1, CutTail},
	} {
		n, cut, ok := ParseFit(s)
		if !ok || n != want.n || cut != want.cut {
			t.Errorf("ParseFit(%q) = %d, %d, %v", s, n, cut, ok)
		}
		if back := FitArg(n, cut); strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(back, "head:"), "tail:"), "0") != strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(s, "head:"), "tail:"), "0") {
			t.Errorf("FitArg(%d, %d) = %q, from %q", n, cut, back, s)
		}
	}
	for _, s := range []string{"", "0", "-3", "+3", "4x", " 3", "3 ", "1.5", "99999999999999999999",
		"head:", "tail:0", "head:-1", "TAIL:3", "both:3", "tail:3:3", ":3", "head3", "tail:+3", "head: 3"} {
		if n, cut, ok := ParseFit(s); ok {
			t.Errorf("ParseFit(%q) = %d, %d, ok", s, n, cut)
		}
	}
}
