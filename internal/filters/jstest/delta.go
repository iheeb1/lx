package jstest

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	vitestFailLineRe = lazyre.New(`^ FAIL  (\S.*)$`)
	vitestSectionRe  = lazyre.New(`^⎯+(?: (.+?) ⎯+)?$|^⎯+\[\d+/\d+\]⎯*$`)
)

func (jestFilter) Items(c *engine.Context, out string) []engine.Item {
	return runnerItems(c, out, "jest")
}

func (vitestFilter) Items(c *engine.Context, out string) []engine.Item {
	return runnerItems(c, out, "vitest")
}

func (mochaFilter) Items(c *engine.Context, out string) []engine.Item {
	return runnerItems(c, out, "mocha")
}

func (npmTestFilter) Items(c *engine.Context, out string) []engine.Item {
	return runnerItems(c, out, detectRunner(out))
}

func runnerItems(c *engine.Context, out, runner string) []engine.Item {
	var render func(*engine.Context, string) (result, bool)
	var parse func([]string) []engine.Item
	switch runner {
	case "jest":
		render, parse = renderJest, jestItems
	case "vitest":
		render, parse = renderVitest, vitestItems
	case "mocha":
		render, parse = renderMocha, mochaItems
	default:
		return nil
	}
	view := out
	if r, ok := render(c, out); ok {
		view = r.out
	}
	return parse(strings.Split(view, "\n"))
}

func jestItems(lines []string) []engine.Item {
	var items []engine.Item
	file := ""
	for i := 0; i < len(lines); i++ {
		if fail, f, ok := jestSuiteLine(lines[i]); ok {
			file = ""
			if fail {
				if k := strings.Index(f, " ("); k > 0 {
					f = f[:k]
				}
				file = f
			}
			continue
		}
		m := jestBulletRe.FindStringSubmatch(lines[i])
		if m == nil || m[1] == "Console" {
			continue
		}
		j := i + 1
		for j < len(lines) && (strings.TrimSpace(lines[j]) == "" || indentOf(lines[j]) >= 4) {
			j++
		}
		for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		key, block := m[1], strings.Join(lines[i:j], "\n")
		if file != "" {
			key, block = file+" › "+key, "FAIL "+file+"\n"+block
		}
		items = append(items, engine.Item{Key: key, Block: block})
		i = j - 1
	}
	return items
}

func vitestItems(lines []string) []engine.Item {
	var items []engine.Item
	end := func(i int) int {
		j := i + 1
		for j < len(lines) && !vitestFailLineRe.MatchString(lines[j]) && !vitestSectionRe.MatchString(lines[j]) &&
			!strings.HasPrefix(lines[j], " Test Files  ") {
			j++
		}
		for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		return j
	}
	for i := 0; i < len(lines); i++ {
		key := ""
		if m := vitestFailLineRe.FindStringSubmatch(lines[i]); m != nil {
			key = strings.TrimSpace(m[1])
		} else if m := vitestSectionRe.FindStringSubmatch(lines[i]); m != nil && strings.HasPrefix(m[1], "Un") && m[1] != "Unhandled Errors" {
			if i+1 < len(lines) {
				key = m[1] + ": " + strings.TrimSpace(lines[i+1])
			}
		}
		if key == "" {
			continue
		}
		j := end(i)
		items = append(items, engine.Item{Key: key, Block: strings.Join(lines[i:j], "\n")})
		i = j - 1
	}
	return items
}

func mochaItems(lines []string) []engine.Item {
	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "  ") && strings.HasSuffix(ln, " failing") && mochaCountRe.MatchString(ln) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var items []engine.Item
	for i := start; i < len(lines); i++ {
		m := mochaDetailRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		title := []string{m[2]}
		t := i
		for k := i; k < min(i+12, len(lines)); k++ {
			if k > i {
				title = append(title, strings.TrimSpace(lines[k]))
			}
			if strings.HasSuffix(lines[k], ":") {
				t = k
				break
			}
		}
		if t == i {
			title = title[:1]
		} else {
			title = title[:t-i+1]
		}
		key := strings.TrimSuffix(strings.Join(strings.Fields(strings.Join(title, " ")), " "), ":")
		j := t + 1
		for j < len(lines) && !mochaDetailRe.MatchString(lines[j]) {
			if strings.TrimSpace(lines[j]) == "" {
				k := j
				for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
					k++
				}
				if k == len(lines) || indentOf(lines[k]) < 4 {
					break
				}
			}
			j++
		}
		for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		items = append(items, engine.Item{Key: key, Block: strings.Join(lines[i:j], "\n")})
		i = j - 1
	}
	return items
}
