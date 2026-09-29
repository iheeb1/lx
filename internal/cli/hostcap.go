package cli

import (
	"os"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/hook"
)

const (
	receiptRoom = 200

	minHostLimit = 1000

	exitUnknown = -1
)

var claudeLimits = hook.ClaudeOutputLimits

func hostLimits() (limit, capChars int) { return hostLimitsFor(0) }

func hostLimitsFor(exit int) (limit, capChars int) {
	if v, ok := os.LookupEnv("LX_MAX_CHARS"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			if n <= 0 {
				return 0, 0
			}
			n = max(n, minHostLimit)
			return n, n - receiptRoom
		}
	}
	if os.Getenv("CLAUDECODE") != "1" {
		return 0, 0
	}
	cwd, _ := os.Getwd()
	l := claudeLimits(cwd)
	n := l.Pass
	if exit != 0 {
		n = l.Fail
	}
	return n, n*9/10 - receiptRoom
}

func hostCharCap() int { return hostCharCapFor(0) }

func hostCharCapFor(exit int) int {
	_, c := hostLimitsFor(exit)
	return c
}
