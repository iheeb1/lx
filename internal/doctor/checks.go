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

const (
	hookTimeout    = 5 * time.Second
	shellTimeout   = 3 * time.Second
	versionTimeout = 2 * time.Second
)

const (
	slowHook      = 100 * time.Millisecond
	bigStore      = 512 << 20
	quietFor      = 72 * time.Hour
	activeWithin  = 24 * time.Hour
	maxHookRuns   = 3
	maxWalkedDirs = 20000
)

const probeCmd = "git status"

var reRtk = lazyre.New(`(^|[/\s])rtk(\s|$)|rtk-rewrite\.sh`)

var reSimpleMatcher = lazyre.New(`^[A-Za-z0-9_]+$`)

type foundHook struct {
	entry hookEntry
	kind  hookKind
	cmd   lxHookCmd

	path     string
	missing  string
	viaPATH  bool
	relPATH  bool
	relative bool
}

type state struct {
	e        *Env
	files    []*settingsFile
	hooks    []*foundHook
	offBash  []*foundHook
	rtk      []hookEntry
	checks   []Check
	probe    *shellProbe
	versions map[string]string
	hist     *historyInfo
	zoneList *[]string

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

func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029'
}

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
