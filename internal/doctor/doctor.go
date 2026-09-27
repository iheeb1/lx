// Package doctor answers one question: is lx actually working for this
// user's Claude Code? It reads the same settings files Claude Code and lx's
// hook read, runs lx's own hook once on a harmless payload, asks the login
// shell where lx is, and inspects lx's storage. Every finding comes with a
// copy-paste fix.
//
// Doctor is read-only: it never writes a file except a temporary probe it
// creates and removes in the run store. It never executes a hook command
// unless the command is exactly `<…/lx> hook claude [flags]` made of plain
// words, and never a binary that lives inside the current project (a cloned
// repository must not get code run by `lx doctor`).
//
// Everything it touches comes through Env, so it is tested on fixture homes
// and projects without the real machine's state.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Env is the machine doctor looks at.
type Env struct {
	Home        string // the user's home directory
	Cwd         string // where doctor runs (the hook payload's cwd)
	ConfigDir   string // $CLAUDE_CONFIG_DIR or ~/.claude
	ProjectDir  string // $CLAUDE_PROJECT_DIR, or cwd's nearest ancestor holding .claude ("" if none)
	ManagedPath string // enterprise managed-settings.json (read only)
	Executable  string // this lx binary
	Version     string // this binary's version line, as `lx version` prints it
	TeeDir      string // where full outputs are stored
	HistoryPath string // the savings history (JSON lines)

	Getenv func(string) string
	// Exec runs name with args, feeding stdin, and returns its stdout. A
	// non-zero exit is an error (*exec.ExitError for the real one).
	Exec func(ctx context.Context, name string, args []string, stdin string) (stdout string, err error)
	Now  time.Time
	// Clock measures the hook's latency; nil means time.Now.
	Clock func() time.Time
}

// Check statuses.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip" // the check could not apply (for example: no hook to run)
)

// Check is one finding.
type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix"` // "" when there is nothing to do
}

// Report is everything doctor found, in check order.
type Report struct {
	Version string  `json:"version"`
	Binary  string  `json:"binary"`
	Checks  []Check `json:"checks"`
}

// Failed reports whether any check failed.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

func (r Report) count(status string) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == status {
			n++
		}
	}
	return n
}

// idWidth fits the longest check id (hook-binary).
const idWidth = 11

// Text writes one line per check: a marker (✓ ok, ! warn, ✗ fail, - skip),
// the id, the message, and an indented fix line when there is one.
func (r Report) Text(w io.Writer) {
	pad := strings.Repeat(" ", 2+idWidth+1)
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%s %-*s %s\n", marker(c.Status), idWidth, c.ID, c.Message)
		if c.Fix != "" {
			fmt.Fprintf(w, "%sfix: %s\n", pad, c.Fix)
		}
	}
	fails, warns := r.count(Fail), r.count(Warn)
	switch {
	case fails == 0 && warns == 0:
		fmt.Fprintln(w, "lx doctor: no problems found")
	default:
		fmt.Fprintf(w, "lx doctor: %s, %s\n", plural(fails, "failure"), plural(warns, "warning"))
	}
}

// JSON writes {version, binary, checks: [{id, status, message, fix}]}.
func (r Report) JSON(w io.Writer) error {
	if r.Checks == nil {
		r.Checks = []Check{}
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func marker(status string) string {
	switch status {
	case OK:
		return "✓"
	case Warn:
		return "!"
	case Fail:
		return "✗"
	}
	return "-"
}

// Usage is the one-line synopsis.
const Usage = "usage: lx doctor [--json]"

// Main is `lx doctor [--json]`: it prints the report and returns 1 when a
// check failed, 0 otherwise (2 for a usage error).
func Main(args []string, stdout, stderr io.Writer, e Env) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json", "-json":
			asJSON = true
		case "-h", "--help", "help":
			fmt.Fprintln(stdout, Usage)
			fmt.Fprintln(stdout, "checks that lx's Claude Code hook, PATH, permissions and storage are working (read-only)")
			return 0
		default:
			fmt.Fprintf(stderr, "lx doctor: unknown argument %q\n%s\n", a, Usage)
			return 2
		}
	}
	r := Run(e)
	if asJSON {
		if err := r.JSON(stdout); err != nil {
			fmt.Fprintln(stderr, "lx doctor:", err)
		}
	} else {
		r.Text(stdout)
	}
	if r.Failed() {
		return 1
	}
	return 0
}

// Run performs every check, in order: binary, hook, hook-binary, hook-run,
// path, rtk, perms, env, settings, storage, activity.
func Run(e Env) Report {
	if e.Getenv == nil {
		e.Getenv = func(string) string { return "" }
	}
	if e.Exec == nil {
		e.Exec = func(context.Context, string, []string, string) (string, error) { return "", errNoExec }
	}
	if e.Now.IsZero() {
		e.Now = time.Now()
	}
	if e.Clock == nil {
		e.Clock = time.Now
	}
	s := newState(&e)
	s.checkBinary()
	s.checkHook()
	s.checkHookBinary()
	s.checkHookRun()
	s.checkPath()
	s.checkRtk()
	s.checkPerms()
	s.checkEnv()
	s.checkSettings()
	s.checkStorage()
	s.checkActivity()
	return Report{Version: e.Version, Binary: e.Executable, Checks: s.checks}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func itoa(n int) string { return fmt.Sprint(n) }
