package data

import (
	"encoding/json"
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type headerBlock struct {
	prefix     string
	statusLine string
	status     int
	fields     []string
}

var (
	statusLineRe = lazyre.New(`^HTTP/\d(?:\.\d)? (\d{3})\b`)
	requestRe    = lazyre.New(`^(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|TRACE|CONNECT) \S+ HTTP/\d(?:\.\d)?$`)
	fieldRe      = lazyre.New(`^([!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+):(.*)$`)
)

func keepField(name string) bool {
	switch name {
	case "content-type", "content-length", "location", "retry-after", "www-authenticate",
		"proxy-authenticate", "link", "content-disposition", "warning", "deprecation", "sunset", "set-cookie":
		return true
	}
	return strings.HasPrefix(name, "x-ratelimit") || strings.HasPrefix(name, "ratelimit") ||
		strings.HasPrefix(name, "x-rate-limit") || strings.Contains(name, "error")
}

func (h *headerBlock) render(keepAll bool) []string {
	out := []string{h.statusLine}
	var dropped []string
	for _, f := range h.fields {
		body := strings.TrimPrefix(f, h.prefix)
		m := fieldRe.FindStringSubmatch(body)
		if keepAll || m == nil {
			out = append(out, f)
			continue
		}
		name := strings.ToLower(m[1])
		switch {
		case name == "set-cookie":
			v := strings.TrimSpace(m[2])
			if eq := strings.IndexByte(v, '='); eq > 0 {
				out = append(out, h.prefix+m[1]+": "+v[:eq]+"=… (value hidden)")
			} else {
				out = append(out, f)
			}
		case keepField(name):
			out = append(out, f)
		default:
			dropped = append(dropped, name)
		}
	}
	if len(dropped) > 0 {
		out = append(out, fmt.Sprintf("%s[lx: %s not shown: %s]", h.prefix, engine.Plural(len(dropped), "more header", "more headers"), strings.Join(dropped, ", ")))
	}
	return out
}

func (h *headerBlock) contentType() string {
	for _, f := range h.fields {
		if m := fieldRe.FindStringSubmatch(strings.TrimPrefix(f, h.prefix)); m != nil && strings.EqualFold(m[1], "content-type") {
			return strings.TrimSpace(m[2])
		}
	}
	return ""
}

func parseStatus(line string) int {
	if m := statusLineRe.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

type bodyView struct {
	contentType string
	status      int
	verbatim    bool
	nums        []int
	joins       []bool
	lang        string
}

func renderBody(lines []string, v bodyView) []string {
	if len(v.nums) != len(lines) {
		v.nums = make([]int, len(lines))
		for i := range v.nums {
			v.nums[i] = i + 1
		}
	}
	body := strings.Join(lines, "\n")
	t := strings.TrimSpace(body)
	if t == "" || v.verbatim {
		return lines
	}
	if looksBinary(body) {
		ct := v.contentType
		if ct == "" {
			ct = "unknown type"
		}
		return []string{fmt.Sprintf("[lx: binary body, ~%s (%s), not shown — save it with -o FILE]", humanBytes(len(body)), ct)}
	}
	n := countTokens(body)

	if j, ok := rejoin(lines, v.joins); ok && (t[0] == '{' || t[0] == '[') && json.Valid([]byte(j)) {
		v.joins = nil
		return renderBody(strings.Split(j, "\n"), v)
	}
	if t[0] != '{' && t[0] != '[' && n > jsonBodyBudget {

		if k := jsonStart(lines); k > 0 {
			rest := v
			rest.nums = v.nums[k:]
			if len(v.joins) == len(lines) {
				rest.joins = v.joins[k:]
			}
			return append(append([]string(nil), lines[:k]...), renderBody(lines[k:], rest)...)
		}
	}
	if t[0] == '{' || t[0] == '[' {
		budget := jsonBodyBudget
		if v.status >= 400 {
			budget = errorBodyBudget
		}
		if n > budget {
			if j, ok := compactJSON(t, budget); ok {
				hdr := fmt.Sprintf("[lx: JSON body condensed from ~%s tokens: error fields first, tables for arrays of objects, long arrays cut; not valid JSON — use jq for exact values]", humanTokens(n))
				return append([]string{hdr}, strings.Split(j, "\n")...)
			}
		}
		if n <= fileBudget {
			return lines
		}
	}
	if isHTML(body, v.contentType) && n > htmlBudget {
		title, text := htmlText(body, htmlTextCap)
		out := []string{fmt.Sprintf("[lx: HTML body, %s (~%s tokens): title and visible text only, scripts/styles/markup removed]", humanBytes(len(body)), humanTokens(n))}
		if title != "" {
			out = append(out, "title: "+title)
		}
		return append(out, text...)
	}
	if n > fileBudget {
		w := defaultWindow("", v.lang, nil)
		w.nums = v.nums
		return w.apply(lines)
	}
	return lines
}

func rejoin(lines []string, joins []bool) (string, bool) {
	if len(joins) != len(lines) {
		return "", false
	}
	var b strings.Builder
	any := false
	for i, ln := range lines {
		b.WriteString(ln)
		switch {
		case joins[i] && i+1 < len(lines):
			any = true
		case i+1 < len(lines):
			b.WriteByte('\n')
		}
	}
	return b.String(), any
}

const maxPrelude = 20

func jsonStart(lines []string) int {
	for k := 1; k < len(lines) && k <= maxPrelude; k++ {
		ln := lines[k]
		if ln == "" || ln[0] != '{' && ln[0] != '[' {
			continue
		}
		if json.Valid([]byte(strings.Join(lines[k:], "\n"))) {
			return k
		}
		return -1
	}
	return -1
}

func urlExt(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
			continue
		}
		u := a
		if i := strings.IndexAny(u, "?#"); i >= 0 {
			u = u[:i]
		}
		rest := u[strings.Index(u, "://")+3:]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return langOf(rest[i:])
		}
		return ""
	}
	return ""
}

var curlExit = map[int]string{
	1: "unsupported protocol", 2: "failed to initialize", 3: "malformed URL",
	5: "could not resolve proxy", 6: "could not resolve host", 7: "failed to connect to host",
	8: "weird server reply", 9: "remote access denied", 16: "HTTP/2 framing error",
	18: "partial file: transfer ended early", 22: "HTTP error status (--fail)", 23: "write error",
	26: "read error", 27: "out of memory", 28: "operation timed out",
	35: "TLS/SSL connect error", 47: "too many redirects", 52: "empty reply from server",
	55: "failed sending network data", 56: "failure receiving network data", 58: "problem with the local certificate",
	60: "peer certificate cannot be authenticated", 61: "unrecognized transfer encoding",
	67: "login denied", 77: "problem reading the CA cert", 92: "HTTP/2 stream error", 95: "HTTP/3 error",
}
