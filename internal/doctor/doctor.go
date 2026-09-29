// Package doctor checks that lx is set up correctly.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type Env struct {
	Home        string
	Cwd         string
	ConfigDir   string
	ProjectDir  string
	ManagedPath string
	Executable  string
	Version     string
	TeeDir      string
	HistoryPath string

	Getenv func(string) string

	Exec func(ctx context.Context, name string, args []string, stdin string) (stdout string, err error)
	Now  time.Time

	Clock func() time.Time
}

const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip"
)

type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

type Report struct {
	Version string  `json:"version"`
	Binary  string  `json:"binary"`
	Checks  []Check `json:"checks"`
}

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

const idWidth = 11

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

const Usage = "usage: lx doctor [--json]"

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
	s.checkContext()
	s.checkLimits()
	s.checkSearch()
	s.checkLaya()
	return Report{Version: e.Version, Binary: e.Executable, Checks: s.checks}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func itoa(n int) string { return fmt.Sprint(n) }
