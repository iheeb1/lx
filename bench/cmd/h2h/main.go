//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

type Meta struct {
	Shell    string `json:"shell"`
	Cwd      string `json:"cwd"`
	Repo     string `json:"repo"`
	ExitCode int    `json:"exit_code"`
	EnvSetup string `json:"env_setup"`
}

type Variant struct {
	Command   string  `json:"command"`
	Rewritten bool    `json:"rewritten"`
	TimedOut  bool    `json:"timed_out,omitempty"`
	Exit      int     `json:"exit"`
	Tokens    int     `json:"tokens"`
	Bytes     int     `json:"bytes"`
	ErrKept   int     `json:"error_lines_kept"`
	MedianMs  float64 `json:"median_ms"`
	File      string  `json:"file"`
}

type Row struct {
	ID       string  `json:"id"`
	Category string  `json:"category"`
	Shell    string  `json:"shell"`
	ErrLines int     `json:"error_lines"`
	Raw      Variant `json:"raw"`
	Rtk      Variant `json:"rtk"`
	Lx       Variant `json:"lx"`

	Excluded string `json:"excluded,omitempty"`
}

const runTimeout = 3 * time.Minute

var include = regexp.MustCompile(`^(git (status|log|diff|show|branch|blame)|go (test|build|vet|list)|npx (jest|vitest|tsc|eslint|mocha)|npm (test|run build|ls|outdated)|pytest|python3? -m pytest|grep|rg|find|ls|du|cat|make|tsc|eslint|jest|vitest|mocha)\b`)
var exclude = regexp.MustCompile(`install|uninstall|create|push|pull|fetch|clone|merge|download|tidy|get -u|curl|audit|outdated`)

func main() {
	envFile := flag.String("env", "", "bash file sourced before every run (corpus env)")
	metaDir := flag.String("meta", "", "corpus out dir with <cat>/<name>.meta.json")
	rtk := flag.String("rtk", "rtk", "rtk binary")
	lx := flag.String("lx", "lx", "lx binary")
	runs := flag.Int("runs", 5, "timed runs per variant (median reported)")
	out := flag.String("out", "bench/out/h2h.json", "results file")
	only := flag.String("only", "", "regexp: only case ids matching")
	flag.Parse()

	metas, _ := filepath.Glob(filepath.Join(*metaDir, "*", "*.meta.json"))
	sort.Strings(metas)
	outDir := filepath.Join(filepath.Dir(*out), "h2h")
	var rows []Row
	for _, mp := range metas {
		var m Meta
		b, err := os.ReadFile(mp)
		if err != nil || json.Unmarshal(b, &m) != nil {
			continue
		}
		cat := filepath.Base(filepath.Dir(mp))
		id := cat + "/" + strings.TrimSuffix(filepath.Base(mp), ".meta.json")
		if strings.HasSuffix(id, "-color") || !include.MatchString(m.Shell) || exclude.MatchString(m.Shell) ||
			strings.ContainsAny(m.Shell, "|;&<>$`") && !strings.HasSuffix(m.Shell, "2>&1") {
			continue
		}
		if *only != "" && !regexp.MustCompile(*only).MatchString(id) {
			continue
		}
		cwd := m.Cwd
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(filepath.Dir(*metaDir), cwd)
		}
		setup := strings.ReplaceAll(m.EnvSetup, "$CORPUS", filepath.Dir(*metaDir))
		row := Row{ID: id, Category: cat, Shell: m.Shell}
		rawCmd := m.Shell
		rtkCmd, rtkOK := rewrite(*envFile, *rtk, m.Shell, []int{0, 3})
		lxCmd, lxOK := rewrite(*envFile, *lx, m.Shell, []int{0})
		if !rtkOK {
			rtkCmd = rawCmd
		}
		if !lxOK {
			lxCmd = rawCmd
		}
		fmt.Fprintf(os.Stderr, "%-45s rtk:%-5v lx:%-5v\n", id, rtkOK, lxOK)
		rawOut, rawV := run(*envFile, setup, cwd, rawCmd, *runs)
		clean := textutil.Clean(rawOut)
		row.ErrLines = countErrors(clean)
		row.Raw = finish(rawV, rawOut, clean, row.ErrLines, false, outDir, id, "raw")
		if rawV.TimedOut {
			fmt.Fprintf(os.Stderr, "  skipped: raw run timed out after %v\n", runTimeout)
			continue
		}
		o, v := run(*envFile, setup, cwd, rtkCmd, *runs)
		row.Rtk = finish(v, o, clean, row.ErrLines, rtkOK, outDir, id, "rtk")
		row.Excluded = launchFailure("rtk", o, rawOut)
		o, v = run(*envFile, setup, cwd, lxCmd, *runs)
		row.Lx = finish(v, o, clean, row.ErrLines, lxOK, outDir, id, "lx")
		if row.Excluded == "" {
			row.Excluded = launchFailure("lx", o, rawOut)
		}
		rows = append(rows, row)
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %d cases to %s\n", len(rows), *out)
}

func rewrite(envFile, bin, cmd string, okCodes []int) (string, bool) {
	c := exec.Command("bash", "-c", fmt.Sprintf("source %q >/dev/null 2>&1; %q rewrite %q", envFile, bin, cmd))
	outb, err := c.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return "", false
	}
	for _, k := range okCodes {
		if code == k {
			s := strings.TrimSpace(string(outb))

			name := filepath.Base(bin)
			s = regexp.MustCompile(`(^|&& |; |\| )`+regexp.QuoteMeta(name)+` `).ReplaceAllString(s, "${1}"+bin+" ")
			return s, s != "" && s != cmd
		}
	}
	return "", false
}

func run(envFile, setup, cwd, cmd string, runs int) (string, Variant) {
	script := fmt.Sprintf("source %q >/dev/null 2>&1; cd %q && %s\nexec bash -c %q", envFile, cwd, setup, cmd)
	var out string
	var times []float64
	exit := 0
	var timedOut atomic.Bool
	for i := 0; i < max(1, runs) && !timedOut.Load(); i++ {
		c := exec.Command("bash", "-c", script)
		c.Env = append(os.Environ(), "LX_TRACK=0", "RTK_TELEMETRY_DISABLED=1")

		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var buf bytes.Buffer
		c.Stdout, c.Stderr = &buf, &buf
		start := time.Now()
		if err := c.Start(); err != nil {
			return err.Error(), Variant{Command: cmd, Exit: 127}
		}
		t := time.AfterFunc(runTimeout, func() {
			timedOut.Store(true)
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		})
		err := c.Wait()
		t.Stop()
		times = append(times, float64(time.Since(start).Microseconds())/1000)
		exit = 0
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		}
		if i == 0 {
			out = buf.String()
		}
	}
	if timedOut.Load() {
		return out, Variant{Command: cmd, Exit: -1, TimedOut: true, MedianMs: -1}
	}
	sort.Float64s(times)
	return out, Variant{Command: cmd, Exit: exit, MedianMs: times[len(times)/2]}
}

func finish(v Variant, out, rawClean string, errLines int, rewritten bool, dir, id, kind string) Variant {
	v.Rewritten = rewritten
	v.Bytes = len(out)
	v.Tokens = tokens.Count(out)
	v.ErrKept = errLines - len(fixture.ErrorMessagesMissing(rawClean, textutil.Clean(out)))
	p := filepath.Join(dir, id, kind+".txt")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(out), 0o644)
	v.File = filepath.Join("h2h", id, kind+".txt")
	return v
}

func countErrors(s string) int { return len(fixture.ErrorMessagesMissing(s, "")) }

func launchFailure(tool, out, raw string) string {
	for _, m := range []string{tool + ": Failed to run", "could not determine executable to run", tool + ": command not found"} {
		if strings.Contains(out, m) && !strings.Contains(raw, m) {
			return tool + " could not launch the command in this environment: " + m
		}
	}
	return ""
}
