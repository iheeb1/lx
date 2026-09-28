package discover

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	spillSafeChars = 27000

	viewMaxChars = 26800
)

func isSpill(text string) bool {
	t := strings.TrimLeft(text, " \t\r\n")
	return strings.HasPrefix(t, "<persisted-output>") || strings.HasPrefix(t, "Output too large")
}

func savedPath(text string) string {
	const mark = "Full output saved to: "
	head := strings.TrimLeft(text, " \t\r\n")
	head = head[:min(len(head), 4096)]
	if k := strings.Index(head, "\n\n"); k >= 0 {
		head = head[:k]
	}
	i := strings.Index(head, mark)
	if i < 0 {
		return ""
	}
	rest := head[i+len(mark):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

type bashResult struct {
	stdout, stderr string
	persistedPath  string
	persistedSize  int64
}

func (b *bashResult) parse(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] != '{' {
		return false
	}
	var t struct {
		Stdout *string         `json:"stdout"`
		Stderr *string         `json:"stderr"`
		Path   json.RawMessage `json:"persistedOutputPath"`
		Size   json.RawMessage `json:"persistedOutputSize"`
	}
	if json.Unmarshal(raw, &t) != nil || (t.Stdout == nil && t.Stderr == nil) {
		return false
	}
	*b = bashResult{}
	if t.Stdout != nil {
		b.stdout = *t.Stdout
	}
	if t.Stderr != nil {
		b.stderr = *t.Stderr
	}
	_ = json.Unmarshal(t.Path, &b.persistedPath)
	if n, err := strconv.ParseInt(strings.Trim(string(t.Size), `"`), 10, 64); err == nil {
		b.persistedSize = n
	}
	return true
}

func (b *bashResult) joined() string {
	switch {
	case b.stdout == "":
		return b.stderr
	case b.stderr == "":
		return b.stdout
	}
	return strings.TrimRight(b.stdout, "\n") + "\n" + b.stderr
}

func (b *bashResult) complete() bool {
	if b.persistedPath == "" && b.persistedSize == 0 {
		return true
	}
	return b.persistedSize > 0 && int64(len(b.stdout)+len(b.stderr)) >= b.persistedSize
}

func resolvedRoots(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			abs = r
		}
		out = append(out, abs)
	}
	return out
}

func (s *scanner) readPersisted(p string) (string, bool) {
	if p == "" || !filepath.IsAbs(p) {
		return "", false
	}
	rp, err := filepath.EvalSymlinks(p)
	if err != nil || filepath.Base(filepath.Dir(rp)) != "tool-results" || !within(s.roots, rp) {
		return "", false
	}
	info, err := os.Lstat(rp)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(maxLine) {
		return "", false
	}
	f, err := os.Open(rp)
	if err != nil {
		return "", false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(maxLine)+1))
	if err != nil || len(b) > maxLine {
		return "", false
	}
	return string(b), true
}

func within(roots []string, p string) bool {
	for _, r := range roots {
		rel, err := filepath.Rel(r, p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}
