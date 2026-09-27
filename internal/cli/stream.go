package cli

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// interactive programs take over the terminal; lx must not sit between them
// and the user.
var interactive = map[string]bool{
	"vim": true, "vi": true, "nvim": true, "nano": true, "emacs": true, "less": true,
	"more": true, "man": true, "top": true, "htop": true, "btop": true, "watch": true,
	"ssh": true, "tmux": true, "screen": true, "fzf": true, "lazygit": true, "tig": true,
	"psql": true, "mysql": true, "sqlite3": true, "redis-cli": true, "mongosh": true,
}

// repls are interactive when started without a script/command.
var repls = map[string]bool{"python": true, "python3": true, "node": true, "irb": true, "bash": true, "sh": true, "zsh": true, "fish": true,
	"ipython": true, "bpython": true, "ptpython": true, "pypy": true, "pypy3": true, "deno": true, "pry": true, "iex": true, "ghci": true, "lua": true}

// isREPL reports an interpreter that opens a prompt when run bare,
// including versioned Pythons (python3.12, python3.13t).
func isREPL(name string) bool {
	if repls[name] {
		return true
	}
	v, ok := strings.CutPrefix(name, "python")
	v = strings.TrimSuffix(v, "t")
	return ok && v != "" && strings.Trim(v, "0123456789.") == "" && v[0] != '.'
}

// ShouldStream reports whether argv must run with lx out of the way:
// interactive tools, REPLs, followers (tail -f, logs -f), watchers and dev
// servers. Buffering those would hang the agent until they exit.
//
// Wrappers are looked through with engine.Peel (up to engine.MaxPeel
// layers): `uv run python` opens a REPL, `env FOO=1 node` too.
func ShouldStream(argv []string) bool {
	if len(argv) == 0 {
		return true
	}
	for range engine.MaxPeel {
		inner, _ := engine.Peel(argv)
		if inner == nil {
			break
		}
		// A wrapper is judged on its own words only: flags such as -F or
		// --follow mean "follow" to some tools, so the command inside is
		// judged by its own name (uv run git log --follow does not stream).
		if streams(argv[:len(argv)-len(inner)]) {
			return true
		}
		argv = inner
	}
	return streams(argv)
}

// streams applies the checks to one command, wrappers not peeled.
func streams(argv []string) bool {
	name := filepath.Base(argv[0])
	args := argv[1:]
	if interactive[name] {
		return true
	}
	if isREPL(name) && (len(args) == 0 || replFlag(args)) {
		return true
	}
	logs := false // a "logs" subcommand came before: -f now means follow
	for i, a := range args {
		if a == "--" {
			break
		}
		if followCluster(name, a, logs) {
			return true
		}
		switch a {
		case "--watch", "--watchAll", "--serve", "--interactive", "-it", "-ti":
			return true
		case "logs":
			logs = true
		case "--follow":
			// git log --follow tracks renames; everywhere else it follows output.
			if name != "git" {
				return true
			}
		case "-F":
			// tail -F follows; grep/rg/git -F are fixed strings, ls -F classifies.
			switch name {
			case "grep", "egrep", "fgrep", "rg", "ag", "git", "ls", "gls":
			default:
				return true
			}
		case "-f":
			switch name {
			case "tail", "journalctl", "stern":
				return true
			case "docker", "podman", "kubectl", "oc", "docker-compose":
				// docker build -f FILE, docker compose -f FILE, kubectl apply
				// -f FILE name files; after logs, -f follows.
				if logs {
					return true
				}
			}
		case "-w":
			// kubectl get -w watches; the others rebuild on change.
			switch name {
			case "tsc", "jest", "vitest", "mocha", "webpack", "esbuild", "rollup", "kubectl", "oc":
				return true
			}
		case "-i":
			if name == "docker" || name == "podman" || name == "kubectl" || name == "oc" {
				return true
			}
		}
		if strings.HasPrefix(a, "--watch=") || strings.HasPrefix(a, "--follow=") {
			return true
		}
		if i == 0 && (name == "vitest" && (a == "watch" || a == "dev") || name == "vite" && (a == "dev" || a == "serve" || a == "preview")) {
			return true
		}
	}
	switch name {
	case "vite", "nodemon", "live-server", "http-server", "serve":
		return len(args) == 0 || !slices.Contains(args, "build")
	case "npm", "pnpm", "yarn", "bun":
		script := firstPositional(args)
		if script == "run" || script == "run-script" {
			script = firstPositional(args[slices.Index(args, script)+1:])
		}
		switch script {
		case "dev", "start", "serve", "watch", "preview", "develop":
			return true
		}
	case "next", "nuxt", "astro", "remix", "expo":
		return firstPositional(args) == "dev" || firstPositional(args) == "start"
	case "docker", "podman":
		sub := dockerSub(args)
		switch sub {
		case "events":
			return !slices.ContainsFunc(args, func(a string) bool { return a == "--until" || strings.HasPrefix(a, "--until=") })
		case "stats":
			return !slices.Contains(args, "--no-stream")
		case "compose":
			return composeStreams(args)
		}
		return sub == "attach" || sub == "exec" && !slices.Contains(args, "-d")
	case "docker-compose":
		return composeStreams(args)
	case "kubectl", "oc":
		sub := firstPositional(args)
		return sub == "exec" || sub == "port-forward" || sub == "attach" || sub == "edit" || sub == "proxy"
	case "stern":
		// stern follows unless told not to.
		return !slices.Contains(args, "--no-follow") && !slices.Contains(args, "--no-follow=true")
	case "go", "cargo":
		return false
	}
	return false
}

// replFlag: -i before the script or code (python -i, node -i, bash -i)
// opens the interactive prompt.
func replFlag(args []string) bool {
	for _, a := range args {
		if a == "-i" {
			return true
		}
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" || a == "-c" || a == "-m" || a == "-e" {
			return false // the script, stdin, or the code to run: options end
		}
	}
	return false
}

// followCluster reports a short-option cluster that follows: tail -fn 50,
// journalctl -fu nginx, docker logs -ft, kubectl logs -fc app. In a
// cluster, f counts only before an option that takes a value (for
// journalctl -uf, "f" is the unit name). A lone -f is judged by streams.
func followCluster(name, a string, afterLogs bool) bool {
	if len(a) < 3 || a[0] != '-' || a[1] == '-' {
		return false
	}
	var value string
	switch name {
	case "tail":
		// Like the hook's tailFollows: any f or F (tail -5f, -Fn 20).
		return strings.ContainsAny(a[1:], "fF")
	case "journalctl":
		value = "unptoSUDMgFbic"
	case "docker", "podman", "docker-compose":
		value = "n"
	case "kubectl", "oc":
		value = "cl"
	default:
		return false
	}
	if name != "journalctl" && !afterLogs {
		return false
	}
	for i := 1; i < len(a); i++ {
		if a[i] == 'f' {
			return true
		}
		if strings.IndexByte(value, a[i]) >= 0 {
			return false
		}
	}
	return false
}

// dockerGlobalValue are docker's options before the subcommand that take
// a value (docker --context prod stats).
var dockerGlobalValue = map[string]bool{"-c": true, "--context": true, "-H": true, "--host": true,
	"--config": true, "-l": true, "--log-level": true, "--tlscacert": true, "--tlscert": true, "--tlskey": true}

// dockerSub is docker's subcommand, skipping global options and their values.
func dockerSub(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case dockerGlobalValue[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

// composeStreams: compose up (not detached) keeps running; run, exec,
// attach and watch run a container's process or take over the terminal.
func composeStreams(args []string) bool {
	if slices.Contains(args, "up") && !slices.Contains(args, "-d") {
		return true
	}
	return slices.ContainsFunc(args, func(a string) bool { return a == "run" || a == "exec" || a == "attach" || a == "watch" })
}

func firstPositional(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}
