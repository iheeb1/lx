package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/fixture"
)

func main() {
	dir := fixture.Root() + "/testdata/corpus"
	cases, err := fixture.ReadAll(dir)
	if err != nil {
		panic(err)
	}
	only := ""
	if len(os.Args) > 1 {
		only = os.Args[1]
	}
	for _, fc := range cases {
		id := fc.Category + "/" + fc.Name
		if fc.Meta.ExitCode == 0 || only != "" && !strings.Contains(id, only) {
			continue
		}
		pr := engine.Process(fc.Context(), fc.Raw, engine.Options{})
		errs := fixture.ErrorMessagesMissing(fc.Clean(), pr.Output)
		locs := fixture.LocationsMissing(fc.Clean(), pr.Output)
		if len(errs)+len(locs) == 0 {
			continue
		}
		fmt.Printf("=== %s (%s)\n", id, pr.Filter)
		for _, e := range errs {
			fmt.Printf("  err: %.150s\n", e)
		}
		for i, l := range locs {
			if i == 8 {
				fmt.Printf("  … +%d locations\n", len(locs)-8)
				break
			}

			for _, ln := range strings.Split(fc.Clean(), "\n") {
				if strings.Contains(ln, l) {
					fmt.Printf("  loc: %.150s\n", strings.TrimSpace(ln))
					break
				}
			}
		}
	}
}
