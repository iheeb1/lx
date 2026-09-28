package cli

import (
	"os"
	"strconv"
	"strings"
)

const (
	claudeDefaultLimit = 30000

	receiptRoom = 200

	minHostLimit = 1000
)

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

func hostCharCap() int {
	_, c := hostLimits()
	return c
}
