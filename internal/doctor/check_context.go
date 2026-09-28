package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/iheeb1/lx/internal/agentctx"
)

func (s *state) checkContext() {
	const id = "context"
	src, err := agentctx.Locate(s.e.Getenv, s.e.Cwd)
	var nf *agentctx.NotFoundError
	switch {
	case errors.Is(err, agentctx.ErrDisabled):
		s.add(id, Skip, "LX_CONTEXT=0: lx doesn't read the agent's session (no focus, context pressure or inferred mode)", "")
		return
	case errors.Is(err, agentctx.ErrNoAgent):
		return
	case errors.Is(err, agentctx.ErrNoID):
		s.add(id, Skip, "this Claude Code doesn't set CLAUDE_CODE_SESSION_ID, so lx can't find the session and works without context", "")
		return
	case errors.As(err, &nf):
		fix := "set CLAUDE_CONFIG_DIR to the directory Claude Code uses, or LX_CONTEXT=0 to stop looking"
		if nf.Agent == agentctx.Codex {
			fix = "set CODEX_HOME to the directory Codex uses, or LX_CONTEXT=0 to stop looking"
		}
		s.add(id, Warn, "no transcript for "+nf.Agent+" session "+shortID(nf.Session)+" under "+s.show(nf.Dir)+": lx works without context", fix)
		return
	case err != nil:
		s.add(id, Warn, errText(err)+": lx works without context", "")
		return
	}
	src.Argv = []string{"lx", "doctor"}
	snap, err := agentctx.Load(src, agentctx.DefaultTail)
	if err != nil {
		s.add(id, Warn, "can't read the transcript "+s.show(src.Path)+" ("+errText(err)+"): lx works without context", "")
		return
	}
	size := ""
	if st, err := os.Stat(snap.Path); err == nil {
		size = mib(st.Size()) + ", "
	}
	who := snap.Agent + " session " + shortID(snap.SessionID)
	if snap.Subagent != "" {
		who += " (subagent " + shortID(snap.Subagent) + ")"
	}
	model := snap.Model
	if model == "" {
		model = "not recorded yet"
	}
	msg := fmt.Sprintf("%s: transcript readable (%sreads the last %s); model %s, window %s tokens (%s)",
		who, size, mib(int64(snap.Bytes)), model, groupInt(snap.Pressure.Window), windowFrom(snap.WindowFrom))
	if snap.Pressure.Known() {
		msg += fmt.Sprintf(", %d%% used", int(snap.Pressure.Fraction()*100))
	}
	if v := strings.TrimSpace(s.e.Getenv("LX_CONTEXT_WINDOW")); v != "" && src.Window == 0 {
		s.add(id, Warn, msg+"; LX_CONTEXT_WINDOW="+v+" is not a token count, so lx ignores it", "set it like LX_CONTEXT_WINDOW=200k or 1m, or unset it")
		return
	}
	s.add(id, OK, msg, "")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

func mib(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KiB", (n+1023)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

func groupInt(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func windowFrom(s string) string {
	switch s {
	case "LX_CONTEXT_WINDOW":
		return "LX_CONTEXT_WINDOW"
	case "transcript":
		return "from the transcript"
	case "usage":
		return "usage above the model's usual window"
	case "default":
		return "default, unknown model"
	}
	return "model table"
}
