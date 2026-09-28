package doctor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	beginMark    = "lx-doctor:begin"
	notFoundMark = "lx-doctor:not-found"
	probeScript  = "echo " + beginMark + "; command -v lx || echo " + notFoundMark
)

type shellProbe struct {
	shell string
	path  string
	err   error
}

func (s *state) probeShell() *shellProbe {
	if s.probe != nil {
		return s.probe
	}
	sh, args := s.e.Getenv("SHELL"), []string{"-lic", probeScript}
	if sh == "" {
		sh, args = "/bin/sh", []string{"-lc", probeScript}
	}
	p := &shellProbe{shell: sh}
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	out, err := s.e.Exec(ctx, sh, args, "")
	cancel()
	begun, notFound, last := false, false, ""
	for _, line := range strings.Split(stripEscapes(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == beginMark:
			begun, notFound, last, p.path = true, false, "", ""
		case !begun:
		case strings.HasPrefix(line, "/"):
			p.path = line
		case line == notFoundMark:
			notFound = true
		case line != "":
			last = line
		}
	}
	if notFound {
		p.path = ""
	}
	switch {
	case p.path != "" || notFound:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		p.err = fmt.Errorf("%s did not answer within %d s", sh, int(shellTimeout/time.Second))
	case err != nil:
		p.err = err
	case !begun:
		p.err = errors.New("the shell exited before running `command -v lx` (a startup file may exec another program)")
	case last != "":
		p.err = fmt.Errorf("`command -v lx` printed %q (an alias or function?)", clip(last, 80))
	default:
		p.err = errors.New("`command -v lx` printed nothing")
	}
	s.probe = p
	return p
}

func (s *state) reference() (path, label string, from *foundHook) {
	for _, h := range s.hooks {
		if h.kind == lxVerified && h.path != "" && h.missing == "" {
			return h.path, "hook's lx", h
		}
	}
	return s.e.Executable, "lx you ran", nil
}

func (s *state) hookHasPrefix() bool {
	for _, h := range s.hooks {
		if h.kind == lxVerified && h.cmd.hasPrefix {
			return true
		}
	}
	return false
}

func (s *state) rewritesByPath() (yes bool, how string) {
	if s.rewriteBin != "" {
		return s.rewriteBin != "lx", "rewrites call " + s.show(s.rewriteBin) + " by its full path"
	}
	return s.hookHasPrefix(), "the hook's --prefix keeps rewrites working"
}

func (s *state) checkPath() {
	const id = "path"
	ref, label, refHook := s.reference()
	prefix, how := s.rewritesByPath()
	p := s.probeShell()
	exportFix := func() string {
		dir := ref
		if !s.canRun(dir) {
			dir = s.e.Executable
		}
		if dir == "" {
			return ""
		}
		return "export PATH=" + shellQuote(filepath.Dir(dir)) + `:"$PATH"` + "  # in your shell's startup file"
	}
	switch {
	case p.path == "" && p.err != nil:
		s.add(id, Warn, "could not ask your shell where lx is: "+errText(p.err), "check by hand: "+shellQuote(p.shell)+" -lic 'command -v lx'")
	case p.path == "" && prefix:
		s.add(id, Warn, "lx is not on your shell's PATH: "+how+
			", but the `lx show <id>` a receipt suggests will fail with command not found", exportFix())
	case p.path == "":
		fix := exportFix()
		if fix != "" {
			fix += ", or re-run `lx init` so rewrites call lx by its full path"
		}
		s.add(id, Fail, "lx is not on your shell's PATH ("+filepath.Base(p.shell)+"): rewritten commands will fail with command not found", fix)
	case ref == "":
		s.add(id, OK, "your shell runs lx from "+s.show(p.path), "")
	case filepath.Clean(p.path) == filepath.Clean(ref) || sameFile(p.path, ref):
		what := "the hook's binary"
		if refHook == nil {
			what = "this binary"
		}
		s.add(id, OK, "your shell runs lx from "+s.show(p.path)+", "+what, "")
	default:
		msg := fmt.Sprintf("your shell runs lx from %s (%s), but the %s is %s (%s)",
			s.show(p.path), s.versionOf(p.path), label, s.show(ref), s.versionOf(ref))
		if prefix {
			msg += "; rewrites call lx by its full path, but an `lx show` the agent types runs the shell's"
		} else {
			msg += ": rewritten commands run the shell's"
		}
		fix := exportFix() + ", or remove the other lx"
		if refHook != nil && !s.canRun(ref) {
			fix = "point the hook at a trusted lx: " + s.hookFixFor(refHook)
		}
		s.add(id, Warn, msg, fix)
	}
}
