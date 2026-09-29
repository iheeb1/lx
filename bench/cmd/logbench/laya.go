//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	daemon "github.com/iheeb1/lx/integrations/laya"
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/laya"
	"github.com/iheeb1/lx/internal/lazyre"
)

type layaInfo struct {
	Python     string  `json:"python"`
	Laya       string  `json:"laya"`
	Model      string  `json:"model"`
	StartedBy  string  `json:"started_by"`
	LoadMs     int64   `json:"load_ms"`
	RSSBytes   int64   `json:"rss_bytes"`
	RSSPeak    bool    `json:"rss_is_peak"`
	MsPerItem  float64 `json:"ms_per_item"`
	Requests   int     `json:"requests"`
	Items      int     `json:"items"`
	VenvBytes  int64   `json:"venv_bytes"`
	ModelBytes int64   `json:"model_bytes"`
	TimeoutMs  int64   `json:"judge_timeout_ms"`
}

type layaDaemon struct {
	c      laya.Client
	proc   *os.Process
	info   layaInfo
	log    string
	logOff int
}

var judgedRe = lazyre.New(`judge \S+: (\d+/\d+) items`)

// judged/sent for each request the daemon logged since the last call
func (d *layaDaemon) judged() []string {
	b, err := os.ReadFile(d.log)
	if err != nil {
		return nil
	}
	if len(b) < d.logOff {
		d.logOff = 0
	}
	var out []string
	for _, m := range judgedRe.FindAllStringSubmatch(string(b[d.logOff:]), -1) {
		out = append(out, m[1])
	}
	d.logOff = len(b)
	return out
}

// `lx laya start` when this lx has it, else the same script and environment
func startLaya(sb *sandbox, py string, running *atomic.Pointer[layaDaemon]) (*layaDaemon, error) {
	getenv := func(k string) string {
		if k == "HOME" {
			return sb.home
		}
		return ""
	}
	paths := laya.PathsFor(getenv, sb.home, "")
	d := &layaDaemon{c: laya.Client{Socket: paths.Socket}, log: paths.Log}
	running.Store(d)
	d.info.Python = py
	d.info.TimeoutMs = engine.DefaultJudgeTimeout.Milliseconds()

	r := sb.run("lx laya start --wait 5m", []string{"LX_LAYA_PYTHON=" + py}, false, 1)
	switch {
	case r.exit == 0:
		d.info.StartedBy = "lx laya start"
	case strings.Contains(r.out, "command not found: laya") || strings.Contains(r.out, "unknown command"):
		if err := d.spawn(sb, paths, py); err != nil {
			return nil, err
		}
		d.info.StartedBy = "lx_laya.py (this lx has no `lx laya` command)"
	default:
		return nil, fmt.Errorf("lx laya start: %s", strings.TrimSpace(r.out))
	}
	st, err := d.c.Status(2 * time.Second)
	if err != nil {
		d.stop()
		return nil, err
	}
	d.info.Model, d.info.Laya, d.info.LoadMs = st.Model, st.Laya, st.LoadMS
	d.info.VenvBytes = dirSize(filepath.Dir(filepath.Dir(py)))
	for _, kv := range sb.env {
		if hf, ok := strings.CutPrefix(kv, "HF_HOME="); ok {
			d.info.ModelBytes = dirSize(filepath.Join(hf, "hub", "models--convaiinnovations--laya", "snapshots"))
		}
	}
	return d, nil
}

func (d *layaDaemon) spawn(sb *sandbox, paths laya.Paths, py string) error {
	script := filepath.Join(sb.dir, "lx_laya.py")
	if err := os.WriteFile(script, []byte(daemon.Script), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.Socket), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(paths.Log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	c := exec.Command(py, script, "--socket", paths.Socket, "--pid", paths.Pid, "--log", paths.Log)
	c.Env = append(append([]string(nil), sb.env...), "PYTHONUNBUFFERED=1", "PYTHONDONTWRITEBYTECODE=1",
		"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1")
	c.Dir = "/"
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = laya.DetachAttr()
	if err := c.Start(); err != nil {
		return err
	}
	d.proc = c.Process
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			b, _ := os.ReadFile(paths.Log)
			return fmt.Errorf("laya daemon exited (%v):\n%s", err, tail(string(b), 15))
		case <-time.After(200 * time.Millisecond):
		}
		if in, err := d.c.Ping(500 * time.Millisecond); err == nil && in.Loaded {
			return nil
		} else if err == nil && in.Error != "" {
			d.stop()
			return errors.New(in.Error)
		}
	}
	d.stop()
	return errors.New("laya: model not loaded after 5 minutes")
}

func (d *layaDaemon) status() laya.Info {
	st, _ := d.c.Status(2 * time.Second)
	return st
}

func (d *layaDaemon) stop() {
	st, err := d.c.Status(2 * time.Second)
	if err == nil {
		d.info.RSSBytes, d.info.RSSPeak, d.info.MsPerItem = st.RSS, st.RSSPeak, st.MSPerItem
		d.info.Requests, d.info.Items = st.Requests, st.Items
	}
	if d.c.Shutdown(2*time.Second) != nil && d.proc != nil {
		d.proc.Kill()
	}
	for i := 0; i < 50; i++ {
		if _, err := d.c.Ping(100 * time.Millisecond); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
