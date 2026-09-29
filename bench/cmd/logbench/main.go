//go:build unix

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/iheeb1/lx/internal/laya"
	"github.com/iheeb1/lx/internal/lazyre"
)

type spec struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	tool  string
	laya  bool
	task  bool
	env   []string
}

var specs = []spec{
	{Key: "raw", Label: "raw output", tool: "raw"},
	{Key: "rtk", Label: "rtk 0.50 (its hook's rewrite)", tool: "rtk"},
	{Key: "rtk-log", Label: "rtk log <file>", tool: "rtk-log"},
	{Key: "lx", Label: "lx", tool: "lx"},
	{Key: "lx-task", Label: "lx + task", tool: "lx", task: true},
	{Key: "lx-laya", Label: "lx + laya", tool: "lx", laya: true},
	{Key: "lx-laya-task", Label: "lx + laya + task", tool: "lx", laya: true, task: true},
	// every item judged: what laya would do with no deadline
	{Key: "lx-laya-30s", Label: "lx + laya, 30 s timeout", tool: "lx", laya: true, env: []string{"LX_LAYA_TIMEOUT=30s"}},
}

type layaUse struct {
	RequestsPerRun float64    `json:"requests_per_run"`
	ItemsPerRun    float64    `json:"items_per_run"`
	Last           *laya.Last `json:"last,omitempty"`
	Folded         bool       `json:"folded"`
	FoldedRuns     int        `json:"runs_with_folds"`
	Judged         []string   `json:"judged_per_request,omitempty"`
}

type variant struct {
	Key       string `json:"key"`
	Command   string `json:"command"`
	Rewritten bool   `json:"rewritten"`
	Exit      int    `json:"exit"`
	Bytes     int    `json:"bytes"`
	Lines     int    `json:"lines"`
	Tokens    int    `json:"tokens"`
	score
	MedianMs float64  `json:"median_ms"`
	Receipt  string   `json:"receipt,omitempty"`
	Laya     *layaUse `json:"laya,omitempty"`
	File     string   `json:"file"`
}

type caseOut struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Command   string    `json:"command"`
	Task      string    `json:"task"`
	Lines     int       `json:"lines"`
	Filter    string    `json:"lx_filter"`
	Templates int       `json:"templates"`
	AllTmpl   int       `json:"templates_total,omitempty"`
	Trivial   int       `json:"templates_trivial,omitempty"`
	ErrLines  int       `json:"error_lines"`
	ErrKinds  int       `json:"error_kinds"`
	Levels    string    `json:"error_lines_from"`
	Variants  []variant `json:"variants"`
}

type report struct {
	Generated    string    `json:"generated"`
	Platform     string    `json:"platform"`
	Tokens       string    `json:"tokens"`
	LoghubCommit string    `json:"loghub_commit"`
	Lx           string    `json:"lx"`
	Rtk          string    `json:"rtk"`
	Runs         int       `json:"runs"`
	Laya         *layaInfo `json:"laya,omitempty"`
	LayaSkipped  string    `json:"laya_skipped,omitempty"`
	Variants     []spec    `json:"variants"`
	Cases        []caseOut `json:"cases"`
}

var (
	filterRe  = lazyre.New(`(?m)^lx: filter=(\S+)`)
	receiptRe = lazyre.New(`(?m)^\[lx: .*\]$`)
)

func main() {
	lxBin := flag.String("lx", "lx", "lx binary")
	rtkBin := flag.String("rtk", "rtk", "rtk binary")
	rtkEnv := flag.String("rtk-env", "", "bash file sourced before every rtk run (its sandboxed HOME, telemetry off)")
	layaPy := flag.String("laya-python", os.Getenv("LX_LAYA_PYTHON"), "python with laya installed, for the lx + laya variants (empty: skip them)")
	tokPy := flag.String("tiktoken", "python3", "python with tiktoken, for exact o200k counts")
	cache := flag.String("cache", "bench/.cache/loghub", "where loghub's samples are downloaded")
	fixDir := flag.String("fixtures", "internal/filters/infra/testdata/infra", "lx's log fixtures")
	runs := flag.Int("runs", 5, "timed runs per variant (median reported)")
	out := flag.String("out", "bench/out/logbench.json", "results file")
	only := flag.String("only", "", "regexp: only case ids matching")
	again := flag.Bool("rescore", false, "score the views saved next to -out again, without running anything")
	flag.Parse()

	var match *regexp.Regexp
	if *only != "" {
		match = regexp.MustCompile(*only)
	}
	var cases []*logCase
	for _, s := range systems {
		if match != nil && !match.MatchString(s.name) {
			continue
		}
		c, err := loadLoghub(*cache, s)
		if err != nil {
			fatal(err)
		}
		cases = append(cases, c)
	}
	for _, f := range fixtures {
		if match != nil && !match.MatchString(f.name) {
			continue
		}
		c, err := loadFixture(*fixDir, f.name, f.task)
		if err != nil {
			fatal(err)
		}
		cases = append(cases, c)
	}

	if *again {
		if err := rescore(*out, cases); err != nil {
			fatal(err)
		}
		fmt.Fprintln(os.Stderr, "rescored", *out)
		return
	}

	sb, err := newSandbox(*lxBin, *rtkBin, *rtkEnv)
	if err != nil {
		fatal(err)
	}
	var daemon atomic.Pointer[layaDaemon]
	cleanup := sync.OnceFunc(func() {
		if d := daemon.Load(); d != nil {
			d.stop()
		}
		sb.close()
	})
	defer cleanup()
	die := func(err error) {
		cleanup()
		fatal(err)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup()
		os.Exit(130)
	}()
	rep := report{Generated: time.Now().UTC().Format(time.RFC3339), Platform: runtime.GOOS + "/" + runtime.GOARCH,
		LoghubCommit: loghubCommit, Runs: *runs, Variants: specs}
	rep.Lx = strings.TrimSpace(sb.run("lx version", nil, false, 1).out)
	rep.Rtk = strings.TrimSpace(sb.run("rtk --version", nil, true, 1).out)

	var d *layaDaemon
	if *layaPy == "" {
		rep.LayaSkipped = "no -laya-python (or LX_LAYA_PYTHON)"
	} else if d, err = startLaya(sb, *layaPy, &daemon); err != nil {
		rep.LayaSkipped = err.Error()
		fmt.Fprintln(os.Stderr, "logbench: laya variants skipped:", err)
	}

	var files []string
	for i, c := range cases {
		if err := sb.useLog(c.file); err != nil {
			die(err)
		}
		sc := newScorer(c.raw, c.tr, c.isErr)
		co := caseOut{ID: c.id, Source: c.source, Command: c.shell, Task: c.task.prompt}
		co.describe(c, sc)
		sid := fmt.Sprintf("0b1e7c4a-0000-4000-8000-%012d", i+1)
		sessEnv, err := sb.session(sid, c.task, c.shell)
		if err != nil {
			die(err)
		}
		lxCmd, lxOK := sb.rewrite("lx", c.shell, false, 0)
		if lxOK {
			probe := sb.run(strings.Replace(lxCmd, "lx ", "lx -v ", 1), lxEnv(sb, c, "probe", []string{"LX_CONTEXT=0", "LX_LAYA=0"}), false, 1)
			if m := filterRe.FindStringSubmatch(probe.out); m != nil {
				co.Filter = m[1]
			}
		}
		for _, s := range specs {
			if s.laya && d == nil {
				continue
			}
			var v variant
			var r result
			switch s.tool {
			case "raw":
				v.Command = c.shell
				r = sb.run(c.shell, nil, false, *runs)
			case "rtk":
				v.Command, v.Rewritten = sb.rewrite("rtk", c.shell, true, 0, 3)
				r = sb.run(v.Command, nil, true, *runs)
			case "rtk-log":
				v.Command, v.Rewritten = "rtk log app.log", true
				r = sb.run(v.Command, nil, true, *runs)
			case "lx":
				v.Command, v.Rewritten = lxCmd, lxOK
				var extra []string
				if s.task {
					extra = append(extra, sessEnv...)
				} else {
					extra = append(extra, "LX_CONTEXT=0")
				}
				if !s.laya {
					extra = append(extra, "LX_LAYA=0")
				}
				extra = append(extra, s.env...)
				var before laya.Info
				if s.laya {
					before = d.status()
					d.judged()
				}
				r = sb.run(v.Command, lxEnv(sb, c, s.Key, extra), false, *runs)
				if s.laya {
					after := d.status()
					n := float64(max(1, *runs))
					v.Laya = &layaUse{RequestsPerRun: float64(after.Requests-before.Requests) / n,
						ItemsPerRun: float64(after.Items-before.Items) / n, Folded: layaFolded(r.out), Judged: d.judged()}
					for _, o := range r.all {
						if layaFolded(o) {
							v.Laya.FoldedRuns++
						}
					}
					if after.Requests > before.Requests {
						v.Laya.Last = after.Last
					}
				}
				if all := receiptRe.FindAllString(r.out, -1); len(all) > 0 {
					v.Receipt = all[len(all)-1]
				}
			}
			if r.timedOut {
				fmt.Fprintf(os.Stderr, "logbench: %s %s timed out after %v\n", c.id, s.Key, runTimeout)
			}
			v.Key, v.Exit, v.MedianMs = s.Key, r.exit, r.medianMs
			v.Bytes, v.Lines = len(r.out), strings.Count(r.out, "\n")
			v.score = sc.score(r.out)
			v.File = filepath.Join("logbench", c.id, s.Key+".txt")
			p := filepath.Join(filepath.Dir(*out), v.File)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				die(err)
			}
			if err := os.WriteFile(p, []byte(r.out), 0o644); err != nil {
				die(err)
			}
			files = append(files, p)
			co.Variants = append(co.Variants, v)
			fmt.Fprintf(os.Stderr, "%-20s %-13s %7d bytes  tmpl %3d/%-3d  err %3d/%-3d  %8.1f ms\n",
				c.id, s.Key, v.Bytes, v.Templates, co.Templates, v.ErrKinds, co.ErrKinds, v.MedianMs)
		}
		rep.Cases = append(rep.Cases, co)
	}
	cleanup()
	if d != nil {
		rep.Laya = &d.info
	}

	counts, counter := exactCounts(*tokPy, files)
	rep.Tokens = counter
	for i := range rep.Cases {
		for k := range rep.Cases[i].Variants {
			v := &rep.Cases[i].Variants[k]
			v.Tokens = counts[filepath.Join(filepath.Dir(*out), v.File)]
		}
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d cases to %s (%s)\n", len(rep.Cases), *out, counter)
}

func (co *caseOut) describe(c *logCase, sc *scorer) {
	co.Lines = strings.Count(strings.TrimRight(c.raw, "\n"), "\n") + 1
	co.ErrLines, co.ErrKinds, co.Levels = len(sc.errLines), len(sc.errKinds), c.levels
	co.Templates, co.AllTmpl, co.Trivial = 0, 0, 0
	if c.tr == nil {
		return
	}
	co.Templates, co.AllTmpl, co.ErrKinds = len(sc.eligible), len(c.tr.events), len(sc.errEvent)
	for _, e := range c.tr.events {
		if e.pat.trivial() {
			co.Trivial++
		}
	}
}

func rescore(out string, cases []*logCase) error {
	b, err := os.ReadFile(out)
	if err != nil {
		return err
	}
	var rep report
	if err := json.Unmarshal(b, &rep); err != nil {
		return fmt.Errorf("%s: %w", out, err)
	}
	byID := map[string]*logCase{}
	for _, c := range cases {
		byID[c.id] = c
	}
	for i := range rep.Cases {
		co := &rep.Cases[i]
		c := byID[co.ID]
		if c == nil {
			continue
		}
		sc := newScorer(c.raw, c.tr, c.isErr)
		co.describe(c, sc)
		for k := range co.Variants {
			v := &co.Variants[k]
			view, err := os.ReadFile(filepath.Join(filepath.Dir(out), v.File))
			if err != nil {
				return err
			}
			v.score = sc.score(string(view))
		}
	}
	b, _ = json.MarshalIndent(rep, "", "  ")
	return os.WriteFile(out, append(b, '\n'), 0o644)
}

// As under Claude Code. A store per variant keeps the scored view at `lx show 1`.
func lxEnv(sb *sandbox, c *logCase, key string, extra []string) []string {
	dir := filepath.Join(sb.dir, "lx", c.id, key)
	env := []string{"CLAUDECODE=1", "LX_TEE_DIR=" + filepath.Join(dir, "tee"), "LX_DATA_DIR=" + filepath.Join(dir, "data")}
	return append(env, extra...)
}

func layaFolded(out string) bool {
	return strings.Contains(out, "judged routine (laya)") || strings.Contains(out, " (routine)")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "logbench:", err)
	os.Exit(1)
}
