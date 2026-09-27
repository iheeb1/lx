package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// show abbreviates the home directory to ~ (messages only; fixes use full,
// quoted paths).
func (s *state) show(p string) string {
	h := s.e.Home
	if h == "" || h == "/" || p == "" {
		return p
	}
	if p == h {
		return "~"
	}
	if strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~" + p[len(h):]
	}
	return p
}

func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return clip(firstLine(err.Error()), 160)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

func fmtLatency(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1 {
		return "<1 ms"
	}
	return strconv.FormatInt(ms, 10) + " ms"
}

func fmtAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
}

func fmtBytes(n int64) string {
	unit, div := "GB", float64(1<<30)
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		unit, div = "KB", 1<<10
	case n < 1<<30:
		unit, div = "MB", 1<<20
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/div), ".0") + " " + unit
}

// stripEscapes removes terminal escape sequences (shell integrations print
// them from interactive shells) and carriage returns.
func stripEscapes(s string) string {
	if !strings.ContainsAny(s, "\x1b\r") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\r':
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']': // OSC … BEL or ST
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[': // CSI … final byte
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
		case c == 0x1b:
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// matchesBash: a matcher that is not "Bash" may still be a regular
// expression selecting it ("Bash|Edit", ".*").
func matchesBash(m string) bool {
	if reSimpleMatcher.MatchString(m) {
		return false // a plain tool name matches exactly, and it is not "Bash"
	}
	re, err := regexp.Compile(m)
	return err == nil && re.MatchString("Bash")
}
