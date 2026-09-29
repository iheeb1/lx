package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	claudeInline     = 30000
	claudeFailInline = 10000
	minOutputSetting = 4000
	maxOutputSetting = 128000
	minOutputWindow  = 1000
	maxSettingsFile  = 1 << 20
)

type OutputLimits struct {
	Pass, Fail int
	Setting    int
	From       string
}

func ClaudeOutputLimits(cwd string) OutputLimits {
	if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" {
		cwd = d
	}
	home, _ := os.UserHomeDir()
	return OutputLimitsFrom(managedPath, cwd, home, userClaudeDir(), os.Getenv("BASH_MAX_OUTPUT_LENGTH"))
}

func OutputLimitsFrom(managed, dir, home, user, bashMax string) OutputLimits {
	l := OutputLimits{Pass: claudeInline}
	l.Setting, l.From = managedSetting(managed)
	if l.Setting == 0 {
		l.Setting, l.From = projectSetting(dir, home, user)
	}
	if l.Setting == 0 && user != "" {
		l.Setting, l.From = firstSetting(filepath.Join(user, "settings.json"))
	}
	if l.Setting > 0 {
		l.Pass = l.Setting
	} else if n, err := strconv.Atoi(strings.TrimSpace(bashMax)); err == nil && n > 0 {
		// it sizes only the read-back window, so it can lower the limit but never raise it
		l.Pass = min(max(n, minOutputWindow), claudeInline)
	}
	l.Fail = min(l.Pass, claudeFailInline)
	return l
}

func managedSetting(managed string) (int, string) {
	if managed == "" {
		return 0, ""
	}
	dir := filepath.Join(filepath.Dir(managed), "managed-settings.d")
	ents, _ := os.ReadDir(dir)
	var files []string
	for i := len(ents) - 1; i >= 0; i-- {
		if n := ents[i].Name(); !strings.HasPrefix(n, ".") && strings.HasSuffix(n, ".json") {
			files = append(files, filepath.Join(dir, n))
		}
	}
	return firstSetting(append(files, managed)...)
}

// lx can't tell which ancestor Claude Code started in, so the smallest value wins.
func projectSetting(dir, home, user string) (n int, from string) {
	if dir == "" {
		return 0, ""
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return 0, ""
	}
	home = filepath.Clean(home)
	ui, _ := os.Stat(user)
	for {
		dot := filepath.Join(dir, ".claude")
		if fi, err := os.Stat(dot); err == nil && fi.IsDir() && dir != home && (ui == nil || !os.SameFile(fi, ui)) {
			if v, p := firstSetting(filepath.Join(dot, "settings.local.json"), filepath.Join(dot, "settings.json")); v > 0 && (n == 0 || v < n) {
				n, from = v, p
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return n, from
		}
		dir = parent
	}
}

func firstSetting(paths ...string) (int, string) {
	for _, p := range paths {
		if n, ok := outputSetting(p); ok {
			return n, p
		}
	}
	return 0, ""
}

var outputKey = []byte(`"bashOutputMaxChars"`)

func outputSetting(path string) (int, bool) {
	data := readSettings(path)
	if !bytes.Contains(data, outputKey) {
		return 0, false
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &doc) != nil {
		return 0, false
	}
	var f float64
	if json.Unmarshal(doc["bashOutputMaxChars"], &f) != nil || f <= 0 || f != math.Trunc(f) {
		return 0, false
	}
	return int(min(max(f, minOutputSetting), maxOutputSetting)), true
}

func readSettings(path string) []byte {
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxSettingsFile {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSettingsFile+1))
	if err != nil || len(data) > maxSettingsFile {
		return nil
	}
	return data
}
