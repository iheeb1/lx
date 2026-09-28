package jstools

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func (tsc) Items(c *engine.Context, s string) []engine.Item {
	lines := strings.Split(s, "\n")
	var items []engine.Item
	for i := 0; i < len(lines); {
		if _, _, ok := tscHeader(lines[i]); !ok {
			i++
			continue
		}
		d, end := tscParse(lines, i)
		key, loc := d.key, ""
		if d.file != "" {
			key = d.file + ": " + d.key
			m, ok := scanTSCPretty(d.header)
			if !ok {
				m, ok = scanTSCPlain(d.header)
			}
			if ok {
				loc = m[0] + ":" + m[1]
			}
		}
		block := append(append([]string{d.header}, d.chain...), d.related...)
		items = append(items, engine.Item{Key: key, Block: strings.Join(block, "\n"), Loc: loc})
		i = max(end, i+1)
	}
	return items
}

func (eslint) Items(c *engine.Context, s string) []engine.Item {
	if strings.Contains(s, "Oops! Something went wrong!") {
		return nil
	}
	lines := strings.Split(s, "\n")
	var items []engine.Item
	file, last := "", -1
	for i, ln := range lines {
		switch {
		case isESHeader(lines, i):
			file, last = engine.Relativize(c, ln), -1
		case file == "":
		case ln == "":
			file, last = "", -1
		default:
			m, ok := parseESMsg(ln)
			if !ok {
				if last >= 0 && (ln[0] == ' ' || ln[0] == '\t') {
					items[last].Block += "\n" + ln
				}
				continue
			}
			line, _, _ := strings.Cut(m.pos, ":")
			msg := joinNonEmpty("  ", m.sev, m.text, m.rule)
			items = append(items, engine.Item{Key: file + ": " + msg, Block: file + ":" + m.pos + "  " + msg, Loc: file + ":" + line})
			last = len(items) - 1
		}
	}
	return items
}
