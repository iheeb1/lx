// Package baseline holds the blind truncations lx is measured against: what
// an agent reads when it cuts a command's output instead of condensing it.
// The corpus benchmark and `lx discover --fidelity` share them, so both
// compare lx with exactly the same cut.
package baseline

import (
	"strings"

	"github.com/iheeb1/lx/internal/tokens"
)

// HeadTail keeps the first and last lines of s, half the budget (in tokens)
// each — the best a size-matched blind truncation can do. s is returned
// unchanged when it fits the budget.
func HeadTail(s string, budget int) string {
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
		tail = append(tail, lines[i])
		used += c
	}
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 { // collected last line first
		tail[i], tail[j] = tail[j], tail[i]
	}
	return strings.Join(head, "\n") + "\n…\n" + strings.Join(tail, "\n")
}
