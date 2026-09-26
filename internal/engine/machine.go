package engine

import "strings"

// MachineReadable reports whether the user asked for output meant for a
// program (porcelain, JSON, NUL-separated, custom templates). Such output
// must reach the caller byte-for-byte, so lx passes it through untouched.
//
// Format flags are judged by their value: `--format=json` is for programs,
// `ls --format=long` and `ruff --output-format concise` are for people.
func MachineReadable(c *Context) bool {
	name := c.Name()
	args := c.Args()
	sub := c.Sub()
	for i, a := range args {
		if a == "--" {
			break
		}
		flag, val, hasVal := strings.Cut(a, "=")
		next := func() string {
			if hasVal {
				return val
			}
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		switch {
		case strings.HasPrefix(a, "--porcelain"), strings.HasPrefix(a, "--line-porcelain"),
			a == "--incremental" && name == "git",
			a == "-z", a == "--null", a == "-0", a == "--print0", a == "-print0",
			isGrep(name) && shortFlag(a, "Z"),
			strings.HasPrefix(a, "--json"),
			strings.HasPrefix(a, "--pretty=format:"), strings.HasPrefix(a, "--pretty=tformat:"),
			strings.HasPrefix(a, "--template"), strings.HasPrefix(a, "--jq"),
			strings.HasPrefix(a, "--message-format"):
			return true
		case a == "-json":
			// go test -json is rendered by the go-test-json filter; every
			// other go subcommand's JSON goes to programs untouched.
			if !(name == "go" && sub == "test") {
				return true
			}
		case a == "-p" && name == "git" && sub == "blame":
			return true
		case flag == "--format", flag == "--output-format", flag == "--reporter", flag == "--formatter", flag == "-f" && isFormatTool(name):
			v := next()
			// Templates in tools whose --format is a template language.
			if flag == "--format" && (name == "git" || name == "docker" || name == "podman" || name == "gh" || name == "kubectl") {
				return true
			}
			if isMachineFormat(v) {
				return true
			}
		case a == "-o" || a == "--output" || flag == "--output" || strings.HasPrefix(a, "-o="):
			v := next()
			if strings.HasPrefix(a, "-o=") {
				v = a[3:]
			}
			if isMachineFormat(v) {
				return true
			}
		case strings.HasPrefix(a, "-o") && len(a) > 2 && (name == "kubectl" || name == "oc"):
			if isMachineFormat(a[2:]) {
				return true
			}
		}
	}
	if name == "git" {
		switch sub {
		case "rev-parse", "ls-files", "ls-tree", "cat-file", "for-each-ref", "config",
			"hash-object", "rev-list", "name-rev", "symbolic-ref", "show-ref",
			"format-patch", "var", "check-ignore", "merge-base", "mktree", "update-index":
			return true
		}
		if sub == "diff" && c.HasFlag("--name-only", "--name-status", "--raw", "--numstat", "--shortstat") {
			return true
		}
		if sub == "status" {
			// Short format (-s, --short, and combined forms like -sb) is
			// git's own compact format; scripts parse it.
			for _, a := range args {
				if a == "--short" || shortFlag(a, "s") {
					return true
				}
			}
		}
	}
	if isGrep(name) || name == "git" && sub == "grep" {
		if c.HasFlag("--files-with-matches", "--files-without-match", "--count", "--quiet") {
			return true
		}
		for _, a := range args {
			if a == "--" {
				break
			}
			if shortFlag(a, "lLcq") {
				return true
			}
		}
	}
	return false
}

// shortFlag reports whether a is a (possibly combined) short-flag word such
// as "-rl" containing any of letters.
func shortFlag(a, letters string) bool {
	return len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters)
}

func isGrep(name string) bool {
	return name == "grep" || name == "rg" || name == "egrep" || name == "fgrep" || name == "ag"
}

// isFormatTool: tools whose -f selects an output format (eslint -f json).
func isFormatTool(name string) bool {
	return name == "eslint" || name == "stylelint" || name == "golangci-lint" || name == "rubocop"
}

func isMachineFormat(f string) bool {
	f = strings.ToLower(strings.Trim(f, `"'`))
	for _, p := range []string{"json", "yaml", "yml", "jsonpath", "go-template", "template", "custom-columns",
		"tsv", "csv", "xml", "junit", "sarif", "checkstyle", "ndjson", "jsonl", "tap", "name", "wide-json", "github", "gitlab", "pylint", "azure", "teamcity"} {
		if f == p || strings.HasPrefix(f, p+"=") || strings.HasPrefix(f, p+"-") || strings.HasPrefix(f, p+"(") || (p == "json" && strings.HasPrefix(f, "json")) {
			return true
		}
	}
	// A format string with placeholders is a template.
	return strings.Contains(f, "%") || strings.Contains(f, "{{")
}
