package python

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// scriptFilter handles a python interpreter running a script, -c code or a
// module other than pytest/pip/mypy/ruff. Its output is the program's own,
// so the filter only acts when there is a traceback: library frames
// (site-packages, the standard library, <frozen …>) are folded per
// traceback — including the short, library-only tracebacks of chained
// exceptions — while the "Traceback" header, every frame of the project
// with its source line, the frame that raised, the chained-exception
// separators and the exception lines stay verbatim. The rest of the output
// then goes through the generic reducer (log templating, repeats, paths
// made relative), which never drops an error line.
//
// Folded library frames hold source code such as `raise exception`, which
// the line classifier takes for an error message, so the filter implements
// engine.Guarded and runs ensureErrors itself with exactly those lines
// exempted.
type scriptFilter struct{}

func (scriptFilter) GuardsErrors() bool { return true }

func (scriptFilter) Name() string { return "python" }

func (scriptFilter) Match(c *engine.Context) bool {
	return parseInvocation(c).tool == "script"
}

// pyServers are modules that serve until stopped.
var pyServers = map[string]bool{
	"http.server": true, "uvicorn": true, "gunicorn": true, "hypercorn": true, "daphne": true,
	"streamlit": true, "jupyter": true, "notebook": true, "jupyterlab": true, "gradio": true,
	"livereload": true, "smtpd": true, "aiosmtpd": true, "celery": true, "watchfiles": true,
}

// Stream: servers, watchers and interactive sessions never end (or wait for
// the user), so they run in passthrough: `python -i x.py`, `python -m
// http.server`, `python manage.py runserver`, `flask run`, anything with
// --reload.
func (scriptFilter) Stream(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.tool != "script" {
		return false
	}
	argv := peelWrappers(c.Argv)
	if len(argv) > 0 && pythonRe.MatchString(filepath.Base(argv[0])) {
		for _, a := range argv[1:] {
			if !strings.HasPrefix(a, "-") || a == "--" {
				break
			}
			if a == "-i" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "i") && !strings.ContainsAny(a[1:], "cmWX") {
				return true // -i: interactive after the script
			}
		}
	}
	args := inv.args
	if len(args) >= 2 && args[0] == "-m" {
		if pyServers[args[1]] {
			return true
		}
		args = args[1:]
	}
	for i, a := range args {
		switch {
		case a == "--reload" || a == "--watch":
			return true
		case (a == "runserver" || a == "runserver_plus" || a == "run" || a == "serve" || a == "dev") && i > 0:
			// manage.py runserver, python -m flask run, mkdocs serve
			return true
		}
	}
	return false
}

func (scriptFilter) Apply(c *engine.Context, text string) (string, bool) {
	lines := strings.Split(text, "\n")
	found := false
	for _, ln := range lines {
		if isTracebackStart(ln) {
			found = true
			break
		}
	}
	if !found {
		return "", false // plain program output: the generic reducer's job
	}
	folded, hidden := foldPyTracebacks(lines)
	exempt := make([]bool, len(lines))
	for _, i := range hidden {
		exempt[i] = true
	}
	out := strings.Split(engine.Generic(c, strings.Join(folded, "\n")), "\n")
	out, _ = ensureErrors(lines, exempt, out)
	if c.Exit != 0 && !anyError(out) {
		// A faulthandler dump without "Fatal Python error" (dump_traceback_later).
		out = append(out, fmt.Sprintf("[lx: python exited %d]", c.Exit))
	}
	return strings.Join(out, "\n"), true
}
