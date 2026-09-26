package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func TestGenericCorpus(t *testing.T) {
	dump := os.Getenv("LX_DUMP")
	type row struct {
		name    string
		in, out int
		missing int
		elapsed time.Duration
	}
	var rows []row
	cat := map[string][2]int{}
	for _, c := range fixture.All(t) {
		in := c.Clean()
		ctx := c.Context()
		start := time.Now()
		out, shape := engine.GenericShape(ctx, in)
		el := time.Since(start)
		out2 := engine.Generic(ctx, in)
		name := c.Category + "/" + c.Name
		if out != out2 {
			t.Errorf("%s: not deterministic", name)
		}
		ti, to := tokens.Count(in), tokens.Count(out)
		if to > ti {
			t.Errorf("%s: output has more tokens (%d > %d)", name, to, ti)
		}
		miss := unexplainedMissing(in, out, shape)
		if len(miss) > 0 {
			t.Errorf("%s: %d error lines missing, e.g. %q", name, len(miss), miss[0])
		}
		if dump != "" {
			p := filepath.Join(dump, c.Category+"__"+c.Name+".out")
			os.WriteFile(p, []byte(out), 0o644)
		}
		rows = append(rows, row{name + " [" + shape + "]", ti, to, len(miss), el})
		v := cat[c.Category]
		cat[c.Category] = [2]int{v[0] + ti, v[1] + to}
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-50s %8d → %8d  %5.1f%%  %v\n", r.name, r.in, r.out, 100*(1-float64(r.out)/float64(max(r.in, 1))), r.elapsed.Round(time.Millisecond))
	}
	var cats []string
	for k := range cat {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	for _, k := range cats {
		v := cat[k]
		fmt.Fprintf(&b, "%-10s %9d → %9d  %5.1f%%\n", k, v[0], v[1], 100*(1-float64(v[1])/float64(max(v[0], 1))))
	}
	t.Log("\n" + b.String())
}

// unexplainedMissing returns the error-class input lines absent from out
// that no justified exception covers:
//
//   - shape "json": the missing lines are JSON source lines whose string
//     values merely contain error words (issue titles/bodies). They are
//     data, re-rendered as table cells / minified JSON, not status lines.
//   - shape "paths": the missing lines are file names containing words
//     like "error" or "conflict"; the tree still lists every name (checked:
//     the base name, or a pruned heavy directory, appears in the output).
//   - lines longer than 1200 runes (minified bundles) are cut by
//     ShortenLine, as they always were; their head must still appear.
func unexplainedMissing(in, out, shape string) []string {
	var bad []string
	for _, ln := range engine.MissingErrorLines(in, out) {
		switch {
		case shape == "json":
			continue
		case shape == "paths":
			base := strings.TrimSuffix(ln, ":")
			base = base[strings.LastIndexByte(base, '/')+1:]
			if strings.Contains(out, base) || strings.Contains(out, " files]") {
				continue
			}
		case utf8.RuneCountInString(ln) > 1200:
			head := string([]rune(ln)[:200])
			if strings.Contains(out, head) {
				continue
			}
		}
		bad = append(bad, ln)
	}
	return bad
}
