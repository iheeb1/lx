package python

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type scriptFilter struct{}

func (scriptFilter) GuardsErrors() bool { return true }

func (scriptFilter) Name() string { return "python" }

func (scriptFilter) Match(c *engine.Context) bool {
	return parseInvocation(c).tool == "script"
}

var pyServers = map[string]bool{
	"http.server": true, "uvicorn": true, "gunicorn": true, "hypercorn": true, "daphne": true,
	"streamlit": true, "jupyter": true, "notebook": true, "jupyterlab": true, "gradio": true,
	"livereload": true, "smtpd": true, "aiosmtpd": true, "celery": true, "watchfiles": true,
}

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
				return true
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
		return "", false
	}
	folded, hidden := foldPyTracebacks(lines)
	exempt := make([]bool, len(lines))
	for _, i := range hidden {
		exempt[i] = true
	}
	out := strings.Split(engine.Generic(c, strings.Join(folded, "\n")), "\n")
	out, _ = ensureErrors(lines, exempt, out)
	if c.Exit != 0 && !anyError(out) {
		out = append(out, fmt.Sprintf("[lx: python exited %d]", c.Exit))
	}
	return strings.Join(out, "\n"), true
}
