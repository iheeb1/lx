// Package python handles pytest, pip, mypy, ruff and tracebacks.
package python

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.Register(pytestFilter{})
	engine.Register(pipInstallFilter{})
	engine.Register(pipUninstallFilter{})
	engine.Register(pipListFilter{})
	engine.Register(pipShowFilter{})
	engine.Register(mypyFilter{})
	engine.Register(ruffFilter{})
	engine.Register(scriptFilter{})
}

type invocation struct {
	tool string
	args []string
}

var (
	pythonRe = lazyre.New(`^(?:python(?:\d+(?:\.\d+)?t?)?|pypy3?)$`)
	pipRe    = lazyre.New(`^pip(?:\d+(?:\.\d+)?)?$`)
)

var runnerValueFlags = map[string]bool{
	"--with": true, "--with-editable": true, "--with-requirements": true, "--python": true, "-p": true,
	"--package": true, "--extra": true, "--group": true, "--only-group": true, "--no-group": true,
	"--env-file": true, "--directory": true, "--project": true, "--index": true, "--default-index": true,
	"--index-url": true, "-i": true, "--extra-index-url": true, "--find-links": true, "-f": true,
	"--config-file": true, "--cache-dir": true, "--color": true, "-C": true, "-P": true, "--venv": true,
}

func parseInvocation(c *engine.Context) invocation {
	if inner := unwrapShell(c.Argv); inner != nil {
		return parseArgv(inner, 0)
	}
	return parseArgv(c.Argv, 0)
}

func unwrapShell(argv []string) []string {
	if len(argv) != 3 {
		return nil
	}
	switch filepath.Base(argv[0]) {
	case "sh", "bash", "zsh", "dash":
	default:
		return nil
	}
	if argv[1] != "-c" && argv[1] != "-lc" && argv[1] != "-ec" {
		return nil
	}
	var words []string
	var w strings.Builder
	inWord := false
	s := argv[2]
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil
			}
			w.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case ch == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				switch {
				case s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0:
					i++
					w.WriteByte(s[i])
				case s[i] == '$' || s[i] == '`':
					return nil
				default:
					w.WriteByte(s[i])
				}
			}
			if i >= len(s) {
				return nil
			}
			inWord = true
		case ch == ' ' || ch == '\t':
			if inWord {
				words = append(words, w.String())
				w.Reset()
				inWord = false
			}
		case strings.IndexByte(";&|<>$`()\n\\*?{}#~", ch) >= 0:
			if strings.HasPrefix(s[i:], ">&1") && inWord && w.String() == "2" {
				w.Reset()
				inWord = false
				i += 2
				continue
			}
			return nil
		default:
			w.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		words = append(words, w.String())
	}
	if len(words) == 0 {
		return nil
	}
	return words
}

var assignRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*=`)

func peelWrappers(argv []string) []string {
	for len(argv) > 0 {
		switch a := argv[0]; {
		case assignRe.MatchString(a):
			argv = argv[1:]
		case filepath.Base(a) == "env":
			argv = skipOpts(argv[1:], map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true}, true)
		case filepath.Base(a) == "timeout":
			argv = skipOpts(argv[1:], map[string]bool{"-s": true, "--signal": true, "-k": true, "--kill-after": true}, false)
			if len(argv) > 0 {
				argv = argv[1:]
			}
		case filepath.Base(a) == "nice":
			argv = skipOpts(argv[1:], map[string]bool{"-n": true, "--adjustment": true}, false)
		case filepath.Base(a) == "nohup":
			argv = argv[1:]
		default:
			return argv
		}
	}
	return argv
}

func skipOpts(args []string, valued map[string]bool, assign bool) []string {
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--":
			return args[1:]
		case valued[a]:
			args = args[min(2, len(args)):]
		case strings.HasPrefix(a, "-") && a != "-", assign && assignRe.MatchString(a):
			args = args[1:]
		default:
			return args
		}
	}
	return args
}

var coverageValueFlags = map[string]bool{"--rcfile": true, "--source": true, "--include": true, "--omit": true,
	"--data-file": true, "--context": true, "--concurrency": true, "--debug": true}

func coverageRun(args []string, depth int) invocation {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--module":
			if i+1 < len(args) {
				return moduleInvocation(args[i+1], args[i+2:], depth)
			}
			return invocation{}
		case coverageValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return invocation{"script", args[i:]}
		}
	}
	return invocation{}
}

func parseArgv(argv []string, depth int) invocation {
	argv = peelWrappers(argv)
	if len(argv) == 0 || depth > 4 {
		return invocation{}
	}
	name := filepath.Base(argv[0])
	args := argv[1:]
	switch {
	case name == "coverage":
		if len(args) > 0 && args[0] == "run" {
			return coverageRun(args[1:], depth+1)
		}
	case name == "pytest" || name == "py.test":
		return invocation{"pytest", args}
	case pipRe.MatchString(name):
		return invocation{"pip", args}
	case name == "mypy":
		return invocation{"mypy", args}
	case name == "ruff":
		return invocation{"ruff", args}
	case pythonRe.MatchString(name):
		return parsePython(args, depth)
	case name == "uv" || name == "poetry" || name == "pdm" || name == "pipenv" || name == "hatch" || name == "rye":
		if len(args) > 0 && args[0] == "run" {
			return parseArgv(skipRunnerFlags(args[1:]), depth+1)
		}
		if name == "uv" && len(args) > 1 && args[0] == "tool" && args[1] == "run" {
			return parseArgv(skipRunnerFlags(args[2:]), depth+1)
		}
	case name == "uvx":
		return parseArgv(skipRunnerFlags(args), depth+1)
	case name == "pipx":
		if len(args) > 0 && args[0] == "run" {
			return parseArgv(skipRunnerFlags(args[1:]), depth+1)
		}
	case depth > 0 && strings.HasSuffix(name, ".py"):
		return invocation{"script", argv}
	}
	return invocation{}
}

func skipRunnerFlags(args []string) []string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return args[i+1:]
		case runnerValueFlags[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return args[i:]
		}
	}
	return nil
}

func parsePython(args []string, depth int) invocation {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-" || !strings.HasPrefix(a, "-"):
			return invocation{"script", args[i:]}
		case a == "--":
			if i+1 < len(args) {
				return invocation{"script", args[i+1:]}
			}
			return invocation{}
		case strings.HasPrefix(a, "--"):
			if a == "--check-hash-based-pycs" {
				i++
			}
			continue
		}

		for k := 1; k < len(a); k++ {
			switch a[k] {
			case 'm':
				mod, rest := a[k+1:], args[i+1:]
				if mod == "" {
					if i+1 >= len(args) {
						return invocation{}
					}
					mod, rest = args[i+1], args[i+2:]
				}
				return moduleInvocation(mod, rest, depth)
			case 'c':
				return invocation{"script", args[i:]}
			case 'W', 'X':
				if k == len(a)-1 {
					i++
				}
				k = len(a)
			}
		}
	}
	return invocation{}
}

func moduleInvocation(mod string, rest []string, depth int) invocation {
	switch mod {
	case "pytest", "py.test":
		return invocation{"pytest", rest}
	case "pip":
		return invocation{"pip", rest}
	case "mypy":
		return invocation{"mypy", rest}
	case "ruff":
		return invocation{"ruff", rest}
	case "coverage":
		if len(rest) > 0 && rest[0] == "run" {
			return coverageRun(rest[1:], depth+1)
		}
		return invocation{}
	}
	return invocation{"script", append([]string{"-m", mod}, rest...)}
}

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

func argValues(args []string, long, short string) []string {
	var out []string
	for i, a := range args {
		switch {
		case a == "--":
			return out
		case a == long || short != "" && a == short:
			if i+1 < len(args) {
				out = append(out, args[i+1])
			}
		case strings.HasPrefix(a, long+"="):
			out = append(out, a[len(long)+1:])
		case short != "" && strings.HasPrefix(a, short) && len(a) > len(short) && !strings.HasPrefix(a, "--"):
			out = append(out, strings.TrimPrefix(a[len(short):], "="))
		}
	}
	return out
}

var (
	sitePkgRe = lazyre.New(`(?:site|dist)-packages[/\\]([^/\\"]+)`)
	stdlibRe  = lazyre.New(`[/\\][Ll]ib[/\\](?:python\d(?:\.\d+)?t?[/\\])?([^/\\"]+)`)
	frozenRe  = lazyre.New(`^<frozen ([\w]+)`)

	stdlibPathRe = lazyre.New(`/lib(?:64)?/(?:python|pypy)\d[\d.]*t?/|\\Lib\\`)
	stdlibRootRe = lazyre.New(`/lib(?:64)?/(?:python|pypy)\d[\d.]*t?/([^/]+)`)
)

func isLibPath(p string) bool {
	if strings.HasPrefix(p, "<frozen ") {
		return true
	}
	if strings.Contains(p, "site-packages") || strings.Contains(p, "dist-packages") {
		return true
	}
	return stdlibPathRe.MatchString(p)
}

func libRoot(p string) string {
	if m := sitePkgRe.FindStringSubmatch(p); m != nil {
		return strings.TrimSuffix(m[1], ".py")
	}
	if m := frozenRe.FindStringSubmatch(p); m != nil {
		return m[1]
	}
	if m := stdlibRootRe.FindStringSubmatch(p); m != nil {
		return strings.TrimSuffix(m[1], ".py")
	}
	if m := stdlibRe.FindStringSubmatch(p); m != nil {
		return strings.TrimSuffix(m[1], ".py")
	}
	return "lib"
}

func foldMarker(indent string, n int, roots []string) string {
	var uniq []string
	seen := map[string]bool{}
	for _, r := range roots {
		if !seen[r] {
			seen[r] = true
			uniq = append(uniq, r)
		}
	}
	if len(uniq) > 3 {
		uniq = append(uniq[:3], "…")
	}
	return fmt.Sprintf("%s… %s (%s)", indent, engine.Plural(n, "library frame", "library frames"), strings.Join(uniq, ", "))
}

var (
	pyFrameRe      = lazyre.New(`^(\s*)File "([^"]+)", line \d+(?:,? in .+)?$`)
	pyFaultFrameRe = lazyre.New(`", line \d+ in \S`)

	pyPrefixRe  = lazyre.New(`^(?:INTERNALERROR>|\s*\|)`)
	pyRepeatRe  = lazyre.New(`^\s*\[Previous line repeated \d+ more times?\]$`)
	tracebackRe = lazyre.New(`^\s*(?:\+ )?(?:Exception Group )?Traceback \(most recent call last\):$`)

	faultRe = lazyre.New(`^Fatal Python error: |^(?:Current thread|Thread) 0x[0-9a-f]+ (?:\[[^\]]*\] )?\(most recent call first\):$`)
	chainRe = lazyre.New(`^\s*(?:The above exception was the direct cause of the following exception:|During handling of the above exception, another exception occurred:)$`)
)

func splitFramePrefix(ln string) (pre, rest string) {
	if m := pyPrefixRe.FindString(ln); m != "" {
		return m, ln[len(m):]
	}
	return "", ln
}

func parseFrame(ln string) (pre, indent, path string, ok bool) {
	if len(ln) > 2000 {
		return "", "", "", false
	}
	pre, rest := splitFramePrefix(ln)
	m := pyFrameRe.FindStringSubmatch(rest)
	if m == nil {
		return "", "", "", false
	}
	return pre, m[1], m[2], true
}

func isTracebackStart(ln string) bool {
	_, rest := splitFramePrefix(ln)
	return tracebackRe.MatchString(rest) || faultRe.MatchString(ln)
}

type pyFrame struct {
	start, end int
	lib        bool
	root       string
	keep       bool
	real       bool
}

func foldPyTracebacks(lines []string) (out []string, folded []int) {
	out = make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		pre, indent, _, ok := parseFrame(lines[i])
		if !ok {
			out = append(out, lines[i])
			i++
			continue
		}
		fault := pyFaultFrameRe.MatchString(lines[i])
		var frames []pyFrame
		j := i
		for j < len(lines) {
			fpre, find, path, ok := parseFrame(lines[j])
			if !ok || fpre != pre || find != indent {
				if pyRepeatRe.MatchString(lines[j]) {
					frames = append(frames, pyFrame{start: j, end: j + 1, keep: true})
					j++
					continue
				}
				break
			}
			f := pyFrame{start: j, lib: isLibPath(path), real: true}
			if f.lib {
				f.root = libRoot(path)
			}
			k := j + 1
			for k < len(lines) {
				npre, nrest := splitFramePrefix(lines[k])
				if npre != pre || strings.TrimSpace(nrest) == "" || len(leading(nrest)) <= len(indent) {
					break
				}
				k++
			}
			f.end = k
			frames = append(frames, f)
			j = k
		}
		raised := -1
		for k := range frames {
			f := &frames[k]
			if f.real && (raised < 0 || !fault) {
				raised = k
			}
			if !f.lib {
				f.keep = true
			}
		}
		if raised >= 0 {
			frames[raised].keep = true
		}
		for k := 0; k < len(frames); {
			if frames[k].keep {
				out = append(out, lines[frames[k].start:frames[k].end]...)
				k++
				continue
			}
			e := k
			var roots []string
			for e < len(frames) && !frames[e].keep {
				roots = append(roots, frames[e].root)
				e++
			}
			if e-k < 2 {
				out = append(out, lines[frames[k].start:frames[k].end]...)
			} else {
				out = append(out, foldMarker(pre+indent, e-k, roots))
				for _, f := range frames[k:e] {
					for n := f.start; n < f.end; n++ {
						folded = append(folded, n)
					}
				}
			}
			k = e
		}
		i = j
	}
	return out, folded
}

func leading(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

func squashLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func ensureErrors(in []string, exempt []bool, out []string) ([]string, int) {
	return ensureErrorsFn(in, exempt, out, func(i int) bool { return engine.IsError(in[i]) })
}

func ensureErrorsFn(in []string, exempt []bool, out []string, isErr func(i int) bool) ([]string, int) {
	emitted := make(map[string]bool, len(out))
	for _, ln := range out {
		emitted[squashLine(ln)] = true
	}
	var norm string
	var missing []string
	seen := map[string]bool{}
	for i, ln := range in {
		if exempt != nil && i < len(exempt) && exempt[i] {
			continue
		}
		t := squashLine(ln)
		if t == "" || seen[t] || emitted[t] || !isErr(i) {
			continue
		}
		seen[t] = true
		if norm == "" {
			var b strings.Builder
			for _, o := range out {
				b.WriteString(squashLine(o))
				b.WriteByte('\n')
			}
			norm = b.String()
		}
		if !strings.Contains(norm, t) {
			missing = append(missing, ln)
		}
	}
	if len(missing) == 0 {
		return out, 0
	}
	out = append(out, "[lx: error lines from the full output]")
	out = append(out, missing...)
	return out, len(missing)
}

var criticalRe = lazyre.New(`^(?:CRITICAL|FATAL)\b`)

func capLines(lines []string, head, tail int, isErr func(n int) bool) (kept []string, dropped []int) {
	if len(lines) <= head+tail {
		return append([]string(nil), lines...), nil
	}
	gap := 0
	flush := func() {
		if gap > 0 {
			kept = append(kept, fmt.Sprintf("… %s hidden …", engine.Plural(gap, "line", "lines")))
			gap = 0
		}
	}
	for i, ln := range lines {
		if i < head || i >= len(lines)-tail || isErr(i) || criticalRe.MatchString(ln) {
			flush()
			kept = append(kept, ln)
			continue
		}
		gap++
		dropped = append(dropped, i)
	}
	flush()
	return kept, dropped
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func relativize(c *engine.Context, lines []string) []string {
	for i, ln := range lines {
		if strings.Contains(ln, "/") && !strings.Contains(ln, "file://") && !engine.IsError(ln) {
			lines[i] = engine.Relativize(c, ln)
		}
	}
	return lines
}
