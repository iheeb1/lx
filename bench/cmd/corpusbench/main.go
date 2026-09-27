// Command corpusbench runs every capture in testdata/corpus through lx's
// full pipeline (all filters registered) and records what an agent would
// have read with and without lx, plus fidelity metrics.
//
//	go run ./bench/cmd/corpusbench -out bench/out/results.json
//
// Token counts here come from lx's offline estimator; bench/tiktoken.py adds
// exact cl100k/o200k counts from the saved views for the published numbers.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/iheeb1/lx/internal/baseline"
	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

type Row struct {
	Category   string `json:"category"`
	Name       string `json:"name"`
	Shell      string `json:"shell"`
	Exit       int    `json:"exit"`
	Filter     string `json:"filter"`
	Lossy      bool   `json:"lossy"`
	RawBytes   int    `json:"raw_bytes"`
	OutBytes   int    `json:"out_bytes"`
	RawTokens  int    `json:"raw_tokens"`
	OutTokens  int    `json:"out_tokens"`
	RawLines   int    `json:"raw_lines"`
	OutLines   int    `json:"out_lines"`
	ErrLines   int    `json:"error_lines"`
	ErrKept    int    `json:"error_lines_kept"`
	Locs       int    `json:"locations"`
	LocsKept   int    `json:"locations_kept"`
	AppLocs    int    `json:"app_locations"`
	AppKept    int    `json:"app_locations_kept"`
	GuardAdded int    `json:"guard_added"`
	Micros     int64  `json:"process_us"`
	View       string `json:"view"` // path of the condensed output, relative to -out's dir

	// What agents do without lx, scored the same way.
	Tail40   Naive `json:"tail40"`   // `cmd | tail -40`
	HeadTail Naive `json:"headtail"` // head+tail cut to lx's token count
}

type Naive struct {
	Tokens   int `json:"tokens"`
	ErrKept  int `json:"error_lines_kept"`
	LocsKept int `json:"locations_kept"`
	AppKept  int `json:"app_locations_kept"`
}

type Results struct {
	Generated string         `json:"generated"`
	Cases     []Row          `json:"cases"`
	Totals    map[string]Tot `json:"totals"` // by category, plus "all"
	Estimator []EstRow       `json:"estimator,omitempty"`
}

// EstRow scores a token-estimation method against exact tiktoken counts.
type EstRow struct {
	Method   string  `json:"method"`   // "bytes/4", "bytes/3", "lx"
	Encoding string  `json:"encoding"` // cl100k_base, o200k_base
	MeanErr  float64 `json:"mean_abs_err_pct"`
	P90Err   float64 `json:"p90_abs_err_pct"`
	MaxErr   float64 `json:"max_abs_err_pct"`
	Files    int     `json:"files"`
}

type Tot struct {
	Cases     int     `json:"cases"`
	RawTokens int     `json:"raw_tokens"`
	OutTokens int     `json:"out_tokens"`
	SavedPct  float64 `json:"saved_pct"`
	ErrLines  int     `json:"error_lines"`
	ErrKept   int     `json:"error_lines_kept"`
	Locs      int     `json:"locations"`
	LocsKept  int     `json:"locations_kept"`
}

func main() {
	out := flag.String("out", "bench/out/results.json", "results file")
	corpus := flag.String("corpus", filepath.Join(fixture.Root(), "testdata", "corpus"), "corpus dir")
	quiet := flag.Bool("q", false, "no table")
	flag.Parse()

	cases, err := fixture.ReadAll(*corpus)
	if err != nil {
		fatal(err)
	}
	viewDir := filepath.Join(filepath.Dir(*out), "views")
	res := Results{Generated: time.Now().UTC().Format(time.RFC3339), Totals: map[string]Tot{}}
	overUncapped, overCapped := 0, 0 // views over Claude Code's default cap (30,000×9/10 − 200)
	for _, fc := range cases {
		c := fc.Context()
		start := time.Now()
		pr := engine.Process(c, fc.Raw, engine.Options{})
		us := time.Since(start).Microseconds()
		if len(pr.Output) > 26800 {
			overUncapped++
		}
		if cp := engine.Process(fc.Context(), fc.Raw, engine.Options{MaxChars: 26800}); len(cp.Output) > 26800 {
			overCapped++
		}
		view := pr.Output
		if pr.Lossy {
			view += "\n" + engine.Receipt(pr, "1")
		}
		clean := textutil.Clean(fc.Raw)
		errIn := countErrorLines(clean)
		locIn := len(uniq(fixture.LocRe.FindAllString(clean, -1)))
		r := Row{
			Category: fc.Category, Name: fc.Name, Shell: fc.Meta.Shell, Exit: fc.Meta.ExitCode,
			Filter: pr.Filter, Lossy: pr.Lossy,
			RawBytes: len(fc.Raw), OutBytes: len(view),
			RawTokens: pr.RawTokens, OutTokens: tokensOf(pr, view),
			RawLines: pr.RawLines, OutLines: strings.Count(view, "\n") + 1,
			ErrLines: errIn, ErrKept: errIn - len(fixture.ErrorMessagesMissing(clean, view)),
			Locs: locIn, LocsKept: locIn - len(fixture.LocationsMissing(clean, view)),
			AppLocs: len(fixture.AppLocations(clean)), AppKept: len(fixture.AppLocations(clean)) - len(fixture.AppLocationsMissing(clean, view)),
			GuardAdded: pr.GuardAdded, Micros: us,
			View: filepath.Join("views", fc.Category, fc.Name+".txt"),
		}
		r.Tail40 = score(clean, tailLines(clean, 40), locIn)
		r.HeadTail = score(clean, baseline.HeadTail(clean, r.OutTokens), locIn)
		if pr.Filter == "passthrough" {
			view = fc.Raw
		}
		p := filepath.Join(viewDir, fc.Category, fc.Name+".txt")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(view), 0o644); err != nil {
			fatal(err)
		}
		res.Cases = append(res.Cases, r)
		for _, k := range []string{fc.Category, "all"} {
			t := res.Totals[k]
			t.Cases++
			t.RawTokens += r.RawTokens
			t.OutTokens += r.OutTokens
			t.ErrLines += r.ErrLines
			t.ErrKept += r.ErrKept
			t.Locs += r.Locs
			t.LocsKept += r.LocsKept
			res.Totals[k] = t
		}
	}
	fmt.Fprintf(os.Stderr, "views over 26,800 chars: %d uncapped, %d with MaxChars 26800\n", overUncapped, overCapped)
	for k, t := range res.Totals {
		if t.RawTokens > 0 {
			t.SavedPct = 100 * float64(t.RawTokens-t.OutTokens) / float64(t.RawTokens)
		}
		res.Totals[k] = t
	}
	res.Estimator = estimator(*corpus, filepath.Join(filepath.Dir(*out), "tokstats.json"))
	b, _ := json.MarshalIndent(res, "", "  ")
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fatal(err)
	}
	if !*quiet {
		printTable(res)
	}
}

// tokensOf counts what the agent reads, receipt included.
func tokensOf(pr engine.Result, view string) int {
	if pr.Filter == "passthrough" {
		return pr.RawTokens
	}
	return engineCount(view)
}

// countErrorLines counts distinct error messages (see
// fixture.ErrorMessagesMissing): the denominator of "errors kept".
func countErrorLines(s string) int {
	return len(fixture.ErrorMessagesMissing(s, ""))
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func printTable(res Results) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "case\tfilter\traw tok\tlx tok\tsaved\terrors kept\tlocs kept\tµs\t")
	rows := append([]Row(nil), res.Cases...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Category < rows[j].Category })
	for _, r := range rows {
		fmt.Fprintf(w, "%s/%s\t%s\t%d\t%d\t%s\t%d/%d\t%d/%d\t%d\t\n", r.Category, r.Name, r.Filter,
			r.RawTokens, r.OutTokens, pct(r.RawTokens, r.OutTokens), r.ErrKept, r.ErrLines, r.LocsKept, r.Locs, r.Micros)
	}
	w.Flush()
	fmt.Println()
	cats := make([]string, 0, len(res.Totals))
	for k := range res.Totals {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "category\tcases\traw tok\tlx tok\tsaved\terrors kept\tlocs kept\t")
	for _, k := range cats {
		t := res.Totals[k]
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%.1f%%\t%d/%d\t%d/%d\t\n", k, t.Cases, t.RawTokens, t.OutTokens, t.SavedPct, t.ErrKept, t.ErrLines, t.LocsKept, t.Locs)
	}
	w.Flush()
}

func pct(raw, out int) string {
	if raw == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(raw-out)/float64(raw))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "corpusbench:", err)
	os.Exit(1)
}

func score(clean, view string, locIn int) Naive {
	errIn := countErrorLines(clean)
	return Naive{
		Tokens:   engineCount(view),
		ErrKept:  errIn - len(fixture.ErrorMessagesMissing(clean, view)),
		LocsKept: locIn - len(fixture.LocationsMissing(clean, view)),
		AppKept:  len(fixture.AppLocations(clean)) - len(fixture.AppLocationsMissing(clean, view)),
	}
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// estimator scores lx's offline token estimator and the bytes/N rules of
// thumb against exact counts written by bench/tiktoken_counts.py.
func estimator(corpus, statsPath string) []EstRow {
	b, err := os.ReadFile(statsPath)
	if err != nil {
		return nil
	}
	var rows [][]any
	if json.Unmarshal(b, &rows) != nil {
		return nil
	}
	type pt struct{ bytes, est, exact [2]float64 }
	var pts []pt
	for _, row := range rows {
		raw, err := os.ReadFile(filepath.Join(corpus, row[0].(string)))
		if err != nil {
			continue
		}
		s := ansiRe.ReplaceAllString(string(raw), "")
		if len(s) < 200 {
			continue
		}
		est := float64(engineCount(s))
		pts = append(pts, pt{
			bytes: [2]float64{float64(len(s)), float64(len(s))},
			est:   [2]float64{est, est},
			exact: [2]float64{row[2].(float64), row[3].(float64)},
		})
	}
	var out []EstRow
	for ei, enc := range []string{"cl100k_base", "o200k_base"} {
		for _, m := range []string{"bytes/4", "bytes/3", "lx"} {
			var errs []float64
			for _, p := range pts {
				var guess float64
				switch m {
				case "bytes/4":
					guess = p.bytes[ei] / 4
				case "bytes/3":
					guess = p.bytes[ei] / 3
				default:
					guess = p.est[ei]
				}
				e := guess - p.exact[ei]
				if e < 0 {
					e = -e
				}
				errs = append(errs, 100*e/p.exact[ei])
			}
			sort.Float64s(errs)
			sum := 0.0
			for _, e := range errs {
				sum += e
			}
			out = append(out, EstRow{Method: m, Encoding: enc, Files: len(errs),
				MeanErr: sum / float64(len(errs)), P90Err: errs[len(errs)*9/10], MaxErr: errs[len(errs)-1]})
		}
	}
	return out
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
