package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type httpieFilter struct{}

func (httpieFilter) Name() string    { return "httpie" }
func (httpieFilter) IsContent() bool { return true }

func (httpieFilter) Match(c *engine.Context) bool {
	switch effective(c).Name() {
	case "http", "https", "xh", "xhs":
		return true
	}
	return false
}

var (
	httpieDiagRe = lazyre.New(`^(?:http|https|xh|xhs): (?:error|warning)\b|^(?:xh|xhs): `)

	usageErrRe = lazyre.New(`^error:(?: |$)`)
)

func httpieArgs(args []string) (headersOnly, origin bool) {
	for _, a := range args {
		switch {
		case a == "--headers", a == "-h":
			headersOnly = true
		case strings.HasPrefix(strings.ToLower(a), "origin:"):
			origin = true
		}
	}
	return headersOnly, origin
}

func (httpieFilter) Apply(c *engine.Context, out string) (string, bool) {
	headersOnly, origin := httpieArgs(effective(c).Args())
	lines := strings.Split(out, "\n")
	var (
		res       []string
		body      []string
		bodyNums  []int
		cur, last *headerBlock
		inReq     bool
		hasText   bool
		hidReq    int
		diag      bool
	)
	flush := func() {
		if len(body) > 0 {
			b, nums := trimBlank(body, bodyNums)
			v := bodyView{nums: nums}
			if last != nil {
				v.status, v.contentType = last.status, last.contentType()
			}
			res = append(res, renderBody(b, v)...)
		}
		body, bodyNums, hasText = nil, nil, false
	}
	for i, ln := range lines {
		if ln != "" && (ln[0] == 'h' || ln[0] == 'x') && httpieDiagRe.MatchString(ln) {
			flush()
			res = append(res, ln)
			diag = true
			continue
		}
		if strings.HasPrefix(ln, "error:") && usageErrRe.MatchString(ln) && cur == nil && !inReq {
			diag = true
		}
		if inReq {
			switch {
			case ln == "":
				inReq = false
			case origin:
				res = append(res, ln)
			default:
				hidReq++
			}
			continue
		}
		if cur != nil {
			if ln == "" {
				res = append(res, cur.render(headersOnly || origin || cur.status >= 400)...)
				res = append(res, "")
				last, cur = cur, nil
				continue
			}
			if fieldRe.MatchString(ln) {
				cur.fields = append(cur.fields, ln)
				continue
			}
			res = append(res, cur.render(headersOnly || origin || cur.status >= 400)...)
			last, cur = cur, nil
		}

		if !hasText || i > 0 && strings.TrimSpace(lines[i-1]) == "" {
			switch {
			case requestRe.MatchString(ln):
				flush()
				if len(res) > 0 {
					res = append(res, "")
				}
				res = append(res, ln)
				inReq = true
				continue
			case statusLineRe.MatchString(ln):
				flush()
				if len(res) > 0 {
					res = append(res, "")
				}
				cur = &headerBlock{statusLine: ln, status: parseStatus(ln)}
				continue
			}
		}
		body, bodyNums = append(body, ln), append(bodyNums, i+1)
		hasText = hasText || strings.TrimSpace(ln) != ""
	}
	if cur != nil {
		res = append(res, cur.render(headersOnly || origin || cur.status >= 400)...)
		last = cur
	}
	flush()
	if hidReq > 0 {
		res = append(res, "[lx: "+engine.Plural(hidReq, "request header line", "request header lines")+" not shown]")
	}
	if c.Exit != 0 && !diag {
		why := httpieExit[c.Exit]
		if why == "" {
			why = "see EXIT STATUS in http --help"
		}
		res = append(res, fmt.Sprintf("[lx: %s exited %d (%s); it printed no error message]", effective(c).Name(), c.Exit, why))
	}
	return joinLines(res), true
}

var httpieExit = map[int]string{
	1: "error", 2: "request timed out", 3: "HTTP 3xx response (--check-status)", 4: "HTTP 4xx response (--check-status)",
	5: "HTTP 5xx response (--check-status)", 6: "too many redirects", 7: "plugin error",
}

func trimBlank(lines []string, nums []int) ([]string, []int) {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines, nums = lines[1:], nums[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines, nums = lines[:len(lines)-1], nums[:len(nums)-1]
	}
	return lines, nums
}
