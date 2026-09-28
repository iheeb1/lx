package python

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var ptShortInfoRe = lazyre.New(`^(FAILED|ERROR) ([^\s\[]+(?:\[.*?\])?)(?: - .*)?$`)

func (pytestFilter) Items(c *engine.Context, out string) []engine.Item {
	view := out
	if r, _, ok := reducePytest(c, out); ok {
		view = r
	}
	lines := strings.Split(view, "\n")
	byName, dup := map[string]string{}, map[string]bool{}
	var short []engine.Item
	for _, ln := range lines {
		m := ptShortInfoRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		key := m[2]
		if m[1] == "ERROR" {
			key = "ERROR " + key
		}
		short = append(short, engine.Item{Key: key, Block: ln})
		if _, name, ok := strings.Cut(m[2], "::"); ok {
			name = strings.ReplaceAll(name, "::", ".")
			if _, seen := byName[name]; seen {
				dup[name] = true
			}
			byName[name] = m[2]
		}
	}

	var items []engine.Item
	for i := 0; i < len(lines); i++ {
		m := ptBlockRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		j := i + 1
		for j < len(lines) && !ptBlockRe.MatchString(lines[j]) && !strings.HasPrefix(lines[j], "===") {
			j++
		}
		for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		items = append(items, engine.Item{Key: pytestKey(m[1], byName, dup), Block: strings.Join(lines[i:j], "\n")})
		i = j - 1
	}
	if len(items) == 0 {
		return short
	}
	return items
}

func pytestKey(title string, byName map[string]string, dup map[string]bool) string {
	prefix, name := "", title
	for _, p := range []string{"ERROR at setup of ", "ERROR at teardown of "} {
		if rest, ok := strings.CutPrefix(title, p); ok {
			prefix, name = p, rest
		}
	}
	if id, ok := byName[name]; ok && !dup[name] {
		return prefix + id
	}
	return title
}
