package baseline

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func reference(s string, budget int) string {
	lines := strings.Split(s, "\n")
	if tokens.Count(s) <= budget {
		return s
	}
	half := budget / 2
	var head, tail []string
	used := 0
	for _, ln := range lines {
		c := tokens.Count(ln) + 1
		if used+c > half {
			break
		}
		head = append(head, ln)
		used += c
	}
	used = 0
	for i := len(lines) - 1; i >= len(head); i-- {
		c := tokens.Count(lines[i]) + 1
		if used+c > half {
			break
		}
		tail = append([]string{lines[i]}, tail...)
		used += c
	}
	return strings.Join(head, "\n") + "\n…\n" + strings.Join(tail, "\n")
}

func TestHeadTailMatchesReference(t *testing.T) {
	var b strings.Builder
	for i := range 500 {
		fmt.Fprintf(&b, "line %d: some words here, error at file_%d.go:%d\n", i, i%7, i)
	}
	inputs := []string{"", "one line", "a\nb\nc", b.String(), strings.Repeat("x", 5000), strings.Repeat("\n", 300)}
	for _, in := range inputs {
		for _, budget := range []int{0, 1, 2, 10, 57, 400, 1000, 100000} {
			if got, want := HeadTail(in, budget), reference(in, budget); got != want {
				t.Fatalf("budget %d, input %.40q: HeadTail differs from the reference", budget, in)
			}
		}
	}
}

func TestHeadTailShape(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "line %03d\n", i)
	}
	s := strings.TrimSuffix(b.String(), "\n")
	if got := HeadTail(s, 1<<20); got != s {
		t.Error("output within budget must be returned unchanged")
	}
	got := HeadTail(s, 100)
	if !strings.HasPrefix(got, "line 000\n") || !strings.HasSuffix(got, "\nline 199") || !strings.Contains(got, "\n…\n") {
		t.Errorf("head+tail cut:\n%s", got)
	}
	if n := tokens.Count(got); n > 100+5 {
		t.Errorf("cut is %d tokens for a budget of 100", n)
	}
}

func TestHeadTailMatchesReferenceOnCorpus(t *testing.T) {
	n := 0
	for _, c := range fixture.All(t) {
		clean := c.Clean()
		total := tokens.Count(clean)
		for _, budget := range []int{0, 7, 64, 150, 500, 2000, 8000, total / 3, total / 2, total - 1, total} {
			if got, want := HeadTail(clean, budget), reference(clean, budget); got != want {
				t.Fatalf("%s/%s at budget %d: HeadTail differs from the reference", c.Category, c.Name, budget)
			}
			n++
		}
	}
	if n < 1000 {
		t.Errorf("only %d cuts compared", n)
	}
}
