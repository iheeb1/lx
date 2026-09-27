package cli

import (
	"os"

	"github.com/iheeb1/lx/internal/discover"
)

// runDiscover runs `lx discover [--days N] [--limit N] [--dir DIR] [--json]
// [--fidelity] [--examples]`: replay the user's own Claude Code transcripts
// through lx and report what it would save, and with --fidelity whether its
// views kept what the agent acted on. Flags and output live in
// internal/discover (discover.Main), where they are tested.
func runDiscover(args []string) int { return discover.Main(args, os.Stdout, os.Stderr) }
