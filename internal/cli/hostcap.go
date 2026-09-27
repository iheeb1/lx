package cli

import (
	"os"
	"strconv"
	"strings"
)

// Host output limits.
//
// Claude Code replaces a Bash result longer than BASH_MAX_OUTPUT_LENGTH
// characters (default 30,000) with a short preview plus a saved file, so a
// view over it loses its receipt and the agent reads a fragment. lx keeps
// what it prints under the limit instead: views (engine.Options.MaxChars)
// and `lx show` both fit hostCharCap().
const (
	claudeDefaultLimit = 30000
	// receiptRoom is left for the receipt line after a view.
	receiptRoom = 200
	// minHostLimit: smaller limits count as this much; below it nothing
	// useful fits.
	minHostLimit = 1000
)

// hostLimits returns the host's output limit in characters and the cap lx
// fits its output into; (0, 0) when there is no known limit.
//
//   - LX_MAX_CHARS=N: the limit is N (at least minHostLimit), the cap
//     N−200; N <= 0 disables the cap, even inside Claude Code.
//   - else, in Claude Code's Bash tool (CLAUDECODE=1): the limit is
//     BASH_MAX_OUTPUT_LENGTH when it is a positive integer, else 30,000,
//     and the cap is 9/10 of it minus 200.
//   - else there is no cap.
//
// The cap counts bytes, never fewer than the characters a host counts.
func hostLimits() (limit, capChars int) {
	if v, ok := os.LookupEnv("LX_MAX_CHARS"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			if n <= 0 {
				return 0, 0
			}
			n = max(n, minHostLimit)
			return n, n - receiptRoom
		}
	}
	if os.Getenv("CLAUDECODE") == "1" {
		n := claudeDefaultLimit
		if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("BASH_MAX_OUTPUT_LENGTH"))); err == nil && v > 0 {
			n = max(v, minHostLimit)
		}
		return n, n*9/10 - receiptRoom
	}
	return 0, 0
}

// hostCharCap is the character cap for everything lx prints (0 = none).
func hostCharCap() int {
	_, c := hostLimits()
	return c
}
