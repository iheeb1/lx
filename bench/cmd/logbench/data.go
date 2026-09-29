//go:build unix

package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// master on 2025-06-13; research-only license, so fetched at bench time, never committed
const loghubCommit = "dd61d0952749ee7963bde24220d1be5ede023033"

type task struct{ title, prompt, clause string }

type system struct {
	name string
	task task
}

var systems = []system{
	{"HDFS", task{"Investigate HDFS block transfer failures", "Why are HDFS block transfers failing?", "why HDFS block transfers are failing"}},
	{"Hadoop", task{"Debug the failed MapReduce job", "Why did the MapReduce job fail?", "why the MapReduce job failed"}},
	{"Spark", task{"Investigate a slow Spark job", "Why is this Spark job slow?", "why this Spark job is slow"}},
	{"Zookeeper", task{"ZooKeeper connection drops", "Why do the ZooKeeper nodes keep losing their connections?", "why the ZooKeeper nodes keep losing their connections"}},
	{"BGL", task{"BlueGene/L kernel failures", "What is causing the kernel failures on the BlueGene/L nodes?", "what is causing the kernel failures on the BlueGene/L nodes"}},
	{"HPC", task{"Unhealthy HPC nodes", "Which HPC nodes are unhealthy, and why?", "which HPC nodes are unhealthy and why"}},
	{"Thunderbird", task{"Thunderbird node errors", "Why are the Thunderbird cluster nodes reporting errors?", "why the Thunderbird nodes are reporting errors"}},
	{"Linux", task{"Suspicious logins on a Linux server", "Is someone trying to break into this server?", "whether someone is trying to break into this server"}},
	{"Android", task{"Android screen wakeups", "Why does the phone's screen keep waking up?", "why the screen keeps waking up"}},
	{"HealthApp", task{"Step count not updating", "Why is the step count not updating?", "why the step count is not updating"}},
	{"Apache", task{"Apache errors", "Why is Apache logging errors?", "why Apache is logging errors"}},
	{"OpenSSH", task{"SSH brute-force attempts", "Who is attacking the SSH server?", "who is attacking the SSH server"}},
	{"OpenStack", task{"Nova instance problems", "Why are Nova instances misbehaving?", "why Nova instances are misbehaving"}},
	{"Mac", task{"Mac network drops after sleep", "Why does the Mac lose its network connection after sleep?", "why the Mac loses its network connection after sleep"}},
}

var fixtures = []struct {
	name string
	task task
}{
	{"docker-compose-logs", task{"Worker email failures", "Why are the worker's emails failing?", "why the worker's emails are failing"}},
	{"docker-logs-node", task{"Checkout request failures", "Why are checkout requests failing?", "why checkout requests are failing"}},
	{"docker-logs-real", task{"Orders endpoint errors", "Why is GET /api/orders returning 500?", "why GET /api/orders returns 500"}},
	{"journalctl-unit", task{"shop-api crash", "Why did shop-api crash this morning?", "why shop-api crashed this morning"}},
	{"kubectl-logs-panic", task{"orders pod crash loop", "Why is the orders pod crash-looping?", "why the orders pod is crash-looping"}},
}

type logCase struct {
	id, source, shell string
	file              string
	task              task
	raw               string
	tr                *truth
	isErr             []bool
	levels            string
}

func fetch(cache, name string) error {
	for _, f := range []string{name + "_2k.log", name + "_2k.log_structured.csv"} {
		p := filepath.Join(cache, f)
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			continue
		}
		if err := os.MkdirAll(cache, 0o755); err != nil {
			return err
		}
		url := fmt.Sprintf("https://raw.githubusercontent.com/logpai/loghub/%s/%s/%s", loghubCommit, name, f)
		fmt.Fprintln(os.Stderr, "fetch", url)
		tmp := p + ".part"
		c := exec.Command("curl", "-fsSL", "--retry", "2", "-o", tmp, url)
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("downloading %s: %w", url, err)
		}
		if err := os.Rename(tmp, p); err != nil {
			return err
		}
	}
	return nil
}

func loadLoghub(cache string, s system) (*logCase, error) {
	if err := fetch(cache, s.name); err != nil {
		return nil, err
	}
	logPath := filepath.Join(cache, s.name+"_2k.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	raw := string(b)
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	f, err := os.Open(filepath.Join(cache, s.name+"_2k.log_structured.csv"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.TrimPrefix(h, "\ufeff")] = i
	}
	idCol, okID := col["EventId"]
	tCol, okT := col["EventTemplate"]
	lvCol, hasLevel := col["Level"]
	if !okID || !okT {
		return nil, fmt.Errorf("%s: no EventId/EventTemplate columns", s.name)
	}
	tr := &truth{}
	byID := map[string]*event{}
	var levels []string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
		if len(rec) <= max(idCol, tCol) {
			return nil, fmt.Errorf("%s: short row %q", s.name, rec)
		}
		e := byID[rec[idCol]]
		if e == nil {
			e = &event{ID: rec[idCol], Template: rec[tCol], pat: compile(rec[tCol])}
			byID[e.ID] = e
			tr.events = append(tr.events, e)
		}
		e.Count++
		tr.line = append(tr.line, e)
		if hasLevel && lvCol < len(rec) {
			levels = append(levels, rec[lvCol])
		}
	}
	if len(tr.line) != len(lines) {
		return nil, fmt.Errorf("%s: %d log lines but %d structured rows", s.name, len(lines), len(tr.line))
	}
	isErr, from := levelErrors(levels, lines)
	return &logCase{id: s.name, source: "loghub", shell: "docker logs app", file: logPath, task: s.task,
		raw: raw, tr: tr, isErr: isErr, levels: from}, nil
}

func loadFixture(dir, name string, t task) (*logCase, error) {
	p := filepath.Join(dir, name+".txt")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var meta struct {
		Shell string `json:"shell"`
	}
	mb, err := os.ReadFile(filepath.Join(dir, name+".meta.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(mb, &meta); err != nil || meta.Shell == "" {
		return nil, fmt.Errorf("%s: no shell in meta.json (%v)", name, err)
	}
	raw := string(b)
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	isErr, from := levelErrors(nil, lines)
	return &logCase{id: name, source: "fixture", shell: meta.Shell, file: p, task: t, raw: raw, isErr: isErr, levels: from}, nil
}
