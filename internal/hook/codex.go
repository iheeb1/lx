package hook

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

func renderCodex(p *payload, o outcome) []byte {
	hs := &object{}
	hs.set("hookEventName", jsonString("PreToolUse"))
	switch {
	case o.decision == VerdictDeny:
		hs.set("permissionDecision", jsonString(VerdictDeny))
		hs.set("permissionDecisionReason", jsonString(o.reason))
	case o.rewritten != "":
		// Codex applies updatedInput only under "allow", which approves nothing.
		hs.set("permissionDecision", jsonString(VerdictAllow))
		hs.set("permissionDecisionReason", jsonString(RewriteReason))
		hs.set("updatedInput", p.updatedInput(o.rewritten))
	default:
		return nil
	}
	top := &object{}
	top.set("hookSpecificOutput", hs.compact())
	return top.compact()
}

func evalCodex(cmd string, env evalEnv, cwd string) outcome {
	return evaluateCodex(cmd, env, func() codexPolicy { return loadCodexPolicy(cwd) })
}

var lxBuiltins = map[string]bool{
	"show": true, "gain": true, "tune": true, "pipe": true, "ctx": true, "filters": true, "version": true,
	"help": true, "hook": true, "rewrite": true, "init": true, "doctor": true, "discover": true,
	"-h": true, "--help": true, "--version": true, "-V": true,
}

func evaluateCodex(cmd string, env evalEnv, load func() codexPolicy) outcome {
	a := analyze(cmd)
	targets := a.hookTargets()
	if env.Background || env.BadMode {
		targets = nil
	}
	var lxSegs []*segment
	for _, s := range a.segs {
		if s.isLx {
			lxSegs = append(lxSegs, s)
		}
	}
	if len(targets) == 0 && len(lxSegs) == 0 {
		return outcome{}
	}
	pol := load()
	for _, s := range lxSegs {
		inner := lxInner(s.words[s.cmdIdx:])
		if len(inner) == 0 || lxBuiltins[s.words[s.cmdIdx+1].val] {
			continue
		}
		for _, w := range vals(s.words[s.cmdIdx+1:]) {
			if file := named(pol.programs, w); file != "" {
				return outcome{decision: VerdictDeny, reason: "lx: `" + inner[0].text + "` is named by a Codex rule in " +
					file + ", which Codex does not apply through lx; run it without lx"}
			}
		}
		if pol.unknown != "" {
			return outcome{decision: VerdictDeny, reason: "lx: lx cannot read the Codex rules in " + pol.unknown +
				", so it cannot tell whether one names `" + inner[0].text + "`; run it without lx"}
		}
	}
	if len(targets) == 0 || pol.unknown != "" || named(pol.programs, "lx") != "" {
		return outcome{}
	}
	for _, t := range targets {
		for _, w := range vals(t.words) {
			if named(pol.programs, w) != "" || named(pol.viaLx, w) != "" {
				return outcome{}
			}
		}
	}
	return outcome{rewritten: splice(cmd, targets, resolvePrefix(env.Prefix)), reason: RewriteReason}
}

type codexPolicy struct {
	programs map[string]string
	viaLx    map[string]string
	unknown  string
}

func named(m map[string]string, word string) string {
	if f, ok := m[word]; ok {
		return f
	}
	return m[filepath.Base(word)]
}

func addName(m map[string]string, word, file string) {
	for _, w := range []string{word, filepath.Base(word)} {
		if w != "" && w != "." && w != "/" && m[w] == "" {
			m[w] = file
		}
	}
}

// `lx P …` keeps only P away from lx; lx alone, or with a flag, stops every rewrite.
func (p *codexPolicy) addRule(head string, next []string, file string) {
	if filepath.Base(head) != "lx" {
		addName(p.programs, head, file)
		return
	}
	for _, n := range next {
		if n == "" || strings.HasPrefix(n, "-") {
			next = nil
			break
		}
	}
	if len(next) == 0 {
		addName(p.programs, head, file)
	}
	for _, n := range next {
		addName(p.viaLx, n, file)
	}
}

const (
	maxRulesFile  = 1 << 20
	maxRulesFiles = 256
)

func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func codexRuleDirs(cwd string) []string {
	var dirs []string
	if h := codexHome(); h != "" {
		dirs = append(dirs, filepath.Join(h, "rules"))
	}
	dirs = append(dirs, filepath.Join(codexSystemDir, "rules"))
	if cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			for d := abs; ; d = filepath.Dir(d) {
				dirs = append(dirs, filepath.Join(d, ".codex", "rules"))
				if filepath.Dir(d) == d {
					break
				}
			}
		}
	}
	return dirs
}

var codexSystemDir = func() string {
	if runtime.GOOS != "windows" {
		return "/etc/codex"
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "OpenAI", "Codex")
}()

func noSuchFile(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

var codexMDM = func() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	const dir, plist = "/Library/Managed Preferences", "com.openai.codex.plist"
	return []string{filepath.Join(dir, plist), filepath.Join(dir, os.Getenv("USER"), plist)}
}

func loadCodexPolicy(cwd string) codexPolicy {
	p := codexPolicy{programs: map[string]string{}, viaLx: map[string]string{}}
	for _, f := range codexMDM() {
		if _, err := os.Lstat(f); err == nil {
			p.unknown = f
		}
	}
	seen := map[string]bool{}
	files := 0
	for _, dir := range codexRuleDirs(cwd) {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		ents, err := os.ReadDir(dir)
		if err != nil {
			if !noSuchFile(err) {
				p.unknown = dir
			}
			continue
		}
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".rules") {
				continue
			}
			if files++; files > maxRulesFiles {
				p.unknown = dir
				return p
			}
			p.scan(filepath.Join(dir, e.Name()), false)
		}
	}
	for _, name := range []string{"requirements.toml", "managed_config.toml"} {
		p.scan(filepath.Join(codexSystemDir, name), true)
	}
	return p
}

func (p *codexPolicy) scan(path string, toml bool) {
	f, err := os.Open(path)
	if err != nil {
		if !noSuchFile(err) {
			p.unknown = path
		}
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxRulesFile+1))
	if err != nil || len(b) > maxRulesFile {
		p.unknown = path
		return
	}
	toks, ok := starTokens(string(b), toml)
	if !ok {
		p.unknown = path
		return
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if !toml && t.is('i', "prefix_rule") && i+1 < len(toks) && toks[i+1].is('p', "(") {
			end := closing(toks, i+1)
			if heads, next, ok := patternHead(toks[i+2 : end]); ok {
				for _, h := range heads {
					p.addRule(h, next, path)
				}
				i = end
				continue
			}
			p.unknown = path
		}
		if t.kind == 's' {
			addName(p.programs, t.text, path)
		}
	}
}

type stok struct {
	kind byte
	text string
}

func (t stok) is(kind byte, text string) bool { return t.kind == kind && t.text == text }

func starTokens(src string, toml bool) ([]stok, bool) {
	var out []stok
	raw := false
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'':
			mode := byte(0)
			switch {
			case toml && c == '\'':
				mode = 'l'
			case raw:
				mode = 'r'
			}
			s, n, ok := starString(src[i:], mode)
			if !ok {
				return nil, false
			}
			out = append(out, stok{'s', s})
			i += n
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i + 1
			for j < len(src) && (src[j] == '_' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			out = append(out, stok{'i', src[i:j]})
			raw = j-i <= 2 && strings.ContainsAny(src[i:j], "rR") && j < len(src) && (src[j] == '"' || src[j] == '\'')
			i = j
			continue
		case strings.IndexByte("()[]{},=:+", c) >= 0:
			out = append(out, stok{'p', src[i : i+1]})
			i++
		default:
			i++
		}
		raw = false
	}
	return out, true
}

func starString(s string, mode byte) (string, int, bool) {
	q := s[:1]
	if len(s) >= 3 && s[1] == s[0] && s[2] == s[0] {
		q = s[:3]
	}
	var b strings.Builder
	for i := len(q); i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], q):
			return b.String(), i + len(q), true
		case s[i] == '\\' && mode == 'r' && i+1 < len(s):
			b.WriteString(s[i : i+2])
			i += 2
		case s[i] == '\\' && mode == 0:
			r, n, ok := unescape(s[i+1:])
			if !ok {
				return "", 0, false
			}
			b.WriteString(r)
			i += 1 + n
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return "", 0, false
}

func unescape(s string) (string, int, bool) {
	if s == "" {
		return "", 0, false
	}
	switch c := s[0]; c {
	case '\\', '"', '\'':
		return s[:1], 1, true
	case 'n', 't', 'r', 'a', 'b', 'f', 'v', 'e':
		return string("\n\t\r\a\b\f\v\x1b"[strings.IndexByte("ntrabfve", c)]), 1, true
	case ' ', '\t', '\r', '\n':
		return "", len(s) - len(strings.TrimLeft(s, " \t\r\n")), true
	case 'x', 'u', 'U':
		n := 2
		switch c {
		case 'u':
			n = 4
		case 'U':
			n = 8
		}
		if len(s) <= n {
			return "", 0, false
		}
		v, err := strconv.ParseUint(s[1:1+n], 16, 32)
		if err != nil || v > unicode.MaxRune {
			return "", 0, false
		}
		return string(rune(v)), 1 + n, true
	case '0', '1', '2', '3', '4', '5', '6', '7':
		n := 1
		for n < 3 && n < len(s) && s[n] >= '0' && s[n] <= '7' {
			n++
		}
		v, _ := strconv.ParseUint(s[:n], 8, 32)
		return string(rune(v)), n, true
	}
	return "", 0, false
}

func closing(toks []stok, open int) int {
	depth := 0
	for i := open; i < len(toks); i++ {
		if toks[i].kind != 'p' {
			continue
		}
		switch toks[i].text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(toks)
}

func patternHead(call []stok) (heads, next []string, ok bool) {
	at := -1
	if len(call) > 0 && call[0].is('p', "[") {
		at = 1
	}
	depth := 0
	for i := 0; at < 0 && i+2 < len(call); i++ {
		if call[i].kind == 'p' {
			switch call[i].text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			}
		}
		if depth == 0 && call[i].is('i', "pattern") && call[i+1].is('p', "=") && call[i+2].is('p', "[") {
			at = i + 3
		}
	}
	if at < 0 {
		return nil, nil, false
	}
	heads, j, ok := patternElem(call, at)
	if !ok {
		return nil, nil, false
	}
	if call[j].is('p', ",") {
		next, _, _ = patternElem(call, j+1)
	}
	return heads, next, true
}

func patternElem(call []stok, at int) ([]string, int, bool) {
	ends := func(j int) bool { return j < len(call) && (call[j].is('p', ",") || call[j].is('p', "]")) }
	if at >= len(call) {
		return nil, 0, false
	}
	if h := call[at]; h.kind == 's' {
		return []string{h.text}, at + 1, ends(at + 1)
	}
	if !call[at].is('p', "[") {
		return nil, 0, false
	}
	var alts []string
	for j := at + 1; j+1 < len(call); j += 2 {
		if call[j].kind != 's' || !ends(j+1) {
			return nil, 0, false
		}
		alts = append(alts, call[j].text)
		if call[j+1].is('p', "]") {
			return alts, j + 2, ends(j + 2)
		}
	}
	return nil, 0, false
}
