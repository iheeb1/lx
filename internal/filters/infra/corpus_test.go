package infra

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

type corpusCase struct {
	name    string
	filter  string
	process string
	why     string

	drops func(t *testing.T, clean, got string, missing []string)
}

const (
	small   = "engine: at most SmallOutput (150) tokens — the filter is not called"
	notWith = "engine never-worse gate: under 10% saved"
)

var corpusCases = []corpusCase{
	{name: "docker-build-fail", filter: "docker-build"},
	{name: "docker-build-fail-long", filter: "docker-build"},
	{name: "docker-build-legacy-fail", filter: "docker-build"},
	{name: "docker-build-ok", filter: "docker-build"},
	{name: "docker-compose-logs", filter: "logs"},
	{name: "docker-compose-ps", filter: "docker-table", process: "passthrough", why: notWith + ": only the constant CREATED column goes"},
	{name: "docker-images-many", filter: "docker-table"},
	{name: "docker-images-v29", filter: "docker-table", process: "passthrough", why: "a 10-row table is kept as docker printed it"},
	{name: "docker-logs-node", filter: "logs"},
	{name: "docker-ps", filter: "docker-table", process: "passthrough", why: notWith + ": dropping the constant CREATED column saves little (padding is cheap in tokens)"},
	{name: "docker-ps-a-many", filter: "docker-table"},
	{name: "docker-ps-no-trunc", filter: "docker-table", process: "passthrough", why: notWith + ": --no-trunc cells are never cut; only two constant columns go"},
	{name: "docker-pull-denied", filter: "docker-pull", process: "passthrough", why: small},
	{name: "docker-pull-layers", filter: "docker-pull"},
	{name: "docker-pull-uptodate", filter: "docker-pull", process: "passthrough", why: small},
	{name: "journalctl-unit", filter: "logs"},
	{name: "kubectl-describe-deploy", filter: "kubectl-describe"},
	{name: "kubectl-describe-pod", filter: "kubectl-describe", drops: eventDrops},
	{name: "kubectl-events", filter: "kubectl-events", drops: eventDrops},
	{name: "kubectl-get-all", filter: "kubectl-get", process: "passthrough", why: "small tables are kept as kubectl printed them"},
	{name: "kubectl-get-events-A", filter: "kubectl-events", process: "passthrough", why: notWith + ": only 7 of 34 rows repeat, and each merged row lists the others' ages, so the full table is shown", drops: eventDrops},
	{name: "kubectl-get-nodes", filter: "kubectl-get", process: "passthrough", why: small},
	{name: "kubectl-get-pods", filter: "kubectl-get", process: "passthrough", why: "small tables are kept as kubectl printed them"},
	{name: "kubectl-get-pods-A-many", filter: "kubectl-get"},
	{name: "kubectl-logs-panic", filter: "logs"},

	{name: "docker-build-legacy-long", filter: "docker-build"},
	{name: "docker-images-real", filter: "docker-table", process: "passthrough", why: small},
	{name: "docker-logs-real", filter: "logs"},
	{name: "docker-ps-a-real-many", filter: "docker-table"},
	{name: "docker-pull-notfound", filter: "docker-pull", process: "passthrough", why: small},
}

func loadCase(t testing.TB, name string) fixture.Case {
	t.Helper()
	c, err := fixture.Read("testdata", "infra", name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCorpusCoversEveryCapture(t *testing.T) {
	have := map[string]bool{}
	for _, cc := range corpusCases {
		have[cc.name] = true
	}
	local, err := fixture.ReadAll("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range local {
		if !have[c.Name] {
			t.Errorf("capture %s/%s has no corpus case", c.Category, c.Name)
		}
	}

	for _, c := range fixture.All(t) {
		if f := engine.Find(c.Context()); f != nil && !have[c.Name] {
			for _, n := range []string{"docker-build", "docker-pull", "logs", "docker-table", "kubectl-events", "kubectl-get", "kubectl-describe"} {
				if f.Name() == n {
					t.Errorf("shared capture %s/%s matches %s but has no case", c.Category, c.Name, n)
				}
			}
		}
	}
}

func TestCorpus(t *testing.T) {
	var table strings.Builder
	for _, cc := range corpusCases {
		t.Run(cc.name, func(t *testing.T) {
			fc := loadCase(t, cc.name)
			c := fc.Context()
			clean := fc.Clean()
			f := engine.Find(c)
			if f == nil || f.Name() != cc.filter {
				t.Fatalf("filter = %v, want %s", f, cc.filter)
			}
			got, ok := f.Apply(c, clean)
			if !ok {
				t.Fatal("filter bailed")
			}
			if again, _ := f.Apply(c, clean); again != got {
				t.Fatal("Apply is not deterministic")
			}
			fixture.Golden(t, "infra", cc.name, got)
			if missing := fixture.ErrorLinesMissing(clean, got); len(missing) > 0 {
				if cc.drops == nil {
					t.Errorf("%d error lines missing, e.g. %q", len(missing), missing[0])
				} else {
					cc.drops(t, clean, got, missing)
				}
			}
			if fc.Meta.ExitCode != 0 {
				if lm := fixture.LocationsMissing(clean, got); len(lm) > 0 {
					t.Errorf("failing run lost locations: %v", lm)
				}
			}
			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("guard re-added %d error lines:\n%s", res.GuardAdded, res.Output)
			}
			want := cc.process
			if want == "" {
				want = cc.filter
			}
			if res.Filter != want {
				t.Errorf("pipeline filter = %s, want %s (%s)", res.Filter, want, cc.why)
			}
			raw, out := tokens.Count(fc.Raw), res.OutTokens
			line := fmt.Sprintf("%-26s %7d → %6d tokens %6.1f%%  %s", cc.name, raw, out, 100*(1-float64(out)/float64(max(raw, 1))), res.Filter)
			t.Log(line)
			table.WriteString(line + "\n")
		})
	}
	t.Log("\n" + table.String())
}

var mergedRe = regexp.MustCompile(` \[×(\d+) rows(?:; others: (.*))?\]$`)

func eventDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	type mergedRow struct {
		text string
		ages []string
		abbr bool
	}
	var merged []mergedRow
	for _, ln := range strings.Split(got, "\n") {
		if m := mergedRe.FindStringSubmatch(ln); m != nil {
			merged = append(merged, mergedRow{
				text: strings.Join(strings.Fields(mergedRe.ReplaceAllString(ln, "")), " "),
				ages: strings.Split(m[2], ", "),
				abbr: strings.Contains(m[2], "…"),
			})
		}
	}
	for _, m := range missing {
		f := regexp.MustCompile(`\s{2,}`).Split(strings.TrimSpace(m), -1)
		if len(f) < 4 {
			t.Errorf("non-event line dropped: %q", m)
			continue
		}
		typ, msg := "", strings.Join(strings.Fields(f[len(f)-1]), " ")
		for _, x := range f {
			if x == "Normal" || x == "Warning" {
				typ = x
			}
		}
		found := false
		for _, k := range merged {
			if typ == "" || !strings.Contains(k.text, typ) || !strings.HasSuffix(k.text, msg) {
				continue
			}
			for _, a := range k.ages {
				if strings.Contains(m, "  "+a+"  ") || strings.HasPrefix(m, a+"  ") {
					found = true
				}
			}
			found = found || k.abbr
		}
		if !found {
			t.Errorf("event row dropped without a merged row listing its age: %q", m)
		}
	}
}
