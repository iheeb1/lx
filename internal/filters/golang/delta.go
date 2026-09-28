package golang

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var goDiagRe = lazyre.New(`^(?:vet: )?(\S*?\.(?:go|s|c|h|cc|cpp|m|mod|sum|work)):(\d+)(?::\d+)?: (.+)$`)

func (testText) Items(c *engine.Context, out string) []engine.Item {
	lines := splitLines(out)
	if len(lines) == 0 {
		return nil
	}
	fetched, _ := condenseFetch(lines)
	return parseText(fetched).items(c)
}

func (testJSON) Items(c *engine.Context, out string) []engine.Item {
	r, _, ok := parseJSON(out)
	if !ok {
		return nil
	}
	return r.items(c)
}

func (r *run) items(c *engine.Context) []engine.Item {
	rd := &renderer{c: c, verbose: r.json}
	var items []engine.Item
	for _, ln := range r.pre {
		if it, ok := diagItem(ln); ok {
			items = append(items, it)
		}
	}
	for _, s := range r.segs {
		failed, n := s.failed(), len(items)
		for _, it := range s.viewItems() {
			switch it.kind {
			case itResult:
				if failed && it.t.status == 'F' && !implied(s, it.t) {
					items = append(items, testItem(rd, s.pkg, it.t))
				}
			case itStray:
				if d, ok := diagItem(it.line); ok {
					items = append(items, d)
				}
			}
		}
		if !failed {
			continue
		}
		if len(s.crash) > 0 {
			key := strings.TrimSpace(s.crash[0])
			if s.pkg != "" {
				key = s.pkg + " " + key
			}
			items = append(items, engine.Item{Key: key, Block: strings.Join(foldCrash(c, s.crash), "\n")})
		}
		if len(items) == n && s.pkg != "" {
			body, _, _ := rd.segment(s)
			if len(body) > 0 && body[len(body)-1] == s.verdict {
				body = body[:len(body)-1]
			}
			if body = trimBlankRuns(body); len(body) > 0 {
				items = append(items, engine.Item{Key: s.pkg, Block: strings.Join(body, "\n")})
			}
		}
	}
	return items
}

func implied(s *segment, t *gtest) bool {
	if nonBlank(t.out) > 0 {
		return false
	}
	for _, o := range s.order {
		if o.status == 'F' && strings.HasPrefix(o.name, t.name+"/") {
			return true
		}
	}
	return false
}

func testItem(rd *renderer, pkg string, t *gtest) engine.Item {
	lines := dedent(append([]string{t.result}, rd.testOutput(t)...))
	key := t.name
	if pkg != "" {
		key = pkg + " " + t.name
	}
	return engine.Item{Key: key, Block: engine.RelativizeNonErrors(rd.c, strings.Join(lines, "\n"))}
}

func diagItem(ln string) (engine.Item, bool) {
	m := goDiagRe.FindStringSubmatch(ln)
	if m == nil {
		return engine.Item{}, false
	}
	return engine.Item{Key: m[1] + ": " + m[3], Block: ln, Loc: m[1] + ":" + m[2]}, true
}

func dedent(lines []string) []string {
	cut := -1
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n := len(ln) - len(strings.TrimLeft(ln, " "))
		if cut < 0 || n < cut {
			cut = n
		}
	}
	if cut <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		if len(ln) >= cut {
			out[i] = ln[cut:]
		}
	}
	return out
}
