package search

import (
	"path"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// opts is what the command line says about the shape of the output.
type opts struct {
	tool     string // "grep", "rg" or "git-grep"
	patterns []string
	fixed    bool
	icase    bool
	smart    bool // rg -S: case-insensitive unless the pattern has capitals
	word     bool
	flavor   byte // 'b' BRE, 'e' ERE / Rust regex, 'p' PCRE
	numbered bool
	column   bool // rg --column / --vimgrep: path:line:col:text
	// withFile: 1 path prefix printed, -1 not printed, 0 unknown (a single
	// operand that is an unexpanded glob or might be a file or directory).
	withFile  int
	recursive bool
	context   bool
	invert    bool     // -v: the lines printed are the ones not matching
	only      bool     // -o: one output line per match, so line numbers repeat
	operands  []string // search paths (grep/rg) or pathspecs (git grep)
	groupSep  string
	files     bool // rg --files
	bail      bool // a flag that changes the output format
}

// tool returns which search tool argv runs, and its arguments.
func tool(e *engine.Context) (string, []string) {
	switch e.Name() {
	case "grep", "egrep", "fgrep", "ggrep":
		return "grep", e.Args()
	case "rg":
		return "rg", e.Args()
	case "git":
		args := e.Args()
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace" || a == "--config-env":
				i++
			case strings.HasPrefix(a, "-"):
			case a == "grep":
				return "git-grep", args[i+1:]
			default:
				return "", nil
			}
		}
	}
	return "", nil
}

// Short flags taking a value (the rest of the cluster, or the next word).
var shortValue = map[string]string{
	"grep":     "ABCdDefm",
	"rg":       "ABCeEfgjmMrtTd",
	"git-grep": "ABCefm",
}

// Short flags that change the output format beyond what is parsed here.
var shortBail = map[string]string{
	"grep":     "clLqZzbTuV",
	"rg":       "clq0bphV",
	"git-grep": "clLqzOp",
}

// Long flags taking a value, which may be the next word.
var longValue = map[string]map[string]bool{
	"grep": set("--regexp", "--file", "--max-count", "--after-context", "--before-context", "--context",
		"--include", "--exclude", "--exclude-from", "--exclude-dir", "--directories", "--devices",
		"--label", "--binary-files", "--group-separator"),
	"rg": set("--regexp", "--file", "--glob", "--iglob", "--type", "--type-not", "--type-add", "--type-clear",
		"--max-count", "--max-columns", "--max-depth", "--max-filesize", "--after-context",
		"--before-context", "--context", "--context-separator", "--replace", "--threads", "--encoding",
		"--engine", "--sort", "--sortr", "--path-separator", "--pre", "--pre-glob", "--ignore-file",
		"--colors", "--color", "--dfa-size-limit", "--regex-size-limit", "--hostname-bin",
		"--hyperlink-format", "--field-match-separator", "--field-context-separator"),
	"git-grep": set("--regexp", "--file", "--max-count", "--after-context", "--before-context", "--context",
		"--max-depth", "--threads", "--color"),
}

// Long flags that change the output format beyond what is parsed here.
var longBail = map[string]map[string]bool{
	"grep": set("--count", "--files-with-matches", "--files-without-match", "--quiet", "--silent",
		"--null", "--null-data", "--byte-offset", "--initial-tab", "--version", "--help", "--unix-byte-offsets"),
	"rg": set("--count", "--count-matches", "--files-with-matches", "--files-without-match", "--quiet",
		"--json", "--null", "--null-data", "--byte-offset", "--heading", "--pretty", "--stats",
		"--type-list", "--help", "--version", "--debug", "--trace", "--generate", "--pcre2-version",
		"--field-match-separator", "--field-context-separator", "--passthru", "--passthrough"),
	"git-grep": set("--count", "--files-with-matches", "--name-only", "--files-without-match", "--quiet",
		"--null", "--heading", "--break", "--show-function", "--open-files-in-pager", "--column",
		"--function-context"),
}

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

var digitsRe = regexp.MustCompile(`^\d+$`)

// parseOpts reads a grep / rg / git grep command line. It never fails;
// o.bail reports flags whose output this package does not model.
func parseOpts(tool, name string, args []string) opts {
	o := opts{tool: tool, flavor: 'b', groupSep: "--"}
	switch {
	case tool == "rg", name == "egrep":
		o.flavor = 'e'
	case name == "fgrep":
		o.fixed = true
	}
	var positional []string
	withH, withNoH := false, false
	explicitPattern := false
	lineNum := 0 // rg: last of -n / -N wins
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "--") {
			name, val, hasVal := strings.Cut(a, "=")
			if longBail[tool][name] {
				o.bail = true
			}
			if !hasVal && longValue[tool][name] {
				if i+1 < len(args) {
					val = args[i+1]
					i++
				}
			}
			switch name {
			case "--regexp":
				o.patterns = append(o.patterns, val)
				explicitPattern = true
			case "--file":
				explicitPattern = true
			case "--fixed-strings":
				o.fixed = true
			case "--extended-regexp":
				o.flavor = 'e'
			case "--basic-regexp":
				o.flavor = 'b'
			case "--perl-regexp", "--pcre2":
				o.flavor = 'p'
			case "--ignore-case":
				o.icase = true
			case "--smart-case":
				o.smart = true
			case "--case-sensitive":
				o.icase, o.smart = false, false
			case "--word-regexp":
				o.word = true
			case "--invert-match":
				o.invert = true
			case "--only-matching":
				o.only = true
			case "--line-number":
				o.numbered = true
				lineNum = 1
			case "--no-line-number":
				lineNum = -1
			case "--column", "--vimgrep":
				o.column = true
				if name == "--vimgrep" {
					withH = true
				}
				if lineNum == 0 {
					lineNum = 2 // implied
				}
			case "--with-filename":
				withH = true
			case "--no-filename":
				withNoH = true
			case "--recursive", "--dereference-recursive":
				o.recursive = true
			case "--directories":
				o.recursive = o.recursive || val == "recurse"
			case "--after-context", "--before-context", "--context":
				o.context = o.context || val != "0"
			case "--group-separator", "--context-separator":
				o.groupSep = val
			case "--no-group-separator", "--no-context-separator":
				o.groupSep = ""
			case "--files":
				o.files = true
			}
			continue
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		// A short cluster: -rn, -C2, -e PATTERN, -ePATTERN, -5 (grep context).
		if digitsRe.MatchString(a[1:]) && tool != "rg" {
			o.context = o.context || a != "-0"
			continue
		}
		for j := 1; j < len(a); j++ {
			ch := a[j]
			if strings.IndexByte(shortBail[tool], ch) >= 0 {
				o.bail = true
			}
			if strings.IndexByte(shortValue[tool], ch) >= 0 {
				val := a[j+1:]
				if val == "" && i+1 < len(args) {
					val = args[i+1]
					i++
				}
				switch ch {
				case 'e':
					o.patterns = append(o.patterns, val)
					explicitPattern = true
				case 'f':
					explicitPattern = true
				case 'A', 'B', 'C':
					o.context = o.context || val != "0"
				case 'd':
					if tool == "grep" && val == "recurse" {
						o.recursive = true
					}
				}
				break
			}
			switch ch {
			case 'F':
				o.fixed = true
			case 'E':
				o.flavor = 'e'
			case 'G':
				if tool != "rg" {
					o.flavor = 'b'
				}
			case 'P':
				o.flavor = 'p'
			case 'i', 'y':
				o.icase = true
			case 'S':
				if tool == "rg" {
					o.smart = true
				}
			case 's':
				if tool == "rg" {
					o.icase, o.smart = false, false
				}
			case 'w':
				o.word = true
			case 'v':
				o.invert = true
			case 'o':
				o.only = true
			case 'n':
				o.numbered = true
				lineNum = 1
			case 'N':
				if tool == "rg" {
					lineNum = -1
				}
			case 'H':
				withH = true
			case 'h':
				withNoH = true
			case 'I':
				if tool == "rg" {
					withNoH = true
				}
			case 'r', 'R':
				if tool != "rg" {
					o.recursive = true
				}
			case 'W':
				if tool == "git-grep" {
					o.context = true
				}
			}
		}
	}
	if tool == "rg" {
		o.numbered = lineNum > 0
		if lineNum < 0 {
			o.column = false
		}
	}
	if !explicitPattern && len(positional) > 0 && !o.files {
		o.patterns = append(o.patterns, positional[0])
		positional = positional[1:]
	}
	o.operands = positional

	switch {
	case withNoH && !withH:
		o.withFile = -1
	case withH:
		o.withFile = 1
	case tool == "git-grep":
		o.withFile = 1
	case tool == "grep":
		switch {
		case o.recursive || len(o.operands) > 1:
			o.withFile = 1
		case len(o.operands) == 1 && hasGlob(o.operands[0]):
			o.withFile = 0
		default:
			o.withFile = -1
		}
	case tool == "rg":
		switch {
		case len(o.operands) != 1:
			o.withFile = 1
		case hasGlob(o.operands[0]):
			o.withFile = 0
		case looksLikeFile(o.operands[0]):
			o.withFile = -1
		case looksLikeDir(o.operands[0]):
			o.withFile = 1
		default:
			o.withFile = 0
		}
	}
	return o
}

func hasGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

func looksLikeFile(p string) bool {
	if strings.HasSuffix(p, "/") {
		return false
	}
	b := path.Base(p)
	i := strings.LastIndexByte(b, '.')
	return i > 0 && i < len(b)-1
}

func looksLikeDir(p string) bool {
	b := path.Base(p)
	return strings.HasSuffix(p, "/") || b == "." || b == ".." || !strings.Contains(b, ".")
}
