package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/lazyre"
)

// Timeouts for the programs doctor runs.
const (
	hookTimeout    = 5 * time.Second
	shellTimeout   = 3 * time.Second
	versionTimeout = 2 * time.Second
)

// Thresholds.
const (
	slowHook      = 100 * time.Millisecond
	bigStore      = 512 << 20
	quietFor      = 72 * time.Hour // no lx run for this long…
	activeWithin  = 24 * time.Hour // …while Claude Code wrote a transcript this recently
	maxHookRuns   = 3              // distinct hook commands (and binaries) checked
	maxWalkedDirs = 20000          // bound on the transcript mtime walk
)

// probeCmd is the harmless command the hook self-test asks about.
const probeCmd = "git status"

// The rtk pattern `lx init` uses, plus rtk's older script hook.
var reRtk = lazyre.New(`(^|[/\s])rtk(\s|$)|rtk-rewrite\.sh`)

var reSimpleMatcher = lazyre.New(`^[A-Za-z0-9_]+$`)

// foundHook is an lx hook found in some settings file.
type foundHook struct {
	entry hookEntry
	kind  hookKind
	cmd   lxHookCmd

	// Resolution of cmd.bin, for lxVerified hooks.
	path     string // absolute path of the binary ("" when not found)
	missing  string // why it cannot run: "does not exist", …
	viaPATH  bool   // bare `lx`, looked up on PATH
	relPATH  bool   // PATH has relative entries: sh might pick a different lx
	relative bool   // a relative path: it depends on where Claude Code starts
}

type state struct {
	e        *Env
	files    []*settingsFile
	hooks    []*foundHook // lx hooks whose matcher selects Bash
	offBash  []*foundHook // lx hooks whose matcher never selects Bash
	rtk      []hookEntry
	checks   []Check
	probe    *shellProbe
	versions map[string]string
	hist     *historyInfo
	zoneList *[]string // zones(), once computed
	// rewriteBin is the program the first successful self-test's rewrite
	// calls: "lx" (found on PATH) or an absolute path ("" = no self-test).
	rewriteBin string
}

func newState(e *Env) *state {
	s := &state{e: e, versions: map[string]string{}}
	s.files = e.settingsFiles()
	for _, f := range s.files {
		for _, h := range f.Hooks {
			if h.Type != "" && h.Type != "command" {
				continue
			}
			if reRtk.MatchString(h.Command) {
				s.rtk = append(s.rtk, h)
			}
			cmd, kind := classify(h.Command, e.Home)
			if kind == notLx {
				continue
			}
			fh := &foundHook{entry: h, kind: kind, cmd: cmd}
			if kind == lxVerified {
				s.resolve(fh)
			}
			if h.coversBash() {
				s.hooks = append(s.hooks, fh)
			} else {
				s.offBash = append(s.offBash, fh)
			}
		}
	}
	return s
}

func (s *state) add(id, status, msg, fix string) {
	s.checks = append(s.checks, Check{ID: id, Status: status, Message: printable(msg), Fix: printable(fix)})
}

// printable escapes control and bidirectional-override characters. Hook
// commands and paths come from files a repository can ship; printed raw,
// an escape sequence could redraw the terminal and fake a ✓.
func printable(s string) string {
	clean := true
	for _, r := range s {
		if unsafeRune(r) || r == utf8.RuneError {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == utf8.RuneError:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case unsafeRune(r):
			if r < 0x100 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unsafeRune: control characters, bidirectional overrides, and the line
// and paragraph separators (U+2028, U+2029) some terminals break lines at.
func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029'
}

// resolve finds the file a verified hook's argv[0] names.
func (s *state) resolve(h *foundHook) {
	bin := h.cmd.bin
	if !strings.Contains(bin, "/") {
		h.viaPATH = true
		h.path, h.relPATH = lookPath(bin, s.e.Getenv("PATH"))
		if h.path == "" {
			h.missing = "is not on PATH"
		}
		return
	}
	if !filepath.IsAbs(bin) {
		h.relative = true
		base := s.e.ProjectDir
		if base == "" {
			base = s.e.Cwd
		}
		bin = filepath.Join(base, bin)
	}
	h.path = filepath.Clean(bin)
	if h.path != bin {
		// `.` or `..` parts: the kernel resolves `..` after a symlink to
		// the symlink target's parent, which Clean does not. Judge (and
		// show) the file exec will actually reach.
		if r, err := filepath.EvalSymlinks(bin); err == nil {
			h.path = r
		}
	}
	h.missing = executableProblem(bin)
}

func executableProblem(p string) string {
	fi, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist"
	case err != nil:
		return "cannot be read (" + errText(err) + ")"
	case fi.IsDir() || fi.Mode()&0o111 == 0:
		return "is not an executable file"
	}
	return ""
}

// lookPath finds name in a PATH list. Relative entries are never used;
// rel reports that one appeared before the match (a shell would search it).
func lookPath(name, pathEnv string) (found string, rel bool) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || !filepath.IsAbs(dir) {
			rel = true
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, rel
		}
	}
	return "", rel
}

// ---- binary ---------------------------------------------------------------

func (s *state) checkBinary() {
	v := s.version()
	if s.e.Executable == "" {
		s.add("binary", Warn, v+": cannot locate this binary", "")
		return
	}
	s.add("binary", OK, v+" at "+s.show(s.e.Executable), "")
}

func (s *state) version() string {
	if s.e.Version == "" {
		return "lx (unknown version)"
	}
	return s.e.Version
}
