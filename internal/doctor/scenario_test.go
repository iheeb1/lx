package doctor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the want.txt / want.json goldens")

// scenario is testdata/<case>/case.json. Every field is optional.
type scenario struct {
	Env         map[string]string `json:"env"`          // this process's environment (Getenv and child programs)
	ConfigDir   string            `json:"config_dir"`   // relative to the root; default home/.claude
	Cwd         string            `json:"cwd"`          // relative; default "project" when it exists, else "home"
	Exe         string            `json:"exe"`          // this binary, relative; default bin/lx
	Shell       *string           `json:"shell"`        // what `command -v lx` prints (@ROOT@ expanded); default @ROOT@/bin/lx
	ShellBanner string            `json:"shell_banner"` // what the shell's startup files print first
	ShellError  string            `json:"shell_error"`  // the shell cannot be run
	Bins        map[string]string `json:"bins"`         // more fake lx copies: dir → version line
	Symlinks    map[string]string `json:"symlinks"`     // link → target, both relative
	GitRoots    []string          `json:"git"`          // directories to give a .git entry, relative
	NeverRun    []string          `json:"never_run"`    // programs doctor must not execute, relative
	TeeForeign  int               `json:"tee_foreign"`  // files in the run store that lx did not write
	TeeLocked   bool              `json:"tee_unwritable"`
	TeeRuns     int               `json:"tee_runs"`      // stored runs to fake
	TeeSparseMB int64             `json:"tee_sparse_mb"` // plus one sparse run of this size
	HistoryAges []float64         `json:"history_hours"` // run records, hours before Now
	Transcript  *float64          `json:"transcript_hours"`
	LatencyMs   []int             `json:"latency_ms"` // what the fake clock measures for each hook run (the last repeats); default 7
	HookMode    string            `json:"hook_mode"`  // FAKE_LX_HOOK for the fake lx
	Path        string            `json:"path"`       // PATH as Getenv reports it (@ROOT@ expanded)
}

const fakeShell = "/fake/bin/zsh"

var testNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

type harness struct {
	t     *testing.T
	root  string
	sc    scenario
	env   Env
	mu    sync.Mutex
	calls []string
}

// setup copies testdata/<name> into a temp root, expands @ROOT@ in its
// JSON files, installs the fake lx and builds the Env.
func setup(t *testing.T, name string) *harness {
	t.Helper()
	src := filepath.Join("testdata", name)
	root, err := filepath.EvalSymlinks(t.TempDir()) // macOS: /var → /private/var
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: root}
	if b, err := os.ReadFile(filepath.Join(src, "case.json")); err == nil {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&h.sc); err != nil {
			t.Fatalf("%s/case.json: %v", name, err)
		}
	}
	sc := &h.sc
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ents {
		if d.IsDir() {
			copyTree(t, filepath.Join(src, d.Name()), filepath.Join(root, d.Name()), root)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.installLx("bin", "lx 0.2.0-test")
	for dir, v := range sc.Bins {
		h.installLx(dir, v)
	}
	for _, d := range sc.GitRoots {
		if err := os.MkdirAll(filepath.Join(root, d, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range sc.Symlinks {
		l := filepath.Join(root, link)
		if err := os.MkdirAll(filepath.Dir(l), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, target), l); err != nil {
			t.Fatal(err)
		}
	}

	e := Env{
		Home:        filepath.Join(root, "home"),
		ConfigDir:   filepath.Join(root, "home", ".claude"),
		ManagedPath: filepath.Join(root, "managed", "managed-settings.json"),
		Executable:  filepath.Join(root, "bin", "lx"),
		Version:     "lx 0.2.0-test",
		TeeDir:      filepath.Join(root, "cache", "lx", "runs"),
		HistoryPath: filepath.Join(root, "data", "lx", "history.jsonl"),
		Now:         testNow,
	}
	if sc.ConfigDir != "" {
		e.ConfigDir = filepath.Join(root, sc.ConfigDir)
	}
	if sc.Exe != "" {
		e.Executable = filepath.Join(root, sc.Exe)
	}
	switch {
	case sc.Cwd != "":
		e.Cwd = filepath.Join(root, sc.Cwd)
	case exists(filepath.Join(root, "project")):
		e.Cwd = filepath.Join(root, "project")
	default:
		e.Cwd = e.Home
	}
	if err := os.MkdirAll(e.Cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	penv := map[string]string{"SHELL": fakeShell, "PATH": "/usr/bin:/bin"}
	if sc.Path != "" {
		penv["PATH"] = h.expand(sc.Path)
	}
	for k, v := range sc.Env {
		penv[k] = h.expand(v)
	}
	e.Getenv = func(k string) string { return penv[k] }
	e.ProjectDir = FindProjectDir(e.Cwd, e.Home, e.ConfigDir, e.Getenv)

	h.storage(&e)

	// Programs see the same environment Getenv reports.
	child := []string{"PATH=" + penv["PATH"], "HOME=" + e.Home, "FAKE_LX_HOOK=" + sc.HookMode}
	for k, v := range sc.Env {
		child = append(child, k+"="+h.expand(v))
	}
	real := ExecWith(child)
	e.Exec = func(ctx context.Context, name string, args []string, stdin string) (string, error) {
		h.mu.Lock()
		h.calls = append(h.calls, strings.Join(append([]string{name}, args...), " "))
		h.mu.Unlock()
		if name == fakeShell {
			banner := "welcome to zsh\n" + h.expand(sc.ShellBanner)
			switch {
			case sc.ShellError != "":
				return "", errors.New(sc.ShellError)
			case sc.Shell == nil:
				return banner + beginMark + "\n" + filepath.Join(root, "bin", "lx") + "\n", nil
			case *sc.Shell == "":
				return banner + beginMark + "\n" + notFoundMark + "\n", nil
			}
			return banner + "\x1b]1337;SetUserVar=x\x07" + beginMark + "\r\n" + h.expand(*sc.Shell) + "\r\n", nil
		}
		return real(ctx, name, args, stdin)
	}
	lats := sc.LatencyMs
	if len(lats) == 0 {
		lats = []int{7}
	}
	var clockMu sync.Mutex
	tick := testNow
	calls := 0
	e.Clock = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		calls++
		if calls%2 == 0 { // the second reading of each run
			run := min(calls/2-1, len(lats)-1)
			tick = tick.Add(time.Duration(lats[run]) * time.Millisecond)
		}
		return tick
	}
	h.env = e
	return h
}

func (h *harness) expand(s string) string { return strings.ReplaceAll(s, "@ROOT@", h.root) }

var (
	masterMu  sync.Mutex
	masterDir string
	masters   = map[string]string{}
)

func TestMain(m *testing.M) {
	flag.Parse()
	code := m.Run()
	if masterDir != "" {
		os.RemoveAll(masterDir)
	}
	os.Exit(code)
}

// fakeLx returns a copy of testdata/fakelx/lx for key, executed once.
// Scenarios hard-link it: macOS scans every new executable on its first
// run (a few hundred ms), which would make timeouts flaky. Each key gets
// its own file, so "another lx" is never os.SameFile as this one.
func fakeLx(key string) (string, error) {
	masterMu.Lock()
	defer masterMu.Unlock()
	if p, ok := masters[key]; ok {
		return p, nil
	}
	script, err := os.ReadFile(filepath.Join("testdata", "fakelx", "lx"))
	if err != nil {
		return "", err
	}
	if masterDir == "" {
		if masterDir, err = os.MkdirTemp("", "lx-doctor-fake-"); err != nil {
			return "", err
		}
	}
	p := filepath.Join(masterDir, strings.ReplaceAll(key, "/", "_")+"-lx")
	if err := os.WriteFile(p, script, 0o755); err != nil {
		return "", err
	}
	_ = exec.Command(p, "warm-up").Run()
	masters[key] = p
	return p, nil
}

func (h *harness) installLx(dir, version string) {
	h.t.Helper()
	d := filepath.Join(h.root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		h.t.Fatal(err)
	}
	master, err := fakeLx(dir)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.Link(master, filepath.Join(d, "lx")); err != nil {
		script, err := os.ReadFile(master)
		if err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "lx"), script, 0o755); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// storage fakes the run store, the history and a transcript.
func (h *harness) storage(e *Env) {
	t, sc := h.t, h.sc
	if sc.TeeRuns > 0 || sc.TeeLocked || sc.TeeSparseMB > 0 || sc.TeeForeign > 0 {
		if err := os.MkdirAll(e.TeeDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= sc.TeeForeign; i++ {
		if err := os.WriteFile(filepath.Join(e.TeeDir, "notes-"+itoa(i)+".txt"), []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= sc.TeeRuns; i++ {
		base := filepath.Join(e.TeeDir, itoa(i))
		if err := os.WriteFile(base+".log", bytes.Repeat([]byte("x"), 1000*i), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".json", []byte(`{"id":`+itoa(i)+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if sc.TeeSparseMB > 0 {
		if err := os.MkdirAll(e.TeeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(e.TeeDir, "99.log"), os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(sc.TeeSparseMB << 20); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if sc.TeeLocked {
		if err := os.Chmod(e.TeeDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(e.TeeDir, 0o700) })
	}
	if len(sc.HistoryAges) > 0 {
		var b strings.Builder
		for _, age := range sc.HistoryAges {
			ts := testNow.Add(-time.Duration(age * float64(time.Hour))).Unix()
			b.WriteString(`{"t":` + itoa(int(ts)) + `,"cmd":"git status","filter":"git-status","raw":100,"out":20,"ms":5,"exit":0}` + "\n")
		}
		b.WriteString(`{"t":` + itoa(int(testNow.Unix())) + `,"kind":"show","cmd":"git status","of":1,"mode":"full","out":90}` + "\n")
		if err := os.MkdirAll(filepath.Dir(e.HistoryPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.HistoryPath, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if sc.Transcript != nil {
		p := filepath.Join(e.ConfigDir, "projects", "-Users-me-app", "sess-1.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"never":"read"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := testNow.Add(-time.Duration(*sc.Transcript * float64(time.Hour)))
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

// copyTree copies src to dst, expanding @ROOT@ in .json files.
func copyTree(t *testing.T, src, dst, root string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".json") {
			b = bytes.ReplaceAll(b, []byte("@ROOT@"), []byte(root))
		}
		return os.WriteFile(out, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// fileState is a file's identity for the no-write check.
type fileState struct {
	sum   [32]byte
	mtime time.Time
	mode  fs.FileMode
	link  string
}

// snapshot records every file and symlink under the root (directory
// mtimes are not compared: the run store's changes with the write probe).
func (h *harness) snapshot() map[string]fileState {
	h.t.Helper()
	m := map[string]fileState{}
	err := filepath.WalkDir(h.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := fileState{mtime: fi.ModTime(), mode: fi.Mode()}
		if fi.Mode()&fs.ModeSymlink != 0 {
			st.link, _ = os.Readlink(p)
		} else if fi.Size() < 8<<20 { // big (sparse) run files: size and mtime suffice
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			st.sum = sha256.Sum256(b)
		}
		m[p] = st
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func (h *harness) normalize(s string) string {
	return strings.ReplaceAll(s, h.root, "$ROOT")
}

func caseNames(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() && exists(filepath.Join("testdata", e.Name(), "want.txt")) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func TestScenarios(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake lx is a POSIX shell script")
	}
	names := caseNames(t)
	if len(names) < 15 {
		t.Fatalf("only %d scenarios, want at least 15", len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := setup(t, name)
			if h.sc.TeeLocked && os.Geteuid() == 0 {
				t.Skip("root can write anywhere")
			}
			before := h.snapshot()

			var out, errb bytes.Buffer
			code := Main(nil, &out, &errb, h.env)
			if errb.Len() > 0 {
				t.Errorf("stderr: %s", errb.String())
			}
			got := h.normalize(out.String())
			golden(t, filepath.Join("testdata", name, "want.txt"), got)

			// Exit code agrees with the report.
			failed := strings.Contains(got, "\n✗ ") || strings.HasPrefix(got, "✗ ")
			if (code == 1) != failed || (code != 0 && code != 1) {
				t.Errorf("exit %d, but failures shown: %v", code, failed)
			}

			// Read-only: every file under the root is byte-identical with
			// the same mtime and mode; nothing was added or removed.
			after := h.snapshot()
			for p, st := range before {
				a, ok := after[p]
				switch {
				case !ok:
					t.Errorf("doctor removed %s", p)
				case a != st:
					t.Errorf("doctor modified %s", p)
				}
			}
			for p := range after {
				if _, ok := before[p]; !ok {
					t.Errorf("doctor created %s", p)
				}
			}
			if h.sc.TeeRuns > 0 || h.sc.TeeLocked || h.sc.TeeForeign > 0 {
				want := 2*h.sc.TeeRuns + h.sc.TeeForeign
				if h.sc.TeeSparseMB > 0 {
					want++
				}
				if ents, _ := os.ReadDir(h.env.TeeDir); len(ents) != want {
					t.Errorf("run store has %d entries after doctor, want %d", len(ents), want)
				}
			}

			// No program ever ran a command string doctor could not verify,
			// nor anything inside the project.
			for _, c := range h.calls {
				if strings.Contains(c, "SENTINEL") {
					t.Errorf("doctor executed an unverified hook: %s", c)
				}
				prog := c
				if strings.HasPrefix(prog, filepath.Join(h.root, "project")+"/") {
					t.Errorf("doctor executed a program inside the project: %s", c)
				}
				for _, p := range h.sc.NeverRun {
					if strings.HasPrefix(prog, h.root+"/"+p+" ") { // not Join: keep any ../
						t.Errorf("doctor executed %s: %s", p, c)
					}
				}
			}
			if exists(filepath.Join(h.root, "SENTINEL")) {
				t.Errorf("the sentinel file exists: an unverified hook ran")
			}

			if want := filepath.Join("testdata", name, "want.json"); exists(want) || name == "user-hook" {
				var js bytes.Buffer
				jcode := Main([]string{"--json"}, &js, &errb, h.env)
				golden(t, want, h.normalize(js.String()))
				if jcode != code {
					t.Errorf("--json exit %d, text exit %d", jcode, code)
				}
				var r Report
				if err := json.Unmarshal(js.Bytes(), &r); err != nil {
					t.Fatalf("--json output is not JSON: %v", err)
				}
				if r.Failed() != (code == 1) {
					t.Errorf("JSON report Failed()=%v, exit %d", r.Failed(), code)
				}
			}
		})
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs\n--- want\n%s--- got\n%s", path, want, got)
	}
}
