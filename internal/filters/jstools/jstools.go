// Package jstools condenses the output of the JavaScript toolchain: the
// TypeScript compiler, ESLint, the npm/pnpm/yarn/bun package managers and
// the vite/webpack/next builds usually run through `npm run build`.
//
// Test runners (npm test, jest, vitest) are not handled here.
//
// Every filter keeps the tool's own summary lines verbatim and never removes
// an error-class line: noise rules go through out.drop, which keeps any line
// engine.IsError flags. The filters that reformat diagnostics (tsc, eslint and
// npm-run) implement engine.Guarded and prove in their tests that every
// diagnostic keeps its location, message and rule/code; npm-install is
// Guarded for its two documented folds (npm's usage text, successful http
// requests). tsc, eslint, npm-install and npm-run end the view of a failed
// run that shows no error line with a "[lx: … exited N …]" note (failNote).
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

// binName turns an argv word into a bare tool name:
// "node_modules/.bin/tsc" → "tsc", "tsc.cmd" → "tsc", "tsc@5.4" → "tsc".
func binName(s string) string {
	if strings.HasPrefix(s, "@") {
		// Scoped package spec: keep the scope, drop a version.
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

// runnerValueFlags are package-runner flags followed by a value.
var runnerValueFlags = map[string]bool{
	"-p": true, "--package": true, "--cache": true, "--userconfig": true, "--registry": true,
	"-w": true, "--workspace": true, "--prefix": true, "--node-options": true,
	"--shell": true, "--script-shell": true, "-C": true, "--dir": true, "--cwd": true,
	"--filter": true, "-F": true, "--loglevel": true,
}

// skipFlags returns the index of the first positional argument of args,
// skipping runner flags (and their values) and a "--" separator. It returns
// -1 for "-c"/"--call" (a shell string, not a tool) or when there is none.
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

// tool resolves the program a command line runs, peeling package runners:
// npx/pnpx/bunx, npm exec|x, pnpm exec|dlx, yarn exec|dlx|run, bun x|run,
// "pnpm tsc"/"yarn eslint" (bins run through the manager) and
// "node path/to/tsc". It returns the bare tool name and its arguments.
// For a package-manager command that is not a runner ("npm install") it
// returns the manager itself.
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
			// "yarn run tsc" / "bun run eslint": a bin unless it is a script;
			// tool names are never our build script names, so this is safe.
			if b := binName(args[i+1]); jsTools[b] {
				return b, args[i+2:]
			}
		case name != "npm" && jsTools[binName(sub)]:
			return binName(sub), args[i+1:]
		}
	}
	return name, args
}

// jsTools are the bins resolved through a package manager ("pnpm tsc").
var jsTools = map[string]bool{"tsc": true, "vue-tsc": true, "tsgo": true, "eslint": true, "vite": true, "next": true, "webpack": true, "webpack-cli": true}

// hasArg reports whether args hold one of names (or name=value for long
// flags) before a "--" separator.
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

// argValue returns the value of a flag given as "-f v", "--format v" or
// "--format=v".
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

// managerAliases maps package-manager subcommand aliases to one name.
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

// manager parses a package-manager command line: the manager (npm, pnpm,
// yarn, bun), its subcommand with aliases resolved, and the arguments after
// it. Bare "yarn" is "install". ok is false for anything else.
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
		sub = "create" // npm init <initializer> == npm create
	case pm == "bun" && raw == "pm" && i+1 < len(args) && args[i+1] == "ls":
		sub, i = "ls", i+1
	case sub == "":
		if pm != "npm" {
			// "pnpm build" / "yarn lint" run a package.json script.
			return pm, "script", args[i:], true
		}
		return "", "", nil, false
	}
	return pm, sub, args[i+1:], true
}

// out accumulates a filtered view line by line.
type out struct {
	lines []string
	// seen holds the squashed text of the emitted lines (and of dropped
	// lines found in them), and text holds all of them, one per line. Both are
	// built on the first drop of an error-class line, which is rare, and
	// kept up to date after that.
	seen  map[string]bool
	text  strings.Builder
	scans int
}

// add emits lines verbatim.
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

// saw reports whether a line with the squashed text key was emitted.
func (o *out) saw(key string) bool {
	if o.seen == nil {
		o.seen = make(map[string]bool, len(o.lines))
		for _, ln := range o.lines {
			o.remember(ln)
		}
	}
	return o.seen[key]
}

// maxContainsScans bounds the substring searches drop makes, keeping it
// linear; past it an error-class line is simply kept.
const maxContainsScans = 64

// drop removes a noise line — unless it is error-class and its text does not
// already appear in what was emitted, in which case it is kept in place (the
// same test the engine's guard applies, done early so the line stays where
// it was). Filters route every line they discard through drop, so no noise
// rule can hide an error. It reports whether the line was kept.
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

// failNote returns the note that ends the view of a failed run (non-zero
// exit) in which no line says so: a killed build, a script that failed
// silently after the tool's own success line ("webpack compiled
// successfully" then exit 1). Without it the view would read as a success.
// It returns "" when the run succeeded or some line of view is error-class
// or satisfies evidence (a tool's own failure verdict that is not
// error-class, like ESLint's "too many warnings").
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

// cmdLabel names the command for notes: the program and its first
// positional arguments ("npm run build", "npx tsc").
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

// blank emits one empty line unless the view is empty or already ends
// with one.
func (o *out) blank() {
	if n := len(o.lines); n > 0 && o.lines[n-1] != "" {
		o.lines = append(o.lines, "")
	}
}

// String joins the lines, collapsing blank runs and trimming blank edges.
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

// squash collapses whitespace runs and trims, the comparison the engine's
// guard uses.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
