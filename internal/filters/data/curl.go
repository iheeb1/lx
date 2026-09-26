package data

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// curlFilter condenses curl output.
//
//   - The progress meter (its two header lines and every frame, including
//     frames glued to the next stderr line) and the -# bar are dropped.
//   - -v: "* " connection lines are dropped except failures and redirect
//     notes; "{ [N bytes data]" markers are dropped; of the "> " request
//     lines only the method line is kept (all of them when an Origin header
//     was sent).
//   - Response heads (-i, -v, -I, -D -): the status line and the headers an
//     agent acts on are kept (content type/length, location, retry-after,
//     auth challenges, rate limits, link, set-cookie names), the others are
//     named on one line; every header is kept for 4xx/5xx responses, with
//     -I, and when an Origin header was sent.
//   - The body goes through renderBody (JSON, HTML, binary, big text).
//   - "curl: (N) …" errors and warnings are kept verbatim. When curl exited
//     non-zero without printing its error (-s), the exit code is explained.
type curlFilter struct{}

func (curlFilter) Name() string    { return "curl" }
func (curlFilter) IsContent() bool { return true }

func (curlFilter) Match(c *engine.Context) bool { return effective(c).Name() == "curl" }

type curlOpts struct {
	verbose, include, head, dumpStdout, progressBar, writeOut, origin, trace, location bool
}

// curlValueShort are the short options that take a value.
const curlValueShort = "AbcCdDeEFHKmoPQrtTuUwxXyYz"

var curlValueLong = map[string]bool{
	"--header": true, "--data": true, "--data-raw": true, "--data-binary": true, "--data-urlencode": true,
	"--data-ascii": true, "--json": true, "--request": true, "--user": true, "--user-agent": true, "--referer": true,
	"--cookie": true, "--cookie-jar": true, "--form": true, "--form-string": true, "--upload-file": true,
	"--write-out": true, "--proxy": true, "--max-time": true, "--connect-timeout": true, "--config": true,
	"--range": true, "--output": true, "--output-dir": true, "--dump-header": true, "--url": true, "--retry": true,
	"--retry-delay": true, "--retry-max-time": true, "--resolve": true, "--connect-to": true, "--cacert": true,
	"--capath": true, "--cert": true, "--key": true, "--cert-type": true, "--key-type": true, "--pass": true,
	"--trace": true, "--trace-ascii": true, "--stderr": true, "--interface": true, "--limit-rate": true,
	"--max-filesize": true, "--max-redirs": true, "--oauth2-bearer": true, "--proto": true, "--proto-redir": true,
	"--proxy-user": true, "--noproxy": true, "--variable": true, "--expand-url": true, "--aws-sigv4": true,
	"--unix-socket": true, "--abstract-unix-socket": true, "--speed-limit": true, "--speed-time": true,
	"--time-cond": true, "--tls-max": true, "--ciphers": true, "--dns-servers": true, "--doh-url": true,
	"--happy-eyeballs-timeout-ms": true, "--keepalive-time": true, "--local-port": true, "--login-options": true,
	"--mail-from": true, "--mail-rcpt": true, "--netrc-file": true, "--pinnedpubkey": true, "--preproxy": true,
	"--proxy-header": true, "--quote": true, "--random-file": true, "--sasl-authzid": true, "--service-name": true,
	"--socks4": true, "--socks4a": true, "--socks5": true, "--socks5-hostname": true, "--telnet-option": true,
	"--tftp-blksize": true, "--tlsuser": true, "--tlspassword": true, "--url-query": true, "--etag-save": true,
	"--etag-compare": true, "--alt-svc": true, "--hsts": true,
}

func parseCurl(args []string) curlOpts {
	var o curlOpts
	header := func(v string) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "origin:") {
			o.origin = true
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "--") {
			name, val, hasVal := strings.Cut(a, "=")
			if curlValueLong[name] && !hasVal && i+1 < len(args) {
				i++
				val = args[i]
			}
			switch name {
			case "--verbose":
				o.verbose = true
			case "--include", "--show-headers":
				o.include = true
			case "--head":
				o.head = true
			case "--location", "--location-trusted":
				o.location = true
			case "--progress-bar":
				o.progressBar = true
			case "--write-out":
				o.writeOut = true
			case "--header", "--proxy-header":
				header(val)
			case "--dump-header":
				o.dumpStdout = val == "-" || val == "/dev/stdout"
			case "--trace", "--trace-ascii":
				o.trace = val == "-" || val == "%"
			}
			continue
		}
		if len(a) < 2 || a[0] != '-' {
			continue
		}
		for k := 1; k < len(a); k++ {
			ch := a[k]
			if strings.IndexByte(curlValueShort, ch) >= 0 {
				val := a[k+1:]
				if val == "" && i+1 < len(args) {
					i++
					val = args[i]
				}
				switch ch {
				case 'H':
					header(val)
				case 'D':
					o.dumpStdout = val == "-" || val == "/dev/stdout"
				case 'w':
					o.writeOut = true
				}
				break
			}
			switch ch {
			case 'v':
				o.verbose = true
			case 'i':
				o.include = true
			case 'I':
				o.head = true
			case '#':
				o.progressBar = true
			case 'L':
				o.location = true
			}
		}
	}
	return o
}

var (
	meterHeaderRe = regexp.MustCompile(`^\s*% Total\s+% Received\s+% Xferd\s+Average Speed\s+Time\s+Time\s+Time\s+Current$` +
		`|^\s+Dload\s+Upload\s+Total\s+Spent\s+Left\s+Speed$`)
	// meterFrameRe is one progress-meter frame (curl's lib/progress.c:
	// %3d %5s %3d %5s %3d %5s  %5s  %5s %8s %8s %8s %5s).
	meterFrameRe = regexp.MustCompile(`^ *\d{1,3} +` + meterSize + ` +\d{1,3} +` + meterSize + ` +\d{1,3} +` + meterSize +
		` +` + meterSize + ` +` + meterSize + ` +` + meterTime + ` +` + meterTime + ` +` + meterTime + ` +` + meterSize)
	// hashBarRe is the -# bar ("###### 42.0%") and its start-up spinner.
	hashBarRe      = regexp.MustCompile(`^#[#=O\- ]*(?: \d{1,3}\.\d%)?$`)
	curlDiagRe     = regexp.MustCompile(`^curl: `)
	curlExitLineRe = regexp.MustCompile(`^curl: \(\d+\) `)
	curlWarningRe  = regexp.MustCompile(`^Warning: `)
	dataMarkerRe   = regexp.MustCompile(`^[{}] \[\d+ bytes data\]$`)
	// curlInfoRe: "* " lines curl prints after a body starts. Anything else
	// starting with "* " after a response head is body text (a markdown
	// list), not verbose output.
	curlInfoRe = regexp.MustCompile(`^\* (?:Connection #\d+ to host .* left intact|Closing connection(?: #?\d+)?|Connection #\d+ .*|` +
		`Leftovers after chunking.*|Excess found .*|HTTP/\d stream \d+ .*|TLSv[\d.]+ \((?:IN|OUT)\), TLS .*|\(\d+\) \((?:IN|OUT)\), TLS .*|` +
		`we are done reading and this is set to close, stop send|Found bundle for host.*|Re-using existing connection.*|` +
		`Recv failure: .*|Send failure: .*|OpenSSL SSL_read: .*|transfer closed with .*|Operation timed out after .*|` +
		`Failed .*|Issue another request to this URL: .*|Ignoring the response-body|Clear auth, redirects .*|` +
		`abort upload.*|Maximum \(\d+\) redirects followed|stopped the pause stream!?)$`)
	// curlFailRe marks "* " lines worth keeping: failures, TLS/certificate
	// problems, and redirect-following notes.
	curlFailRe = regexp.MustCompile(`(?i)\b(?:fail(?:ed|ure|s)?|errors?|refused|timed out|timeout|unable to|could not|couldn't|` +
		`denied|reset by peer|connection reset|problem|expired|(?:does|did)n?'?t match|not match|abort(?:ed|ing)?|rejected|` +
		`invalid|unrecogni[sz]ed|no route to host|unreachable|bad (?:request|gateway|file|certificate)|closed with \d+ bytes|` +
		`excess found|too many|illegal|not supported|self[- ]signed|unknown ca|revoked|untrusted|incomplete|` +
		`Issue another request to this URL|Maximum \(\d+\) redirects)\b`)
)

const (
	meterSize = `\d+(?:\.\d+)?[kMGTPE]?`
	meterTime = `(?:--:--:--|\d+:\d\d:\d\d|\d+d \d\dh|\d+d)`
	// meterWidth is the width of one frame (curl 7.x-8.x); text glued
	// after it is output from the other stream.
	meterWidth = 78
)

// cutMeterFrame reports whether ln starts with a progress-meter frame and
// returns the text after it (another stream's output glued to the frame).
func cutMeterFrame(ln string) (string, bool) {
	if len(ln) < 60 || !hasMeterShape(ln) {
		return "", false
	}
	loc := meterFrameRe.FindStringIndex(ln)
	if loc == nil {
		return "", false
	}
	end := loc[1]
	// Frames are fixed-width; the regexp's last field is greedy, so prefer
	// the width when the frame ends exactly there (glued digits).
	if len(ln) > meterWidth && end > meterWidth && ln[meterWidth-6] == ' ' {
		if m := meterFrameRe.FindStringIndex(ln[:meterWidth]); m != nil && m[1] == meterWidth {
			end = meterWidth
		}
	}
	return ln[end:], true
}

// hasMeterShape is a cheap filter before the frame regexp: frames contain
// three time fields.
func hasMeterShape(ln string) bool {
	return strings.Count(ln[:min(len(ln), 90)], ":") >= 6 || strings.Contains(ln, "--:--:--")
}

func (curlFilter) Apply(c *engine.Context, out string) (string, bool) {
	e := effective(c)
	o := parseCurl(e.Args())
	if o.trace {
		return "", false // --trace-ascii - output: not modeled
	}
	heads := o.verbose || o.include || o.head || o.dumpStdout
	// curl's exit message is numbered with its exit code; when it landed
	// inside a line of the body (stdout flushed after stderr), move it to
	// a line of its own. Only the number curl exited with is looked for,
	// so body text quoting another curl error stays where it is.
	var gluedExit string
	if c.Exit > 0 {
		gluedExit = fmt.Sprintf("curl: (%d) ", c.Exit)
	}
	lines, lineIdx := splitGlued(strings.Split(out, "\n"), func(ln string) int {
		if gluedExit != "" {
			if k := strings.LastIndex(ln, gluedExit); k > 0 {
				return k
			}
		}
		if o.verbose {
			// "* Connection #0 to host x left intact" printed while a
			// body line was still in curl's stdout buffer.
			return gluedInfo(ln)
		}
		return -1
	})
	// glued[k]: lines[k] is the part of a stdout line before a message
	// that landed inside it; the line's rest follows the message, so the
	// body line continues on the next body line.
	glued := func(k int) bool { return k+1 < len(lines) && lineIdx[k] == lineIdx[k+1] }

	var (
		res       []string
		body      []string // body lines, with meterNote where a frame landed
		bodyNums  []int    // 1-based output line number of each body line (0 for meterNote)
		bodyJoin  []bool   // the body line continues on the next body line (a message was glued into it)
		pending   []string // kept lines that arrived while a body was open
		cur, last *headerBlock
		inBody    bool // a response head ended: lines are body until the next request
		exitLine  bool
		meterHit  bool
		inRequest bool
		hidInfo   int // "* " lines dropped
		hidReq    int // "> " request header lines dropped
	)
	emit := func(ln string) {
		if len(body) > 0 {
			pending = append(pending, ln)
			return
		}
		res = append(res, ln)
	}
	flushBody := func() {
		if len(body) > 0 {
			v := bodyView{verbatim: o.writeOut, nums: bodyNums, joins: bodyJoin, lang: urlExt(e.Args())}
			if last != nil {
				v.status, v.contentType = last.status, last.contentType()
			}
			plain := body
			if meterHit {
				plain, v.nums, v.joins = nil, nil, nil
				for j, b := range body {
					if b != meterNote {
						plain, v.nums, v.joins = append(plain, b), append(v.nums, bodyNums[j]), append(v.joins, bodyJoin[j])
					}
				}
			}
			r := renderBody(plain, v)
			switch {
			case !meterHit:
				res = append(res, r...)
			case sameLines(r, plain):
				res = append(res, body...) // verbatim: mark the spot inline
			default:
				// The body is condensed, so the spots cannot be marked
				// inline: say how many there were.
				res = append(append(res, r...), fmt.Sprintf(meterNoteCondensed, len(body)-len(plain)))
			}
		}
		res = append(res, pending...)
		body, bodyNums, bodyJoin, pending, meterHit = nil, nil, nil, nil, false
	}
	closeHead := func() {
		if cur == nil {
			return
		}
		keepAll := o.head || o.origin || cur.status >= 400
		res = append(res, cur.render(keepAll)...)
		if cur.prefix == "" {
			res = append(res, "") // the blank line ending a head
		}
		last, cur, inBody = cur, nil, hasBody(cur, o)
	}
	var k int // index in lines of the line being read
	addBody := func(i int, ln string) {
		body, bodyNums, bodyJoin = append(body, ln), append(bodyNums, i+1), append(bodyJoin, glued(k))
	}

	for kk, ln := range lines {
		k = kk
		i := lineIdx[k]
		if strings.Contains(ln, "Total") && meterHeaderRe.MatchString(ln) {
			continue
		}
		if rest, ok := cutMeterFrame(ln); ok {
			// A frame after output started may have overwritten the text
			// before it on this line: say so where it happened.
			switch {
			case len(body) > 0, inBody && last != nil:
				// Inside a body, or where one was due after a response
				// head (the frame may have erased the body's first line).
				meterHit = true
				body, bodyNums, bodyJoin = append(body, meterNote), append(bodyNums, 0), append(bodyJoin, false)
			case cur != nil:
				cur.fields = append(cur.fields, meterNote)
			}
			if rest == "" {
				continue
			}
			ln = rest
		}
		if o.progressBar && hashBarRe.MatchString(ln) {
			continue
		}
		// curl's own messages. Inside a body only the "curl: (N) …" exit
		// message is taken as curl's: other lines are body text.
		if curlExitLineRe.MatchString(ln) || len(body) == 0 && (curlDiagRe.MatchString(ln) || curlWarningRe.MatchString(ln)) {
			exitLine = exitLine || curlExitLineRe.MatchString(ln)
			closeHead()
			emit(ln)
			continue
		}
		if o.verbose {
			switch {
			case dataMarkerRe.MatchString(ln):
				continue
			case ln == "*" || strings.HasPrefix(ln, "* "):
				if ln == "* Ignoring the response-body" {
					inBody = false
				}
				switch {
				case inBody && !curlInfoRe.MatchString(ln):
					addBody(i, ln) // body text such as a markdown list
				case curlFailRe.MatchString(ln):
					emit(ln)
				default:
					hidInfo++
				}
				continue
			case ln == ">" || strings.HasPrefix(ln, "> "):
				switch {
				case inRequest && ln == ">":
					inRequest = false
				case inRequest && o.origin:
					res = append(res, ln)
				case inRequest:
					hidReq++
				case requestRe.MatchString(strings.TrimPrefix(ln, "> ")):
					flushBody()
					inBody, inRequest = false, true
					res = append(res, ln)
				case inBody:
					addBody(i, ln) // a quoted line of the body
				default:
					emit(ln)
				}
				continue
			case ln == "<" || strings.HasPrefix(ln, "< "):
				h := strings.TrimPrefix(strings.TrimPrefix(ln, "<"), " ")
				switch {
				case statusLineRe.MatchString(h):
					flushBody()
					closeHead()
					inBody = false
					cur = &headerBlock{prefix: "< ", statusLine: ln, status: parseStatus(h)}
				case cur != nil && ln == "<":
					closeHead()
				case cur != nil:
					cur.fields = append(cur.fields, ln)
				default:
					addBody(i, ln)
				}
				continue
			}
			addBody(i, ln)
			continue
		}
		if heads {
			if cur != nil {
				if ln == "" {
					closeHead()
					continue
				}
				// Every line up to the blank one belongs to the head
				// (fields, folded continuations, a field split by a frame).
				cur.fields = append(cur.fields, ln)
				continue
			}
			if len(body) == 0 && statusLineRe.MatchString(ln) {
				flushBody()
				cur = &headerBlock{statusLine: ln, status: parseStatus(ln)}
				continue
			}
		}
		addBody(i, ln)
	}
	closeHead()
	flushBody()

	if hidInfo+hidReq > 0 {
		var parts []string
		if hidInfo > 0 {
			parts = append(parts, engine.Plural(hidInfo, `"* " line (connection, TLS, HTTP/2 stream)`, `"* " lines (connection, TLS, HTTP/2 stream)`))
		}
		if hidReq > 0 {
			parts = append(parts, engine.Plural(hidReq, "request header line", "request header lines"))
		}
		res = append(res, "[lx: -v output not shown: "+strings.Join(parts, ", ")+"]")
	}
	if c.Exit != 0 && !exitLine {
		why := curlExit[c.Exit]
		if why == "" {
			why = "see EXIT CODES in man curl"
		}
		res = append(res, fmt.Sprintf("[lx: curl exited %d (%s); curl printed no error message]", c.Exit, why))
	}
	for len(res) > 0 && res[len(res)-1] == "" {
		res = res[:len(res)-1]
	}
	for len(res) > 0 && res[0] == "" {
		res = res[1:]
	}
	return strings.Join(res, "\n"), true
}

// gluedInfo returns where a curl -v info line ("* Connection #0 to host x
// left intact") starts inside ln, or -1. Such a line always runs to the
// end of ln, so ln[k:] must be one entirely.
func gluedInfo(ln string) int {
	for k := strings.LastIndex(ln, "* "); k > 0; k = strings.LastIndex(ln[:k], "* ") {
		if curlInfoRe.MatchString(ln[k:]) {
			return k
		}
	}
	return -1
}

// hasBody reports whether curl prints a body after this response head: not
// for HEAD requests, 1xx/204/304, an empty Content-Length, or a redirect
// that -L follows.
func hasBody(h *headerBlock, o curlOpts) bool {
	switch {
	case o.head, h.status >= 100 && h.status < 200, h.status == 204, h.status == 304,
		o.location && h.status >= 300 && h.status < 400:
		return false
	}
	for _, f := range h.fields {
		if m := fieldRe.FindStringSubmatch(strings.TrimPrefix(f, h.prefix)); m != nil &&
			strings.EqualFold(m[1], "content-length") && strings.TrimSpace(m[2]) == "0" {
			return false
		}
	}
	return true
}

// meterNote marks where curl's progress meter was written into the body.
const meterNote = "[lx: curl's progress meter was printed into the output here, so text on this line may be lost — use -s (or -sS) for intact output]"

// meterNoteCondensed follows a condensed body the meter was written into.
const meterNoteCondensed = "[lx: curl's progress meter was printed into the body above (%d×), so some of its text may be lost — use -s (or -sS) for intact output]"

func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
