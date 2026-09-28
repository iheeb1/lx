package build

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	rustDiagRe    = lazyre.New(`^(?:error|warning)(?:\[\w+\])?: \S`)
	rustSummaryRe = lazyre.New("^(?:error|warning): (?:could not compile|test failed|aborting due to|build failed|`[^`]+` \\(.*\\) generated )")
	rustArrowRe   = lazyre.New(`^\s*--> (\S+?):(\d+):\d+$`)
	rustTestRe    = lazyre.New(`^---- (\S.*?) stdout ----$`)
)

func (cargoFilter) Items(c *engine.Context, out string) []engine.Item {
	sub, _ := cargoSub(c)
	return cargoItems(c, out, sub)
}

func (cargoTest) Items(c *engine.Context, out string) []engine.Item {
	return cargoItems(c, out, "test")
}

func cargoItems(c *engine.Context, out, sub string) []engine.Item {
	view := out
	if v, ok := applyCargo(c, out, sub); ok {
		view = v
	}
	lines := strings.Split(view, "\n")
	var items []engine.Item
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if m := rustTestRe.FindStringSubmatch(ln); m != nil {
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(lines[j], "---- ") && lines[j] != "failures:" && !strings.HasPrefix(lines[j], "test result: ") {
				j++
			}
			for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
				j--
			}
			items = append(items, engine.Item{Key: m[1], Block: strings.Join(lines[i:j], "\n")})
			i = j - 1
			continue
		}
		if !rustDiagRe.MatchString(ln) || rustSummaryRe.MatchString(ln) {
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "" && !rustDiagRe.MatchString(lines[j]) {
			j++
		}
		it := engine.Item{Key: ln, Block: strings.Join(lines[i:j], "\n")}
		for _, b := range lines[i+1 : j] {
			if m := rustArrowRe.FindStringSubmatch(b); m != nil {
				it.Key, it.Loc = m[1]+": "+ln, m[1]+":"+m[2]
				break
			}
		}
		items = append(items, it)
		i = j - 1
	}
	return items
}
