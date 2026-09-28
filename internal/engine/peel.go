package engine

import (
	"path/filepath"
	"strings"
)

const MaxPeel = 4

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
	case "timeout", "gtimeout":
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

func peelCommand(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil
	}
	return args
}

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
			i++
		case strings.HasPrefix(a, "-"):
			return nil
		default:
			return peelCommand(args[i:])
		}
	}
	return nil
}

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
		return argv
	}
	out := make([]string, len(argv))
	copy(out, argv)
	out[0] = argv[0][:at]
	return out
}
