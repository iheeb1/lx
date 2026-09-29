package doctor

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/runner"
)

var reSourced = lazyre.New(`(?:^|\s)source\s+(?:'([^']*shell-snapshots/[^']*)'|(\S*shell-snapshots/\S*))`)

const maxSnapshot = 8 << 20

func (s *state) checkSearch() {
	const id = "search"
	if s.e.Getenv("CLAUDECODE") != "1" {
		return
	}
	snap := s.shellSnapshot()
	var runs, differ []string
	noFunctions := false
	for _, t := range [][2]string{{"grep", "ugrep"}, {"find", "bfs"}, {"rg", "rg"}} {
		lx := s.lxRuns(t[0])
		runs = append(runs, t[0]+" → "+lx)
		if snap == nil {
			continue
		}
		if sh := s.shellRuns(t[0], t[1], snap); sh != lx {
			differ = append(differ, t[0]+": the agent's shell runs "+sh+", lx runs "+lx)
			noFunctions = noFunctions || t[0] != "rg" && strings.HasPrefix(lx, "Claude Code's")
		}
	}
	switch {
	case snap == nil:
		s.add(id, Skip, "no Claude Code shell snapshot found, so lx can't compare with the agent's shell; lx runs "+strings.Join(runs, ", "), "")
	case len(differ) > 0:
		msg := strings.Join(differ, "; ") + ", so results can differ"
		if noFunctions {
			msg += " (Claude Code drops its grep and find functions when started with Grep or Glob in --tools or --allowedTools, which lx can't see)"
		}
		s.add(id, Warn, msg, "")
	default:
		s.add(id, OK, "same as the agent's shell: "+strings.Join(runs, ", "), "")
	}
}

func (s *state) lxRuns(tool string) string {
	r, err := runner.Resolve([]string{tool}, s.e.Getenv)
	switch {
	case err != nil:
		return "nothing (not found)"
	case r.Builtin != "":
		return "Claude Code's " + r.Builtin
	}
	return s.show(r.Path)
}

func (s *state) shellRuns(tool, builtin string, snap []byte) string {
	shim := bytes.Contains(snap, []byte("ARGV0="+builtin+" ")) || bytes.Contains(snap, []byte("exec -a "+builtin+" "))
	path, err := exec.LookPath(tool)
	switch {
	case shim && !(tool == "rg" && err == nil) && runner.ClaudeBinary(s.e.Getenv) != "":
		return "Claude Code's " + builtin
	case err == nil:
		return s.show(path)
	case tool == "rg" && bytes.Contains(snap, []byte("\n  alias rg=")):
		return "the rg bundled with Claude Code (an alias)"
	}
	return "nothing (not found)"
}

func (s *state) shellSnapshot() []byte {
	if b := readSnapshot(s.sourcedSnapshot()); b != nil {
		return b
	}
	return readSnapshot(newestSnapshot(filepath.Join(s.e.ConfigDir, "shell-snapshots")))
}

func readSnapshot(path string) []byte {
	if fi, err := os.Stat(path); path == "" || err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSnapshot))
	if err != nil {
		return nil
	}
	return b
}

// The agent's shell runs `source <snapshot> && eval '<command>'`.
func (s *state) sourcedSnapshot() string {
	pid := os.Getppid()
	for range 6 {
		ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
		out, err := s.e.Exec(ctx, "ps", []string{"-ww", "-o", "ppid=", "-o", "args=", "-p", strconv.Itoa(pid)}, "")
		cancel()
		if err != nil {
			return ""
		}
		ppid, args, _ := strings.Cut(strings.TrimSpace(out), " ")
		if m := reSourced.FindStringSubmatch(args); m != nil {
			return m[1] + m[2]
		}
		if pid, err = strconv.Atoi(ppid); err != nil || pid <= 1 {
			return ""
		}
	}
	return ""
}

func newestSnapshot(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestMod int64
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "snapshot-") || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && fi.ModTime().UnixNano() > bestMod {
			best, bestMod = filepath.Join(dir, e.Name()), fi.ModTime().UnixNano()
		}
	}
	return best
}
