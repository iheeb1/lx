// Command charts renders the README charts from benchmark results.
//
//	go run ./bench/cmd/charts -in bench/out -out docs/img
//
// Reads results.json (corpusbench, with exact counts from
// bench/tiktoken_counts.py when available) and h2h.json (optional).
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
)

type exactPair struct {
	Raw map[string]int `json:"raw"`
	Lx  map[string]int `json:"lx"`
}

type naive struct {
	Tokens   int `json:"tokens"`
	ErrKept  int `json:"error_lines_kept"`
	LocsKept int `json:"locations_kept"`
	AppKept  int `json:"app_locations_kept"`
}

type row struct {
	Category  string     `json:"category"`
	Name      string     `json:"name"`
	Shell     string     `json:"shell"`
	Exit      int        `json:"exit"`
	Filter    string     `json:"filter"`
	RawTokens int        `json:"raw_tokens"`
	OutTokens int        `json:"out_tokens"`
	ErrLines  int        `json:"error_lines"`
	ErrKept   int        `json:"error_lines_kept"`
	Locs      int        `json:"locations"`
	LocsKept  int        `json:"locations_kept"`
	AppLocs   int        `json:"app_locations"`
	AppKept   int        `json:"app_locations_kept"`
	Tail40    naive      `json:"tail40"`
	HeadTail  naive      `json:"headtail"`
	Exact     *exactPair `json:"exact"`
}

type results struct {
	Cases     []row `json:"cases"`
	Estimator []struct {
		Method   string  `json:"method"`
		Encoding string  `json:"encoding"`
		MeanErr  float64 `json:"mean_abs_err_pct"`
	} `json:"estimator"`
}

type variant struct {
	Rewritten bool           `json:"rewritten"`
	Tokens    int            `json:"tokens"`
	ErrKept   int            `json:"error_lines_kept"`
	MedianMs  float64        `json:"median_ms"`
	Exit      int            `json:"exit"`
	Exact     map[string]int `json:"exact"`
}

type h2hRow struct {
	ID       string  `json:"id"`
	Category string  `json:"category"`
	Shell    string  `json:"shell"`
	ErrLines int     `json:"error_lines"`
	Raw      variant `json:"raw"`
	Rtk      variant `json:"rtk"`
	Lx       variant `json:"lx"`
}

const enc = "o200k_base"

var (
	sLx  = Series{"with lx", "s1"}
	sRtk = Series{"with rtk 0.50", "s2"}
	sRaw = Series{"raw output", "s3"}
)

func main() {
	in := flag.String("in", "bench/out", "benchmark output dir")
	out := flag.String("out", "docs/img", "chart output dir")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	var res results
	readJSON(filepath.Join(*in, "results.json"), &res)
	write(*out, "savings-by-category.svg", savingsByCategory(res))
	write(*out, "biggest-outputs.svg", biggest(res))
	write(*out, "fidelity.svg", fidelity(res))
	if len(res.Estimator) > 0 {
		write(*out, "estimator.svg", estimatorChart(res))
	}
	var h2h []h2hRow
	if readJSON(filepath.Join(*in, "h2h.json"), &h2h) {
		write(*out, "h2h-tokens.svg", h2hTokens(h2h))
		write(*out, "h2h-fidelity.svg", h2hFidelity(h2h))
		write(*out, "overhead.svg", overhead(h2h))
	}
}

func tok(r row) (raw, lx float64) {
	if r.Exact != nil {
		return float64(r.Exact.Raw[enc]), float64(r.Exact.Lx[enc])
	}
	return float64(r.RawTokens), float64(r.OutTokens)
}

var catLabel = map[string]string{
	"git": "git", "go": "Go toolchain", "node": "Node / npm / TS", "python": "Python / pytest",
	"fs": "ls / find / du", "search": "grep / rg", "data": "curl / cat / JSON", "misc": "make / C builds",
}

func savingsByCategory(res results) string {
	type agg struct{ raw, lx float64 }
	by := map[string]*agg{}
	var all agg
	for _, r := range res.Cases {
		raw, lx := tok(r)
		if by[r.Category] == nil {
			by[r.Category] = &agg{}
		}
		by[r.Category].raw += raw
		by[r.Category].lx += lx
		all.raw += raw
		all.lx += lx
	}
	var rows []BarRow
	for k, a := range by {
		label := catLabel[k]
		if label == "" {
			label = k
		}
		rows = append(rows, BarRow{Label: label, Values: []float64{100 * (a.raw - a.lx) / a.raw},
			Notes: []string{fmt.Sprintf("%s → %s tokens", compact(a.raw), compact(a.lx))}})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Values[0] > rows[j].Values[0] })
	rows = append(rows, BarRow{Label: "All 116 captures", Values: []float64{100 * (all.raw - all.lx) / all.raw},
		Notes: []string{fmt.Sprintf("%s → %s tokens", compact(all.raw), compact(all.lx))}})
	return HBars("Tokens saved, by kind of command",
		fmt.Sprintf("116 real outputs from 11 open-source repos · o200k tokens · %s → %s overall", compact(all.raw), compact(all.lx)),
		[]Series{sLx}, rows, 100, "%")
}

func biggest(res results) string {
	cases := append([]row(nil), res.Cases...)
	sort.Slice(cases, func(i, j int) bool {
		a, _ := tok(cases[i])
		b, _ := tok(cases[j])
		return a > b
	})
	var rows []DumbRow
	for _, r := range cases[:min(14, len(cases))] {
		raw, lx := tok(r)
		rows = append(rows, DumbRow{Label: shortShell(r.Shell), Values: []float64{raw, lx}})
	}
	return Dumbbell("The biggest outputs, before and after",
		"14 largest captures · o200k tokens per run (log scale) · label = reduction factor",
		[]Series{sRaw, sLx}, rows, "")
}

// fidelity compares, over failing runs only, how much diagnostic signal each
// approach keeps: lx vs a head+tail cut to the SAME size vs `| tail -40`.
func fidelity(res results) string {
	var errIn, errLx, errHT, errTail, locIn, locLx, locHT, locTail float64
	var tokLx, tokHT, tokTail, tokRaw float64
	n := 0
	for _, r := range res.Cases {
		if r.Exit == 0 || r.ErrLines == 0 {
			continue
		}
		n++
		errIn += float64(r.ErrLines)
		errLx += float64(r.ErrKept)
		errHT += float64(r.HeadTail.ErrKept)
		errTail += float64(r.Tail40.ErrKept)
		locIn += float64(r.AppLocs)
		locLx += float64(r.AppKept)
		locHT += float64(r.HeadTail.AppKept)
		locTail += float64(r.Tail40.AppKept)
		tokRaw += float64(r.RawTokens)
		tokLx += float64(r.OutTokens)
		tokHT += float64(r.HeadTail.Tokens)
		tokTail += float64(r.Tail40.Tokens)
	}
	pct := func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return 100 * a / b
	}
	series := []Series{
		{"lx", "s1"},
		{"head+tail cut to lx's size", "s2"},
		{"| tail -40", "s3"},
	}
	rows := []BarRow{
		{Label: "error messages kept", Values: []float64{pct(errLx, errIn), pct(errHT, errIn), pct(errTail, errIn)}},
		{Label: "app file:line locations kept", Values: []float64{pct(locLx, locIn), pct(locHT, locIn), pct(locTail, locIn)}},
		{Label: "tokens saved", Values: []float64{100 - pct(tokLx, tokRaw), 100 - pct(tokHT, tokRaw), 100 - pct(tokTail, tokRaw)}},
	}
	return HBars("Same token budget, very different outcomes",
		fmt.Sprintf("%d failing runs (tests, builds, linters) · %s distinct error messages and %s app-code locations in the raw output", n, compact(errIn), compact(locIn)),
		series, rows, 100, "%")
}

func estimatorChart(res results) string {
	var rows []BarRow
	label := map[string]string{"bytes/4": "bytes ÷ 4 (common rule)", "bytes/3": "bytes ÷ 3", "lx": "lx estimator"}
	for _, m := range []string{"bytes/4", "bytes/3", "lx"} {
		var vals []float64
		for _, e := range []string{"cl100k_base", "o200k_base"} {
			for _, er := range res.Estimator {
				if er.Method == m && er.Encoding == e {
					vals = append(vals, er.MeanErr)
				}
			}
		}
		if len(vals) == 2 {
			rows = append(rows, BarRow{Label: label[m], Values: vals})
		}
	}
	return HBars("Counting tokens offline: mean error per capture",
		"lx's pre-tokenizer estimator vs exact tiktoken counts on the same 109 captures (lower is better)",
		[]Series{{"vs cl100k", "s1"}, {"vs o200k", "s2"}}, rows, 0, "%")
}

func h2hTok(v variant) float64 {
	if v.Exact != nil {
		return float64(v.Exact[enc])
	}
	return float64(v.Tokens)
}

func h2hTokens(rows []h2hRow) string {
	sort.Slice(rows, func(i, j int) bool { return h2hTok(rows[i].Raw) > h2hTok(rows[j].Raw) })
	var out []DumbRow
	for _, r := range rows[:min(18, len(rows))] {
		out = append(out, DumbRow{Label: shortShell(r.Shell), Values: []float64{h2hTok(r.Raw), h2hTok(r.Rtk), h2hTok(r.Lx)}})
	}
	return Dumbbell("Head to head on live commands",
		"same repo, same command, run raw / through rtk / through lx · o200k tokens (log scale) · label = raw ÷ lx",
		[]Series{sRaw, sRtk, sLx}, out, "")
}

func h2hFidelity(rows []h2hRow) string {
	var raw, rtk, lx, e, eRtk, eLx float64
	var fRaw, fRtk, fLx float64
	for _, r := range rows {
		raw += h2hTok(r.Raw)
		rtk += h2hTok(r.Rtk)
		lx += h2hTok(r.Lx)
		if r.Raw.Exit != 0 && r.ErrLines > 0 {
			e += float64(r.ErrLines)
			eRtk += float64(r.Rtk.ErrKept)
			eLx += float64(r.Lx.ErrKept)
			fRaw += h2hTok(r.Raw)
			fRtk += h2hTok(r.Rtk)
			fLx += h2hTok(r.Lx)
		}
	}
	return HBars("lx vs rtk: savings and what survives",
		fmt.Sprintf("%d live commands · error lines counted on failing runs only (%s in raw output)", len(rows), compact(e)),
		[]Series{sRtk, sLx},
		[]BarRow{
			{Label: "tokens saved, all runs", Values: []float64{100 * (raw - rtk) / raw, 100 * (raw - lx) / raw}},
			{Label: "tokens saved, failing runs", Values: []float64{100 * (fRaw - fRtk) / fRaw, 100 * (fRaw - fLx) / fRaw}},
			{Label: "error lines kept", Values: []float64{100 * eRtk / e, 100 * eLx / e}},
		}, 100, "%")
}

func overhead(rows []h2hRow) string {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Raw.MedianMs < rows[j].Raw.MedianMs })
	var out []BarRow
	for _, r := range rows {
		if !r.Rtk.Rewritten || !r.Lx.Rewritten || r.Raw.MedianMs > 2000 {
			continue
		}
		out = append(out, BarRow{Label: shortShell(r.Shell), Values: []float64{
			pos(r.Rtk.MedianMs - r.Raw.MedianMs), pos(r.Lx.MedianMs - r.Raw.MedianMs)}})
		if len(out) == 10 {
			break
		}
	}
	return HBars("Added latency per command",
		"median wall time minus the raw command's, macOS arm64 (fast commands, where overhead is most visible)",
		[]Series{sRtk, sLx}, out, 0, "ms")
}

func pos(v float64) float64 { return max(v, 0) }

var envPrefix = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)+`)

func shortShell(s string) string {
	s = strings.ReplaceAll(s, " 2>&1", "")
	s = envPrefix.ReplaceAllString(s, "")
	if len(s) > 34 {
		s = s[:33] + "…"
	}
	return s
}

func readJSON(p string, v any) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(b, v); err != nil {
		fatal(fmt.Errorf("%s: %w", p, err))
	}
	return true
}

func write(dir, name, svg string) {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(svg), 0o644); err != nil {
		fatal(err)
	}
	fmt.Println("wrote", filepath.Join(dir, name))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "charts:", err)
	os.Exit(1)
}
