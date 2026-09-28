package main

import (
	"os"

	"github.com/iheeb1/lx/internal/cli"
	_ "github.com/iheeb1/lx/internal/filters"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
