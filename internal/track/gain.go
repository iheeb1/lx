package track

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Summary aggregates records. Savings are never clamped: a command where lx
// made output bigger counts against the total (it shouldn't happen — the
// never-worse gate prevents it — and if it does you should see it).
//
// Recalls (`lx show` of a stored run) are not commands: they add nothing to
// Commands, Condensed, Raw, Out, Saved, Failures or Daily. The tokens they
// printed are RecallTokens, and NetSaved = Saved − RecallTokens, which can
// be negative: reading a run back in full costs more than its view saved.
type Summary struct {
	Commands     int       `json:"commands"`
	Condensed    int       `json:"condensed"` // commands where lx changed the view
	Raw          int       `json:"raw_tokens"`
	Out          int       `json:"out_tokens"`
	Saved        int       `json:"saved_tokens"`
	Pct          float64   `json:"saved_pct"`
	Failures     int       `json:"failed_commands"`
	Recalls      int       `json:"recalls"`       // lx show runs
	RecallTokens int       `json:"recall_tokens"` // tokens they printed
	NetSaved     int       `json:"net_saved_tokens"`
	NetPct       float64   `json:"net_saved_pct"`
	ByCmd        []CmdStat `json:"by_command"`
	Daily        []DayStat `json:"daily"`
}

type CmdStat struct {
	Cmd          string  `json:"cmd"`
	Count        int     `json:"count"`
	Raw          int     `json:"raw_tokens"`
	Saved        int     `json:"saved_tokens"`
	Pct          float64 `json:"saved_pct"`
	AvgMs        int64   `json:"avg_ms"`
	Recalls      int     `json:"recalls"`
	RecallTokens int     `json:"recall_tokens"`
}

type DayStat struct {
	Day   string `json:"day"`
	Raw   int    `json:"raw_tokens"`
	Saved int    `json:"saved_tokens"`
}

// Summarize builds the report; days bounds the daily series.
func Summarize(recs []Record, days int, now time.Time) Summary {
	var s Summary
	by := map[string]*CmdStat{}
	ms := map[string]int64{}
	daily := map[string]*DayStat{}
	stat := func(cmd string) *CmdStat {
		c := by[cmd]
		if c == nil {
			c = &CmdStat{Cmd: cmd}
			by[cmd] = c
		}
		return c
	}
	for _, r := range recs {
		if r.Kind == KindShow {
			s.Recalls++
			s.RecallTokens += r.Out
			c := stat(r.Cmd)
			c.Recalls++
			c.RecallTokens += r.Out
			continue
		}
		s.Commands++
		s.Raw += r.Raw
		s.Out += r.Out
		if r.Out != r.Raw {
			s.Condensed++
		}
		if r.Exit != 0 {
			s.Failures++
		}
		c := stat(r.Cmd)
		c.Count++
		c.Raw += r.Raw
		c.Saved += r.Raw - r.Out
		ms[r.Cmd] += r.Ms
		d := time.Unix(r.Time, 0).Local().Format("2006-01-02")
		if daily[d] == nil {
			daily[d] = &DayStat{Day: d}
		}
		daily[d].Raw += r.Raw
		daily[d].Saved += r.Raw - r.Out
	}
	s.Saved = s.Raw - s.Out
	s.Pct = pct(s.Saved, s.Raw)
	s.NetSaved = s.Saved - s.RecallTokens
	s.NetPct = pct(s.NetSaved, s.Raw)
	for k, c := range by {
		c.Pct = pct(c.Saved, c.Raw)
		if c.Count > 0 {
			c.AvgMs = ms[k] / int64(c.Count)
		}
		s.ByCmd = append(s.ByCmd, *c)
	}
	sort.Slice(s.ByCmd, func(i, j int) bool {
		if s.ByCmd[i].Saved != s.ByCmd[j].Saved {
			return s.ByCmd[i].Saved > s.ByCmd[j].Saved
		}
		return s.ByCmd[i].Cmd < s.ByCmd[j].Cmd
	})
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		if ds := daily[d]; ds != nil {
			s.Daily = append(s.Daily, *ds)
		} else {
			s.Daily = append(s.Daily, DayStat{Day: d})
		}
	}
	return s
}

func pct(saved, raw int) float64 {
	if raw == 0 {
		return 0
	}
	return 100 * float64(saved) / float64(raw)
}

// Text renders the report for a terminal. Recall lines and the recalls
// column appear once anything was read back with lx show.
func (s Summary) Text(w io.Writer, top int) {
	if s.Commands == 0 && s.Recalls == 0 {
		fmt.Fprintln(w, "lx gain: no commands recorded yet. Run something through lx (e.g. `lx git status`).")
		return
	}
	recalls := s.Recalls > 0
	fmt.Fprintf(w, "lx gain — %s through lx, %s condensed\n\n", plural(s.Commands, "command"), plural(s.Condensed, "view"))
	row := func(label string, n int, note string) {
		fmt.Fprintf(w, "  %-33s%10s%s\n", label, human(n), note)
	}
	row("tokens the agent would have read", s.Raw, "")
	row("tokens it actually read", s.Out, "")
	row("saved", s.Saved, fmt.Sprintf("  (%.1f%%)", s.Pct))
	if recalls {
		row("read back with lx show", s.RecallTokens, "  ("+plural(s.Recalls, "recall")+")")
		row("net saved", s.NetSaved, fmt.Sprintf("  (%.1f%%)", s.NetPct))
		fmt.Fprintf(w, "  %s\n\n", meter(s.NetPct, 40))
	} else {
		fmt.Fprintf(w, "  %s\n\n", meter(s.Pct, 40))
	}

	if len(s.ByCmd) > 0 {
		if recalls {
			fmt.Fprintf(w, "  %-22s %6s %10s %10s %7s %7s  %s\n", "command", "runs", "raw", "saved", "saved%", "recalls", "")
		} else {
			fmt.Fprintf(w, "  %-22s %6s %10s %10s %7s  %s\n", "command", "runs", "raw", "saved", "saved%", "")
		}
		maxSaved := 1
		for _, c := range s.ByCmd {
			maxSaved = max(maxSaved, c.Saved)
		}
		for i, c := range s.ByCmd {
			if i == top {
				fmt.Fprintf(w, "  … %d more commands\n", len(s.ByCmd)-top)
				break
			}
			bar := strings.Repeat("█", max(0, c.Saved*24/maxSaved))
			if recalls {
				fmt.Fprintf(w, "  %-22s %6d %10s %10s %6.1f%% %7d  %s\n", trunc(c.Cmd, 22), c.Count, human(c.Raw), human(c.Saved), c.Pct, c.Recalls, bar)
			} else {
				fmt.Fprintf(w, "  %-22s %6d %10s %10s %6.1f%%  %s\n", trunc(c.Cmd, 22), c.Count, human(c.Raw), human(c.Saved), c.Pct, bar)
			}
		}
		fmt.Fprintln(w)
	}
	if len(s.Daily) > 0 {
		fmt.Fprintf(w, "  saved per day (last %d days)\n  %s\n", len(s.Daily), spark(s.Daily))
		fmt.Fprintf(w, "  %s%s%s\n", s.Daily[0].Day, strings.Repeat(" ", max(1, len(s.Daily)-20)), s.Daily[len(s.Daily)-1].Day)
	}
}

func meter(p float64, width int) string {
	n := int(p/100*float64(width) + 0.5)
	n = min(max(n, 0), width)
	return "[" + strings.Repeat("█", n) + strings.Repeat("░", width-n) + "]"
}

func spark(days []DayStat) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	mx := 0
	for _, d := range days {
		mx = max(mx, d.Saved)
	}
	var b strings.Builder
	for _, d := range days {
		if d.Saved <= 0 || mx == 0 {
			b.WriteRune('·')
			continue
		}
		b.WriteRune(bars[min(len(bars)-1, d.Saved*(len(bars)-1)/mx)])
	}
	return b.String()
}

func human(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	var s string
	switch {
	case n >= 1_000_000:
		s = fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 10_000:
		s = fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		s = fmt.Sprint(n)
	}
	if neg {
		return "-" + s
	}
	return s
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return fmt.Sprintf("%d %ss", n, w)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
