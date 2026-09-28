// Package discover replays Claude Code transcripts through lx.
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

type Options struct {
	Dirs  []string
	Since time.Time
	Limit int

	Fidelity bool

	Examples bool
}

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
	WouldSavePct     float64 `json:"would_save_pct"`
	CandidateSavePct float64 `json:"candidate_would_save_pct"`
	AlreadyLx        int     `json:"already_lx"`
	BadLines         int     `json:"unparsable_lines"`

	HostSpills           int `json:"host_spills"`
	HostSpillsRewritable int `json:"host_spills_rewritable"`
	HostSpillsAvoided    int `json:"host_spills_avoided"`

	Sliced            int `json:"sliced_by_head_tail"`
	SlicedReplayable  int `json:"sliced_replayable"`
	SlicedViewCut     int `json:"sliced_view_cut"`
	SlicedReceiptLost int `json:"sliced_receipt_lost"`
	SlicedErrorsCut   int `json:"sliced_errors_cut"`

	ActedOn *ActedOn `json:"acted_on,omitempty"`

	Top         []Stat `json:"top"`
	Unsupported []Stat `json:"top_unsupported"`
}

type Stat struct {
	Command     string `json:"command"`
	Count       int    `json:"count"`
	Tokens      int    `json:"tokens"`
	TokensAfter int    `json:"tokens_after,omitempty"`
	Saved       int    `json:"saved,omitempty"`
}

const (
	topN         = 15
	topUnsupport = 10
)

var maxLine = 64 << 20

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
		ctx:   contextOn(),
	}
	if o.Fidelity || o.Examples {
		s.fid = newFidelity(o.Examples)
	}
	for _, f := range files {
		if err := s.file(f.path, &r); err != nil {
			continue
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
				return nil
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
	in      *hook.Inspection
	ctx     *sessionPoint
}

type scanner struct {
	seen  map[string]bool
	top   map[string]*Stat
	unsup map[string]*Stat
	roots []string
	fid   *fidelity
	ctx   bool
}

type record struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Cwd       string `json:"cwd"`
	Sidechain bool   `json:"isSidechain"`
	Meta      bool   `json:"isMeta"`
	Summary   bool   `json:"isCompactSummary"`
	Message   struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Usage   usage           `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Compact struct {
		PostTokens int `json:"postTokens"`
	} `json:"compactMetadata"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type usage struct {
	Input         int `json:"input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Text      string          `json:"text"`
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
	var sess *session
	if s.ctx {
		sess = newSession(path)
	}
	for {
		line, err := readLine(br, maxLine)
		if len(line) > 0 {
			switch {
			case bytes.Contains(line, markBash) || bytes.Contains(line, markResult) || s.fid != nil && bytes.Contains(line, markUse):
				s.line(line, open, r, sess)
			case sess != nil && sess.wants(line):
				sess.scan(line)
			}
		}
		if err != nil {
			break
		}
	}
	r.MissingResults += len(open)
	return nil
}

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

func (s *scanner) line(line []byte, open map[string]pending, r *Report, sess *session) {
	var rec record
	if json.Unmarshal(line, &rec) != nil {
		r.BadLines++
		return
	}
	if sess != nil && !sess.begin(&rec) {
		sess = nil
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
		if sess != nil {
			sess.recordBlock(rec.Type, b)
		}
		switch b.Type {
		case "tool_use":
			if b.ID == "" || s.seen[b.ID] {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if b.Name != "Bash" || json.Unmarshal(b.Input, &in) != nil || strings.TrimSpace(in.Command) == "" {
				if s.fid != nil {
					s.seen[b.ID] = true
					s.fid.toolUse(touchedPaths(b.Name, b.Input, rec.Cwd))
				}
				continue
			}
			s.seen[b.ID] = true
			r.BashCalls++
			p := pending{command: in.Command, cwd: rec.Cwd, ctx: sess.point()}
			if s.fid != nil {
				insp := hook.Inspect(in.Command)
				p.in = &insp
				s.fid.toolUse(bashPaths(insp.Commands, rec.Cwd))
			}
			open[b.ID] = p
		case "tool_result":
			p, ok := open[b.ToolUseID]
			if !ok {
				continue
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

type recording struct {
	out string

	full     string
	complete bool
	stdout   string
	split    bool
	spilled  bool
}

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

func fullOutput(raw json.RawMessage) (string, bool) {
	var b bashResult
	if !b.parse(raw) {
		return "", false
	}
	return b.joined(), true
}

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
		var fit engine.Options
		if len(in.Fits) > 0 && len(in.FitCuts) > 0 {
			fit.MaxLines, fit.Cut = in.Fits[0], in.FitCuts[0]
		}
		fit = p.ctx.options(fit, p.command, &engine.Context{Argv: in.Targets[0], Exit: exit})
		v := replay(in.Targets[0], exit, cwd, out, fit)
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

		if o.complete && o.full != o.out {
			v = replay(in.Targets[0], exit, cwd, noNUL(o.full), fit)
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

func noNUL(s string) string { return strings.ReplaceAll(s, "\x00", "\uFFFD") }

func replay(argv []string, exit int, cwd, out string, o engine.Options) *view {
	ctx := &engine.Context{Argv: argv, Exit: exit, Cwd: cwd}
	o.MaxChars = viewMaxChars
	res := engine.Process(ctx, out, o)
	v := &view{res: res, raw: out, after: res.OutTokens}
	if res.Lossy {
		v.receipt = engine.Receipt(res, "1234")
		v.after += tokens.Count(v.receipt)
	}
	v.after = min(v.after, res.RawTokens)
	return v
}

type view struct {
	res     engine.Result
	receipt string
	raw     string
	after   int

	clean   string
	cleaned bool
}

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

const maxKeyName = 40

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

func printing(r rune) bool { return r == ' ' || r != utf8.RuneError && unicode.IsPrint(r) }

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
