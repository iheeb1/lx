package cli

import (
	"os"

	"github.com/iheeb1/lx/internal/discover"
)

func runDiscover(args []string) int { return discover.Main(args, os.Stdout, os.Stderr) }
