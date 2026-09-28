package git

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() { engine.Register(blameFilter{}) }

type blameFilter struct{}

func (blameFilter) Name() string    { return "git-blame" }
func (blameFilter) IsContent() bool { return true }

func (blameFilter) Match(c *engine.Context) bool {
	return isGit(c) && c.Sub() == "blame" && !engine.MachineReadable(c)
}

var blameMachineFlags = []string{"-p", "--porcelain", "--line-porcelain", "--incremental"}

var blameRe = lazyre.New(`^(\^?[0-9a-f]{7,40})((?: +[^ (][^ ]*)*?) +\((.*?) +(\d{4}-\d{2}-\d{2})(?: \d{2}:\d{2}:\d{2} [+-]\d{4})? +(\d+)\)(?: (.*))?$`)

const blameBudget = 7000

func (blameFilter) Apply(c *engine.Context, out string) (string, bool) {
	if hasArg(c, blameMachineFlags...) {
		return out, true
	}
	lines := strings.Split(out, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "", false
	}
	type bl struct {
		sha, extra, author, date, code string
		num                            int
	}
	parsed := make([]bl, len(lines))
	width := 0
	for i, ln := range lines {
		m := blameRe.FindStringSubmatch(ln)
		if m == nil {
			return "", false
		}
		n, err := strconv.Atoi(m[5])
		if err != nil {
			return "", false
		}
		extra := ""
		if f := strings.Fields(m[2]); len(f) > 0 {
			extra = " " + strings.Join(f, " ")
		}
		parsed[i] = bl{sha: m[1], extra: extra, author: strings.TrimSpace(m[3]), date: m[4], num: n, code: m[6]}
		width = max(width, len(m[5]))
	}
	var res []string
	seen := map[string]bool{}
	used := 0
	for i := 0; i < len(parsed); {
		p := parsed[i]
		j := i + 1
		for j < len(parsed) && parsed[j].sha == p.sha && parsed[j].extra == p.extra {
			j++
		}
		var run []string
		head := p.sha + p.extra
		key := p.sha + "\x00" + p.author + "\x00" + p.date
		if !seen[key] {
			seen[key] = true
			head += " (" + p.author + " " + p.date + ")"
		}
		for _, q := range parsed[i:j] {
			ln := fmt.Sprintf("%*d)", width+1, q.num)
			if q.code != "" {
				ln += " " + q.code
			}
			run = append(run, ln)
		}
		if j-i == 1 {
			run[0] = head + " " + strings.TrimLeft(run[0], " ")
		} else {
			run = append([]string{head}, run...)
		}
		cost := countTokens(run)
		if used+cost > blameBudget && used > 0 {
			first, last := parsed[i].num, parsed[len(parsed)-1].num
			contiguous := last-first == len(parsed)-1-i
			for k := i + 1; contiguous && k < len(parsed); k++ {
				contiguous = parsed[k].num == parsed[k-1].num+1
			}
			if contiguous {
				res = append(res, fmt.Sprintf("[… %s not shown (lines %d-%d); add -L %d,%d to see them]",
					plural(len(parsed)-i, "more line"), first, last, first, last))
			} else {
				res = append(res, fmt.Sprintf("[… %s not shown, from line %d to line %d in several ranges]",
					plural(len(parsed)-i, "more line"), first, last))
			}
			break
		}
		used += cost
		res = append(res, run...)
		i = j
	}
	return join(res), true
}
