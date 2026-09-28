package agentctx

import (
	"sort"
	"strings"
	"unicode"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

const (
	maxTerms      = 30
	maxFocusFiles = 20
	maxQuoted     = 60
	promptWeight  = 0.8
)

const fileExts = `go|py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|rb|php|cs|c|h|cc|cpp|hpp|swift|m|mm|scala|dart|ex|exs|erl|lua|zig|sh|bash|zsh|sql|vue|svelte|css|scss|html|yml|yaml|json|jsonl|toml|ini|cfg|conf|xml|proto|tf|gradle|md|txt|lock|mod|sum|golden`

var (
	urlRe      = lazyre.New(`https?://\S+`)
	jsTitleRe  = lazyre.New("\\b(?:it|describe|test)\\(\\s*[\"'`]([^\"'`\\n]{3,80})[\"'`]")
	codeRe     = lazyre.New(`^(?:TS\d{4,5}|E\d{4}|CVE-\d{4}-\d{4,7}|GHSA(?:-[0-9a-z]{4}){3}|[A-Z]{1,3}\d{3,4})$`)
	backtickRe = lazyre.New("`([^`\\n]{2,60})`")
	dquoteRe   = lazyre.New(`"([^"\n]{3,60})"`)
)

func (s *Snapshot) Focus() (f *engine.Focus) {
	if s == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			f = nil
		}
	}()
	c := &collector{seen: map[string]int{}}
	for i, t := range s.Assistant {
		c.text(t, 1.0-0.2*float64(i))
	}
	if s.Prompt != "" {
		c.text(s.Prompt, promptWeight)
	}
	f = &engine.Focus{Terms: c.result()}
	for _, fl := range s.Files {
		if len(f.Files) == maxFocusFiles {
			break
		}
		f.Files = append(f.Files, fl.Path)
	}
	if f.Empty() {
		return nil
	}
	return f
}

type collector struct {
	seen  map[string]int
	terms []engine.FocusTerm
}

func (c *collector) add(t string, w float64) {
	t = strings.TrimSpace(t)
	if len(t) < 3 || len(t) > maxQuoted {
		return
	}
	if k := strings.ToLower(t); stopword[k] || commonTerm[k] {
		return
	}
	if i, ok := c.seen[t]; ok {
		c.terms[i].Weight = max(c.terms[i].Weight, w)
		return
	}
	c.seen[t] = len(c.terms)
	c.terms = append(c.terms, engine.FocusTerm{Text: t, Weight: w})
}

func (c *collector) result() []engine.FocusTerm {
	out := append([]engine.FocusTerm(nil), c.terms...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight > out[j].Weight })
	if len(out) > maxTerms {
		out = out[:maxTerms]
	}
	return out
}

func (c *collector) text(t string, w float64) {
	if strings.Contains(t, "://") {
		t = urlRe.ReplaceAllString(t, " ")
	}
	if strings.Contains(t, "it(") || strings.Contains(t, "describe(") || strings.Contains(t, "test(") {
		for _, m := range jsTitleRe.FindAllStringSubmatch(t, -1) {
			c.add(m[1], w)
		}
	}
	for i := 0; i < len(t); {
		if !tokenByte(t[i]) {
			i++
			continue
		}
		j := i
		for j < len(t) && tokenByte(t[j]) {
			j++
		}
		c.token(t[i:j], w)
		i = j
	}
	for _, re := range []*lazyre.Regexp{backtickRe, dquoteRe} {
		for _, m := range re.FindAllStringSubmatch(t, -1) {
			c.quoted(m[1], w)
		}
	}
	for _, q := range singleQuoted(t) {
		c.quoted(q, w)
	}
}

var tokenBytes = func() (t [256]bool) {
	for c := 0; c < 256; c++ {
		t[c] = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("_./-@+~:", byte(c)) >= 0
	}
	return t
}()

func tokenByte(b byte) bool { return tokenBytes[b] }

func (c *collector) token(tok string, w float64) {
	if i := strings.IndexByte(tok, ':'); i >= 0 {
		tok = tok[:i]
	}
	if tok = strings.TrimRight(tok, ".-"); len(tok) < 3 {
		return
	}
	base := tok[strings.LastIndexByte(tok, '/')+1:]
	if dot := strings.LastIndexByte(base, '.'); dot > 0 && isExt(base[dot+1:]) && identStart(base[0]) {
		c.add(base, w)
		return
	}
	if slash := strings.IndexByte(tok, '/'); slash >= 0 {
		if head := tok[:slash]; testName(head) {
			c.add(head, w)
		}
		return
	}
	if strings.ContainsAny(tok, "@+~") {
		return
	}
	if strings.Contains(tok, "-") {
		if isCode(tok) {
			c.add(tok, w)
		}
		return
	}
	if isCode(tok) || testName(tok) || identStart(tok[0]) && isIdent(tok) {
		c.add(tok, w)
	}
}

func identStart(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' }

func testName(s string) bool {
	for _, p := range []string{"Test", "Benchmark", "Fuzz"} {
		if len(s) > len(p) && strings.HasPrefix(s, p) {
			c := s[len(p)]
			return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
		}
	}
	return len(s) > 5 && strings.HasPrefix(s, "test_")
}

func isCode(s string) bool {
	if len(s) < 4 || s[0] < 'A' || s[0] > 'Z' || strings.IndexAny(s, "0123456789") < 0 {
		return false
	}
	return codeRe.MatchString(s) && !notCode[strings.ToLower(s)] && !strings.HasPrefix(s, "RFC")
}

func singleQuoted(t string) []string {
	var out []string
	for i := 0; i < len(t); i++ {
		if t[i] != '\'' || i > 0 && strings.IndexByte(" \t\n([{=:,", t[i-1]) < 0 {
			continue
		}
		end := strings.IndexByte(t[i+1:min(len(t), i+2+maxQuoted)], '\'')
		if end < 3 {
			continue
		}
		j := i + 1 + end
		if j+1 < len(t) && strings.IndexByte(" \t\n)]},.;:", t[j+1]) < 0 {
			continue
		}
		if q := t[i+1 : j]; !strings.Contains(q, "\n") {
			out = append(out, q)
			i = j
		}
	}
	return out
}

func (c *collector) quoted(q string, w float64) {
	q = strings.TrimSpace(q)
	if q == "" || strings.ContainsAny(q, "\\{}$`\"") || strings.HasPrefix(q, "-") {
		return
	}
	if !strings.ContainsAny(q, " \t") {
		if !strings.ContainsAny(q, "/.") {
			c.add(q, w)
		}
		return
	}
	words := strings.Fields(q)
	if strings.HasPrefix(q, "//") || strings.HasPrefix(q, "#") || len(words) > 5 && !strings.ContainsAny(q, "():_=/<>[]@0123456789") {
		return
	}
	for _, word := range words {
		k := strings.ToLower(strings.Trim(word, ".,:;!?()'\""))
		if len(k) >= 3 && !stopword[k] && !commonTerm[k] {
			c.add(q, w)
			return
		}
	}
}

func isIdent(id string) bool {
	switch {
	case strings.Contains(id, "."):
		return dotted(id)
	case strings.Contains(strings.Trim(id, "_"), "_"):
		return snake(id)
	}
	return camel(id)
}

func dotted(id string) bool {
	parts := strings.Split(id, ".")
	last := strings.ToLower(parts[len(parts)-1])
	if tld[last] || isExt(last) {
		return false
	}
	long := false
	for _, p := range parts {
		if len(p) < 2 {
			return false
		}
		long = long || len(p) >= 3
	}
	return long
}

func snake(id string) bool {
	letters := 0
	for _, r := range id {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters >= 3
}

func camel(id string) bool {
	humps, lower := 0, false
	var prev rune
	for i, r := range id {
		switch {
		case unicode.IsUpper(r):
			if i == 0 || unicode.IsLower(prev) || unicode.IsDigit(prev) {
				humps++
			}
		case unicode.IsLower(r):
			if i == 0 {
				humps++
			}
			lower = true
		}
		prev = r
	}
	return humps >= 2 && lower
}

var exts = setOf(strings.Split(fileExts, "|")...)

func isExt(s string) bool { return exts[s] }

var notCode = setOf("SHA1", "SHA224", "SHA256", "SHA384", "SHA512", "X509", "ISO8601", "AES128", "AES192", "AES256",
	"P256", "P384", "P521", "H264", "H265", "UTF16", "UTF32", "ES2015", "ES2020", "ES2022", "ES2023", "ES2024")

var tld = setOf("com", "org", "net", "io", "dev", "ai", "co", "app", "edu", "gov", "me", "us", "uk", "de", "fr")

var commonTerm = setOf("javascript", "typescript", "github", "gitlab", "macos", "youtube", "linkedin",
	"postgresql", "mysql", "mongodb", "openai", "chatgpt", "powershell", "vscode", "nodejs", "readme",
	"todo", "fixme", "claude", "codex", "stdout", "stderr", "stdin", "e.g", "i.e")

var stopword = setOf(
	"a", "an", "the", "and", "or", "but", "if", "then", "else", "when", "while", "for", "to", "of", "in", "on",
	"at", "by", "with", "from", "as", "is", "are", "was", "were", "be", "been", "being", "it", "its", "this",
	"that", "these", "those", "there", "here", "we", "you", "me", "my", "our", "your", "they", "them",
	"their", "he", "she", "his", "her", "not", "no", "yes", "so", "do", "does", "did", "done", "can", "could",
	"should", "would", "will", "just", "now", "also", "only", "all", "any", "some", "each", "every", "more",
	"most", "less", "than", "too", "very", "into", "out", "up", "down", "over", "under", "again", "still",
	"what", "which", "who", "why", "how", "where", "let", "lets", "let's", "okay", "yet", "via", "per",
	"error", "errors", "err", "test", "tests", "testing", "file", "files", "line", "lines", "code", "function",
	"func", "method", "class", "type", "types", "value", "values", "string", "strings", "int", "bool", "true",
	"false", "null", "nil", "none", "undefined", "return", "returns", "result", "results", "output", "input",
	"data", "main", "index", "list", "map", "set", "get", "new", "old", "add", "run", "runs", "build", "check",
	"fix", "update", "change", "changes", "use", "used", "make", "work", "works", "pass", "passes", "passed",
	"fail", "fails", "failed", "failing", "note", "example", "foo", "bar", "baz", "tmp", "temp", "src", "lib",
	"bin", "pkg", "cmd", "internal", "json", "yaml", "http", "https", "www", "self", "args", "argv",
	"options", "config", "default", "defaults", "name", "path", "dir",
)

func setOf(ws ...string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[strings.ToLower(w)] = true
	}
	return m
}
