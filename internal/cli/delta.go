package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/hook"
	"github.com/iheeb1/lx/internal/tee"
)

const (
	deltaMaxRaw = 8 << 20
	deltaFilter = "delta"
)

func sessionDelta(snap *agentctx.Snapshot, c *engine.Context, raw, normalView string) (view string, ok bool) {
	if snap == nil || c == nil || deltaOff() || len(raw) > deltaMaxRaw {
		return "", false
	}
	defer func() {
		if recover() != nil {
			view, ok = "", false
		}
	}()
	prev, ok := deltaBase(snap, c)
	if !ok {
		return "", false
	}
	prev.MaxChars = hostCharCapFor(c.Exit)
	return engine.Rerun(c, prev, raw, normalView)
}

func deltaOff() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LX_DELTA"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

// A run shown as a delta leans on older runs: they must be in context back to one shown in full.
func deltaBase(snap *agentctx.Snapshot, c *engine.Context) (engine.Prev, bool) {
	var base agentctx.Run
	for _, r := range snap.Runs {
		if r.Pending || r.LxID <= 0 {
			continue
		}
		m, ok := storedRun(r.LxID)
		if !ok || !slices.Equal(m.Argv, c.Argv) || filepath.Clean(m.Cwd) != filepath.Clean(c.Cwd) || !shownWhole(r.Command, c.Argv) {
			continue
		}
		switch {
		case !r.InContext && base.LxID == 0:
			continue
		case !r.InContext:
			return engine.Prev{}, false
		case base.LxID == 0:
			base = r
		}
		if m.Filter == deltaFilter {
			continue
		}
		raw, m, err := tee.Load(base.LxID)
		if err != nil || m.State != tee.StateDone || len(raw) > deltaMaxRaw {
			return engine.Prev{}, false
		}
		ref := selfCommand() + " show " + strconv.Itoa(base.LxID)
		return engine.Prev{Ref: ref, TurnsAgo: base.TurnsAgo, Exit: m.Exit, Raw: raw}, true
	}
	return engine.Prev{}, false
}

func storedRun(id int) (tee.Meta, bool) {
	m, err := tee.ReadMeta(id)
	return m, err == nil && m.State == tee.StateDone
}

func shownWhole(cmd string, argv []string) bool {
	if len(argv) == 0 || rerouted(cmd) || strings.Contains(cmd, "LX_MODE=") || strings.Contains(cmd, "LX_BUDGET=") {
		return false
	}
	for _, a := range hook.Inspect(cmd).Commands {
		if n := len(a) - len(argv); (n == 0 || n == 1 && filepath.Base(a[0]) == "lx") && slices.Equal(a[n:], argv) {
			return true
		}
	}
	return false
}

// Only a stderr redirect leaves lx's output going whole to the model.
func rerouted(cmd string) bool {
	quote := byte(0)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		next := byte(0)
		if i+1 < len(cmd) {
			next = cmd[i+1]
		}
		switch {
		case quote == '\'':
			if ch == '\'' {
				quote = 0
			}
		case ch == '\\':
			i++
		case ch == '`' || ch == '$' && next == '(':
			return true
		case quote == '"':
			if ch == '"' {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
		case ch == '|' && next == '|', ch == '&' && next == '&':
			i++
		case ch == '2' && next == '>' && (i == 0 || strings.IndexByte(" \t;&|(", cmd[i-1]) >= 0):
			i++
			if i+1 < len(cmd) && cmd[i+1] == '>' {
				i++
			}
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				i++
			}
		case ch == '|', ch == '>', ch == '&', ch == '<' && next == '(':
			return true
		}
	}
	return false
}
