package jstest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

// hugeJest: 45,000 passing suites and 500 failing ones (~52k lines).
func hugeJest() string {
	var b strings.Builder
	for i := 0; i < 45000; i++ {
		fmt.Fprintf(&b, "PASS test/suite%d.test.js\n", i)
	}
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "FAIL test/f%d.test.js\n  ● f%d › fails with error\n\n    expect(received).toBe(expected) // Object.is equality\n\n    Expected: %d\n    Received: %d\n\n      1 | test(\"x\", () => {\n    > 2 |   expect(f()).toBe(%d);\n        |               ^\n      3 | });\n\n      at Object.toBe (test/f%d.test.js:2:15)\n\n", i, i, i, i+1, i, i)
	}
	b.WriteString("Test Suites: 500 failed, 45000 passed, 45500 total\nTests:       500 failed, 45000 passed, 45500 total\nSnapshots:   0 total\nTime:        99 s\nRan all test suites.\n")
	return b.String()
}

// hugeVitest: one file with 50,000 passing tests and one failure.
func hugeVitest() string {
	var b strings.Builder
	b.WriteString(" RUN  v4.1.11 /home/user/src/proj\n\n ❯ test/big.test.ts (50000 tests | 1 failed) 900ms\n")
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&b, "     ✓ case %d handles errors 0ms\n", i)
	}
	b.WriteString("     × case x 1ms\n\n⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯\n\n FAIL  test/big.test.ts > case x\nAssertionError: expected 1 to be 2\n ❯ test/big.test.ts:3:5\n\n⎯⎯⎯[1/1]⎯\n\n Test Files  1 failed (1)\n      Tests  1 failed | 50000 passed (50001)\n")
	return b.String()
}

func BenchmarkHugeJest(b *testing.B) {
	in, c := hugeJest(), ctx(1, "jest")
	for i := 0; i < b.N; i++ {
		renderJest(c, in)
	}
}

func BenchmarkHugeVitest(b *testing.B) {
	in, c := hugeVitest(), ctx(1, "vitest", "run")
	for i := 0; i < b.N; i++ {
		renderVitest(c, in)
	}
}

// BenchmarkManyFailures* render ~50k lines holding thousands of failures;
// BenchmarkEngineGuard is the engine's own guard pass over the same input,
// the floor set by engine.IsError.
func BenchmarkManyFailuresJest(b *testing.B) {
	in, c := synthJest(3500), ctx(1, "jest")
	for i := 0; i < b.N; i++ {
		renderJest(c, in)
	}
}

func BenchmarkManyFailuresVitest(b *testing.B) {
	in, c := synthVitest(3000), ctx(1, "vitest", "run")
	for i := 0; i < b.N; i++ {
		renderVitest(c, in)
	}
}

func BenchmarkManyFailuresMocha(b *testing.B) {
	in, c := synthMocha(3500), ctx(3500, "mocha")
	for i := 0; i < b.N; i++ {
		renderMocha(c, in)
	}
}

func BenchmarkEngineGuard(b *testing.B) {
	in := synthVitest(3000)
	for i := 0; i < b.N; i++ {
		engine.Guard(in, "")
	}
}
