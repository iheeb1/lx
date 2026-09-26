package engine_test

import (
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func benchCase(b *testing.B, cat, name string) {
	c := fixture.Load(b, cat, name)
	in, ctx := c.Clean(), c.Context()
	b.SetBytes(int64(len(in)))
	b.ResetTimer()
	for b.Loop() {
		engine.Generic(ctx, in)
	}
}

func BenchmarkGenericMinifiedGrep(b *testing.B) { benchCase(b, "search", "grep-rn-minified-lines") }
func BenchmarkGenericGitLogFull(b *testing.B)   { benchCase(b, "git", "git-log-full-history") }
