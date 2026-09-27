// Package discover measures what lx would save on the user's own Claude Code
// transcripts: it pairs every Bash tool call with its output, replays the
// output through the engine for commands the hook would rewrite, and reports
// the totals. With Options.Fidelity it also checks, for each file:line the
// agent went on to open or edit, whether lx's view showed it (fidelity.go);
// it always counts host spills and head/tail cuts of rewritten commands
// (spill.go, pipes.go). It never stores anything, and never prints outputs
// or arguments — only command keys (tool + first subcommand word) and
// counts — except the file:line examples Options.Examples asks for.
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
	"unicode"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/hook"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

// Options select the transcripts to scan.
type Options struct {
	Dirs  []string  // directories searched recursively for *.jsonl (e.g. ~/.claude/projects)
	Since time.Time // skip files last modified before this (zero = all)
	Limit int       // scan at most this many files, newest first (0 = all)

	// Fidelity measures acted-on fidelity (Report.ActedOn): whether lx's
	// view kept the file:line locations the agent went on to open or edit.
	Fidelity bool
	// Examples lists up to maxExamples locations the agent acted on that
	// lx's view did not show (implies Fidelity). They hold paths from the
	// user's outputs: for the local terminal only.
	Examples bool
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

	// Host spills: Bash results the host replaced with a preview because
	// they exceeded its output limit (Claude Code: ~30k characters).
	HostSpills           int `json:"host_spills"`
	HostSpillsRewritable int `json:"host_spills_rewritable"` // lx rewrites the command and the full output was recorded
	HostSpillsAvoided    int `json:"host_spills_avoided"`    // of those, lx's view + receipt fits under spillSafeChars

	// Rewritten commands piped into head/tail (cmd | head -N): with lx the
	// cut applies to lx's view and receipt. The transcript holds only what
	// the cut kept, so a run is replayed only when the cut kept all of it
	// (fewer lines than N); the three counts below are over those runs.
	Sliced            int `json:"sliced_by_head_tail"`
	SlicedReplayable  int `json:"sliced_replayable"`
	SlicedViewCut     int `json:"sliced_view_cut"`     // lx's view + receipt would not fit the cut (a passthrough view keeps stdout and stderr apart, as without lx)
	SlicedReceiptLost int `json:"sliced_receipt_lost"` // head would drop the receipt line
	SlicedErrorsCut   int `json:"sliced_errors_cut"`   // an error line the agent saw raw would be cut from lx's view

	ActedOn *ActedOn `json:"acted_on,omitempty"` // Options.Fidelity only

	Top         []Stat `json:"top"`
	Unsupported []Stat `json:"top_unsupported"`
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
		roots: resolvedRoots(o.Dirs),
	}
	if o.Fidelity || o.Examples {
		s.fid = newFidelity(o.Examples)
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
	in      *hook.Inspection // set when already inspected (fidelity mode)
}

type scanner struct {
	seen  map[string]bool // tool_use ids already counted (resumed sessions repeat history)
	top   map[string]*Stat
	unsup map[string]*Stat
	roots []string  // scan roots, symlinks resolved: persisted outputs are read only below them
	fid   *fidelity // nil unless Options.Fidelity
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
	markUse    = []byte(`"tool_use"`)
)

func (s *scanner) file(path string, r *Report) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	open := map[string]pending{}
	if s.fid != nil {
		s.fid.newFile()
	}
	for {
		line, err := readLine(br, maxLine)
		if len(line) > 0 && (bytes.Contains(line, markBash) || bytes.Contains(line, markResult) ||
			s.fid != nil && bytes.Contains(line, markUse)) {
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
			if b.ID == "" || s.seen[b.ID] {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if b.Name != "Bash" || json.Unmarshal(b.Input, &in) != nil || strings.TrimSpace(in.Command) == "" {
				if s.fid != nil { // every tool call counts toward the acted-on window
					s.seen[b.ID] = true
					s.fid.toolUse(touchedPaths(b.Name, b.Input, rec.Cwd))
				}
				continue
			}
			s.seen[b.ID] = true
			r.BashCalls++
			p := pending{command: in.Command, cwd: rec.Cwd}
			if s.fid != nil {
				insp := hook.Inspect(in.Command)
				p.in = &insp
				s.fid.toolUse(bashPaths(insp.Commands, rec.Cwd))
			}
			open[b.ID] = p
		case "tool_result":
			p, ok := open[b.ToolUseID]
			if !ok {
				continue // not a Bash call (or already counted)
			}
			delete(open, b.ToolUseID)
			o := s.recorded(b, rec.ToolUseResult, results == 1)
			cwd := p.cwd
			if cwd == "" {
				cwd = rec.Cwd
			}
			exit := 0
			if b.IsError {
				exit = 1
			}
			s.measure(p, cwd, o, exit, r)
		}
	}
}

// recording is a Bash call's output as the transcript holds it.
type recording struct {
	// out is what the token accounting measures, as discover always has:
	// toolUseResult's stdout+stderr when it belongs to this result, else the
	// tool_result text (for a spilled result, the host's preview).
	out string
	// full is the command's whole output, when the transcript (or the file
	// the host saved it to) holds it; complete says whether it does.
	full     string
	complete bool
	stdout   string // stdout alone, when split
	split    bool   // full has stdout and stderr recorded apart (toolUseResult)
	spilled  bool   // the host replaced the result with a preview (output too large)
}

// recorded reads a Bash result. When the host spilled the output and the
// transcript holds only a preview, the file the host saved it to is read
// for the whole output (spill counters and fidelity; the token accounting
// keeps measuring what the transcript recorded, as the agent read that).
func (s *scanner) recorded(b block, tur json.RawMessage, own bool) recording {
	text := resultText(b.Content)
	o := recording{spilled: isSpill(text)}
	var br bashResult
	if own && br.parse(tur) {
		o.out = br.joined()
		if br.complete() && !(o.spilled && br.persistedPath == "") {
			o.full, o.complete, o.stdout, o.split = o.out, true, br.stdout, true
		}
	} else {
		o.out = text
		if !o.spilled {
			o.full, o.complete = text, true
		}
	}
	if !o.complete && (o.spilled || br.persistedPath != "") {
		path := br.persistedPath
		if path == "" {
			path = savedPath(text)
		}
		if full, ok := s.readPersisted(path); ok {
			o.full, o.complete = full, true
		}
	}
	return o
}

// fullOutput returns stdout+stderr from a Bash toolUseResult object, which
// holds the untruncated output (tool_result text may be cut for the model).
func fullOutput(raw json.RawMessage) (string, bool) {
	var b bashResult
	if !b.parse(raw) {
		return "", false
	}
	return b.joined(), true
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

func (s *scanner) measure(p pending, cwd string, o recording, exit int, r *Report) {
	r.Measured++
	if o.spilled {
		r.HostSpills++
	}
	out := noNUL(o.out)
	var in hook.Inspection
	if p.in != nil {
		in = *p.in
	} else {
		in = hook.Inspect(p.command)
	}
	switch {
	case in.AlreadyLx:
		r.AlreadyLx++
		r.BashOutputTokens += tokens.Count(out)
	case in.Changed:
		v := replay(in.Targets[0], exit, cwd, out)
		r.BashOutputTokens += v.res.RawTokens
		r.Candidates++
		r.CandidateTokens += v.res.RawTokens
		r.CandidateOut += v.after
		key := Key(in.Targets[0])
		st := stat(s.top, key)
		st.Count++
		st.Tokens += v.res.RawTokens
		st.TokensAfter += v.after
		st.Saved += v.res.RawTokens - v.after

		// Spills, cuts and fidelity need the command's whole output.
		if o.complete && o.full != o.out {
			v = replay(in.Targets[0], exit, cwd, noNUL(o.full))
		}
		if o.spilled && o.complete {
			r.HostSpillsRewritable++
			if len(v.res.Output)+len(v.receipt)+1 <= spillSafeChars {
				r.HostSpillsAvoided++
			}
		}
		sl := sliceOf(in, p.command, o)
		sliceStats(sl, v, r)
		if s.fid != nil {
			why := ""
			switch {
			case !o.complete:
				why = whySpill
			case len(in.Targets) > 1:
				why = whySeveral
			case sl.sliced && !sl.replayable:
				why = whyHeadTail
			}
			s.fid.candidate(key, cwd, v, why, sl)
		}
	default:
		n := tokens.Count(out)
		r.BashOutputTokens += n
		st := stat(s.unsup, unsupportedKey(p.command, in.Commands))
		st.Count++
		st.Tokens += n
	}
}

// noNUL neutralizes NULs: tokens.Count never returns on a NUL rune that is
// not followed by a letter (its whitespace fallback advances by 0).
// Binary-ish outputs do occur in real transcripts, and one of them must not
// hang the whole scan.
func noNUL(s string) string { return strings.ReplaceAll(s, "\x00", "\uFFFD") }

// replay runs lx's pipeline over a rewritten command's output, as lx would
// have in the agent's session.
func replay(argv []string, exit int, cwd, out string) *view {
	ctx := &engine.Context{Argv: argv, Exit: exit, Cwd: cwd}
	res := engine.Process(ctx, out, engine.Options{MaxChars: viewMaxChars})
	v := &view{res: res, raw: out, after: res.OutTokens}
	if res.Lossy { // the agent also reads the receipt line
		v.receipt = engine.Receipt(res, "1234")
		v.after += tokens.Count(v.receipt)
	}
	v.after = min(v.after, res.RawTokens)
	return v
}

// view is lx's replay of one candidate.
type view struct {
	res     engine.Result
	receipt string // "" when the view is not lossy
	raw     string // the output lx received
	after   int    // tokens the agent reads: view + receipt, at most the raw tokens

	clean   string // textutil.Clean(raw), computed on first use
	cleaned bool
}

// cleanRaw is the normalized raw output (ANSI, \r frames and overstrike
// removed), as lx's filters and the benchmark's metrics see it.
func (v *view) cleanRaw() string {
	if !v.cleaned {
		v.clean, v.cleaned = textutil.Clean(v.raw), true
	}
	return v.clean
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
	if s.fid != nil {
		r.ActedOn = s.fid.report()
	}
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
	name := printable(filepath.Base(argv[0]))
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

// maxKeyName bounds a command name in a report (runes).
const maxKeyName = 40

// printable makes a command name safe to print on a terminal: control and
// other non-printing characters (escape sequences, bidi overrides) become
// '?', and a name longer than maxKeyName runes is cut with "…".
func printable(name string) string {
	n := 0
	clean := true
	for _, r := range name {
		n++
		if !printing(r) {
			clean = false
		}
	}
	if clean && n <= maxKeyName {
		return name
	}
	var b strings.Builder
	n = 0
	for _, r := range name {
		if n == maxKeyName {
			b.WriteString("…")
			break
		}
		if !printing(r) {
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// printing: r prints as itself (invalid UTF-8, such as a lone 0x9b, a C1
// control to some terminals, does not).
func printing(r rune) bool { return r == ' ' || r != utf8.RuneError && unicode.IsPrint(r) }

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
	if r.ActedOn != nil {
		r.ActedOn.text(w)
	}
	fmt.Fprintf(w, "\nHost spills (Bash output over the host's limit, replaced by a preview): %s", num(r.HostSpills))
	if r.HostSpills > 0 {
		fmt.Fprintf(w, " · %s from commands lx rewrites, full output recorded · lx's view fits for %s",
			num(r.HostSpillsRewritable), num(r.HostSpillsAvoided))
	}
	fmt.Fprintf(w, "\nPipelines cutting a rewritten command with head/tail: %s", num(r.Sliced))
	if r.Sliced > 0 {
		fmt.Fprintf(w, " · replayable %s (the cut kept the whole output)", num(r.SlicedReplayable))
		if r.SlicedReplayable > 0 {
			fmt.Fprintf(w, "; with lx: view cut %s · receipt lost %s · error lines cut %s",
				num(r.SlicedViewCut), num(r.SlicedReceiptLost), num(r.SlicedErrorsCut))
		}
	}
	fmt.Fprintln(w)
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
