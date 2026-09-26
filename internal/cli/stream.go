package cli

import (
	"path/filepath"
	"slices"
	"strings"
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
var repls = map[string]bool{"python": true, "python3": true, "node": true, "irb": true, "bash": true, "sh": true, "zsh": true, "fish": true}

// ShouldStream reports whether argv must run with lx out of the way:
// interactive tools, REPLs, followers (tail -f, logs -f), watchers and dev
// servers. Buffering those would hang the agent until they exit.
func ShouldStream(argv []string) bool {
	if len(argv) == 0 {
		return true
	}
	name := filepath.Base(argv[0])
	args := argv[1:]
	if interactive[name] {
		return true
	}
	if repls[name] && len(args) == 0 {
		return true
	}
	for i, a := range args {
		if a == "--" {
			break
		}
		switch a {
		case "--watch", "--watchAll", "--follow", "-F", "--serve", "--interactive", "-it", "-ti":
			return true
		case "-f":
			if name == "tail" || name == "journalctl" || name == "docker" || name == "kubectl" || name == "podman" || name == "stern" {
				return true
			}
		case "-w":
			if name == "tsc" || name == "jest" || name == "vitest" || name == "mocha" || name == "webpack" || name == "esbuild" || name == "rollup" {
				return true
			}
		case "-i":
			if name == "docker" || name == "podman" || name == "kubectl" {
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
		sub := firstPositional(args)
		return sub == "attach" || sub == "exec" && !slices.Contains(args, "-d") || sub == "compose" && slices.Contains(args, "up") && !slices.Contains(args, "-d")
	case "kubectl":
		sub := firstPositional(args)
		return sub == "exec" || sub == "port-forward" || sub == "attach" || sub == "edit" || sub == "proxy"
	case "go", "cargo":
		return false
	}
	return false
}

func firstPositional(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}
