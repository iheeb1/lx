package engine

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"strings"
	"testing"
	"time"

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
