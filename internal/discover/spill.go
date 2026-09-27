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
	// spillSafeChars: a view plus its receipt at most this long stays under
	// Claude Code's Bash limit (30,000 characters) with a 10% margin, the
	// same margin lx keeps when it caps views inside Claude Code.
	spillSafeChars = 27000
	// viewMaxChars is the engine.Options.MaxChars discover replays with:
	// the cap lx applies inside Claude Code (30,000*9/10 − 200 = 26,800),
	// so discover measures what lx does there.
	viewMaxChars = 26800
)

// isSpill reports whether a tool_result is the host's stand-in for an
// output too large to show. Claude Code replaces the whole result with
//
//	<persisted-output>
//	Output too large (45.0KB). Full output saved to: PATH
//
//	Preview (first 2KB):
//	…
//	</persisted-output>
//
// (older versions: the "Output too large" line first). Only a result that
// starts that way is a spill: an output that merely mentions the words —
// grep over this very code, a cat of a transcript — is an ordinary result.
func isSpill(text string) bool {
	t := strings.TrimLeft(text, " \t\r\n")
	return strings.HasPrefix(t, "<persisted-output>") || strings.HasPrefix(t, "Output too large")
}

// savedPath extracts PATH from the "Full output saved to: PATH" line of a
// spill's header (the lines before the preview), never from the preview.
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

// bashResult is the part of a Bash toolUseResult discover reads.
type bashResult struct {
	stdout, stderr string
	persistedPath  string
	persistedSize  int64 // bytes of the full output, when the host persisted it
}

// parse reads a toolUseResult object. It fails unless stdout or stderr is
// present. Fields of an unexpected type are ignored, never fatal.
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

// joined is stdout then stderr, as fullOutput builds it.
func (b *bashResult) joined() string {
	switch {
	case b.stdout == "":
		return b.stderr
	case b.stderr == "":
		return b.stdout
	}
	return strings.TrimRight(b.stdout, "\n") + "\n" + b.stderr
}

// complete: the streams hold the whole output. A persisted output larger
// than the streams means they are the host's truncated copy.
func (b *bashResult) complete() bool {
	if b.persistedPath == "" && b.persistedSize == 0 {
		return true
	}
	return b.persistedSize > 0 && int64(len(b.stdout)+len(b.stderr)) >= b.persistedSize
}

// resolvedRoots returns the scan roots with symlinks resolved (a root that
// cannot be resolved is kept as its absolute path).
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

// readPersisted reads the full output the host saved for a spilled result.
// Only a regular file in a tool-results directory under one of the scan
// roots (symlinks resolved) is read, and at most maxLine bytes of it: a
// transcript cannot make discover read anything else.
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
