//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/tokens"
)

const runTimeout = 3 * time.Minute

// honours --tail: rtk asks docker and kubectl for the last 100 lines
const shim = `#!/bin/sh
n=
while [ $# -gt 0 ]; do
  case "$1" in
    --tail|-n) n=$2; shift ;;
    --tail=*) n=${1#--tail=} ;;
  esac
  shift
done
if [ -n "$n" ] && [ "$n" != all ] && [ "$n" != -1 ]; then exec tail -n "$n" "$LOGBENCH_FILE"; fi
exec cat "$LOGBENCH_FILE"
`

type sandbox struct {
	dir, bin, home, work, claude string
	path                         string
	env                          []string
	rtkEnv                       string
}

func newSandbox(lx, rtk, rtkEnv string) (_ *sandbox, err error) {
	// short: the laya socket lives under it and sun_path holds 104 bytes
	dir, err := os.MkdirTemp("", "lxb")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	sb := &sandbox{dir: dir, bin: filepath.Join(dir, "bin"), home: filepath.Join(dir, "h"),
		work: filepath.Join(dir, "w"), claude: filepath.Join(dir, "claude"), rtkEnv: rtkEnv}
	for _, d := range []string{sb.bin, sb.home, sb.work, sb.claude} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"docker", "kubectl", "journalctl"} {
		if err := os.WriteFile(filepath.Join(sb.bin, name), []byte(shim), 0o755); err != nil {
			return nil, err
		}
	}
	for name, target := range map[string]string{"lx": lx, "rtk": rtk} {
		if target == "" {
			continue
		}
		abs, err := exec.LookPath(target)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if abs, err = filepath.Abs(abs); err != nil {
			return nil, err
		}
		if err := os.Symlink(abs, filepath.Join(sb.bin, name)); err != nil {
			return nil, err
		}
	}
	realHome, _ := os.UserHomeDir()
	hf := os.Getenv("HF_HOME")
	if hf == "" {
		hf = filepath.Join(realHome, ".cache", "huggingface")
		if x := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(x) {
			hf = filepath.Join(x, "huggingface")
		}
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "CLAUDE"), strings.HasPrefix(k, "CODEX"), strings.HasPrefix(k, "LX_"),
			strings.HasPrefix(k, "RTK_"), strings.HasPrefix(k, "XDG_"), k == "BASH_MAX_OUTPUT_LENGTH", k == "HOME",
			k == "PATH", k == "PWD", k == "HF_HOME":
			continue
		}
		sb.env = append(sb.env, kv)
	}
	sb.path = sb.bin + ":" + os.Getenv("PATH")
	sb.env = append(sb.env, "HOME="+sb.home, "PATH="+sb.path, "HF_HOME="+hf, "LX_TRACK=0", "RTK_TELEMETRY_DISABLED=1")
	return sb, nil
}

func (sb *sandbox) close() { os.RemoveAll(sb.dir) }

func (sb *sandbox) useLog(file string) error {
	p := filepath.Join(sb.work, "app.log")
	os.Remove(p)
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	return os.Symlink(abs, p)
}

type result struct {
	out      string
	all      []string
	exit     int
	medianMs float64
	timedOut bool
}

func (sb *sandbox) run(cmd string, env []string, rtk bool, runs int) result {
	script := cmd
	if rtk && sb.rtkEnv != "" {
		script = fmt.Sprintf("source %s >/dev/null 2>&1; export PATH=%s; %s", shellQuote(sb.rtkEnv), shellQuote(sb.path), cmd)
	}
	var res result
	var times []float64
	var timedOut atomic.Bool
	for i := 0; i < max(1, runs) && !timedOut.Load(); i++ {
		c := exec.Command("bash", "-c", script)
		c.Dir = sb.work
		c.Env = append(append(append([]string(nil), sb.env...), "PWD="+sb.work, "LOGBENCH_FILE="+filepath.Join(sb.work, "app.log")), env...)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var buf bytes.Buffer
		c.Stdout, c.Stderr = &buf, &buf
		start := time.Now()
		if err := c.Start(); err != nil {
			return result{out: err.Error(), exit: 127}
		}
		t := time.AfterFunc(runTimeout, func() {
			timedOut.Store(true)
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		})
		err := c.Wait()
		t.Stop()
		times = append(times, float64(time.Since(start).Microseconds())/1000)
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		res.all = append(res.all, buf.String())
		if i == 0 {
			res.out, res.exit = buf.String(), code
		}
	}
	if timedOut.Load() {
		res.timedOut, res.medianMs = true, -1
		return res
	}
	sort.Float64s(times)
	res.medianMs = times[len(times)/2]
	return res
}

func (sb *sandbox) rewrite(tool, cmd string, rtk bool, ok ...int) (string, bool) {
	r := sb.run(fmt.Sprintf("%s rewrite %s", tool, shellQuote(cmd)), nil, rtk, 1)
	for _, k := range ok {
		if r.exit == k {
			s := strings.TrimSpace(r.out)
			return s, s != "" && s != cmd
		}
	}
	return cmd, false
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (sb *sandbox) session(id string, t task, shell string) ([]string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	lines := []any{
		map[string]any{"type": "ai-title", "aiTitle": t.title},
		map[string]any{"type": "user", "uuid": "u1", "timestamp": now,
			"message": map[string]any{"role": "user", "content": t.prompt}},
		map[string]any{"type": "assistant", "uuid": "a1", "timestamp": now, "message": map[string]any{
			"id": "m1", "model": "claude-sonnet-4-5",
			"usage": map[string]int{"input_tokens": 2000, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 18000},
			"content": []any{
				map[string]string{"type": "text", "text": "Let me read the logs to find out " + t.clause + "."},
				map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]string{"command": shell}},
			}}},
	}
	var b bytes.Buffer
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			return nil, err
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	dirs := []string{sb.work}
	if real, err := filepath.EvalSymlinks(sb.work); err == nil && real != sb.work {
		dirs = append(dirs, real)
	}
	for _, d := range dirs {
		p := filepath.Join(sb.claude, "projects", agentctx.Slug(d), id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
			return nil, err
		}
	}
	return []string{"CLAUDE_CONFIG_DIR=" + sb.claude, "CLAUDE_CODE_SESSION_ID=" + id}, nil
}

const tiktokenScript = `import json, sys, tiktoken
enc = tiktoken.get_encoding("o200k_base")
out = {}
for p in json.load(sys.stdin):
    with open(p, encoding="utf-8", errors="replace") as f:
        out[p] = len(enc.encode(f.read(), disallowed_special=()))
json.dump({"version": tiktoken.__version__, "counts": out}, sys.stdout)
`

func exactCounts(py string, files []string) (map[string]int, string) {
	in, _ := json.Marshal(files)
	c := exec.Command(py, "-c", tiktokenScript)
	c.Stdin = bytes.NewReader(in)
	var errb bytes.Buffer
	c.Stderr = &errb
	out, err := c.Output()
	var res struct {
		Version string         `json:"version"`
		Counts  map[string]int `json:"counts"`
	}
	if err == nil && json.Unmarshal(out, &res) == nil && len(res.Counts) == len(files) {
		return res.Counts, "o200k_base (tiktoken " + res.Version + ")"
	}
	fmt.Fprintf(os.Stderr, "logbench: no tiktoken in %s (%v %s); falling back to lx's estimator\n", py, err, strings.TrimSpace(errb.String()))
	counts := map[string]int{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		counts[f] = tokens.Count(string(b))
	}
	return counts, "lx estimator (tiktoken unavailable)"
}

// follows symlinks: huggingface's snapshots point into its blob store
func dirSize(root string) int64 {
	var n int64
	seen := map[string]bool{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil || seen[real] {
			return nil
		}
		seen[real] = true
		if fi, err := os.Stat(real); err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return n
}
