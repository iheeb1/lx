package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iheeb1/lx/internal/laya"
)

func (s *state) checkLaya() {
	const id = "laya"
	data := ""
	if s.e.HistoryPath != "" {
		data = filepath.Dir(s.e.HistoryPath)
	}
	p := laya.PathsFor(s.e.Getenv, s.e.Home, data)
	py, fromEnv := p.Python(s.e.Getenv)

	if fi, err := os.Stat(p.Socket); err == nil && fi.Mode().Type() == os.ModeSocket {
		start := s.e.Clock()
		in, err := laya.Client{Socket: p.Socket}.Ping(time.Second)
		lat := s.e.Clock().Sub(start)
		switch {
		case err == nil && in.Version != laya.Protocol:
			s.add(id, Warn, fmt.Sprintf("the daemon (pid %d) speaks protocol %d, this lx %d, so lx doesn't use it", in.Pid, in.Version, laya.Protocol),
				"lx laya stop && lx laya start")
		case err == nil && !in.Loaded:
			s.add(id, Warn, fmt.Sprintf("the daemon (pid %d) is still loading its model; lx uses it once loaded", in.Pid), "")
		case err == nil:
			s.add(id, OK, fmt.Sprintf("daemon running (pid %d, %s), ping %s", in.Pid, in.Model, fmtLatency(lat)), "")
		case py != "":
			s.add(id, Warn, "the daemon at "+s.show(p.Socket)+" doesn't answer ("+errText(err)+"): lx works without it", "lx laya stop && lx laya start")
		default:
			s.add(id, Warn, "a stale daemon socket at "+s.show(p.Socket)+" and no Laya setup", "lx laya stop")
		}
		return
	}
	switch {
	case py == "":
		s.add(id, Skip, "not set up (optional: lx laya setup)", "")
	case fromEnv && executableProblem(py) != "":
		s.add(id, Warn, "LX_LAYA_PYTHON="+py+" "+executableProblem(py), "point LX_LAYA_PYTHON at a python that has laya installed, or unset it and run lx laya setup")
	default:
		s.add(id, Warn, "set up ("+s.show(py)+") but not running: lx works without it", "lx laya start")
	}
}
