// Package golang handles the go tool.
package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.Register(testJSON{})
	engine.Register(testText{})
	engine.Register(build{})
	engine.Register(mod{})
	engine.Register(list{})
	engine.Register(machine{})
}

var versionedGoRe = lazyre.New(`^go1\.\d+(?:\.\d+)?(?:rc\d+|beta\d+)?$`)

func isGo(c *engine.Context) bool {
	n := strings.TrimSuffix(c.Name(), ".exe")
	return n == "go" || n == "gotip" || versionedGoRe.MatchString(n)
}

func goArgs(c *engine.Context) (sub string, rest []string) {
	args := c.Args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "--C":
			i++
		case strings.HasPrefix(a, "-C=") || strings.HasPrefix(a, "--C="):
		case strings.HasPrefix(a, "-"):
			return "", nil
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

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

var (
	downloadRe = lazyre.New(`^go: (?:downloading|extracting) (\S+) (\S+)$`)

	getReqRe  = lazyre.New(`^# get (https?://\S+)$`)
	getRespRe = lazyre.New(`^# get (https?://\S+): (\d{3}) [^()]*(?:\([\d.]+m?s\))?$`)

	namedModRe = lazyre.New(`^go: (?:upgraded|added|downgraded|removed) (\S+) |^go: found \S+ in (\S+) `)
	zipRe      = lazyre.New(`^https?://[^/]+/(.+)/@v/([^/]+)\.zip$`)
)

func condenseFetch(lines []string) ([]string, bool) {
	var (
		mods, keepDL, zips []string
		reqs, oks          int
		slotDL, slotGet    = -1, -1
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
					out = append(out, ln)
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

const maxListed = 30

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

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return num(n) + " " + many
}

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

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

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
