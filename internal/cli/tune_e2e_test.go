//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/track"
)

func tuneLx(t *testing.T, dir string, env []string, agent bool, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LX_") || strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "BASH_MAX_OUTPUT_LENGTH=") {
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(append(cmd.Env, "LX_TEST_MAIN=1"), env...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	var f *os.File
	if agent {
		var err error
		if f, err = os.CreateTemp(t.TempDir(), "tool-result-*"); err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = f, f
	}
	err := cmd.Run()
	if f != nil {
		b, _ := os.ReadFile(f.Name())
		f.Close()
		o.Write(b)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			exit = 128 + int(ws.Signal())
		}
	} else if err != nil {
		t.Fatal(err)
	}
	return o.String(), e.String(), exit
}

var tuneReceiptRe = regexp.MustCompile(`\n\[lx: [\d,]+→[\d,]+ lines \(−\d+%\) · full output: lx show (\d+)(.*)\]\n$`)

func TestTuneEndToEnd(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "secret-project")
	bin := filepath.Join(base, "fakebin")
	data := filepath.Join(base, "data")
	for _, d := range []string{filepath.Join(proj, ".git"), filepath.Join(proj, "deep-subdir"), bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	outFile := filepath.Join(base, "out.txt")
	script := "#!/bin/sh\ncat \"$FAKE_OUT\"\necho 'ERROR batch 97 failed: connection reset by peer' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "faketool"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"LX_DATA_DIR=" + data, "LX_TEE_DIR=" + filepath.Join(base, "runs"), "FAKE_OUT=" + outFile,
		"LX_MAX_CHARS=0", "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off",
	}
	setOut := func(s string) {
		if err := os.WriteFile(outFile, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lx := func(dir string, extra []string, args ...string) (string, string, int) {
		t.Helper()
		return tuneLx(t, dir, append(append([]string(nil), env...), extra...), false, args...)
	}

	show := func(dir string, args ...string) {
		t.Helper()
		out, _, code := tuneLx(t, dir, env, true, append([]string{"show"}, args...)...)
		if code != 0 || !strings.HasPrefix(out, "[lx show ") {
			t.Fatalf("lx show %v (%d): %.300s", args, code, out)
		}
	}

	condensed := func(dir string, extra ...string) (id, note string) {
		t.Helper()
		out, errOut, code := lx(dir, extra, "faketool", "--nightly")
		if code != 3 {
			t.Fatalf("exit %d, want the command's 3\n%s", code, errOut)
		}
		m := tuneReceiptRe.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("not a condensed view:\n%s", out[max(0, len(out)-400):])
		}
		if !strings.Contains(out+errOut, "ERROR batch 97 failed") {
			t.Fatal("the error line is missing")
		}
		return m[1], m[2]
	}
	whole := func(dir string) {
		t.Helper()
		out, errOut, code := lx(dir, nil, "faketool", "--nightly")
		if code != 3 {
			t.Fatalf("exit %d, want the command's 3", code)
		}

		if strings.Contains(out, "[lx: ") || out != medium() || errOut != "ERROR batch 97 failed: connection reset by peer\n" {
			t.Fatalf("not the whole output (%d bytes, want %d):\n%s", len(out), len(medium()), out[max(0, len(out)-400):])
		}
	}
	level := func() (string, int) {
		t.Helper()
		out, _, code := lx(proj, nil, "tune", "--json")
		var js struct {
			Commands []struct {
				Cmd     string `json:"cmd"`
				Level   string `json:"level"`
				Regrets int    `json:"regrets_7d"`
			} `json:"commands"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &js) != nil {
			t.Fatalf("lx tune --json (%d): %s", code, out)
		}
		for _, c := range js.Commands {
			if c.Cmd == "faketool" {
				return c.Level, c.Regrets
			}
		}
		return "normal", 0
	}

	setOut(medium())
	id, note := condensed(proj)
	if note != "" {
		t.Fatalf("a note with nothing learned: %q", note)
	}
	show(proj, id)
	id, _ = condensed(filepath.Join(proj, "deep-subdir"))
	if lv, n := level(); lv != "normal" || n != 1 {
		t.Fatalf("after one recall: %s, %d", lv, n)
	}
	show(base, id, "--full")
	if lv, n := level(); lv != "loosened" || n != 2 {
		t.Fatalf("after two recalls: %s, %d", lv, n)
	}

	whole(proj)
	whole(filepath.Join(proj, "deep-subdir"))

	setOut(tuneProse(2000))
	_, note = condensed(proj)
	if note != " · loosened after 2 full recalls: lx tune" {
		t.Fatalf("receipt note %q", note)
	}
	out, _, _ := lx(proj, nil, "tune")
	if !regexp.MustCompile(`(?m)^  faketool  loosened  2 full recalls in 7 days · until `).MatchString(out) {
		t.Fatalf("lx tune:\n%s", out)
	}

	for i, how := range [][]string{{"-r"}, {"LX_RAW=1"}} {
		setOut(medium())
		lx(base, nil, "faketool")
		setOut(tuneProse(2000))
		condensed(proj)
		if how[0] == "-r" {
			lx(proj, nil, "-r", "faketool", "--nightly")
			lx(proj, nil, "--raw", "faketool", "--nightly")
		} else {
			lx(proj, how, "faketool", "--nightly")
			lx(proj, []string{"LX_OFF=1"}, "faketool", "--nightly")
		}
		if _, n := level(); n != 3+i {
			t.Fatalf("after raw re-run %d: %d regrets", i+1, n)
		}
	}
	if lv, _ := level(); lv != "raw" {
		t.Fatalf("after four regrets: %s", lv)
	}

	out, _, code := lx(proj, nil, "faketool", "--nightly")
	if code != 3 || strings.Contains(out, "[lx: ") || out != tuneProse(2000) {
		t.Fatalf("raw level, no host limit: exit %d, %d bytes", code, len(out))
	}

	_, note = condensed(proj, "LX_MAX_CHARS=8000")
	if note != " · tuned to raw after 2 full recalls, 2 raw re-runs, but over the output limit: lx tune" {
		t.Fatalf("raw-level receipt note %q", note)
	}

	if out, _, code := lx(filepath.Join(proj, "deep-subdir"), nil, "tune", "--reset", "faketool"); code != 0 || !strings.Contains(out, "forgot faketool in this project") {
		t.Fatalf("reset (%d): %s", code, out)
	}
	setOut(medium())
	if _, note := condensed(proj); note != "" {
		t.Fatalf("after reset: note %q", note)
	}

	tuneSeedDir(t, data, proj, "faketool", 4)
	if _, note := condensed(proj, "LX_TUNE=0"); note != "" {
		t.Fatalf("LX_TUNE=0 applied a level: %q", note)
	}

	b, err := os.ReadFile(filepath.Join(data, "tune.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{base, "secret-project", "deep-subdir", "nightly", "fakebin", "out.txt"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("tune.json contains %q", leak)
		}
	}
	if st, err := os.Stat(filepath.Join(data, "tune.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("tune.json mode: %v %v", st.Mode(), err)
	}
}

func medium() string { return tuneLog(200) }

func tuneSeedDir(t *testing.T, data, proj, cmd string, n int) {
	t.Helper()
	t.Setenv("LX_DATA_DIR", data)
	t.Setenv("LX_TUNE", "")
	t.Setenv("LX_TRACK", "")
	tuneSeed(t, proj, cmd, track.SignalFull, n, time.Now())
}
