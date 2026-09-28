package engine

import (
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/lazyre"
)

var LocRe = lazyre.New(`[\w./@-]+\.(?:go|ts|tsx|js|jsx|mjs|cjs|py|rs|rb|java|kt|c|h|cc|cpp|cs|php|swift|vue|svelte)[:(]\d+`)

func locExt(ext string) bool {
	switch ext {
	case "go", "ts", "tsx", "js", "jsx", "mjs", "cjs", "py", "rs", "rb", "java", "kt",
		"c", "h", "cc", "cpp", "cs", "php", "swift", "vue", "svelte":
		return true
	}
	return false
}

func FindLocs(s string) []string {
	var out []string
	for len(s) > 0 {
		line := s
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			line, s = s[:i], s[i+1:]
		} else {
			s = ""
		}
		if mayHoldLoc(line) {
			out = append(out, LocRe.FindAllString(line, -1)...)
		}
	}
	return out
}

func mayHoldLoc(line string) bool {
	for i := 1; i < len(line)-1; i++ {
		if c := line[i]; (c == ':' || c == '(') && line[i+1] >= '0' && line[i+1] <= '9' {
			lo := max(0, i-7)
			if j := strings.LastIndexByte(line[lo:i], '.'); j >= 0 && locExt(line[lo+j+1:i]) {
				return true
			}
		}
	}
	return false
}

var libLocRe = lazyre.New(`node_modules/|site-packages/|dist-packages/|/lib/python\d|/libexec/src/|/go/src/|/go\d[\w.]*/src/|_testmain\.go|node:internal|\.cargo/registry|/rustc/|/pkg/mod/`)

func LocKey(m string) string {
	m = strings.Replace(m, "(", ":", 1)
	return filepath.Base(m)
}

func LocationsMissing(in, out string) []string {
	have := map[string]bool{}
	for _, m := range FindLocs(out) {
		have[LocKey(m)] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range FindLocs(in) {
		k := LocKey(m)
		if !have[k] && !seen[k] {
			seen[k] = true
			missing = append(missing, m)
		}
	}
	return missing
}

func ErrorMessagesMissing(in, out string) []string {
	var norm strings.Builder
	for _, ln := range strings.Split(out, "\n") {
		norm.WriteString(messageOf(ln))
		norm.WriteByte('\n')
	}
	have := norm.String()
	var missing []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(in, "\n") {
		m := messageOf(ln)
		if m == "" || seen[m] || !IsError(ln) {
			continue
		}
		seen[m] = true
		if !strings.Contains(have, m) {
			missing = append(missing, strings.TrimSpace(ln))
		}
	}
	return missing
}

func messageOf(line string) string {
	f := strings.Fields(line)
	out := f[:0]
	for _, w := range f {
		if strings.ContainsAny(w, "0123456789") {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

func AppLocations(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range FindLocs(s) {
		k := LocKey(m)
		if libLocRe.MatchString(m) || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}

func AppLocationsMissing(in, out string) []string {
	have := map[string]bool{}
	for _, m := range FindLocs(out) {
		have[LocKey(m)] = true
	}
	var missing []string
	for _, m := range AppLocations(in) {
		if !have[LocKey(m)] {
			missing = append(missing, m)
		}
	}
	return missing
}

func SplitLoc(m string) (path, line string, ok bool) {
	i := strings.LastIndexAny(m, ":(")
	if i <= 0 || i == len(m)-1 {
		return "", "", false
	}
	for _, c := range m[i+1:] {
		if c < '0' || c > '9' {
			return "", "", false
		}
	}
	return m[:i], m[i+1:], true
}

func ViewLocKeys(view string) map[string]bool {
	keys := map[string]bool{}
	for _, m := range FindLocs(view) {
		keys[LocKey(m)] = true
	}
	heading := ""
	for _, ln := range strings.Split(view, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
			heading = ""
		case heading != "" && startsWithLineNo(t):
			n := 0
			for n < len(t) && t[n] >= '0' && t[n] <= '9' {
				n++
			}
			keys[heading+":"+t[:n]] = true
		case isBarePath(ln):
			heading = filepath.Base(ln)
		default:
			if !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "\t") && !strings.HasPrefix(t, "…") {
				heading = ""
			}
		}
	}
	return keys
}

func startsWithLineNo(t string) bool {
	n := 0
	for n < len(t) && t[n] >= '0' && t[n] <= '9' {
		n++
	}
	if n == 0 || n > 9 {
		return false
	}
	if n == len(t) {
		return false
	}
	return t[n] == ':' || t[n] == '-'
}

func isBarePath(ln string) bool {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || strings.ContainsAny(ln, " \t:") {
		return false
	}
	m := LocRe.FindString(ln + ":1")
	return m == ln+":1"
}
