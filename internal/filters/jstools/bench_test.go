package jstools

import (
	"fmt"
	"strings"
	"testing"
)

// Benchmarks for the two filters whose inputs get largest (a monorepo's
// type errors or lint report). Run with -bench Apply50k.

func genLines(n int, line func(i int) string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(line(i))
		b.WriteByte('\n')
	}
	return b.String()
}

func BenchmarkApply50kTSC(b *testing.B) {
	in := genLines(50000, func(i int) string {
		return fmt.Sprintf("src/f%d.ts(%d,3): error TS2554: Expected %d arguments, but got 1.", i%500, i, i%9)
	})
	c := ctx(1, "tsc")
	for i := 0; i < b.N; i++ {
		tsc{}.Apply(c, in)
	}
}

func BenchmarkApply50kESLint(b *testing.B) {
	in := genLines(50000, func(i int) string {
		if i%50 == 0 {
			return fmt.Sprintf("/home/user/src/app/src/f%d.js", i)
		}
		sev := "error"
		if i%3 == 0 {
			sev = "warning"
		}
		return fmt.Sprintf("  %d:%d  %s  Message number %d here  rule-%d", i, i%80, sev, i%97, i%13)
	})
	c := ctx(1, "eslint", ".")
	for i := 0; i < b.N; i++ {
		eslint{}.Apply(c, in)
	}
}
