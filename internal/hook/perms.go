package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Rules are the Bash(...) permission rules from Claude Code settings, kept
// verbatim ("Bash(git push:*)"). Other tools' rules are dropped on load.
// Dirs holds permissions.additionalDirectories as absolute paths: read-only
// parity treats them like the project directory.
type Rules struct {
	Allow, Ask, Deny []string
	Dirs             []string
}

// Permission verdicts returned by Decide.
const (
	VerdictDeny  = "deny"
	VerdictAsk   = "ask"
	VerdictAllow = "allow"
)

// LoadClaudeRules merges permissions.allow|ask|deny (and
// additionalDirectories) from, in order:
// <project>/.claude/settings.json, <project>/.claude/settings.local.json,
// <user>/settings.json, <user>/settings.local.json and the managed
// (enterprise) settings file. <project> is $CLAUDE_PROJECT_DIR (set by Claude
// Code for hooks) or else cwd's nearest ancestor containing .claude/; <user>
// is $CLAUDE_CONFIG_DIR or ~/.claude. Missing or malformed files are skipped.
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

// settingsPaths lists the settings files in order; the first nProject are
// the project's.
func settingsPaths(cwd string) (paths []string, nProject int) {
	u := userClaudeDir()
	if proj := projectDir(cwd); proj != "" {
		paths = append(paths,
			filepath.Join(proj, ".claude", "settings.json"),
			filepath.Join(proj, ".claude", "settings.local.json"))
		// With no .claude of its own, a project under $HOME finds ~/.claude:
		// those are the user's settings, not the project's.
		if u == "" || filepath.Join(proj, ".claude") != filepath.Clean(u) {
			nProject = len(paths)
		}
	}
	if u != "" {
		paths = append(paths, filepath.Join(u, "settings.json"), filepath.Join(u, "settings.local.json"))
	}
	return append(paths, managedPath), nProject
}

// managedPath is a variable so tests can keep the machine's real managed
// settings out of the picture.
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

// userClaudeDir is $CLAUDE_CONFIG_DIR or ~/.claude.
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

// managedSettingsPath is where enterprise-managed Claude Code settings live;
// their deny rules must win over everything, so lx reads them too.
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

// appendDirs adds additionalDirectories entries from the settings file at
// path. "~/x" is under the home directory; a relative entry in a project
// settings file is relative to the project (the directory holding its
// .claude folder). Relative entries in user or managed settings are skipped:
// what they are relative to is not settled, and guessing could widen what
// --readonly approves. Non-string entries are ignored.
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

// bashPattern extracts the pattern of a Bash rule: "Bash" and "Bash(*)" match
// everything, "Bash(x)" matches x.
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

// Decide returns "deny", "ask", "allow" or "" for a command, with
// precedence deny > ask > allow. Compound commands are split into segments:
// any segment matching deny (or ask) decides for the whole command, while
// "allow" needs every segment allowed and a command lx can fully parse.
// Each segment is matched with env assignments and wrappers peeled and as
// raw text; for an lx segment the wrapped command is checked too.
func (r Rules) Decide(cmd string) string {
	v, _, _ := r.decide(cmd)
	return v
}

// decide also returns the matching rule and the text it matched.
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
	if !matchedAny { // only cd / stdin filters: nothing was actually approved
		return "", "", ""
	}
	return VerdictAllow, "", ""
}

// permSeg holds the texts a segment is matched as. lenient texts are used
// for deny/ask (more ways to match is safer); strict ones for allow (only
// forms that cannot hide a side effect the rule's author did not approve).
type permSeg struct {
	lenient []string
	strict  []string
	neutral bool // `cd subdir` or a stdin-only head/tail/cat: needs no allow rule
}

func (a *analysis) permSegments() []permSeg {
	var out []permSeg
	if len(a.segs) == 0 || len(a.unsafe) > 0 || a.lx.broken {
		// Whatever we can't split is also matched whole.
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
		// A segment redirecting to or from a file is never allowed by lx:
		// Bash(git:*) must not approve `git status > ~/.bashrc`.
		add(&p.strict, raw)
		add(&p.strict, words) // "git status 2>&1" counts as "git status"
	}
	if s.cmdIdx >= 0 {
		cw := s.words[s.cmdIdx]
		peeledRaw := a.src[cw.start:s.end]
		peeledWords := joinText(s.words[s.cmdIdx:])
		add(&p.lenient, peeledRaw)
		add(&p.lenient, peeledWords)
		add(&p.lenient, strings.Join(s.argv, " "))
		// The command inside a wrapper only lx looks through (uv run git
		// push): the hook rewrites it, so deny and ask rules must see it.
		for _, t := range wrappedTexts(s.argv) {
			add(&p.lenient, t)
		}
		// Wrappers (timeout, nice, …) are transparent for allow; env
		// assignments are not: LD_PRELOAD=x git status is not git status.
		if len(s.envNames) == 0 && onlyDup {
			add(&p.strict, peeledWords)
		}
		if s.isLx {
			for _, inner := range lxInner(s.words[s.cmdIdx:]) {
				add(&p.lenient, inner.text)
				add(&p.lenient, inner.value)
				for _, t := range wrappedTexts(strings.Fields(inner.value)) {
					add(&p.lenient, t)
				}
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

// cdNeutral: "cd sub/dir" changes directory within the current tree and
// does nothing else; it does not need its own allow rule.
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

// plainWords: no word is expanded by the shell ($, wildcards, ~, braces,
// zsh's extended glob), so the words lx sees are the words the command
// gets. `cat -$IFS/etc/passwd` is `cat - /etc/passwd` in bash.
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

// stdinFilter: head, tail or cat reading only stdin (flags, and the count of
// -n/-c, or tail's -b) just shows part of a pipe; it needs no rule of its
// own. Any operand is a file to some platform: `head 5` and GNU `tail +5`
// read a file named 5 or +5, and BSD cat reads -x in `cat - -x` (its flags
// end at the first operand), so `-`, `--` and a count that is not the value
// of -n/-c/-b make the command an ordinary one.
func stdinFilter(argv []string) bool {
	if len(argv) == 0 || !oneOf(argv[0], "head", "tail", "cat") {
		return false
	}
	count := false // the next word is the value of -n/-c/-b
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

// isCount: a head/tail count such as 5, +5, -5 or 5K.
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

// lxCmd is a command lx would run for a model-written lx invocation.
type lxCmd struct {
	text  string // raw source text of the wrapped command
	value string // same, from unquoted word values
	exact bool   // lx's own flags parsed unambiguously
}

// lxInner extracts what `lx [lx-flags] cmd…` runs. If lx's flags are not
// all known, every suffix starting at a non-flag word is returned so an
// unknown flag with a value cannot smuggle a command past a deny rule.
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
			i += 2
		case strings.HasPrefix(a, "--budget="):
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

func firstMatch(rules []string, cmd string, lenient bool) string {
	for _, rl := range rules {
		if p, ok := bashPattern(rl); ok && matchPattern(p, cmd, lenient) {
			return rl
		}
	}
	return ""
}

// matchPattern implements Claude Code's Bash rule patterns: "*" matches
// everything; "prefix:*" is a prefix match on a word boundary ("git push" or
// "git push …"); any other pattern containing "*" is a glob where * matches
// any run of characters; anything else must match exactly.
//
// Whitespace runs are collapsed on both sides. lenient (used for deny and
// ask) also lets "git push *" match a bare "git push".
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

// glob matches s against p where '*' matches any (possibly empty) run.
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
