package engine

import (
	"path/filepath"
	"strings"
)

// MaxPeel is how many wrapper layers Resolve and MachineReadableAny look
// through (`timeout 60 env FOO=1 uv run pytest` is three).
const MaxPeel = 4

// Peel returns the command a transparent wrapper runs, one layer per call:
//
//	FOO=1 BAR=2 cmd…
//	env [-u NAME | -uNAME | --unset[=]NAME]… [NAME=value]… cmd…
//	timeout (or gtimeout) [-s SIG] [-k DUR] [--preserve-status] [--foreground] [-v] DURATION cmd…
//	nice [-n N | -nN | -N | --adjustment[=]N] cmd…
//	nohup cmd…
//	time [-p] cmd…
//	command cmd…
//	uv run | uv tool run | uvx | poetry run | pdm run | pipenv run |
//	hatch run | rye run | pipx run  [runner options] cmd…
//
// via names the wrapper ("env", "timeout", "uv run", …). Peel returns nil
// when argv is none of these, when the wrapper has an option Peel does not
// know (env -i, timeout --bogus, command -v: the wrapper then does
// something other than run the command) or when no command is left.
//
// Peel is pure: it only reads argv and never changes what runs. Filters
// and the hook use the inner argv to recognize the tool; lx always
// executes the full command line.
func Peel(argv []string) (inner []string, via string) {
	inner, via = peelLayer(argv)
	if len(inner) == 0 {
		return nil, ""
	}
	return inner, via
}

func peelLayer(argv []string) (inner []string, via string) {
	if len(argv) == 0 {
		return nil, ""
	}
	if peelIsAssign(argv[0]) {
		i := 1
		for i < len(argv) && peelIsAssign(argv[i]) {
			i++
		}
		return argv[i:], "VAR=value"
	}
	name := strings.TrimSuffix(filepath.Base(argv[0]), ".exe")
	args := argv[1:]
	switch name {
	case "env":
		return peelEnv(args), "env"
	case "timeout", "gtimeout": // gtimeout: GNU timeout from Homebrew coreutils on macOS
		return peelTimeout(args), "timeout"
	case "nice":
		return peelNice(args), "nice"
	case "nohup":
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		return peelCommand(args), "nohup"
	case "time":
		for len(args) > 0 && (args[0] == "-p" || args[0] == "--") {
			stop := args[0] == "--"
			args = args[1:]
			if stop {
				break
			}
		}
		return peelCommand(args), "time"
	case "command":
		// command -v / -V print what a name resolves to; -p changes PATH.
		return peelCommand(args), "command"
	case "uv":
		switch {
		case len(args) > 0 && args[0] == "run":
			return peelRunner(args[1:], false), "uv run"
		case len(args) > 1 && args[0] == "tool" && args[1] == "run":
			return peelRunner(args[2:], true), "uv tool run"
		}
	case "uvx":
		return peelRunner(args, true), "uvx"
	case "pipx":
		if len(args) > 0 && args[0] == "run" {
			return peelRunner(args[1:], true), "pipx run"
		}
	case "poetry", "pdm", "pipenv", "hatch", "rye":
		if len(args) > 0 && args[0] == "run" {
			return peelRunner(args[1:], false), name + " run"
		}
	}
	return nil, ""
}

// Resolve returns the filter for c and the context it applies to. When no
// filter matches c itself, it peels up to MaxPeel wrapper layers and
// returns the first filter matching an inner command, with a copy of c
// whose Argv is that command. It returns nil, c when nothing matches.
//
// Filters that already understand a wrapper (jest under `env`, pytest
// under `uv run`) keep matching the full command line: Find(c) runs first.
func Resolve(c *Context) (Filter, *Context) {
	if f := Find(c); f != nil {
		return f, c
	}
	argv := c.Argv
	for range MaxPeel {
		inner, _ := Peel(argv)
		if inner == nil {
			break
		}
		ic := *c
		ic.Argv = inner
		if f := Find(&ic); f != nil {
			return f, &ic
		}
		argv = inner
	}
	return nil, c
}

// MachineReadableAny is MachineReadable for c or any command Peel finds
// inside it (up to MaxPeel layers): `uv run git status -s` prints git's
// short format just as `git status -s` does.
func MachineReadableAny(c *Context) bool {
	if MachineReadable(c) {
		return true
	}
	argv := c.Argv
	for range MaxPeel {
		inner, _ := Peel(argv)
		if inner == nil {
			return false
		}
		if MachineReadable(&Context{Argv: inner}) {
			return true
		}
		argv = inner
	}
	return false
}

// peelCommand returns args when it starts with a command word, nil when it
// starts with an option (which the wrapper would have parsed as its own).
func peelCommand(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil
	}
	return args
}

// peelIsAssign reports a shell NAME=value word.
func peelIsAssign(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := s[i]
		if c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// peelEnv: env's options come first (getopt stops at the first operand),
// then NAME=value operands (env treats any word with "=" as one), then the
// command. Only -u/--unset is transparent; -i, -, -S, -C, -P, -0, -v and
// anything else return nil.
func peelEnv(args []string) []string {
	i := 0
opts:
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			i++
			break opts
		case a == "-u" || a == "--unset":
			if i+1 >= len(args) {
				return nil
			}
			i += 2
		case strings.HasPrefix(a, "--unset=") && len(a) > len("--unset="), strings.HasPrefix(a, "-u") && len(a) > 2:
			i++
		case strings.HasPrefix(a, "-"):
			return nil
		default:
			break opts
		}
	}
	for i < len(args) && strings.Contains(args[i], "=") {
		i++
	}
	return peelCommand(args[i:])
}

// peelTimeout: options, then a DURATION, then the command.
func peelTimeout(args []string) []string {
	i := 0
opts:
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			i++
			break opts
		case a == "-s" || a == "--signal":
			if i+1 >= len(args) {
				return nil
			}
			i += 2
		case a == "-k" || a == "--kill-after":
			if i+1 >= len(args) || !peelIsDuration(args[i+1]) {
				return nil
			}
			i += 2
		case strings.HasPrefix(a, "--signal=") && len(a) > len("--signal="), strings.HasPrefix(a, "-s") && len(a) > 2 && a[2] != '-':
			i++
		case strings.HasPrefix(a, "--kill-after="):
			if !peelIsDuration(a[len("--kill-after="):]) {
				return nil
			}
			i++
		case strings.HasPrefix(a, "-k") && len(a) > 2:
			if !peelIsDuration(a[2:]) {
				return nil
			}
			i++
		case a == "--preserve-status", a == "--foreground", a == "-v", a == "--verbose":
			i++
		case strings.HasPrefix(a, "-"):
			return nil
		default:
			break opts
		}
	}
	if i >= len(args) || !peelIsDuration(args[i]) {
		return nil
	}
	return peelCommand(args[i+1:])
}

// peelIsDuration accepts timeout's DURATION: a non-negative decimal number with
// an optional s, m, h or d suffix ("60", "1.5", "5m").
func peelIsDuration(s string) bool {
	if n := len(s); n > 0 && strings.IndexByte("smhd", s[n-1]) >= 0 {
		s = s[:n-1]
	}
	digits, dot := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return digits > 0
}

// peelNice: -n N, -nN, -N, --N (a negative N), --adjustment N,
// --adjustment=N, then the command.
func peelNice(args []string) []string {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			return peelCommand(args[i+1:])
		case a == "-n" || a == "--adjustment":
			if i+1 >= len(args) || !peelIsInt(args[i+1]) {
				return nil
			}
			i += 2
		case strings.HasPrefix(a, "--adjustment="):
			if !peelIsInt(a[len("--adjustment="):]) {
				return nil
			}
			i++
		case strings.HasPrefix(a, "-n") && len(a) > 2:
			if !peelIsInt(a[2:]) {
				return nil
			}
			i++
		case len(a) > 1 && a[0] == '-' && peelIsInt(a[1:]):
			i++ // -10, --5
		case strings.HasPrefix(a, "-"):
			return nil
		default:
			return peelCommand(args[i:])
		}
	}
	return nil
}

// peelIsInt accepts an optionally signed decimal integer.
func peelIsInt(s string) bool {
	if s != "" && (s[0] == '-' || s[0] == '+') {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// peelRunnerValueFlags are options of the project runners (uv run, uv tool run,
// uvx, poetry/pdm/pipenv/hatch/rye run, pipx run) that take a separate
// value. It starts from internal/filters/python's copy and adds the value
// options of uv, uvx and pipx that decide which tool runs (--from, --spec)
// or that could otherwise be mistaken for the command. Any other "-" word is
// a boolean flag; "--opt=value" is one word.
var peelRunnerValueFlags = map[string]bool{
	"--with": true, "-w": true, "--with-editable": true, "--with-requirements": true, "--python": true, "-p": true,
	"--package": true, "--extra": true, "--group": true, "--only-group": true, "--no-group": true,
	"--env-file": true, "--directory": true, "--project": true, "--index": true, "--default-index": true,
	"--index-url": true, "-i": true, "--extra-index-url": true, "--find-links": true, "-f": true,
	"--config-file": true, "--cache-dir": true, "--color": true, "-C": true, "-P": true, "--venv": true,
	"--from": true, "--spec": true, "--pip-args": true,
	"--config-setting": true, "--exclude-newer": true, "--exclude-newer-package": true, "--prerelease": true,
	"--resolution": true, "--index-strategy": true, "--keyring-provider": true, "--link-mode": true,
	"--refresh-package": true, "--reinstall-package": true, "--upgrade-package": true,
	"--no-binary-package": true, "--no-build-package": true, "--no-build-isolation-package": true,
	"--python-preference": true, "--allow-insecure-host": true, "--python-platform": true,
	"--no-extra": true, "--preview-features": true,
}

// peelRunner skips a project runner's options and returns the command.
// A lone "-" (uv run - reads a script from stdin) is not a command. For
// tool runners (uvx, uv tool run, pipx run) the command may carry a
// version, "ruff@0.6": the inner argv names the tool without it.
func peelRunner(args []string, tool bool) []string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return peelVersionless(peelCommand(args[i+1:]), tool)
		case a == "-":
			return nil
		case peelRunnerValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return peelVersionless(args[i:], tool)
		}
	}
	return nil
}

func peelVersionless(argv []string, tool bool) []string {
	if !tool || len(argv) == 0 {
		return argv
	}
	at := strings.IndexByte(argv[0], '@')
	if at <= 0 || strings.ContainsAny(argv[0][:at], "/:") {
		return argv // a URL or path, not name@version
	}
	out := make([]string, len(argv))
	copy(out, argv)
	out[0] = argv[0][:at]
	return out
}
