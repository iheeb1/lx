package hook

import (
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func Supported(argv []string) bool {
	return supported(argv, 0)
}

func supported(argv []string, depth int) bool {

	if len(argv) == 0 || has(argv[1:], "--help", "--version") || engine.MachineReadable(&engine.Context{Argv: argv}) {
		return false
	}
	name := filepath.Base(argv[0])
	args := argv[1:]
	if rule, ok := tools[name]; ok {
		return rule(args)
	}
	switch {
	case rePython.MatchString(name):
		return pythonOK(args)
	case rePip.MatchString(name):
		return pipOK(args)
	case reGoVersion.MatchString(name):
		return goOK(args)
	}
	if depth == 0 && argv[0] == "command" {

		return false
	}
	if depth < engine.MaxPeel {
		if inner, _ := engine.Peel(argv); inner != nil {
			if lxOff(argv[:len(argv)-len(inner)]) {
				return false
			}
			return supported(inner, depth+1)
		}
	}
	return false
}

func lxOff(words []string) bool {
	for _, w := range words {
		name, val, ok := strings.Cut(w, "=")
		if ok && (name == "LX_RAW" || name == "LX_OFF") && val != "" && val != "0" {
			return true
		}
	}
	return false
}

func wrappedTexts(argv []string) []string {
	var out []string
	for range engine.MaxPeel {
		inner, _ := engine.Peel(argv)
		if inner == nil {
			break
		}
		out = append(out, strings.Join(inner, " "))
		argv = inner
	}
	return out
}

var (
	rePython = lazyre.New(`^python(\d+(\.\d+)?)?$`)
	rePip    = lazyre.New(`^pip(\d+(\.\d+)?)?$`)

	reGoVersion = lazyre.New(`^go1\.\d+(?:\.\d+)?(?:rc\d+|beta\d+)?$`)
)

type rule func(args []string) bool

var tools map[string]rule

func init() {
	always := func([]string) bool { return true }
	tools = map[string]rule{
		"git":           gitOK,
		"go":            goOK,
		"gotip":         goOK,
		"cargo":         cargoOK,
		"npm":           func(a []string) bool { return pkgOK("npm", a) },
		"pnpm":          func(a []string) bool { return pkgOK("pnpm", a) },
		"yarn":          func(a []string) bool { return pkgOK("yarn", a) },
		"bun":           func(a []string) bool { return pkgOK("bun", a) },
		"npx":           execToolOK,
		"bunx":          execToolOK,
		"jest":          jestOK,
		"vitest":        vitestOK,
		"mocha":         mochaOK,
		"tsc":           tscOK,
		"vue-tsc":       tscOK,
		"tsgo":          tscOK,
		"eslint":        eslintOK,
		"prettier":      prettierOK,
		"playwright":    func(a []string) bool { return firstPos(a, nil) == "test" && playwrightOK(a) },
		"next":          func(a []string) bool { return firstPos(a, nil) == "build" },
		"vite":          func(a []string) bool { return firstPos(a, nil) == "build" && !hasWatch(a) && !has(a, "-w") },
		"pytest":        pytestOK,
		"py.test":       pytestOK,
		"mypy":          mypyOK,
		"ruff":          ruffOK,
		"golangci-lint": golangciOK,
		"make":          makeOK,
		"gmake":         makeOK,
		"ninja":         ninjaOK,
		"cmake":         cmakeOK,
		"gradle":        gradleOK,
		"gradlew":       gradleOK,
		"mvn":           mvnOK,
		"mvnw":          mvnOK,
		"ls":            func(a []string) bool { return !has(a, "--zero", "--dired", "-D") },
		"find":          findOK,
		"fd":            fdOK,
		"fdfind":        fdOK,
		"grep":          grepOK,
		"egrep":         grepOK,
		"fgrep":         grepOK,
		"rg":            rgOK,
		"tree":          treeOK,
		"du":            duOK,
		"docker":        dockerOK,
		"docker-compose": func(a []string) bool {
			return composeOK(a)
		},
		"kubectl":    kubectlOK,
		"journalctl": journalctlOK,
		"curl":       curlOK,
		"brew":       brewOK,
		"terraform":  terraformOK,
		"swift":      func(a []string) bool { return oneOf(firstPos(a, nil), "build", "test") },
		"dotnet":     func(a []string) bool { return oneOf(firstPos(a, nil), "build", "test") && !hasWatch(a) },
		"flutter":    dartOK,
		"dart":       dartOK,
		"composer":   composerOK,
		"bundle":     bundleOK,
		"rspec":      rspecOK,
		"phpunit":    always,

		"just":  justOK,
		"task":  goTaskOK,
		"mise":  miseOK,
		"turbo": turboOK,
		"nx":    nxOK,
		"rake":  rakeOK,
		"deno":  denoOK,
	}
}

func oneOf(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

func firstPos(args []string, valueFlags map[string]bool) string {
	p, _ := posAt(args, valueFlags)
	return p
}

func posAt(args []string, valueFlags map[string]bool) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				return args[i+1], args[i+2:]
			}
			return "", nil
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if valueFlags[a] {
				i++
			}
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func has(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

func hasPrefix(args []string, prefixes ...string) bool {
	for _, a := range args {
		for _, p := range prefixes {
			if strings.HasPrefix(a, p) {
				return true
			}
		}
	}
	return false
}

func shortFlag(args []string, letters, valueLetters string) bool {
	for _, a := range args {
		if len(a) < 2 || a[0] != '-' || a[1] == '-' {
			continue
		}
		for i := 1; i < len(a); i++ {
			c := a[i]
			if strings.IndexByte(letters, c) >= 0 {
				return true
			}
			if strings.IndexByte(valueLetters, c) >= 0 {
				break
			}
		}
	}
	return false
}

func optValues(args []string, long, short string) []string {
	var out []string
	for i, a := range args {
		switch {
		case long != "" && a == long, short != "" && a == short:
			if i+1 < len(args) {
				out = append(out, args[i+1])
			}
		case long != "" && strings.HasPrefix(a, long+"="):
			out = append(out, a[len(long)+1:])
		case short != "" && strings.HasPrefix(a, short+"="):
			out = append(out, a[len(short)+1:])
		case short != "" && len(short) == 2 && strings.HasPrefix(a, short) && len(a) > 2:
			out = append(out, a[2:])
		}
	}
	return out
}

func anyValue(vals []string, pred func(string) bool) bool {
	for _, v := range vals {
		if pred(strings.ToLower(v)) {
			return true
		}
	}
	return false
}

func hasWatch(args []string) bool {
	return has(args, "--watch", "--watchAll", "--watch-all", "--watchAll=true") || hasPrefix(args, "--watch=")
}

var gitSubs = set("status", "log", "diff", "show", "push", "pull", "fetch", "clone",
	"branch", "merge", "rebase", "commit", "stash", "blame", "reflog", "cherry-pick",
	"tag", "remote", "grep", "shortlog", "worktree", "ls-remote", "submodule", "cherry",
	"show-branch")

var gitGlobalValue = set("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env")

var gitGlobalBool = set("--no-pager", "-P", "--paginate", "-p", "--bare", "--no-replace-objects",
	"--literal-pathspecs", "--glob-pathspecs", "--noglob-pathspecs", "--icase-pathspecs",
	"--no-optional-locks", "--no-lazy-fetch", "--no-advice")

func gitOK(args []string) bool {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case gitGlobalValue[a]:
			i += 2
			continue
		case gitGlobalBool[a], strings.HasPrefix(a, "--git-dir="), strings.HasPrefix(a, "--work-tree="),
			strings.HasPrefix(a, "--namespace="), strings.HasPrefix(a, "--config-env="):
			i++
			continue
		case strings.HasPrefix(a, "-"):
			return false
		}
		break
	}
	if i >= len(args) || !gitSubs[args[i]] {
		return false
	}
	sub, rest := args[i], args[i+1:]
	if gitMachine(rest) {
		return false
	}
	switch sub {
	case "remote":
		return has(rest, "-v", "--verbose") || firstPos(rest, nil) == "show"
	case "show":

		for _, a := range rest {
			if !strings.HasPrefix(a, "-") && strings.Contains(a, ":") {
				return false
			}
		}
	case "rebase":
		return !has(rest, "-i", "--interactive", "--edit-todo") && !shortFlag(rest, "i", "sSXx")
	case "commit":
		return !has(rest, "--interactive", "-p", "--patch") && !shortFlag(rest, "p", "mFcCt")
	case "stash":

		return len(rest) > 0 && rest[0] == "show" || !has(rest, "-p", "--patch")
	case "blame":
		return !has(rest, "-p", "--incremental")
	case "grep":
		return gitGrepOK(rest)
	case "worktree":
		return firstPos(rest, nil) == "list"
	case "submodule":
		return oneOf(firstPos(rest, nil), "", "status", "summary")
	}
	return true
}

func gitGrepOK(args []string) bool {
	if has(args, "--quiet", "--open-files-in-pager", "--count", "--files-with-matches", "--name-only",
		"--files-without-match", "--null", "--heading", "--break", "--show-function", "--column",
		"--function-context") {
		return false
	}
	return !shortFlag(args, "clLqzOp", "ABCefm")
}

func gitMachine(args []string) bool {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--porcelain"), strings.HasPrefix(a, "--line-porcelain"),
			a == "-z", a == "--null", a == "--name-only", a == "--name-status",
			a == "--numstat", a == "--raw", strings.HasPrefix(a, "--json"),
			strings.HasPrefix(a, "--format"),
			strings.HasPrefix(a, "--pretty=format:"), strings.HasPrefix(a, "--pretty=tformat:"):
			return true
		case strings.HasPrefix(a, "--pretty="):

			if strings.Contains(a, "%") {
				return true
			}
		}
	}
	return false
}

var goModOK = set("tidy", "download", "verify", "vendor", "init")

func goOK(args []string) bool {
	sub, rest := posAt(args, set("-C"))
	if hasPrefix(args, "-json", "--json") {
		return false
	}
	switch sub {
	case "test":
		return !goFlag(rest, "bench", "fuzz", "list", "x", "n", "h", "help")
	case "build", "vet", "install":
		return !goFlag(rest, "x", "n", "h", "help")
	case "get":
		return !goFlag(rest, "h", "help")
	case "mod":
		return goModOK[firstPos(rest, nil)]
	}
	return false
}

func goFlag(args []string, names ...string) bool {
	for _, a := range args {
		if a == "-args" || a == "--args" || a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		f, val, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		f = strings.TrimPrefix(f, "test.")
		if val != "false" && oneOf(f, names...) {
			return true
		}
	}
	return false
}

var cargoValueFlags = set("--color", "--config", "-Z", "-C", "--manifest-path")

func cargoOK(args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "+") {
		args = args[1:]
	}
	if hasPrefix(args, "--message-format", "--format") || hasWatch(args) {
		return false
	}
	sub, rest := posAt(args, cargoValueFlags)
	if oneOf(sub, "test", "t", "nextest") && has(args, "--list", "--no-run") {
		return false
	}
	if sub == "nextest" {

		return oneOf(firstPos(rest, set("-P", "--profile", "-E", "--filterset", "--partition", "-j",
			"--test-threads", "--manifest-path", "--config-file", "--tool-config-file")), "run", "r")
	}
	return oneOf(sub, "build", "b", "test", "t", "check", "c", "clippy", "install", "update", "fetch")
}

var pkgValueFlags = set("--prefix", "-w", "--workspace", "-C", "--dir", "--filter", "-F",
	"--cwd", "--registry", "--loglevel", "--userconfig", "--reporter")

var pkgBuiltins = set("install", "i", "ci", "add", "remove", "rm", "uninstall", "un",
	"update", "up", "upgrade", "audit", "outdated", "ls", "list", "test", "t", "tst")

func pkgOK(name string, args []string) bool {
	if hasPrefix(args, "--json", "--reporter=json") || has(args, "--parseable", "--porcelain", "-i", "--interactive") || hasWatch(args) {
		return false
	}
	sub, rest := posAt(args, pkgValueFlags)
	switch {
	case sub == "":
		return name == "yarn"
	case pkgBuiltins[sub]:
		return true
	case sub == "run" || sub == "run-script":
		return scriptOK(firstPos(rest, nil))
	case sub == "exec" || sub == "x" || sub == "dlx" && name != "npm":
		return execToolOK(rest)
	case sub == "workspace" && name == "yarn":
		_, inner := posAt(rest, nil)
		return pkgOK(name, inner)
	case name != "npm":

		return scriptOK(sub)
	}
	return false
}

var scriptSplit = lazyre.New(`[^a-z0-9]+`)

func scriptOK(script string) bool {
	if script == "" {
		return false
	}
	parts := scriptSplit.Split(strings.ToLower(script), -1)
	for _, p := range parts {
		for _, bad := range []string{"dev", "start", "serve", "watch", "preview"} {
			if strings.HasPrefix(p, bad) {
				return false
			}
		}
	}
	for _, p := range parts {
		for _, good := range []string{"test", "build", "lint", "typecheck", "check", "compile"} {
			if strings.HasPrefix(p, good) {
				return true
			}
		}
	}
	return false
}

func execToolOK(args []string) bool {
	if has(args, "-c", "--call") {
		return false
	}
	tool, rest := posAt(args, set("-p", "--package"))
	if tool == "" {
		return false
	}
	if at := strings.LastIndexByte(tool, '@'); at > 0 {
		tool = tool[:at]
	}
	tool = filepath.Base(tool)
	switch tool {
	case "jest", "vitest", "mocha", "tsc", "eslint", "prettier", "playwright", "next", "vite", "nx", "turbo":
		return Supported(append([]string{tool}, rest...))
	}
	return false
}

func jestOK(a []string) bool {
	return !hasWatch(a) && !has(a, "-w", "--json", "--listTests", "--showConfig", "-v", "-h", "--init", "--clearCache")
}

func vitestOK(a []string) bool {
	if oneOf(firstPos(a, nil), "watch", "dev", "list", "bench", "init") || hasWatch(a) ||
		has(a, "-w", "--ui", "--standalone", "-v", "-h") {
		return false
	}
	return !anyValue(optValues(a, "--reporter", ""), isJSONish)
}

func mochaOK(a []string) bool {
	return !hasWatch(a) && !has(a, "-w", "-V", "-h", "--list-files", "--list-reporters", "--list-interfaces", "--dry-run") &&
		!anyValue(optValues(a, "--reporter", "-R"), isJSONish)
}

func tscOK(a []string) bool {
	return !hasWatch(a) && !has(a, "-w", "-v", "-h", "--all", "--init", "--showConfig", "--listFilesOnly", "--listFiles",
		"--listEmittedFiles", "--explainFiles", "--traceResolution", "--generateTrace", "--extendedDiagnostics", "--diagnostics")
}

func eslintOK(a []string) bool {
	if has(a, "--print-config", "--init", "--inspect-config", "--mcp", "--env-info", "-v", "-h", "-o", "--output-file") {
		return false
	}
	return !anyValue(optValues(a, "--format", "-f"), func(v string) bool { return v != "stylish" })
}

func prettierOK(a []string) bool {
	return has(a, "--check", "-c") && !has(a, "--write", "-w")
}

func playwrightOK(a []string) bool {
	return !has(a, "--ui", "--debug", "--ui-port", "--ui-host") &&
		!anyValue(optValues(a, "--reporter", ""), isJSONish)
}

func isJSONish(v string) bool { return strings.Contains(v, "json") }

func pytestOK(a []string) bool {
	return !has(a, "--pdb", "--trace", "--pdbcls", "-f", "--looponfail") &&
		!hasPrefix(a, "--pdbcls=") &&

		!has(a, "--co", "--collect-only", "--collectonly", "-V", "-h", "--fixtures", "--funcargs",
			"--fixtures-per-test", "--markers", "--trace-config", "--setup-plan")
}

func pythonOK(args []string) bool {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-m":
			if i+1 >= len(args) {
				return false
			}
			return moduleOK(args[i+1], args[i+2:])
		case strings.HasPrefix(a, "-m") && len(a) > 2:
			return moduleOK(a[2:], args[i+1:])
		case a == "-X" || a == "-W":
			i += 2
		case a == "-c" || !strings.HasPrefix(a, "-") || a == "-":
			return false
		case strings.ContainsAny(a[1:], "cm"):
			return false
		case a[1] != '-' && strings.IndexByte(a[1:], 'i') >= 0:
			return false
		default:
			i++
		}
	}
	return false
}

func moduleOK(mod string, rest []string) bool {
	switch mod {
	case "pytest":
		return pytestOK(rest)
	case "pip":
		return pipOK(rest)
	case "unittest":
		return true
	case "mypy":
		return mypyOK(rest)
	}
	return false
}

func pipOK(args []string) bool {
	sub, rest := posAt(args, set("--proxy", "--python", "--log", "--cache-dir", "--timeout", "--retries"))
	switch sub {
	case "install":
		return !has(rest, "--report", "--dry-run", "-h")
	case "list":
		return !has(rest, "--format", "--json")
	}
	return false
}

func mypyOK(a []string) bool {
	if len(optValues(a, "--output", "-O")) > 0 || has(a, "-V", "-h") {
		return false
	}
	return !has(a, "--install-types") || has(a, "--non-interactive")
}

func ruffOK(a []string) bool {
	sub, rest := posAt(a, set("--config"))
	if sub != "check" || hasWatch(rest) || has(rest, "-w", "-h", "--diff", "--show-settings", "--show-files", "--statistics") {
		return false
	}
	for _, v := range append(optValues(rest, "--output-format", ""), optValues(rest, "--format", "")...) {
		if !oneOf(strings.ToLower(v), "full", "concise", "grouped", "text") {
			return false
		}
	}
	return true
}

func golangciOK(a []string) bool {
	if firstPos(a, set("-c", "--config")) != "run" {
		return false
	}
	machine := []string{"json", "checkstyle", "junit", "sarif", "code-climate", "teamcity", "html", "github-actions"}
	for i, x := range a {
		if !strings.HasPrefix(x, "--out-format") && !strings.HasPrefix(x, "--output.") {
			continue
		}
		v := strings.ToLower(x)
		if x == "--out-format" && i+1 < len(a) {
			v = strings.ToLower(a[i+1])
		}
		for _, m := range machine {
			if strings.Contains(v, m) {
				return false
			}
		}
	}
	return true
}

var makeValueFlags = set("-C", "-f", "--file", "--makefile", "--directory", "-I", "--include-dir",
	"-o", "--old-file", "-W", "--what-if", "--new-file", "--assume-new", "-l", "--load-average")

var longRunningPrefix = []string{"dev", "start", "serve", "watch", "preview", "shell", "console",
	"repl", "debug", "attach", "tail", "tunnel", "forward", "live"}
var longRunningExact = set("up", "run", "log", "logs", "db", "ssh", "exec")

func longRunning(name string) bool {
	for _, p := range scriptSplit.Split(strings.ToLower(name), -1) {
		if longRunningExact[p] {
			return true
		}
		for _, lp := range longRunningPrefix {
			if strings.HasPrefix(p, lp) {
				return true
			}
		}
	}
	return false
}

func makeOK(args []string) bool {

	if has(args, "--just-print", "--dry-run", "--recon", "--print-data-base", "--question", "--debug", "--trace") ||
		shortFlag(args, "npqdvh", "CfIoWjlEO") {
		return false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case makeValueFlags[a]:
			i++
		case a == "-j" || a == "--jobs":
			if i+1 < len(args) && isDigits(args[i+1]) {
				i++
			}
		case strings.HasPrefix(a, "-"), strings.Contains(a, "="):
		default:
			if longRunning(a) {
				return false
			}
		}
	}
	return true
}

var ninjaValueFlags = set("-C", "-f", "-j", "-k", "-l", "-d", "-w")

func ninjaOK(args []string) bool {
	if has(args, "-n", "-h", "--version") || hasPrefix(args, "-t") {
		return false
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case ninjaValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			if longRunning(a) {
				return false
			}
		}
	}
	return true
}

func cmakeOK(args []string) bool {
	if !has(args, "--build") {
		return false
	}
	target := false
	for i, a := range args {
		switch {
		case a == "--":
			return makeOK(args[i+1:])
		case a == "--target" || a == "-t":
			target = true
		case strings.HasPrefix(a, "--target="):
			if longRunning(a[len("--target="):]) {
				return false
			}
			target = false
		case strings.HasPrefix(a, "-"):
			target = false
		case target && longRunning(a):
			return false
		}
	}
	return true
}

var gradleValueFlags = set("-p", "--project-dir", "-b", "--build-file", "-c", "--settings-file",
	"-x", "--exclude-task", "-I", "--init-script", "-g", "--gradle-user-home", "--console", "-D", "-P")

func gradleOK(args []string) bool {

	if has(args, "--continuous", "-t", "--scan", "--debug-jvm", "tasks", "dependencies", "dependencyInsight",
		"properties", "projects", "help", "-h", "-v", "--debug", "-d", "--console=rich", "--console=verbose") {
		return false
	}
	for _, a := range args {
		if strings.HasSuffix(a, ":dependencies") || strings.HasSuffix(a, ":tasks") {
			return false
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case gradleValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			task := strings.ToLower(a[strings.LastIndexByte(a, ':')+1:])
			if strings.HasSuffix(task, "run") || longRunning(task) {
				return false
			}
		}
	}
	return true
}

var mvnValueFlags = set("-f", "--file", "-pl", "--projects", "-rf", "--resume-from", "-s", "--settings",
	"-gs", "--global-settings", "-T", "--threads", "-P", "--activate-profiles", "-D", "-l", "--log-file")

func mvnOK(args []string) bool {
	if has(args, "-v", "-h", "-X", "--debug") {
		return false
	}
	for _, a := range args {

		if strings.HasPrefix(a, "dependency:tree") || strings.HasPrefix(a, "dependency:list") ||
			strings.HasPrefix(a, "help:") || strings.HasPrefix(a, "versions:display") {
			return false
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case mvnValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			g := strings.ToLower(a)
			for _, bad := range []string{":run", ":dev", ":start", ":watch", "exec:", ":exec"} {
				if strings.Contains(g, bad) {
					return false
				}
			}
		}
	}
	return true
}

func findOK(a []string) bool {
	return !has(a, "-exec", "-execdir", "-ok", "-okdir", "-delete", "-print0", "-printf", "-ls",
		"-fprint", "-fprint0", "-fprintf", "-fls")
}

func fdOK(a []string) bool {
	return !has(a, "-x", "-X", "--exec-batch", "-l", "--list-details", "-0", "--print0", "--format", "-h", "-V",
		"--gen-completions") && !hasPrefix(a, "--exec")
}

func grepOK(a []string) bool {
	if has(a, "--files-with-matches", "--files-without-match", "--count", "--quiet", "--silent",
		"--null", "--null-data", "--json", "--byte-offset", "--initial-tab", "--unix-byte-offsets") {
		return false
	}
	return !shortFlag(a, "lLcqzZbTuV", "efmABCdD")
}

func rgOK(a []string) bool {
	if has(a, "--files-with-matches", "--files-without-match", "--count", "--count-matches",
		"--quiet", "--json", "--null", "--null-data", "--search-zip", "--byte-offset", "--heading", "--pretty",
		"--stats", "--type-list", "--debug", "--trace", "--generate", "--pcre2-version",
		"--field-match-separator", "--field-context-separator", "--passthru", "--passthrough") {
		return false
	}
	return !shortFlag(a, "lcq0zbphV", "ABCefgjmMrtTEd")
}

func treeOK(a []string) bool {
	for _, x := range a {
		switch {
		case strings.HasPrefix(x, "--"):
			if has([]string{x}, "--du", "--inodes", "--device", "--fromfile", "--info", "--hyperlink", "--charset") {
				return false
			}
		case strings.HasPrefix(x, "-") && strings.ContainsAny(x[1:], "JXHpugshDiQNqoRT"):
			return false
		}
	}
	return true
}

func duOK(a []string) bool {
	return !has(a, "--null", "--time", "--inodes") && !shortFlag(a, "0", "BdtX")
}

var dockerGlobalValue = set("--context", "-c", "-H", "--host", "--config", "-l", "--log-level",
	"--tlscacert", "--tlscert", "--tlskey")

func dockerOK(args []string) bool {
	if has(args, "-i", "-it", "-ti", "--interactive", "--tty") {
		return false
	}
	sub, rest := posAt(args, dockerGlobalValue)
	switch sub {
	case "ps", "images":
		return !hasPrefix(rest, "--format") && !dockerQuiet(rest)
	case "logs":
		return dockerLogsOK(rest)
	case "build":
		return true
	case "pull":
		return true
	case "compose":
		return composeOK(rest)
	case "buildx":
		return firstPos(rest, set("--builder")) == "build"
	case "image":
		s2, r2 := posAt(rest, nil)
		switch s2 {
		case "ls", "list":
			return !hasPrefix(r2, "--format") && !dockerQuiet(r2)
		case "pull", "build":
			return true
		}
		return false
	case "container":
		s2, r2 := posAt(rest, nil)
		switch s2 {
		case "ls", "list", "ps":
			return !hasPrefix(r2, "--format") && !dockerQuiet(r2)
		case "logs":
			return dockerLogsOK(r2)
		}
	}
	return false
}

func dockerQuiet(a []string) bool {
	return has(a, "-q", "--quiet") || shortFlag(a, "q", "fn")
}

func dockerLogsOK(a []string) bool {
	return !has(a, "-f", "--follow") && !shortFlag(a, "f", "n")
}

var composeGlobalValue = set("-f", "--file", "-p", "--project-name", "--profile", "--env-file",
	"--project-directory", "--ansi", "--progress", "--parallel")

func composeOK(args []string) bool {
	sub, rest := posAt(args, composeGlobalValue)
	switch sub {
	case "ps":
		return !hasPrefix(rest, "--format") && !dockerQuiet(rest)
	case "logs":
		return !has(rest, "-f", "--follow") && !shortFlag(rest, "f", "n")
	case "build", "pull":
		return true
	}
	return false
}

var kubectlGlobalValue = set("-n", "--namespace", "--context", "--kubeconfig", "--cluster", "--user",
	"-s", "--server", "--token", "--as", "--as-group", "--request-timeout")

func kubectlOK(args []string) bool {
	if has(args, "-w", "--watch", "--watch-only", "-i", "-it", "--stdin", "--tty") {
		return false
	}
	sub, rest := posAt(args, kubectlGlobalValue)
	switch sub {
	case "get", "events", "top":
		return !anyValue(optValues(args, "--output", "-o"), func(v string) bool {
			for _, p := range []string{"json", "yaml", "jsonpath", "name", "go-template", "template"} {
				if strings.HasPrefix(v, p) {
					return true
				}
			}
			return false
		})
	case "describe":
		return true
	case "logs":
		return !has(rest, "-f", "--follow") && !shortFlag(rest, "f", "clL")
	}
	return false
}

func journalctlOK(a []string) bool {

	if has(a, "-f", "--follow", "-F", "--field") || shortFlag(a, "f", "unptoSUDMgFbic") {
		return false
	}
	return !anyValue(optValues(a, "--output", "-o"), func(v string) bool {
		return strings.HasPrefix(v, "json") || v == "export"
	})
}

func curlOK(a []string) bool {
	if has(a, "-o", "-O", "--output", "--output-dir", "--remote-name", "--remote-name-all",
		"-w", "--write-out", "-K", "--config") {
		return false
	}
	return !shortFlag(a, "oOwK", "AbcCdDeEFHmPQrtTuUxXyYz")
}

func brewOK(a []string) bool {
	return oneOf(firstPos(a, nil), "install", "upgrade", "list", "ls", "outdated") && !hasPrefix(a, "--json")
}

func terraformOK(a []string) bool {
	if hasPrefix(a, "-json", "--json") {
		return false
	}
	return oneOf(firstPos(a, nil), "plan", "init", "validate")
}

func dartOK(a []string) bool {
	if !oneOf(firstPos(a, nil), "test", "analyze") {
		return false
	}
	for _, x := range a {
		if strings.HasPrefix(x, "-") && (strings.Contains(x, "json") || strings.Contains(x, "machine")) {
			return false
		}
	}
	vals := append(optValues(a, "--reporter", "-r"), optValues(a, "--format", "")...)
	return !anyValue(vals, func(v string) bool { return strings.Contains(v, "json") || strings.Contains(v, "machine") })
}

func bundleOK(a []string) bool {
	sub, rest := posAt(a, nil)
	switch sub {
	case "install":
		return true
	case "exec":
		tool, r2 := posAt(rest, nil)
		return tool == "rspec" && rspecOK(r2)
	}
	return false
}

func rspecOK(a []string) bool {
	return !anyValue(optValues(a, "--format", "-f"), func(v string) bool {
		return strings.HasPrefix(v, "j") || strings.HasPrefix(v, "h")
	})
}

func runnerTaskOK(name string) bool {
	if name == "" || strings.ContainsAny(name, "$`") || riskyTask(name) {
		return false
	}
	for _, p := range scriptSplit.Split(strings.ToLower(name), -1) {
		if runnerCheckWords[p] {
			return true
		}
	}
	return false
}

var runnerCheckWords = set("test", "tests", "spec", "specs", "lint", "lints", "check", "checks",
	"typecheck", "typechecks", "build", "builds", "compile")

func riskyTask(name string) bool {
	if longRunning(name) {
		return true
	}
	for _, p := range scriptSplit.Split(strings.ToLower(name), -1) {
		if strings.HasPrefix(p, "deploy") || strings.HasPrefix(p, "release") || strings.HasPrefix(p, "publish") {
			return true
		}
	}
	return false
}

func runnerArgsOK(args []string) bool {
	for _, a := range args {
		if strings.ContainsAny(a, "$`") {
			return false
		}
	}
	return !hasWatch(args) && !has(args, "-w", "--ui", "--inspect", "--inspect-brk", "--inspect-wait") &&
		!hasPrefix(args, "--inspect=", "--inspect-brk=", "--inspect-wait=")
}

var taskNameRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_-]*(?:(?::|::)[A-Za-z0-9_-]+)*$`)

func flagWord(a string) (string, bool) {
	if strings.HasPrefix(a, "--") {
		if k := strings.IndexByte(a, '='); k > 0 {
			return a[:k], true
		}
	}
	return a, false
}

var (
	justValue = set("-f", "--justfile", "-d", "--working-directory", "--shell", "--shell-arg",
		"--dotenv-filename", "--dotenv-path", "-E", "--color", "--command-color", "--tempdir",
		"--timestamp-format")
	justBool = set("-q", "--quiet", "-v", "--verbose", "-vv", "-vvv", "--yes", "--highlight",
		"--no-highlight", "--no-dotenv", "--unstable", "--timestamp", "--explain", "--no-deps",
		"--clear-shell-args", "-g", "--global-justfile", "-u", "--unsorted")
)

func justOK(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		f, attached := flagWord(a)
		switch {
		case a == "--set":
			i += 2
		case justValue[f]:
			if !attached {
				i++
			}
		case justBool[a]:
		case strings.HasPrefix(a, "-"):

			return false
		case strings.Contains(a, "=") && peelAssign(a):

		default:

			if !runnerTaskOK(a) {
				return false
			}
			rest := args[i+1:]
			for _, r := range rest {
				if taskNameRe.MatchString(r) && !strings.Contains(r, "=") && riskyTask(r) {
					return false
				}
			}
			return runnerArgsOK(rest)
		}
	}
	return false
}

func peelAssign(a string) bool {
	eq := strings.IndexByte(a, '=')
	return eq > 0 && taskNameRe.MatchString(a[:eq]) && !strings.Contains(a[:eq], ":")
}

var (
	goTaskValue = set("-d", "--dir", "-t", "--taskfile", "-o", "--output", "-C", "--concurrency",
		"--interval", "--output-group-begin", "--output-group-end", "--sort", "--cacert", "--cert",
		"--cert-key", "--remote-cache-dir", "--expiry", "--timeout", "--trusted-hosts")
	goTaskBool = set("-f", "--force", "-s", "--silent", "-v", "--verbose", "-p", "--parallel", "-y", "--yes",
		"-x", "--exit-code", "-c", "--color", "--insecure", "--download", "--offline", "-g", "--global",
		"--output-group-error-only", "-F", "--failfast", "--disable-fuzzy")
)

func goTaskOK(args []string) bool {
	tasks := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		f, attached := flagWord(a)
		switch {
		case a == "--":
			return tasks > 0 && runnerArgsOK(args[i+1:])
		case goTaskValue[f]:
			if !attached {
				i++
			}
		case goTaskBool[a]:
		case strings.HasPrefix(a, "-"):

			return false
		case peelAssign(a):
		default:
			if !runnerTaskOK(a) {
				return false
			}
			tasks++
		}
	}
	return tasks > 0
}

var (
	miseGlobalValue = set("-C", "--cd", "-E", "--env", "-j", "--jobs")
	miseRunValue    = set("-C", "--cd", "-E", "--env", "-j", "--jobs", "-o", "--output", "-s", "--shell",
		"-t", "--tool")
	miseRunBool = set("-c", "--continue-on-error", "-f", "--force", "-p", "--prefix", "-i", "--interleave",
		"-q", "--quiet", "-S", "--silent", "--timings", "--no-timings", "--no-cache", "--fresh-env",
		"--skip-deps", "--no-prepare", "-y", "--yes", "-v", "--verbose")
)

func miseOK(args []string) bool {
	sub, rest := posAt(args, miseGlobalValue)
	if sub != "run" && sub != "r" || hasPrefix(args[:len(args)-len(rest)], "--raw", "-r") {
		return false
	}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		f, attached := flagWord(a)
		switch {
		case miseRunValue[f]:
			if !attached {
				i++
			}
		case miseRunBool[a]:
		case strings.HasPrefix(a, "-"):

			return false
		default:

			groups := [][]string{{}}
			for _, w := range rest[i:] {
				if w == ":::" {
					groups = append(groups, []string{})
					continue
				}
				groups[len(groups)-1] = append(groups[len(groups)-1], w)
			}
			for _, g := range groups {
				if len(g) == 0 || !runnerTaskOK(g[0]) || !runnerArgsOK(g[1:]) {
					return false
				}
			}
			return true
		}
	}
	return false
}

var (
	turboValue = set("--filter", "-F", "--concurrency", "--cache-dir", "--cache", "--env-mode",
		"--log-order", "--log-prefix", "--output-logs", "--profile", "--anon-profile", "--token", "--team",
		"--api", "--login", "--cwd", "--heap", "--global-deps", "--scope", "--since", "--cache-workers",
		"--remote-cache-timeout", "--framework-inference", "--ui", "--env-var", "--pass-through-env", "--verbosity")
	turboBool = set("--continue", "--force", "--parallel", "--no-cache", "--no-daemon", "--daemon", "--only",
		"--affected", "--color", "--no-color", "--remote-only", "--no-update-notifier", "--skip-infer",
		"--single-package", "-v", "-vv", "-vvv", "--summarize", "--include-dependencies",
		"--no-deps", "--experimental-write-cache")
)

func turboOK(args []string) bool {
	if anyValue(optValues(args, "--ui", ""), func(v string) bool { return v != "stream" }) {
		return false
	}
	tasks := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		f, attached := flagWord(a)
		switch {
		case a == "--":
			return tasks > 0 && runnerArgsOK(args[i+1:])
		case turboValue[f]:
			if !attached {
				i++
			}
		case turboBool[f]:
		case strings.HasPrefix(a, "-"):

			return false
		case a == "run" && tasks == 0 && i == firstPosIndex(args, turboValue):
		default:
			if !runnerTaskOK(a) {
				return false
			}
			tasks++
		}
	}
	return tasks > 0
}

func firstPosIndex(args []string, valueFlags map[string]bool) int {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return -1
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if f, attached := flagWord(a); valueFlags[f] && !attached {
				i++
			}
			continue
		}
		return i
	}
	return -1
}

var (
	nxValue = set("-c", "--configuration", "-p", "--projects", "--exclude", "--base", "--head", "--files",
		"--runner", "--output-style", "--outputStyle", "-t", "--targets", "--target")
	nxBuiltins = set("add", "connect", "daemon", "graph", "dep-graph", "exec", "format", "format:check",
		"format:write", "generate", "g", "import", "init", "list", "migrate", "release", "repair", "report",
		"reset", "show", "sync", "sync:check", "view-logs", "watch", "login", "logout", "mcp", "print-affected",
		"workspace-generator", "configure-ai-agents")
)

func nxOK(args []string) bool {
	if has(args, "--graph", "--tui", "--help", "--version", "--dry-run", "-d") || !runnerArgsOK(args) ||
		anyValue(optValues(args, "--output-style", "--outputStyle"), func(v string) bool { return v == "tui" }) {
		return false
	}
	sub, rest := posAt(args, nxValue)
	switch {
	case sub == "run":
		spec := firstPos(rest, nxValue)
		parts := strings.Split(spec, ":")
		if len(parts) < 2 || parts[0] == "" || !runnerTaskOK(parts[1]) {
			return false
		}
		return len(parts) < 3 || !riskyTask(parts[2])
	case sub == "run-many" || sub == "affected":
		targets := nxTargets(args)
		if len(targets) == 0 {
			return false
		}
		for _, t := range targets {
			if !runnerTaskOK(t) {
				return false
			}
		}
		return true
	case strings.HasPrefix(sub, "affected:"):
		return runnerTaskOK(strings.TrimPrefix(sub, "affected:"))
	case nxBuiltins[sub] || strings.Contains(sub, ":"):

		if parts := strings.Split(sub, ":"); len(parts) >= 2 && !nxBuiltins[sub] && parts[0] != "" {
			return runnerTaskOK(parts[1]) && (len(parts) < 3 || !riskyTask(parts[2]))
		}
		return false
	}
	return runnerTaskOK(sub)
}

func nxTargets(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		var vals []string
		switch {
		case a == "-t" || a == "--target" || a == "--targets":
		case strings.HasPrefix(a, "-t=") || strings.HasPrefix(a, "--target=") || strings.HasPrefix(a, "--targets="):
			_, v, _ := strings.Cut(a, "=")
			vals = append(vals, v)
		default:
			continue
		}
		for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			vals = append(vals, args[i])
		}
		for _, v := range vals {
			out = append(out, strings.Split(v, ",")...)
		}
	}
	return out
}

var (
	rakeValue = set("-f", "--rakefile", "-C", "--directory", "-I", "--libdir", "-r", "--require",
		"-R", "--rakelibdir", "--suppress-backtrace", "-j", "--jobs")
	rakeBool = set("-t", "--trace", "-q", "--quiet", "-s", "--silent", "-v", "--verbose", "-m", "--multitask",
		"-N", "--no-search", "--nosearch", "-g", "--system", "-G", "--no-system", "--nosystem",
		"--backtrace", "-X", "--no-deprecation-warnings", "--comments")
)

func rakeOK(args []string) bool {
	tasks := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		f, attached := flagWord(a)
		switch {
		case a == "-j" || a == "--jobs":
			if i+1 < len(args) && isDigits(args[i+1]) {
				i++
			}
		case rakeValue[f]:
			if !attached {
				i++
			}
		case rakeBool[a]:
		case strings.HasPrefix(a, "-"):

			return false
		case peelAssign(a):
		default:
			name := a
			if k := strings.IndexByte(name, '['); k > 0 {
				name = name[:k]
			}
			if !runnerTaskOK(name) {
				return false
			}
			tasks++
		}
	}
	return tasks > 0
}

var denoValue = set("-c", "--config", "--cwd", "--import-map", "-L", "--log-level", "--lock", "--filter",
	"-f", "--reporter", "--junit-path", "--coverage", "--seed", "--shuffle", "--parallel", "--ext")

func denoOK(args []string) bool {
	sub, rest := posAt(args, denoValue)
	if hasWatch(args) || !runnerArgsOK(args) {
		return false
	}
	switch sub {
	case "test", "check":
		return true
	case "lint":
		return !has(rest, "--rules")
	case "task":
		if has(rest, "--eval") {
			return false
		}
		name, after := posAt(rest, denoValue)
		return runnerTaskOK(name) && runnerArgsOK(after)
	}
	return false
}

var composerBuiltins = set("about", "archive", "audit", "browse", "home", "bump", "check-platform-reqs",
	"clear-cache", "clearcache", "cc", "config", "create-project", "depends", "why", "diagnose",
	"dump-autoload", "dumpautoload", "exec", "fund", "global", "help", "init", "licenses", "list",
	"outdated", "prohibits", "why-not", "reinstall", "remove", "rm", "require", "r", "search",
	"self-update", "selfupdate", "show", "info", "status", "suggests", "update", "u", "upgrade", "validate")

func composerOK(args []string) bool {
	sub, rest := posAt(args, set("-d", "--working-dir"))
	switch {
	case sub == "install" || sub == "i":
		return true
	case sub == "run-script" || sub == "run":
		if has(rest, "--list", "-l") {
			return false
		}
		name, after := posAt(rest, set("--timeout"))
		return runnerTaskOK(name) && runnerArgsOK(after)
	case composerBuiltins[sub]:
		return false
	}
	return runnerTaskOK(sub) && runnerArgsOK(rest)
}
