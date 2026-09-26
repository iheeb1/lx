// Package golang condenses the output of the go command: go test (text and
// -json), go build / vet / install, go mod / go get, and go list.
//
// Filters (first match wins, registered in this order):
//
//	go-test-json  go test -json            events decoded and rendered as go test text
//	go-test       go test                  failures in full, passing tests counted
//	go-build      go build|vet|install, go test -c   diagnostics verbatim, downloads counted
//	go-mod        go mod tidy|download|vendor|verify|init, go get
//	go-list       go list                  content; only download chatter condensed
//	go-machine    go … -json, go list -f   machine output kept verbatim (content)
//
// Every filter keeps each error-class line verbatim, keeps the go command's
// own verdict lines (ok / FAIL / FAIL pkg [build failed]) and bails
// (ok=false) when the output does not have a shape it recognizes.
package golang

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() {
	engine.Register(testJSON{})
	engine.Register(testText{})
	engine.Register(build{})
	engine.Register(mod{})
	engine.Register(list{})
	engine.Register(machine{})
}

// versionedGoRe matches golang.org/dl wrappers such as go1.22.3 or go1.23rc1
// (gotip is matched by name).
var versionedGoRe = regexp.MustCompile(`^go1\.\d+(?:\.\d+)?(?:rc\d+|beta\d+)?$`)

// isGo reports whether argv[0] is the go command.
func isGo(c *engine.Context) bool {
	n := strings.TrimSuffix(c.Name(), ".exe")
	return n == "go" || n == "gotip" || versionedGoRe.MatchString(n)
}

// goArgs splits the go command line into the subcommand and the arguments
// after it. The only flag the go command accepts before the subcommand is
// -C dir.
func goArgs(c *engine.Context) (sub string, rest []string) {
	args := c.Args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "--C":
			i++
		case strings.HasPrefix(a, "-C=") || strings.HasPrefix(a, "--C="):
		case strings.HasPrefix(a, "-"):
			// Unknown pre-subcommand flag: not a shape we know.
			return "", nil
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// flagSet reports whether any of names (given without dashes) appears in
// args as -name, --name, -name=value or --name=value (value not "false").
// go test also accepts the test binary's -test.name spelling. Scanning stops
// at -args, after which flags belong to the test binary.
func flagSet(args []string, names ...string) bool {
	for _, a := range args {
		if a == "-args" || a == "--args" || a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		f := strings.TrimLeft(a, "-")
		val := ""
		if k, v, ok := strings.Cut(f, "="); ok {
			f, val = k, v
		}
		f = strings.TrimPrefix(f, "test.")
		for _, n := range names {
			if f == n && val != "false" {
				return true
			}
		}
	}
	return false
}

// positionals returns the non-flag arguments (a crude split: go flags that
// take a separate value are listed in valueFlags).
func positionals(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-args" || a == "--args" || a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			f := strings.TrimLeft(a, "-")
			if !strings.Contains(f, "=") && valueFlags[strings.TrimPrefix(f, "test.")] {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

var valueFlags = map[string]bool{
	"run": true, "skip": true, "bench": true, "count": true, "timeout": true, "cpu": true,
	"parallel": true, "p": true, "tags": true, "o": true, "coverprofile": true, "covermode": true,
	"coverpkg": true, "exec": true, "vet": true, "shuffle": true, "fuzz": true, "fuzztime": true,
	"benchtime": true, "cpuprofile": true, "memprofile": true, "blockprofile": true,
	"mutexprofile": true, "trace": true, "outputdir": true, "ldflags": true, "gcflags": true,
	"asmflags": true, "mod": true, "modfile": true, "pkgdir": true, "toolexec": true,
	"overlay": true, "pgo": true, "buildmode": true, "compiler": true, "installsuffix": true,
	"gccgoflags": true, "reuse": true, "list": true, "C": true, "fullpath": true,
}

// Lines the go command prints while fetching modules.
var (
	downloadRe = regexp.MustCompile(`^go: (?:downloading|extracting) (\S+) (\S+)$`)
	// go -x module fetch trace: request and response lines.
	getReqRe  = regexp.MustCompile(`^# get (https?://\S+)$`)
	getRespRe = regexp.MustCompile(`^# get (https?://\S+): (\d{3}) [^()]*(?:\([\d.]+m?s\))?$`)
	// Lines that name a module the download summary need not repeat.
	namedModRe = regexp.MustCompile(`^go: (?:upgraded|added|downgraded|removed) (\S+) |^go: found \S+ in (\S+) `)
	zipRe      = regexp.MustCompile(`^https?://[^/]+/(.+)/@v/([^/]+)\.zip$`)
)

// condenseFetch replaces "go: downloading M V" lines with one summary line at
// the first one's position, and drops the "# get URL" / "# get URL: 200 OK"
// trace printed by -x (counted in a summary line; any non-2xx response is
// kept verbatim). Download lines that are themselves error-class (a module
// path such as github.com/pkg/errors) are also kept verbatim, right after
// the summary, so the error guard finds them. It returns the new lines and
// whether anything changed.
func condenseFetch(lines []string) ([]string, bool) {
	var (
		mods, keepDL, zips []string
		reqs, oks          int
		slotDL, slotGet    = -1, -1 // where the summary lines go in out
	)
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if m := downloadRe.FindStringSubmatch(ln); m != nil {
			if slotDL < 0 {
				slotDL = len(out)
				out = append(out, "")
			}
			mods = append(mods, m[1])
			if engine.IsError(ln) {
				keepDL = append(keepDL, ln)
			}
			continue
		}
		if strings.HasPrefix(ln, "# get ") {
			if m := getRespRe.FindStringSubmatch(ln); m != nil {
				if m[2][0] != '2' {
					out = append(out, ln) // 404, 410, 5xx: kept
					continue
				}
				oks++
				if z := zipRe.FindStringSubmatch(m[1]); z != nil {
					zips = append(zips, unescapeModPath(z[1])+"@"+z[2])
				}
			} else if getReqRe.MatchString(ln) {
				reqs++
			} else {
				out = append(out, ln)
				continue
			}
			if slotGet < 0 {
				slotGet = len(out)
				out = append(out, "")
			}
			continue
		}
		out = append(out, ln)
	}
	if slotDL < 0 && slotGet < 0 {
		return lines, false
	}
	named := map[string]bool{}
	for _, ln := range out {
		if m := namedModRe.FindStringSubmatch(ln); m != nil {
			named[m[1]+m[2]] = true
		}
	}
	res := make([]string, 0, len(out)+len(keepDL))
	for i, ln := range out {
		switch i {
		case slotDL:
			res = append(res, downloadSummary(mods, named))
			res = append(res, keepDL...)
		case slotGet:
			s := fmt.Sprintf("[go -x: %s hidden (%s, %s)",
				engine.Plural(reqs+oks, "\"# get\" trace line", "\"# get\" trace lines"),
				engine.Plural(reqs, "request", "requests"), engine.Plural(oks, "OK response", "OK responses"))
			if len(zips) > 0 {
				s += "; module zips fetched: "
				if len(zips) <= maxListed {
					s += strings.Join(zips, ", ")
				} else {
					s += num(len(zips))
				}
			}
			res = append(res, s+"]")
		default:
			res = append(res, ln)
		}
	}
	return res, true
}

// maxListed: longer module lists are reduced to their count.
const maxListed = 30

// downloadSummary counts the downloaded modules and lists them (up to
// maxListed), leaving out those that a later "go: upgraded/added/…" line
// names anyway.
func downloadSummary(mods []string, named map[string]bool) string {
	s := "[go: downloading " + engine.Plural(len(mods), "module", "modules")
	var rest []string
	for _, m := range mods {
		if !named[m] {
			rest = append(rest, m)
		}
	}
	switch {
	case len(rest) < len(mods) && len(rest) == 0:
		s += " (all named below)"
	case len(rest) < len(mods):
		s += " (" + num(len(mods)-len(rest)) + " named below)"
		if len(rest) <= maxListed {
			s += "; also: " + strings.Join(rest, ", ")
		}
	case len(mods) <= maxListed:
		s += ": " + strings.Join(mods, ", ")
	}
	return s + "]"
}

// unescapeModPath undoes the module proxy's case encoding ("!x" → "X").
func unescapeModPath(p string) string {
	if !strings.Contains(p, "!") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] == '!' && i+1 < len(p) && p[i+1] >= 'a' && p[i+1] <= 'z' {
			b.WriteByte(p[i+1] - 'a' + 'A')
			i++
			continue
		}
		b.WriteByte(p[i])
	}
	return b.String()
}

// plural is engine.Plural with thousands separators.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return num(n) + " " + many
}

// num formats n with thousands separators.
func num(n int) string {
	s := strconv.Itoa(n)
	if n < 10000 {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// splitLines splits normalized output into lines, dropping one trailing
// empty line.
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// indentWidth counts leading spaces, a tab counting as 4 (go test indents
// with 4 spaces; compilers continue diagnostics with a tab).
func indentWidth(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}
