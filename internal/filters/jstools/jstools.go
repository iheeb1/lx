// Package jstools handles tsc, eslint, npm and JS builds.
package jstools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() {
	engine.Register(tsc{})
	engine.Register(eslint{})
	engine.Register(npmLs{})
	engine.Register(npmAudit{})
	engine.Register(npmOutdated{})
	engine.Register(npmInstall{})
	engine.Register(npmRun{})
}

func binName(s string) string {
	if strings.HasPrefix(s, "@") {
		if i := strings.LastIndexByte(s, '@'); i > 0 {
			s = s[:i]
		}
		return s
	}
	s = filepath.Base(s)
	if i := strings.IndexByte(s, '@'); i > 0 {
		s = s[:i]
	}
	for _, ext := range []string{".cmd", ".exe", ".ps1", ".cjs", ".mjs", ".js"} {
		s = strings.TrimSuffix(s, ext)
	}
	return s
}

var runnerValueFlags = map[string]bool{
	"-p": true, "--package": true, "--cache": true, "--userconfig": true, "--registry": true,
	"-w": true, "--workspace": true, "--prefix": true, "--node-options": true,
	"--shell": true, "--script-shell": true, "-C": true, "--dir": true, "--cwd": true,
	"--filter": true, "-F": true, "--loglevel": true,
}

func skipFlags(args []string) int {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				return i + 1
			}
			return -1
		case a == "-c" || a == "--call" || strings.HasPrefix(a, "--call="):
			return -1
		case runnerValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return i
		}
	}
	return -1
}

func tool(c *engine.Context) (string, []string) {
	name := binName(c.Name())
	args := c.Args()
	peel := func(from []string) (string, []string) {
		i := skipFlags(from)
		if i < 0 {
			return "", nil
		}
		return binName(from[i]), from[i+1:]
	}
	switch name {
	case "npx", "pnpx", "bunx":
		return peel(args)
	case "node":
		i := skipFlags(args)
		if i >= 0 {
			if b := binName(args[i]); b == "tsc" || b == "eslint" || b == "vue-tsc" {
				return b, args[i+1:]
			}
		}
		return name, args
	case "npm", "pnpm", "yarn", "bun":
		i := skipFlags(args)
		if i < 0 {
			return name, args
		}
		sub := args[i]
		switch {
		case name == "npm" && (sub == "exec" || sub == "x"),
			name == "pnpm" && (sub == "exec" || sub == "dlx"),
			name == "yarn" && (sub == "exec" || sub == "dlx"),
			name == "bun" && sub == "x":
			return peel(args[i+1:])
		case name != "npm" && sub == "run" && i+1 < len(args):
			if b := binName(args[i+1]); jsTools[b] {
				return b, args[i+2:]
			}
		case name != "npm" && jsTools[binName(sub)]:
			return binName(sub), args[i+1:]
		}
	}
	return name, args
}

var jsTools = map[string]bool{"tsc": true, "vue-tsc": true, "tsgo": true, "eslint": true, "vite": true, "next": true, "webpack": true, "webpack-cli": true}

func hasArg(args []string, names ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, n := range names {
			if a == n || strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

func argValue(args []string, names ...string) (string, bool) {
	for i, a := range args {
		if a == "--" {
			break
		}
		for _, n := range names {
			if a == n && i+1 < len(args) {
				return args[i+1], true
			}
			if strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"=") {
				return a[len(n)+1:], true
			}
		}
	}
	return "", false
}

var managerAliases = map[string]string{
	"install": "install", "i": "install", "in": "install", "ins": "install", "inst": "install", "insta": "install",
	"instal": "install", "isnt": "install", "isnta": "install", "isntal": "install", "isntall": "install",
	"add": "install", "a": "install",
	"ci": "ci", "clean-install": "ci", "ic": "ci", "install-clean": "ci", "isntall-clean": "ci",
	"uninstall": "uninstall", "un": "uninstall", "unlink": "uninstall", "remove": "uninstall", "rm": "uninstall", "r": "uninstall",
	"update": "update", "up": "update", "upgrade": "update", "udpate": "update",
	"ls": "ls", "list": "ls", "la": "ls", "ll": "ls",
	"audit": "audit", "outdated": "outdated", "create": "create",
	"run": "run", "run-script": "run", "rum": "run", "urn": "run",
}

func manager(c *engine.Context) (pm, sub string, rest []string, ok bool) {
	pm = binName(c.Name())
	if pm != "npm" && pm != "pnpm" && pm != "yarn" && pm != "bun" {
		return "", "", nil, false
	}
	args := c.Args()
	i := skipFlags(args)
	if i < 0 {
		if pm == "yarn" && !hasArg(args, "--version", "-v", "--help", "-h") {
			return pm, "install", nil, true
		}
		return "", "", nil, false
	}
	raw := args[i]
	sub = managerAliases[raw]
	switch {
	case pm == "npm" && raw == "init" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
		sub = "create"
	case pm == "bun" && raw == "pm" && i+1 < len(args) && args[i+1] == "ls":
		sub, i = "ls", i+1
	case sub == "":
		if pm != "npm" {
			return pm, "script", args[i:], true
		}
		return "", "", nil, false
	}
	return pm, sub, args[i+1:], true
}

type out struct {
	lines []string

	seen  map[string]bool
	text  strings.Builder
	scans int
}

func (o *out) add(s ...string) {
	for _, ln := range s {
		o.lines = append(o.lines, ln)
		if o.seen != nil {
			o.remember(ln)
		}
	}
}

func (o *out) remember(ln string) {
	sq := squash(ln)
	o.seen[sq] = true
	o.text.WriteString(sq)
	o.text.WriteByte('\n')
}

func (o *out) saw(key string) bool {
	if o.seen == nil {
		o.seen = make(map[string]bool, len(o.lines))
		for _, ln := range o.lines {
			o.remember(ln)
		}
	}
	return o.seen[key]
}

const maxContainsScans = 64

func (o *out) drop(s string) bool {
	if strings.TrimSpace(s) == "" || !engine.IsError(s) {
		return false
	}
	key := squash(s)
	if o.saw(key) {
		return false
	}
	if o.scans < maxContainsScans {
		o.scans++
		if strings.Contains(o.text.String(), key) {
			o.seen[key] = true
			return false
		}
	}
	o.add(s)
	return true
}

func failNote(c *engine.Context, view []string, evidence func(string) bool) string {
	if !c.Failed() {
		return ""
	}
	for _, ln := range view {
		if evidence != nil && evidence(ln) || engine.IsError(ln) {
			return ""
		}
	}
	why := ""
	if c.Exit > 128 && c.Exit < 160 {
		why = fmt.Sprintf(", killed by signal %d", c.Exit-128)
	}
	return fmt.Sprintf("[lx: %s exited %d%s, but no line above reports an error]", cmdLabel(c), c.Exit, why)
}

func cmdLabel(c *engine.Context) string {
	words := []string{binName(c.Name())}
	for _, a := range c.Args() {
		if len(words) == 3 {
			break
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	return strings.Join(words, " ")
}

func (o *out) blank() {
	if n := len(o.lines); n > 0 && o.lines[n-1] != "" {
		o.lines = append(o.lines, "")
	}
}

func (o *out) String() string {
	var b strings.Builder
	prevBlank := true
	for _, ln := range o.lines {
		blank := strings.TrimSpace(ln) == ""
		if blank && prevBlank {
			continue
		}
		if blank {
			ln = ""
		}
		b.WriteString(ln)
		b.WriteByte('\n')
		prevBlank = blank
	}
	return strings.TrimRight(b.String(), "\n")
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
