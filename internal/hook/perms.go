package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type Rules struct {
	Allow, Ask, Deny []string
	Dirs             []string
}

const (
	VerdictDeny  = "deny"
	VerdictAsk   = "ask"
	VerdictAllow = "allow"
)

func LoadClaudeRules(cwd string) Rules {
	var r Rules
	seen := map[string]bool{}
	paths, nProject := settingsPaths(cwd)
	for i, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		r.mergeFile(p, i < nProject)
	}
	return r
}

func settingsPaths(cwd string) (paths []string, nProject int) {
	u := userClaudeDir()
	if proj := projectDir(cwd); proj != "" {
		paths = append(paths,
			filepath.Join(proj, ".claude", "settings.json"),
			filepath.Join(proj, ".claude", "settings.local.json"))

		if u == "" || filepath.Join(proj, ".claude") != filepath.Clean(u) {
			nProject = len(paths)
		}
	}
	if u != "" {
		paths = append(paths, filepath.Join(u, "settings.json"), filepath.Join(u, "settings.local.json"))
	}
	return append(paths, managedPath), nProject
}

var managedPath = managedSettingsPath()

func projectDir(cwd string) string {
	if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" {
		return d
	}
	if cwd == "" {
		return ""
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".claude")); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func userClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func managedSettingsPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return `C:\Program Files\ClaudeCode\managed-settings.json`
	}
	return "/etc/claude-code/managed-settings.json"
}

func (r *Rules) mergeFile(path string, project bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var doc struct {
		Permissions struct {
			Allow []json.RawMessage `json:"allow"`
			Ask   []json.RawMessage `json:"ask"`
			Deny  []json.RawMessage `json:"deny"`
			Dirs  []json.RawMessage `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return
	}
	r.Allow = appendBash(r.Allow, doc.Permissions.Allow)
	r.Ask = appendBash(r.Ask, doc.Permissions.Ask)
	r.Deny = appendBash(r.Deny, doc.Permissions.Deny)
	r.Dirs = appendDirs(r.Dirs, doc.Permissions.Dirs, path, project)
}

func appendDirs(dst []string, raws []json.RawMessage, path string, project bool) []string {
	for _, raw := range raws {
		var d string
		if json.Unmarshal(raw, &d) != nil || strings.TrimSpace(d) == "" {
			continue
		}
		switch {
		case d == "~" || strings.HasPrefix(d, "~/"):
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				continue
			}
			d = filepath.Join(home, strings.TrimPrefix(d[1:], "/"))
		case filepath.IsAbs(d):
		default:
			settingsDir := filepath.Dir(path)
			if !project || filepath.Base(settingsDir) != ".claude" {
				continue
			}
			d = filepath.Join(filepath.Dir(settingsDir), d)
		}
		d = filepath.Clean(d)
		if !contains(dst, d) {
			dst = append(dst, d)
		}
	}
	return dst
}

func appendBash(dst []string, raws []json.RawMessage) []string {
	for _, raw := range raws {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		if _, ok := bashPattern(s); ok {
			dst = append(dst, strings.TrimSpace(s))
		}
	}
	return dst
}

func bashPattern(rule string) (string, bool) {
	rule = strings.TrimSpace(rule)
	if rule == "Bash" {
		return "*", true
	}
	if strings.HasPrefix(rule, "Bash(") && strings.HasSuffix(rule, ")") {
		p := strings.TrimSpace(rule[len("Bash(") : len(rule)-1])
		return p, p != ""
	}
	return "", false
}

func (r Rules) empty() bool { return len(r.Allow)+len(r.Ask)+len(r.Deny) == 0 }

func (r Rules) Decide(cmd string) string {
	v, _, _ := r.decide(cmd)
	return v
}

func (r Rules) decide(cmd string) (verdict, rule, subject string) {
	if r.empty() || strings.TrimSpace(cmd) == "" {
		return "", "", ""
	}
	return r.decideAnalysis(analyze(cmd))
}

func (r Rules) decideAnalysis(a *analysis) (verdict, rule, subject string) {
	cands := a.permSegments()
	for _, pass := range []struct {
		verdict string
		rules   []string
	}{{VerdictDeny, r.Deny}, {VerdictAsk, r.Ask}} {
		for _, c := range cands {
			for _, text := range c.lenient {
				if rl := firstMatch(pass.rules, text, true); rl != "" {
					return pass.verdict, rl, text
				}
			}
		}
	}
	if len(r.Allow) == 0 || len(a.unsafe) > 0 || a.lx.broken || a.sc.syntaxErr || len(cands) == 0 {
		return "", "", ""
	}
	matchedAny := false
	for _, c := range cands {
		if c.neutral {
			continue
		}
		matchedAny = true
		matched := false
		for _, text := range c.strict {
			if firstMatch(r.Allow, text, false) != "" {
				matched = true
				break
			}
		}
		if !matched {
			return "", "", ""
		}
	}
	if !matchedAny {
		return "", "", ""
	}
	return VerdictAllow, "", ""
}

type permSeg struct {
	lenient []string
	strict  []string
	neutral bool
}

func (a *analysis) permSegments() []permSeg {
	var out []permSeg
	if len(a.segs) == 0 || len(a.unsafe) > 0 || a.lx.broken {

		if t := strings.TrimSpace(a.src); t != "" {
			out = append(out, permSeg{lenient: []string{t}})
		}
	}
	for _, s := range a.segs {
		out = append(out, a.permSegment(s))
	}
	return out
}

func (a *analysis) permSegment(s *segment) permSeg {
	var p permSeg
	add := func(dst *[]string, t string) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		for _, x := range *dst {
			if x == t {
				return
			}
		}
		*dst = append(*dst, t)
	}
	raw := a.src[s.start:s.end]
	words := joinText(s.words)
	onlyDup := redirsOK(s.simple)
	add(&p.lenient, raw)
	add(&p.lenient, words)
	if onlyDup {

		add(&p.strict, raw)
		add(&p.strict, words)
	}
	if s.cmdIdx >= 0 {
		cw := s.words[s.cmdIdx]
		peeledRaw := a.src[cw.start:s.end]
		peeledWords := joinText(s.words[s.cmdIdx:])
		add(&p.lenient, peeledRaw)
		add(&p.lenient, peeledWords)
		add(&p.lenient, strings.Join(s.argv, " "))

		for _, t := range wrappedTexts(s.argv) {
			add(&p.lenient, t)
		}

		if len(s.envNames) == 0 && onlyDup {
			add(&p.strict, peeledWords)
		}
		if s.isLx {
			for _, t := range lxTexts(s) {
				add(&p.lenient, t)
			}
			for _, inner := range lxInner(s.words[s.cmdIdx:]) {
				if inner.exact && len(s.envNames) == 0 && onlyDup {
					add(&p.strict, inner.text)
				}
			}
		}
		if len(s.envNames) == 0 && onlyDup && s.cmdIdx == 0 && plainWords(s.words) &&
			(cdNeutral(s.argv) || stdinFilter(s.argv)) {
			p.neutral = true
		}
	}
	return p
}

func joinText(ts []token) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.text
	}
	return strings.Join(parts, " ")
}

func cdNeutral(argv []string) bool {
	if len(argv) != 2 || argv[0] != "cd" {
		return false
	}
	d := argv[1]
	if d == "" || strings.ContainsAny(d[:1], "/~-$") || strings.ContainsAny(d, "$`*?") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(d), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func plainWords(ws []token) bool {
	for _, w := range ws {
		if w.expand {
			return false
		}
		if g, sp := wordShape(w.text, true); g || sp {
			return false
		}
	}
	return true
}

func stdinFilter(argv []string) bool {
	if len(argv) == 0 || !oneOf(argv[0], "head", "tail", "cat") {
		return false
	}
	count := false
	for _, a := range argv[1:] {
		if count {
			if !isCount(a) {
				return false
			}
			count = false
			continue
		}
		if a == "-" || a == "--" || !strings.HasPrefix(a, "-") {
			return false
		}
		count = argv[0] != "cat" && (oneOf(a, "-n", "-c", "--lines", "--bytes") || argv[0] == "tail" && a == "-b")
	}
	return !count
}

func isCount(s string) bool {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n == 0 || len(s)-n > 3 {
		return false
	}
	for _, c := range s[n:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

type lxCmd struct {
	text  string
	value string
	exact bool
}

func lxTexts(s *segment) []string {
	var out []string
	lxWord := s.words[s.cmdIdx].text
	for _, inner := range lxInner(s.words[s.cmdIdx:]) {
		out = append(out, inner.text, inner.value)
		out = append(out, wrappedTexts(strings.Fields(inner.value))...)
		out = append(out, lxWord+" "+inner.text)
	}
	return out
}

func lxInner(ts []token) []lxCmd {
	args := ts[1:]
	i := 0
	exact := true
loop:
	for i < len(args) {
		a := args[i].val
		switch {
		case a == "--":
			i++
			break loop
		case a == "-r" || a == "--raw" || a == "-v" || a == "--verbose":
			i++
		case a == "-b" || a == "--budget":
			if i+1 < len(args) && args[i+1].expand {
				exact = false
			}
			i += 2
		case strings.HasPrefix(a, "--budget="):
			if args[i].expand {
				exact = false
			}
			i++
		case a == "--fit" && i+1 < len(args) && validFit(args[i+1]):
			i += 2
		case strings.HasPrefix(a, "--fit=") && validFit(token{val: a[len("--fit="):], expand: args[i].expand}):
			i++
		case (a == "-m" || a == "--mode") && i+1 < len(args) && validMode(args[i+1]):
			i += 2
		case strings.HasPrefix(a, "--mode=") && validMode(token{val: a[len("--mode="):], expand: args[i].expand}):
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
			exact = false
			i++
		default:
			break loop
		}
	}
	mk := func(from int, ex bool) lxCmd {
		return lxCmd{text: joinText(args[from:]), value: strings.Join(vals(args[from:]), " "), exact: ex}
	}
	var out []lxCmd
	if i < len(args) {
		out = append(out, mk(i, exact))
	}
	if !exact {
		for j := range args {
			if j != i && !strings.HasPrefix(args[j].val, "-") {
				out = append(out, mk(j, false))
			}
		}
	}
	return out
}

func validFit(w token) bool {
	_, _, ok := engine.ParseFit(w.val)
	return ok && !w.expand
}

func validMode(w token) bool {
	_, ok := engine.ParseMode(w.val)
	return ok && !w.expand
}

func firstMatch(rules []string, cmd string, lenient bool) string {
	for _, rl := range rules {
		if p, ok := bashPattern(rl); ok && matchPattern(p, cmd, lenient) {
			return rl
		}
	}
	return ""
}

func matchPattern(pattern, cmd string, lenient bool) bool {
	p, c := squashSpace(pattern), squashSpace(cmd)
	switch {
	case p == "":
		return false
	case p == "*":
		return true
	case strings.HasSuffix(p, ":*"):
		prefix := strings.TrimSpace(p[:len(p)-2])
		if prefix == "" {
			return true
		}
		if !strings.Contains(prefix, "*") {
			return c == prefix || strings.HasPrefix(c, prefix+" ")
		}
		return glob(prefix+" *", c) || glob(prefix, c)
	case strings.Contains(p, "*"):
		if glob(p, c) {
			return true
		}
		return lenient && strings.HasSuffix(p, " *") && glob(strings.TrimSuffix(p, " *"), c)
	}
	return c == p
}

func squashSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func glob(p, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case pi < len(p) && p[pi] == s[si]:
			pi++
			si++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
