// Command lx runs a command and prints a condensed view of its output for
// LLM coding agents, keeping the exit code and every error line.
package main

import (
	"os"

	"github.com/iheeb1/lx/internal/cli"
	_ "github.com/iheeb1/lx/internal/filters"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
