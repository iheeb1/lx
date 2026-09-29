package engine_test

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

type fakeJudge struct {
	decide func(family, item string) engine.JudgeVerdict
	sleep  time.Duration
	err    error
	short  bool
	panics bool

	mu       sync.Mutex
	calls    int
	families []string
	tasks    []string
	items    []string
}

func (f *fakeJudge) Judge(family, task string, items []string, timeout time.Duration) ([]engine.JudgeVerdict, error) {
	f.mu.Lock()
	f.calls++
	f.families = append(f.families, family)
	f.tasks = append(f.tasks, task)
	f.items = append(f.items, items...)
	f.mu.Unlock()
	if f.sleep > 0 {
		time.Sleep(f.sleep)
	}
	if f.panics {
		panic("judge exploded")
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([]engine.JudgeVerdict, len(items))
	for i, it := range items {
		out[i] = f.decide(family, it)
	}
	if f.short {
		out = out[:len(out)-1]
	}
	return out, nil
}

func (f *fakeJudge) seen() (calls int, items []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]string(nil), f.items...)
}

func noise(conf float64) func(string, string) engine.JudgeVerdict {
	return func(string, string) engine.JudgeVerdict { return engine.JudgeVerdict{Confidence: conf} }
}

func hashed(family, item string) engine.JudgeVerdict {
	h := fnv.New32a()
	h.Write([]byte(family + item))
	v := h.Sum32()
	return engine.JudgeVerdict{Keep: v%3 == 0, Confidence: float64(v%1000) / 999}
}

var svcNames = strings.Fields("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo " +
	"sierra tango uniform victor whiskey xray yankee zulu amber basalt cobalt dune ember fjord garnet harbor iris jasper")

var svcRoles = []string{"api", "db", "cache", "queue"}

func svcLog() string {
	seed := uint64(7)
	next := func(n int) int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int((seed >> 33) % uint64(n))
	}
	var b strings.Builder
	for i := 0; i < 900; i++ {
		ts := fmt.Sprintf("2026-09-26T10:%02d:%02d.%03dZ", i/60%60, i%60, next(1000))
		switch {
		case i%150 == 75:
			fmt.Fprintf(&b, "%s ERROR [orders] unhandled exception in handler order=%d\n", ts, 9000+i)
			b.WriteString("Traceback (most recent call last):\n")
			b.WriteString("  File \"/app/orders/views.py\", line 118, in get\n")
			b.WriteString("KeyError: 'lines'\n")
		case i%90 == 40:
			fmt.Fprintf(&b, "%s WARN  [pool] connection pool nearly exhausted used=%d max=100\n", ts, 80+next(20))
		case i%37 == 5:
			fmt.Fprintf(&b, "%s INFO  [billing] invoice reconciled with ledger ref=%d amount=%d\n", ts, 100000+next(90000), next(5000))
		default:
			n := svcNames[next(len(svcNames))] + "-" + svcRoles[next(len(svcRoles))]
			fmt.Fprintf(&b, "%s INFO  [%s] %s-sync %s-flush %s-commit %s-verify batch=%d took=%dms\n", ts, n, n, n, n, n, 1000+next(9000), 1+next(900))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func buildLog() string {
	var b strings.Builder
	b.WriteString("==> building project\n")
	for i := 0; i < 400; i++ {
		switch {
		case i == 130:
			b.WriteString("src/pay/ledger.c:88:5: error: use of undeclared identifier 'balanse'\n")
		case i == 131:
			b.WriteString("        balanse += amount;\n")
		case i == 260:
			b.WriteString("warning: unused variable 'tmp' in module cache\n")
		default:
			fmt.Fprintf(&b, "  step %s: preparing %s artifacts for stage %s\n", svcNames[i%len(svcNames)], svcNames[(i*7)%len(svcNames)], svcNames[(i*3)%len(svcNames)])
		}
	}
	b.WriteString("2 targets failed, 38 succeeded in 12.4s\n")
	return strings.TrimRight(b.String(), "\n")
}

var dockerLogs = []string{"docker", "logs", "api"}

func judged(argv []string, exit int, raw string, opt engine.Options) engine.Result {
	return engine.Process(&engine.Context{Argv: argv, Exit: exit}, raw, opt)
}

func laNote(r engine.Result) string {
	for _, n := range r.Notes {
		if strings.HasPrefix(n, "laya: ") {
			return n
		}
	}
	return ""
}

var routineRe = regexp.MustCompile(`^\[×([\d,]+)\] .* \(routine\)$`)

var omittedRe = regexp.MustCompile(`(?m)^… \d+ more templates? \([\d,]+ lines?\) omitted …$`)

func routineLines(view string) []string {
	var out []string
	for _, ln := range strings.Split(view, "\n") {
		if routineRe.MatchString(ln) {
			out = append(out, ln)
		}
	}
	return out
}

func newMissing(clean, with, without string) []string {
	had := map[string]bool{}
	for _, ln := range engine.MissingErrorLines(clean, without) {
		had[ln] = true
	}
	var out []string
	for _, ln := range engine.MissingErrorLines(clean, with) {
		if !had[ln] {
			out = append(out, ln)
		}
	}
	return out
}

func TestJudgeFoldsRoutineTemplates(t *testing.T) {
	raw := svcLog()
	plain := judged(dockerLogs, 0, raw, engine.Options{})
	fj := &fakeJudge{decide: noise(0.9)}
	res := judged(dockerLogs, 0, raw, engine.Options{Judge: fj, Task: "fix the orders crash"})
	if res.Filter != "logs" || plain.Filter != "logs" {
		t.Fatalf("filter %q / %q", res.Filter, plain.Filter)
	}
	folded := routineLines(res.Output)
	if len(folded) == 0 {
		t.Fatalf("nothing folded:\n%s", res.Output)
	}
	if want := fmt.Sprintf("laya: %d templates judged routine", len(folded)); laNote(res) != want {
		t.Errorf("note %q, want %q", laNote(res), want)
	}
	for _, n := range svcNames {
		if !strings.Contains(res.Output, "["+n+"-") {
			t.Errorf("template %s no longer represented:\n%s", n, res.Output)
		}
	}
	least := math.MaxInt
	for _, ln := range folded {
		n, _ := strconv.Atoi(strings.ReplaceAll(routineRe.FindStringSubmatch(ln)[1], ",", ""))
		least = min(least, n)
	}
	for _, ln := range strings.Split(res.Output, "\n") {
		if m := regexp.MustCompile(`^\[×([\d,]+)\] `).FindStringSubmatch(ln); m != nil && !routineRe.MatchString(ln) {
			if n, _ := strconv.Atoi(strings.ReplaceAll(m[1], ",", "")); n > least {
				t.Errorf("%q outnumbers a judged template (×%d) but was not sent", ln, least)
			}
		}
	}
	if m := newMissing(textutil.Clean(raw), res.Output, plain.Output); len(m) > 0 {
		t.Errorf("judge cost error lines: %q", m)
	}
	for _, e := range []string{"KeyError: 'lines'", "WARN  [pool] connection pool nearly exhausted", "ERROR [orders] unhandled exception"} {
		if !strings.Contains(res.Output, e) {
			t.Errorf("%q missing:\n%s", e, res.Output)
		}
	}
	tightPlain := judged(dockerLogs, 0, raw, engine.Options{Budget: 2500})
	tight := judged(dockerLogs, 0, raw, engine.Options{Budget: 2500, Judge: &fakeJudge{decide: noise(0.9)}})
	if !omittedRe.MatchString(tightPlain.Output) || !omittedRe.MatchString(tight.Output) {
		t.Fatalf("a 2500-token budget no longer omits templates:\n%s", tight.Output)
	}
	if n := len(routineLines(tight.Output)); n != 0 {
		t.Errorf("%d judged templates kept while others were omitted:\n%s", n, tight.Output)
	}
	if strings.Count(tight.Output, "\n") < strings.Count(tightPlain.Output, "\n")*9/10 {
		t.Errorf("judged tight view %d lines, plain %d", strings.Count(tight.Output, "\n"), strings.Count(tightPlain.Output, "\n"))
	}
	calls, items := fj.seen()
	if calls != 1 {
		t.Errorf("%d judge calls", calls)
	}
	if len(items) != 24 || len(folded) != len(items) {
		t.Errorf("%d items sent, %d folded: want the 24 most frequent templates", len(items), len(folded))
	}
	for _, it := range items {
		if strings.Contains(it, "WARN") || strings.Contains(it, "ERROR") || engine.Classify(it) != engine.Normal {
			t.Errorf("warning/error template sent to the judge: %q", it)
		}
		if !strings.Contains(it, "similar lines]") {
			t.Errorf("item lacks its count: %q", it)
		}
	}
	if fj.families[0] != engine.FamilyLogs || fj.tasks[0] != "fix the orders crash" {
		t.Errorf("family %q task %q", fj.families[0], fj.tasks[0])
	}
	t.Logf("%d → %d tokens (plain view %d)\n%s", res.RawTokens, res.OutTokens, plain.OutTokens, res.Output)
}

func TestJudgeFoldsGenericLogs(t *testing.T) {
	raw := svcLog()
	argv := []string{"./run.sh"}
	plain := judged(argv, 0, raw, engine.Options{})
	res := judged(argv, 0, raw, engine.Options{Judge: &fakeJudge{decide: noise(0.9)}})
	if !strings.HasPrefix(res.Output, "[log: ") || !strings.HasPrefix(plain.Output, "[log: ") {
		t.Fatalf("not templated:\n%s", res.Output)
	}
	if n := len(routineLines(res.Output)); n != 24 || laNote(res) != "laya: 24 templates judged routine" {
		t.Errorf("%d folded, note %q (templateLogs in modes.go must call TemplateLogsFor(c, lines))", n, laNote(res))
	}
	if m := newMissing(textutil.Clean(raw), res.Output, plain.Output); len(m) > 0 {
		t.Errorf("judge cost error lines: %q", m)
	}
}

func TestJudgeUnsureKeepsTodaysView(t *testing.T) {
	raw := svcLog()
	plain := judged(dockerLogs, 0, raw, engine.Options{})
	for name, d := range map[string]func(string, string) engine.JudgeVerdict{
		"needed":   func(string, string) engine.JudgeVerdict { return engine.JudgeVerdict{Keep: true, Confidence: 0.64} },
		"low":      noise(0.64),
		"nan":      noise(math.NaN()),
		"negative": noise(-1),
		"over one": noise(1.5),
	} {
		res := judged(dockerLogs, 0, raw, engine.Options{Judge: &fakeJudge{decide: d}})
		if res.Output != plain.Output || laNote(res) != "" {
			t.Errorf("%s: view changed or noted %q", name, laNote(res))
		}
	}
}

func TestJudgeKeepsNeededTemplatesInFull(t *testing.T) {
	raw := svcLog()
	plain := judged(dockerLogs, 0, raw, engine.Options{})
	fj := &fakeJudge{decide: func(string, string) engine.JudgeVerdict { return engine.JudgeVerdict{Keep: true, Confidence: 0.99} }}
	res := judged(dockerLogs, 0, raw, engine.Options{Judge: fj})
	if laNote(res) != "laya: 24 templates kept in full" || len(routineLines(res.Output)) != 0 {
		t.Errorf("note %q:\n%s", laNote(res), res.Output)
	}
	full := len(regexp.MustCompile(`(?m)^2026-09-26T\S+ INFO .* \[×\d+\]$`).FindAllString(res.Output, -1))
	if full != 24 || res.OutTokens <= plain.OutTokens {
		t.Errorf("%d templates in full, %d tokens (plain %d)", full, res.OutTokens, plain.OutTokens)
	}
}

func TestJudgeThresholdByMode(t *testing.T) {
	raw := svcLog()
	for _, c := range []struct {
		mode engine.Mode
		exit int
		thr  float64
	}{
		{engine.ModeAuto, 0, 0.65},
		{engine.ModeAuto, 1, 0.65},
		{engine.ModeError, 0, 0.80},
		{engine.ModeVerify, 1, 0.80},
		{engine.ModeDebug, 0, 0.75},
		{engine.ModeVerify, 0, 0.60},
		{engine.ModeMinimal, 0, 0.55},
	} {
		plain := judged(dockerLogs, c.exit, raw, engine.Options{Mode: c.mode})
		below := judged(dockerLogs, c.exit, raw, engine.Options{Mode: c.mode, Judge: &fakeJudge{decide: noise(c.thr - 0.01)}})
		at := judged(dockerLogs, c.exit, raw, engine.Options{Mode: c.mode, Judge: &fakeJudge{decide: noise(c.thr)}})
		if below.Output != plain.Output {
			t.Errorf("%v exit %d: view changed below %.2f", c.mode, c.exit, c.thr)
		}
		if at.Output == plain.Output {
			t.Errorf("%v exit %d: view unchanged at %.2f", c.mode, c.exit, c.thr)
		}
	}
}

func TestJudgeFailuresFallBack(t *testing.T) {
	raw := svcLog()
	start := time.Now()
	plain := judged(dockerLogs, 0, raw, engine.Options{})
	base := time.Since(start)
	for name, fj := range map[string]*fakeJudge{
		"error":   {decide: noise(1), err: errors.New("daemon gone")},
		"short":   {decide: noise(1), short: true},
		"panic":   {decide: noise(1), panics: true},
		"timeout": {decide: noise(1), sleep: 3 * time.Second},
	} {
		start := time.Now()
		res := judged(dockerLogs, 0, raw, engine.Options{Judge: fj, JudgeTimeout: 40 * time.Millisecond})
		if el := time.Since(start); el > 2*base+time.Second {
			t.Errorf("%s: took %v (plain view %v)", name, el, base)
		}
		if res.Output != plain.Output || laNote(res) != "" {
			t.Errorf("%s: view changed:\n%s", name, res.Output)
		}
	}
}

func TestJudgeAsksOncePerRun(t *testing.T) {
	focus := &engine.Focus{Terms: []engine.FocusTerm{{Text: "reconciled", Weight: 1}}}
	fj := &fakeJudge{decide: noise(1)}
	res := judged(dockerLogs, 0, svcLog(), engine.Options{Judge: fj, Focus: focus, Budget: 900})
	if !res.Lossy {
		t.Fatal("expected a cut view")
	}
	if calls, _ := fj.seen(); calls != 1 {
		t.Errorf("the focused and focus-free passes asked %d times", calls)
	}
}

func TestJudgeSkipsSmallViews(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "2026-09-26T10:%02d:%02d.000Z INFO  [http] GET /api/orders?page=%d 200 %dms\n", i/60, i%60, i%5, i%90)
	}
	fj := &fakeJudge{decide: noise(1)}
	res := judged(dockerLogs, 0, b.String(), engine.Options{Judge: fj})
	if calls, _ := fj.seen(); calls != 0 {
		t.Errorf("judge called for a %d-line view", res.OutLines)
	}
}

func TestJudgeNeverSeesFocusTemplates(t *testing.T) {
	raw := svcLog()
	fj := &fakeJudge{decide: noise(1)}
	focus := &engine.Focus{Terms: []engine.FocusTerm{{Text: "reconciled", Weight: 1}}}
	res := judged(dockerLogs, 0, raw, engine.Options{Judge: fj, Focus: focus})
	_, items := fj.seen()
	for _, it := range items {
		if strings.Contains(it, "reconciled") {
			t.Errorf("focus template sent: %q", it)
		}
	}
	if !strings.Contains(res.Output, "invoice reconciled with ledger ref=") {
		t.Errorf("focus template folded:\n%s", res.Output)
	}
}

func TestJudgeFoldsRoutineChunks(t *testing.T) {
	raw := buildLog()
	argv := []string{"./build.sh"}
	plain := judged(argv, 1, raw, engine.Options{})
	fj := &fakeJudge{decide: noise(0.95)}
	res := judged(argv, 1, raw, engine.Options{Judge: fj})
	markers := regexp.MustCompile(`(?m)^\[… (\d+) lines judged routine \(laya\) …\]$`).FindAllStringSubmatch(res.Output, -1)
	if len(markers) == 0 {
		t.Fatalf("no chunk folded:\n%s", res.Output)
	}
	n := 0
	for _, m := range markers {
		k, _ := strconv.Atoi(m[1])
		n += k
	}
	if want := fmt.Sprintf("laya: %d routine lines folded", n); laNote(res) != want {
		t.Errorf("note %q, want %q", laNote(res), want)
	}
	clean := textutil.Clean(raw)
	if m := newMissing(clean, res.Output, plain.Output); len(m) > 0 {
		t.Errorf("error lines lost: %q", m)
	}
	for _, s := range []string{"ledger.c:88:5: error", "balanse += amount;", "warning: unused variable", "2 targets failed, 38 succeeded", "==> building project"} {
		if !strings.Contains(res.Output, s) {
			t.Errorf("%q missing:\n%s", s, res.Output)
		}
	}
	_, items := fj.seen()
	for _, it := range items {
		for _, ln := range strings.Split(it, "\n") {
			if engine.Classify(ln) != engine.Normal || strings.Contains(ln, "succeeded") {
				t.Errorf("chunk with a protected line sent: %q", ln)
			}
		}
		if k := strings.Count(it, "\n") + 1; k < 12 || k > 25 {
			t.Errorf("chunk of %d lines", k)
		}
	}
	if fj.families[0] != engine.FamilyLines {
		t.Errorf("family %q", fj.families[0])
	}
	if tokens.Count(res.Output) >= tokens.Count(plain.Output) {
		t.Errorf("no saving")
	}
}

func TestJudgeSkipsTooManyChunks(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 900; i++ {
		fmt.Fprintf(&b, "  step %s: preparing %s artifacts for stage %s\n", svcNames[i%len(svcNames)], svcNames[(i*7)%len(svcNames)], svcNames[(i*3)%len(svcNames)])
	}
	fj := &fakeJudge{decide: noise(1)}
	judged([]string{"./build.sh"}, 0, b.String(), engine.Options{Judge: fj})
	if calls, _ := fj.seen(); calls != 0 {
		t.Errorf("judge called with more than 24 chunks")
	}
}

func TestJudgeListingsNeedMoreConfidence(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "suite %s > case %s handles %s input\n", svcNames[i%len(svcNames)], svcNames[(i*7)%len(svcNames)], svcNames[(i*3)%len(svcNames)])
	}
	argv := []string{"mytool", "list"}
	below := &fakeJudge{decide: noise(0.79)}
	if res := judged(argv, 0, b.String(), engine.Options{Judge: below}); laNote(res) != "" {
		t.Errorf("listing folded at 0.79: %q", laNote(res))
	}
	at := &fakeJudge{decide: noise(0.80)}
	if res := judged(argv, 0, b.String(), engine.Options{Judge: at}); laNote(res) == "" {
		t.Error("listing not folded at 0.80")
	}
	if at.families[0] != engine.FamilyListing {
		t.Errorf("family %q", at.families[0])
	}
}

func TestJudgeNeverCostsErrorLines(t *testing.T) {
	cases := allCaptures(t)
	type extra struct {
		argv []string
		exit int
		raw  string
	}
	var more []extra
	for _, argv := range [][]string{dockerLogs, {"./run.sh"}, {"./build.sh"}} {
		more = append(more, extra{argv, 1, svcLog()}, extra{argv, 1, buildLog()})
	}
	judges := map[string]func(string, string) engine.JudgeVerdict{"noise": noise(1), "hashed": hashed}
	calls := map[string]int{}
	changed := 0
	check := func(name string, argv []string, exit int, raw string) {
		for _, mode := range []engine.Mode{engine.ModeAuto, engine.ModeMinimal} {
			plain := judged(argv, exit, raw, engine.Options{Mode: mode, MaxChars: 26800})
			for jn, d := range judges {
				fj := &fakeJudge{decide: d}
				res := judged(argv, exit, raw, engine.Options{Mode: mode, MaxChars: 26800, Judge: fj})
				n, items := fj.seen()
				calls[jn] += n
				for _, it := range items {
					for _, ln := range strings.Split(it, "\n") {
						if engine.Classify(ln) != engine.Normal && ln != "[…]" {
							t.Errorf("%s (%s): error or warning line sent to the judge: %q", name, mode, ln)
						}
					}
				}
				if res.Output == plain.Output {
					continue
				}
				changed++
				if m := newMissing(textutil.Clean(raw), res.Output, plain.Output); len(m) > 0 {
					t.Errorf("%s (%s, %v judge): lost %q", name, mode, jn, m)
				}
			}
		}
	}
	big := 0
	for _, c := range cases {
		if strings.Count(c.Raw, "\n") < 80 && tokens.Count(c.Raw) < 1500 {
			continue
		}
		big++
		check(c.Category+"/"+c.Name, c.Meta.Argv, c.Meta.ExitCode, c.Raw)
	}
	for i, e := range more {
		check(fmt.Sprintf("synthetic %d %v", i, e.argv), e.argv, e.exit, e.raw)
	}
	if calls["noise"] == 0 || changed == 0 {
		t.Error("the judge never changed a view")
	}
	t.Logf("%d of %d captures big enough to judge; judge calls %v; %d views changed", big, len(cases), calls, changed)
}

func TestJudgeSkipsContentReaders(t *testing.T) {
	src := strings.Repeat("func handle(w http.ResponseWriter, r *http.Request) {\n\tctx := r.Context()\n\tdefer span.End()\n}\n", 120)
	for _, argv := range [][]string{
		{"sed", "-n", "1,480p", "server.go"}, {"nl", "server.go"}, {"awk", "{print}", "server.go"},
		{"timeout", "5", "sed", "-n", "1,480p", "server.go"}, {"env", "LC_ALL=C", "awk", "1", "server.go"}, {"git", "show", "HEAD:server.go"},
	} {
		plain := judged(argv, 0, src, engine.Options{})
		fj := &fakeJudge{decide: noise(1)}
		res := judged(argv, 0, src, engine.Options{Judge: fj})
		if calls, _ := fj.seen(); calls != 0 || res.Output != plain.Output {
			t.Errorf("%v: file content sent to the judge (%d calls) or changed", argv, calls)
		}
	}
	fj := &fakeJudge{decide: noise(1)}
	judged([]string{"./gen.sh"}, 0, src, engine.Options{Judge: fj})
	if calls, _ := fj.seen(); calls == 0 {
		t.Error("the same output from a script was never judged")
	}
}

func TestJudgeNeverSendsAlarmLevels(t *testing.T) {
	var b strings.Builder
	alarms := []string{}
	for i := 0; i < 400; i++ {
		var ln string
		switch {
		case i%40 == 11:
			ln = fmt.Sprintf(`{"level":50,"time":%d,"msg":"db connection lost","attempt":%d}`, 1700000000+i, i)
		case i%40 == 23:
			ln = fmt.Sprintf(`t=2026-09-26T10:00:%02dZ lvl=crit msg="disk nearly full" pct=%d`, i%60, 90+i%9)
		case i%80 == 31:
			ln = fmt.Sprintf("ERRO[%04d] worker %d stalled", i, i)
		case i%80 == 71:
			ln = fmt.Sprintf("2026-09-26 10:00:%02d CRIT raid array degraded disk=%d", i%60, i%4)
		default:
			ln = fmt.Sprintf(`{"level":30,"time":%d,"msg":"request done","path":"/api/%s","ms":%d}`, 1700000000+i, svcNames[i%len(svcNames)], i%97)
		}
		if !strings.Contains(ln, `"level":30`) {
			alarms = append(alarms, ln)
		}
		b.WriteString(ln + "\n")
	}
	for _, argv := range [][]string{dockerLogs, {"./run.sh"}} {
		fj := &fakeJudge{decide: noise(1)}
		res := judged(argv, 0, b.String(), engine.Options{Judge: fj})
		calls, items := fj.seen()
		if calls == 0 {
			t.Fatalf("%v: never judged", argv)
		}
		for _, it := range items {
			if strings.Contains(it, `"level":50`) || strings.Contains(it, "lvl=crit") || strings.Contains(it, "ERRO[") || strings.Contains(it, "CRIT") {
				t.Errorf("%v: error-level record sent: %q", argv, it)
			}
		}
		for _, a := range alarms {
			if !strings.Contains(res.Output, a) {
				t.Errorf("%v: %q folded", argv, a)
			}
		}
	}
}

func TestJudgeKeepsTemplatesHidingAlarms(t *testing.T) {
	raw := svcLog()
	for i := 0; i < 60; i++ {
		lvl := "info"
		if i%20 == 9 {
			lvl = "crit"
		}
		raw += fmt.Sprintf("\n2026-09-26T11:%02d:%02d.000Z msg=sweep job=cache lvl=%s host=web1 n=%d", i/60, i%60, lvl, i*7)
	}
	plain := judged(dockerLogs, 0, raw, engine.Options{})
	if !strings.Contains(plain.Output, "crit ×3") {
		t.Fatalf("fixture no longer merges the crit lines into one template:\n%s", plain.Output)
	}
	fj := &fakeJudge{decide: noise(1)}
	res := judged(dockerLogs, 0, raw, engine.Options{Judge: fj})
	_, items := fj.seen()
	for _, it := range items {
		if strings.Contains(it, "msg=sweep") {
			t.Errorf("template with crit lines sent: %q", it)
		}
	}
	if !strings.Contains(res.Output, "crit ×3") || len(routineLines(res.Output)) == 0 {
		t.Errorf("crit lines folded, or nothing folded:\n%s", res.Output)
	}
}

func TestJudgeKeepsPlanChanges(t *testing.T) {
	var b strings.Builder
	b.WriteString("Terraform will perform the following actions:\n\n")
	for i := 0; i < 30; i++ {
		verb, sym := "created", "+"
		if i == 17 {
			verb, sym = "destroyed", "-"
		}
		fmt.Fprintf(&b, "  # aws_s3_object.asset[%d] will be %s\n  %s resource \"aws_s3_object\" \"asset\" {\n      %s bucket = \"prod-assets\"\n"+
			"      %s key    = \"static/file-%d.js\"\n      %s etag   = (known after apply)\n    }\n\n", i, verb, sym, sym, sym, i, sym)
	}
	b.WriteString("Plan: 29 to add, 0 to change, 1 to destroy.\n")
	fj := &fakeJudge{decide: noise(1)}
	res := judged([]string{"terraform", "plan"}, 0, b.String(), engine.Options{Judge: fj})
	if !strings.Contains(res.Output, "asset[17] will be destroyed") || !strings.Contains(res.Output, `- key    = "static/file-17.js"`) {
		t.Errorf("planned destroy folded:\n%s", res.Output)
	}
	_, items := fj.seen()
	for _, it := range items {
		for _, ln := range strings.Split(it, "\n") {
			if s := strings.TrimSpace(ln); strings.HasPrefix(s, "+ ") || strings.HasPrefix(s, "- ") {
				t.Errorf("change line sent: %q", ln)
			}
		}
	}
}
