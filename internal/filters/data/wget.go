package data

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// wgetFilter condenses GNU wget's log: the dot/bar progress and successful
// resolve/connect lines are dropped; the request line, the response status,
// Length, Saving to, redirects, the result line and every failure are kept.
// With -S the server's headers are trimmed like curl's; a body written to
// stdout (-O -) goes through renderBody.
type wgetFilter struct{}

func (wgetFilter) Name() string    { return "wget" }
func (wgetFilter) IsContent() bool { return true }

func (wgetFilter) Match(c *engine.Context) bool { return effective(c).Name() == "wget" }

var (
	wgetReqRe      = regexp.MustCompile(`^--\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}--  \S`)
	wgetResolveRe  = regexp.MustCompile(`^Resolving \S+ .*\.\.\. `)
	wgetConnectRe  = regexp.MustCompile(`^Connecting to \S+.*\.\.\. `)
	wgetReuseRe    = regexp.MustCompile(`^Reusing existing connection to `)
	wgetAwaitRe    = regexp.MustCompile(`^(?:HTTP|Proxy) request sent, awaiting response\.\.\.`)
	wgetKeepRe     = regexp.MustCompile(`^(?:Length: |Saving to: |Location: .*\[following\]$|Retrying\.|Giving up\.|FINISHED --|Total wall clock time: |Downloaded: \d+ files?|Converting links|Converted links|Remote file |Server file no newer|File .* already there|Cannot write to |No such file|Username/Password Authentication Failed|Authentication selected: |Unable to establish SSL connection|ERROR: |WARNING: |  Unable to locally verify|Disabling SSL due to encountered errors|Read error |Connection closed at byte )`)
	wgetResultRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} (?:\(.*\) - |ERROR \d+: |URL:)`)
	wgetDotsRe     = regexp.MustCompile(`^ *\d+[KMG] [ .,]{10,}`)
	wgetBarRe      = regexp.MustCompile(`^\S.* +\d{1,3}%\[[=> ]*\] +\S+ +\S+\s+(?:in |eta )`)
	wgetDiagRe     = regexp.MustCompile(`^wget: `)
	wgetHeaderLead = "  "
)

// wgetArgs reads the flags that shape wget's output: -q/--quiet, -S/
// --server-response, and whether the document goes to stdout (-O -,
// -qO-, --output-document=-), the only case where output lines can be a
// response body rather than wget's log.
func wgetArgs(args []string) (quiet, server, stdout bool) {
	isStdout := func(v string) bool { return v == "-" || v == "/dev/stdout" }
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		switch {
		case a == "--quiet":
			quiet = true
		case a == "--server-response":
			server = true
		case a == "--output-document" && i+1 < len(args):
			i++
			stdout = isStdout(args[i])
		case strings.HasPrefix(a, "--output-document="):
			stdout = isStdout(strings.TrimPrefix(a, "--output-document="))
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			for k := 1; k < len(a); k++ {
				ch := a[k]
				if ch == 'q' {
					quiet = true
				}
				if ch == 'S' {
					server = true
				}
				if strings.IndexByte("OoaAeDlPtTUwY", ch) >= 0 {
					v := a[k+1:]
					if v == "" && i+1 < len(args) {
						i++
						v = args[i]
					}
					if ch == 'O' {
						stdout = isStdout(v)
					}
					break // the rest was the value
				}
			}
		}
	}
	return quiet, server, stdout
}

// wgetExit explains wget's exit codes, for runs that failed silently (-q).
var wgetExit = map[int]string{
	1: "generic error", 2: "parse error in options or .wgetrc", 3: "file I/O error", 4: "network failure",
	5: "SSL verification failure", 6: "username/password authentication failure", 7: "protocol error",
	8: "the server issued an error response (4xx/5xx)",
}

func (wgetFilter) Apply(c *engine.Context, out string) (string, bool) {
	quiet, _, stdout := wgetArgs(effective(c).Args())
	lines := strings.Split(out, "\n")
	var (
		res       []string
		body      []string
		bodyNums  []int // 1-based output line number of each body line
		cur, last *headerBlock
		ctype     string
		logLines  int
		unknown   int // lines that are neither wget's known log lines nor a body
	)
	closeHead := func() {
		if cur != nil {
			res = append(res, cur.render(cur.status >= 400)...)
			last, cur = cur, nil
		}
	}
	for i, ln := range lines {
		// -S: the server's response head, indented by two spaces.
		if cur != nil {
			if strings.HasPrefix(ln, wgetHeaderLead) && fieldRe.MatchString(strings.TrimPrefix(ln, wgetHeaderLead)) {
				cur.fields = append(cur.fields, ln)
				continue
			}
			closeHead()
		}
		if h := strings.TrimPrefix(ln, wgetHeaderLead); strings.HasPrefix(ln, wgetHeaderLead) && statusLineRe.MatchString(h) {
			cur = &headerBlock{prefix: wgetHeaderLead, statusLine: ln, status: parseStatus(h)}
			logLines++
			continue
		}
		switch {
		case wgetDotsRe.MatchString(ln), strings.Contains(ln, "%[") && wgetBarRe.MatchString(ln) && !engine.IsError(ln):
			logLines++
		case wgetResolveRe.MatchString(ln), wgetConnectRe.MatchString(ln):
			logLines++
			if !strings.HasSuffix(ln, "connected.") && !resolvedOK(ln) {
				res = append(res, ln)
			}
		case wgetReuseRe.MatchString(ln):
			logLines++
		case wgetReqRe.MatchString(ln), wgetAwaitRe.MatchString(ln), wgetKeepRe.MatchString(ln),
			wgetResultRe.MatchString(ln), wgetDiagRe.MatchString(ln):
			logLines++
			if strings.HasPrefix(ln, "Length: ") {
				if a, b := strings.LastIndexByte(ln, '['), strings.LastIndexByte(ln, ']'); a >= 0 && b > a {
					ctype = ln[a+1 : b]
				}
			}
			res = append(res, ln)
		case ln == "" && len(body) == 0:
			// blank separators of the log
		case !stdout:
			// No document on stdout: every line is wget's log. Lines not
			// known above are kept as they are.
			unknown++
			res = append(res, ln)
		default:
			body, bodyNums = append(body, ln), append(bodyNums, i+1)
		}
	}
	closeHead()
	if !quiet && (logLines == 0 || unknown > logLines) {
		return "", false // not wget's log format (localized, or unknown version)
	}
	// Whether wget itself reported the failure, judged before the body
	// (whose text may hold "error" as data) is added.
	toolErr := hasErrorLine(res)
	res = condenseRequests(res)
	if len(body) > 0 {
		v := bodyView{contentType: ctype, nums: bodyNums}
		if last != nil {
			v.status = last.status
			if ct := last.contentType(); ct != "" {
				v.contentType = ct
			}
		}
		res = append(res, renderBody(body, v)...)
	}
	if c.Exit != 0 && !toolErr {
		why := wgetExit[c.Exit]
		if why == "" {
			why = "see EXIT STATUS in man wget"
		}
		res = append(res, fmt.Sprintf("[lx: wget exited %d (%s); wget printed no error message]", c.Exit, why))
	}
	return joinLines(res), true
}

const (
	// maxRequests: logs of up to this many requests are shown whole …
	maxRequests = 20
	// … longer ones (wget -r, -i list) keep the first and last requests
	// and every request that did not succeed.
	keepFirstRequests = 10
	keepLastRequests  = 5
)

var (
	wgetStatusRe  = regexp.MustCompile(`awaiting response\.\.\. (\d{3})\b`)
	wgetSummaryRe = regexp.MustCompile(`^(?:FINISHED --|Total wall clock time: |Downloaded: \d+ files?|Converting links|Converted links)`)
)

// condenseRequests shortens the log of a many-request run (wget -r, -i):
// each request (from its "--date--  URL" line to the next) that succeeded
// — no error-class line and every status below 400 — is counted instead
// of shown, except the first 10 and the last 5. Failed requests and the
// closing summary are always shown whole.
func condenseRequests(res []string) []string {
	var groups [][]string
	var pre, post []string
	for _, ln := range res {
		switch {
		case wgetSummaryRe.MatchString(ln):
			post = append(post, ln)
		case wgetReqRe.MatchString(ln):
			groups = append(groups, []string{ln})
		case len(groups) == 0:
			pre = append(pre, ln)
		default:
			groups[len(groups)-1] = append(groups[len(groups)-1], ln)
		}
	}
	if len(groups) <= maxRequests {
		return res
	}
	ok := func(g []string) bool {
		for _, ln := range g {
			if engine.IsError(ln) {
				return false
			}
			if m := wgetStatusRe.FindStringSubmatch(ln); m != nil && m[1] >= "400" {
				return false
			}
		}
		return true
	}
	out := append([]string(nil), pre...)
	hidden := 0
	flush := func() {
		if hidden > 0 {
			out = append(out, "[lx: "+pluralInt(hidden, "request", "requests")+" that succeeded (status below 400, no error) not shown]")
			hidden = 0
		}
	}
	for k, g := range groups {
		if k >= keepFirstRequests && k < len(groups)-keepLastRequests && ok(g) {
			hidden++
			continue
		}
		flush()
		out = append(out, g...)
	}
	flush()
	return append(out, post...)
}

// hasErrorLine reports whether any line is error-class.
func hasErrorLine(lines []string) bool {
	for _, ln := range lines {
		if engine.IsError(ln) {
			return true
		}
	}
	return false
}

// resolvedOK: "Resolving host (host)... 1.2.3.4, ::1" succeeded.
func resolvedOK(ln string) bool {
	_, after, ok := strings.Cut(ln, "... ")
	return ok && !strings.HasPrefix(after, "failed")
}
