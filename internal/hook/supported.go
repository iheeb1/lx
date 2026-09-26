package hook

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Supported reports whether lx should wrap argv (argv[0] may be a path).
//
// The table is conservative on purpose. A command is excluded when its
// output must reach the caller byte-for-byte (porcelain, JSON, NUL-separated,
// custom formats, file contents), when it streams or waits for a human
// (watch, follow, interactive, dev servers), or when it runs arbitrary
// program output (go run, python script.py). Flags are judged per tool:
// -f means "follow" to docker logs but "file" to docker compose and make.
func Supported(argv []string) bool {
	if len(argv) == 0 {
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
	}
	return false
}

var (
	rePython = regexp.MustCompile(`^python(\d+(\.\d+)?)?$`)
	rePip    = regexp.MustCompile(`^pip(\d+(\.\d+)?)?$`)
)

type rule func(args []string) bool

var tools map[string]rule

func init() {
	always := func([]string) bool { return true }
	tools = map[string]rule{
		"git":           gitOK,
		"go":            goOK,
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
		"gradle":        gradleOK,
		"gradlew":       gradleOK,
		"mvn":           mvnOK,
		"mvnw":          mvnOK,
		"ls":            always,
		"find":          findOK,
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
		"composer":   func(a []string) bool { return oneOf(firstPos(a, nil), "install", "i") },
		"bundle":     bundleOK,
		"rspec":      rspecOK,
		"phpunit":    always,
	}
}

// ---- argument helpers ----

func oneOf(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

// firstPos returns the first positional argument, skipping flags and the
// values of flags listed in valueFlags. Everything after "--" is positional.
func firstPos(args []string, valueFlags map[string]bool) string {
	p, _ := posAt(args, valueFlags)
	return p
}

// posAt is firstPos that also returns the arguments after the positional.
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

// has reports whether any argument equals a name, or is "--name=…" for a
// long name. It scans past "--": `npm test -- --watch` still watches.
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

// shortFlag reports whether a single-dash cluster ("-rnl", "-sSLo") sets any
// of letters. Parsing stops at the first letter in valueLetters because the
// rest of the cluster is that flag's value ("-A3", "-XPOST", "-ffile").
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

// optValue returns the values given to an option in any of the forms
// "--opt v", "--opt=v", "-o v", "-ov", "-o=v".
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

// ---- git ----

var gitSubs = set("status", "log", "diff", "show", "push", "pull", "fetch", "clone",
	"branch", "merge", "rebase", "commit", "stash", "blame", "reflog", "cherry-pick",
	"tag", "remote")

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
			return false // --version, --help, --exec-path, …
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
		// git show REV:path prints a file's exact bytes.
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
		return !has(rest, "-p", "--patch")
	case "blame":
		return !has(rest, "-p", "--incremental")
	}
	return true
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
			// --pretty=%h is a custom format too; named formats are for humans.
			if strings.Contains(a, "%") {
				return true
			}
		}
	}
	return false
}

// ---- go / cargo ----

var goModOK = set("tidy", "download", "verify", "why", "vendor", "init")

func goOK(args []string) bool {
	sub, rest := posAt(args, set("-C"))
	if hasPrefix(args, "-json", "--json") {
		return false
	}
	switch sub {
	case "test", "build", "vet", "get", "install":
		return true
	case "mod":
		return goModOK[firstPos(rest, nil)]
	}
	return false
}

func cargoOK(args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "+") {
		args = args[1:] // +nightly
	}
	if hasPrefix(args, "--message-format", "--format") || hasWatch(args) {
		return false
	}
	sub := firstPos(args, set("--color", "--config", "-Z", "-C", "--manifest-path"))
	return oneOf(sub, "build", "b", "test", "t", "check", "c", "clippy", "install", "update", "fetch")
}

// ---- JavaScript package managers and runners ----

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
		return name == "yarn" // bare `yarn [--flags]` = yarn install
	case pkgBuiltins[sub]:
		return true
	case sub == "run" || sub == "run-script":
		return scriptOK(firstPos(rest, nil))
	case sub == "exec" || sub == "x" || sub == "dlx" && name != "npm":
		return execToolOK(rest)
	case sub == "workspace" && name == "yarn":
		_, inner := posAt(rest, nil) // yarn workspace <name> <command…>
		return pkgOK(name, inner)
	case name != "npm":
		// yarn/pnpm/bun run package scripts by bare name (yarn build).
		return scriptOK(sub)
	}
	return false
}

var scriptSplit = regexp.MustCompile(`[^a-z0-9]+`)

// scriptOK: package scripts named for one-shot checks (test, build, lint,
// typecheck, check, compile — "test:unit", "type-check", "build:prod"), never
// ones named for long-running processes (dev, start, serve, watch, preview).
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

// execToolOK handles npx/bunx/pnpm exec/pnpm dlx/yarn dlx/npm exec TOOL ….
func execToolOK(args []string) bool {
	if has(args, "-c", "--call") {
		return false
	}
	tool, rest := posAt(args, set("-p", "--package"))
	if tool == "" {
		return false
	}
	if at := strings.LastIndexByte(tool, '@'); at > 0 {
		tool = tool[:at] // tsc@5.4 → tsc
	}
	tool = filepath.Base(tool)
	switch tool {
	case "jest", "vitest", "mocha", "tsc", "eslint", "prettier", "playwright", "next", "vite":
		return Supported(append([]string{tool}, rest...))
	}
	return false
}

func jestOK(a []string) bool {
	return !hasWatch(a) && !has(a, "-w", "--json", "--listTests", "--showConfig")
}

func vitestOK(a []string) bool {
	if oneOf(firstPos(a, nil), "watch", "dev") || hasWatch(a) || has(a, "-w", "--ui", "--standalone") {
		return false
	}
	return !anyValue(optValues(a, "--reporter", ""), isJSONish)
}

func mochaOK(a []string) bool {
	return !hasWatch(a) && !has(a, "-w") && !anyValue(optValues(a, "--reporter", "-R"), isJSONish)
}

func tscOK(a []string) bool {
	return !hasWatch(a) && !has(a, "-w", "--showConfig", "--listFilesOnly")
}

func eslintOK(a []string) bool {
	if has(a, "--print-config", "--init", "--inspect-config", "--mcp") {
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

// ---- Python ----

func pytestOK(a []string) bool {
	return !has(a, "--pdb", "--trace", "--pdbcls", "-f", "--looponfail") &&
		!hasPrefix(a, "--pdbcls=")
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
			return false // -c code, a script, or stdin: arbitrary output
		case strings.ContainsAny(a[1:], "cm"):
			return false // clustered -uc / -um forms: bail
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
		return !has(rest, "--report")
	case "list":
		return !has(rest, "--format", "--json")
	}
	return false
}

func mypyOK(a []string) bool {
	if len(optValues(a, "--output", "-O")) > 0 {
		return false
	}
	return !has(a, "--install-types") || has(a, "--non-interactive")
}

func ruffOK(a []string) bool {
	sub, rest := posAt(a, set("--config"))
	if sub != "check" || hasWatch(rest) || has(rest, "-w") {
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

// ---- build tools ----

var makeValueFlags = set("-C", "-f", "--file", "--makefile", "--directory", "-I", "--include-dir",
	"-o", "--old-file", "-W", "--what-if", "--new-file", "--assume-new", "-l", "--load-average")

// longRunning are target/task name parts that usually start servers,
// watchers or shells. Matching is by prefix on dash/colon-separated parts.
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
	if has(args, "-p", "--print-data-base") {
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

var gradleValueFlags = set("-p", "--project-dir", "-b", "--build-file", "-c", "--settings-file",
	"-x", "--exclude-task", "-I", "--init-script", "-g", "--gradle-user-home", "--console", "-D", "-P")

func gradleOK(args []string) bool {
	if has(args, "--continuous", "-t", "--scan", "--debug-jvm") {
		return false
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

// ---- file listing and search ----

func findOK(a []string) bool {
	return !has(a, "-exec", "-execdir", "-ok", "-okdir", "-delete", "-print0",
		"-fprint", "-fprint0", "-fprintf", "-fls")
}

func grepOK(a []string) bool {
	if has(a, "--files-with-matches", "--files-without-match", "--count", "--quiet", "--silent",
		"--null", "--null-data", "--json") {
		return false
	}
	return !shortFlag(a, "lLcqzZ", "efmABCdD")
}

func rgOK(a []string) bool {
	if has(a, "--files-with-matches", "--files-without-match", "--count", "--count-matches",
		"--quiet", "--json", "--null", "--null-data", "--search-zip") {
		return false
	}
	return !shortFlag(a, "lcq0z", "ABCefgjmMrtTEd")
}

func treeOK(a []string) bool {
	return !has(a, "-J", "-X") && !shortFlag(a, "JX", "LPIoHT")
}

func duOK(a []string) bool {
	return !has(a, "--null") && !shortFlag(a, "0", "BdtX")
}

// ---- containers and clusters ----

var dockerGlobalValue = set("--context", "-c", "-H", "--host", "--config", "-l", "--log-level",
	"--tlscacert", "--tlscert", "--tlskey")

func dockerOK(args []string) bool {
	if has(args, "-i", "-it", "-ti", "--interactive", "--tty") {
		return false
	}
	sub, rest := posAt(args, dockerGlobalValue)
	switch sub {
	case "ps", "images":
		return !hasPrefix(rest, "--format")
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
		return oneOf(s2, "ls", "list", "pull", "build") && !hasPrefix(r2, "--format")
	case "container":
		s2, r2 := posAt(rest, nil)
		switch s2 {
		case "ls", "list", "ps":
			return !hasPrefix(r2, "--format")
		case "logs":
			return dockerLogsOK(r2)
		}
	}
	return false
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
		return !hasPrefix(rest, "--format")
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
	if has(a, "-f", "--follow") || shortFlag(a, "f", "unptoSUDMgFbic") {
		return false
	}
	return !anyValue(optValues(a, "--output", "-o"), func(v string) bool {
		return strings.HasPrefix(v, "json") || v == "export"
	})
}

// ---- network and package managers ----

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
