// Package discover measures what lx would save on the user's own Claude Code
// transcripts: it pairs every Bash tool call with its output, replays the
// output through the engine for commands the hook would rewrite, and reports
// the totals. It never stores or prints outputs or arguments — only command
// keys (tool + first subcommand word) and token counts.
package discover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/hook"
	"github.com/iheeb1/lx/internal/tokens"
)

// Options select the transcripts to scan.
type Options struct {
	Dirs  []string  // directories searched recursively for *.jsonl (e.g. ~/.claude/projects)
	Since time.Time // skip files last modified before this (zero = all)
	Limit int       // scan at most this many files, newest first (0 = all)
}

// Report is the result of Scan.
type Report struct {
	Files            int     `json:"files_scanned"`
	BashCalls        int     `json:"bash_calls"`
	Measured         int     `json:"bash_calls_with_output"`
	MissingResults   int     `json:"bash_calls_without_output"`
	BashOutputTokens int     `json:"bash_output_tokens"`
	Candidates       int     `json:"candidates"`
	CandidateTokens  int     `json:"candidate_tokens"`
	CandidateOut     int     `json:"candidate_tokens_after"`
	WouldSaveTokens  int     `json:"would_save_tokens"`
	WouldSavePct     float64 `json:"would_save_pct"`           // of all Bash output tokens
	CandidateSavePct float64 `json:"candidate_would_save_pct"` // of the candidates' tokens
	AlreadyLx        int     `json:"already_lx"`
	BadLines         int     `json:"unparsable_lines"`
	Top              []Stat  `json:"top"`
	Unsupported      []Stat  `json:"top_unsupported"`
}

// Stat aggregates one command key.
type Stat struct {
	Command     string `json:"command"` // "git status", "npm test" — never full arguments
	Count       int    `json:"count"`
	Tokens      int    `json:"tokens"`                 // raw output tokens
	TokensAfter int    `json:"tokens_after,omitempty"` // after lx (candidates only)
	Saved       int    `json:"saved,omitempty"`
}

const (
	topN         = 15
	topUnsupport = 10
)

// maxLine: transcript lines longer than this are skipped, not buffered.
var maxLine = 64 << 20

// Scan reads every transcript under o.Dirs and builds the report.
func Scan(o Options) (Report, error) {
	var r Report
	files, err := collect(o)
	if err != nil {
		return r, err
	}
	s := &scanner{
		seen:  map[string]bool{},
		top:   map[string]*Stat{},
		unsup: map[string]*Stat{},
	}
	for _, f := range files {
		if err := s.file(f.path, &r); err != nil {
			continue // unreadable transcript: skip, keep going
		}
		r.Files++
	}
	r.finish(s)
	return r, nil
}

type fileInfo struct {
	path string
	mod  time.Time
}

func collect(o Options) ([]fileInfo, error) {
	var files []fileInfo
	var firstErr error
	for _, dir := range o.Dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == dir {
					return err
				}
				return nil // unreadable subdirectory
			}
			if d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if !o.Since.IsZero() && info.ModTime().Before(o.Since) {
				return nil
			}
			files = append(files, fileInfo{p, info.ModTime()})
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].path < files[j].path
	})
	if o.Limit > 0 && len(files) > o.Limit {
		files = files[:o.Limit]
	}
	if len(files) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return files, nil
}

type pending struct {
	command string
	cwd     string
}

type scanner struct {
	seen  map[string]bool // tool_use ids already counted (resumed sessions repeat history)
	top   map[string]*Stat
	unsup map[string]*Stat
}

// record is the subset of a transcript line discover reads.
type record struct {
	Type    string `json:"type"`
	Cwd     string `json:"cwd"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

var (
	markBash   = []byte(`"Bash"`)
	markResult = []byte(`"tool_result"`)
)

func (s *scanner) file(path string, r *Report) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	open := map[string]pending{}
	for {
		line, err := readLine(br, maxLine)
		if len(line) > 0 && (bytes.Contains(line, markBash) || bytes.Contains(line, markResult)) {
			s.line(line, open, r)
		}
		if err != nil {
			break
		}
	}
	r.MissingResults += len(open)
	return nil
}

// readLine returns the next line without its newline. Lines longer than max
// are consumed and returned empty so a pathological line cannot exhaust
// memory. err is io.EOF (or a read error) after the last line.
func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > max {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			if tooLong {
				return nil, nil
			}
			return bytes.TrimRight(buf, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			if tooLong {
				return nil, err
			}
			return bytes.TrimRight(buf, "\r\n"), err
		}
	}
}

func (s *scanner) line(line []byte, open map[string]pending, r *Report) {
	var rec record
	if json.Unmarshal(line, &rec) != nil {
		r.BadLines++
		return
	}
	var blocks []block
	if len(rec.Message.Content) == 0 || rec.Message.Content[0] != '[' ||
		json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return
	}
	results := 0
	for _, b := range blocks {
		if b.Type == "tool_result" {
			results++
		}
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			if b.Name != "Bash" || b.ID == "" || s.seen[b.ID] {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &in) != nil || strings.TrimSpace(in.Command) == "" {
				continue
			}
			s.seen[b.ID] = true
			r.BashCalls++
			open[b.ID] = pending{command: in.Command, cwd: rec.Cwd}
		case "tool_result":
			p, ok := open[b.ToolUseID]
			if !ok {
				continue // not a Bash call (or already counted)
			}
			delete(open, b.ToolUseID)
			out, ok := "", false
			if results == 1 { // toolUseResult belongs to the line's only result
				out, ok = fullOutput(rec.ToolUseResult)
			}
			if !ok {
				out = resultText(b.Content)
			}
			cwd := p.cwd
			if cwd == "" {
				cwd = rec.Cwd
			}
			exit := 0
			if b.IsError {
				exit = 1
			}
			s.measure(p.command, cwd, out, exit, r)
		}
	}
}

// fullOutput returns stdout+stderr from a Bash toolUseResult object, which
// holds the untruncated output (tool_result text may be cut for the model).
func fullOutput(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return "", false
	}
	var t struct {
		Stdout *string `json:"stdout"`
		Stderr *string `json:"stderr"`
	}
	if json.Unmarshal(raw, &t) != nil || (t.Stdout == nil && t.Stderr == nil) {
		return "", false
	}
	var out, errOut string
	if t.Stdout != nil {
		out = *t.Stdout
	}
	if t.Stderr != nil {
		errOut = *t.Stderr
	}
	switch {
	case out == "":
		return errOut, true
	case errOut == "":
		return out, true
	}
	return strings.TrimRight(out, "\n") + "\n" + errOut, true
}

// resultText flattens tool_result content: a string, or text blocks.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func (s *scanner) measure(command, cwd, out string, exit int, r *Report) {
	r.Measured++
	// tokens.Count never returns on a NUL rune that is not followed by a
	// letter (its whitespace fallback advances by 0). Binary-ish outputs do
	// occur in real transcripts; neutralize NULs so one of them cannot hang
	// the whole scan.
	out = strings.ReplaceAll(out, "\x00", "\uFFFD")
	in := hook.Inspect(command)
	switch {
	case in.AlreadyLx:
		r.AlreadyLx++
		r.BashOutputTokens += tokens.Count(out)
	case in.Changed:
		ctx := &engine.Context{Argv: in.Targets[0], Exit: exit, Cwd: cwd}
		res := engine.Process(ctx, out, engine.Options{})
		after := res.OutTokens
		if res.Lossy { // the agent also reads the receipt line
			after += tokens.Count(engine.Receipt(res, "1234"))
		}
		if after > res.RawTokens {
			after = res.RawTokens
		}
		r.BashOutputTokens += res.RawTokens
		r.Candidates++
		r.CandidateTokens += res.RawTokens
		r.CandidateOut += after
		st := stat(s.top, Key(in.Targets[0]))
		st.Count++
		st.Tokens += res.RawTokens
		st.TokensAfter += after
		st.Saved += res.RawTokens - after
	default:
		n := tokens.Count(out)
		r.BashOutputTokens += n
		st := stat(s.unsup, unsupportedKey(command, in.Commands))
		st.Count++
		st.Tokens += n
	}
}

func stat(m map[string]*Stat, key string) *Stat {
	st, ok := m[key]
	if !ok {
		st = &Stat{Command: key}
		m[key] = st
	}
	return st
}

func (r *Report) finish(s *scanner) {
	r.WouldSaveTokens = r.CandidateTokens - r.CandidateOut
	if r.BashOutputTokens > 0 {
		r.WouldSavePct = pct(r.WouldSaveTokens, r.BashOutputTokens)
	}
	if r.CandidateTokens > 0 {
		r.CandidateSavePct = pct(r.WouldSaveTokens, r.CandidateTokens)
	}
	r.Top = rank(s.top, topN, func(st *Stat) int { return st.Saved })
	r.Unsupported = rank(s.unsup, topUnsupport, func(st *Stat) int { return st.Tokens })
}

func pct(a, b int) float64 {
	return float64(int(float64(a)/float64(b)*1000+0.5)) / 10
}

func rank(m map[string]*Stat, n int, by func(*Stat) int) []Stat {
	all := make([]Stat, 0, len(m))
	for _, st := range m {
		all = append(all, *st)
	}
	sort.Slice(all, func(i, j int) bool {
		if by(&all[i]) != by(&all[j]) {
			return by(&all[i]) > by(&all[j])
		}
		return all[i].Command < all[j].Command
	})
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// ---- command keys ----

// subcommandTools are keyed by their first subcommand word.
var subcommandTools = map[string]bool{
	"git": true, "go": true, "cargo": true, "npm": true, "pnpm": true, "yarn": true, "bun": true,
	"npx": true, "bunx": true, "docker": true, "docker-compose": true, "kubectl": true, "pip": true,
	"pip3": true, "brew": true, "terraform": true, "dotnet": true, "swift": true, "flutter": true,
	"dart": true, "gh": true, "uv": true, "poetry": true, "make": true, "composer": true,
	"bundle": true, "ruff": true, "golangci-lint": true, "gradle": true, "gradlew": true,
	"mvn": true, "mvnw": true, "helm": true, "aws": true, "gcloud": true, "az": true,
	"systemctl": true, "apt": true, "apt-get": true, "rails": true, "rake": true,
	"playwright": true, "vitest": true, "next": true, "vite": true,
}

// Key is the privacy-preserving label for argv: the tool's base name plus,
// for tools with subcommands, the first subcommand word ("git status",
// "npm run", "python -m pytest"). Arguments are never included.
func Key(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	name := filepath.Base(argv[0])
	args := argv[1:]
	if strings.HasPrefix(name, "python") && len(args) >= 2 && args[0] == "-m" && safeWord(args[1]) {
		return name + " -m " + args[1]
	}
	if subcommandTools[name] {
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "-C" || a == "-c" || a == "-n" || a == "--namespace" || a == "-f" || a == "--file" {
				i++
				continue
			}
			if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
				continue
			}
			if safeWord(a) {
				return name + " " + a
			}
			break
		}
	}
	return name
}

// safeWord accepts short lowercase command-like words only, so paths,
// patterns, URLs and values never end up in a report.
func safeWord(s string) bool {
	if len(s) == 0 || len(s) > 24 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':') {
			return false
		}
	}
	return s[0] >= 'a' && s[0] <= 'z'
}

// setup commands usually frame the command whose output matters
// (cd x && cat y, echo "== y" && cat y).
var setup = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "export": true, "set": true, "unset": true,
	"source": true, ".": true, "true": true, ":": true, "echo": true, "printf": true, "sleep": true,
}

func unsupportedKey(command string, cmds [][]string) string {
	for _, argv := range cmds {
		if len(argv) > 0 && !setup[filepath.Base(argv[0])] {
			return Key(argv)
		}
	}
	if len(cmds) > 0 && len(cmds[0]) > 0 {
		return Key(cmds[0])
	}
	if f := strings.Fields(command); len(f) > 0 {
		if k := Key(f[:1]); safeWord(strings.ToLower(k)) {
			return k
		}
	}
	return "(other)"
}

// ---- text output ----

// Text writes the report as aligned tables with small ASCII bars.
func (r Report) Text(w io.Writer) {
	fmt.Fprintf(w, "lx discover: %s transcripts, %s Bash calls, %s output tokens\n",
		num(r.Files), num(r.BashCalls), human(r.BashOutputTokens))
	if r.BashCalls == 0 {
		fmt.Fprintln(w, "no Bash calls found")
		return
	}
	fmt.Fprintf(w, "  would rewrite   %s calls, %s → %s tokens (−%s, %.1f%% of those, %.1f%% of all Bash output)\n",
		num(r.Candidates), human(r.CandidateTokens), human(r.CandidateOut), human(r.WouldSaveTokens),
		r.CandidateSavePct, r.WouldSavePct)
	fmt.Fprintf(w, "  already lx      %s calls\n", num(r.AlreadyLx))
	if r.MissingResults > 0 {
		fmt.Fprintf(w, "  no output       %s calls (interrupted or still running)\n", num(r.MissingResults))
	}

	if len(r.Top) > 0 {
		fmt.Fprintln(w, "\nTop commands by tokens lx would save")
		rows := make([][]string, 0, len(r.Top))
		maxv := 0
		for _, st := range r.Top {
			maxv = max(maxv, st.Saved)
		}
		for _, st := range r.Top {
			saved := "0"
			if st.Saved > 0 {
				saved = "−" + human(st.Saved)
			}
			rows = append(rows, []string{st.Command, num(st.Count), human(st.Tokens), human(st.TokensAfter),
				saved, fmt.Sprintf("%d%%", pctInt(st.Saved, st.Tokens)), bar(st.Saved, maxv)})
		}
		table(w, []string{"command", "calls", "tokens", "after", "saved", "", ""}, rows)
	}
	if len(r.Unsupported) > 0 {
		fmt.Fprintln(w, "\nNot rewritten (largest output first)")
		rows := make([][]string, 0, len(r.Unsupported))
		maxv := 0
		for _, st := range r.Unsupported {
			maxv = max(maxv, st.Tokens)
		}
		for _, st := range r.Unsupported {
			rows = append(rows, []string{st.Command, num(st.Count), human(st.Tokens), bar(st.Tokens, maxv)})
		}
		table(w, []string{"command", "calls", "tokens", ""}, rows)
	}
}

// table prints rows with the first column left-aligned, numbers right-aligned
// and the last column (the bar) unpadded.
func table(w io.Writer, head []string, rows [][]string) {
	widths := make([]int, len(head))
	for _, row := range append([][]string{head}, rows...) {
		for i, c := range row {
			widths[i] = max(widths[i], len([]rune(c)))
		}
	}
	line := func(row []string) {
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range row {
			pad := strings.Repeat(" ", widths[i]-len([]rune(c)))
			switch {
			case i == 0:
				b.WriteString(c + pad)
			case i == len(row)-1:
				b.WriteString(c)
			default:
				b.WriteString(pad + c)
			}
			if i < len(row)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	line(head)
	for _, row := range rows {
		line(row)
	}
}

const barWidth = 20

func bar(v, maxv int) string {
	if maxv <= 0 || v <= 0 {
		return ""
	}
	n := (v*barWidth + maxv - 1) / maxv
	return strings.Repeat("#", n)
}

func pctInt(a, b int) int {
	if b == 0 {
		return 0
	}
	return int(float64(a)/float64(b)*100 + 0.5)
}

func num(n int) string {
	s := fmt.Sprint(n)
	if n < 10000 {
		return s
	}
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func human(n int) string {
	switch {
	case n >= 10_000_000:
		return fmt.Sprintf("%dM", (n+500_000)/1_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
