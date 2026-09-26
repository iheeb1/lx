package jstools

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

// corpusCase is one real capture this package owns: from the shared corpus
// (testdata/corpus) or, when local, from this package's testdata (captured
// for formats the corpus lacks: pnpm, yarn, webpack, next, more tsc/eslint).
type corpusCase struct {
	category, name string
	local          bool
	filter         string // filter engine.Find must pick
	// process is the Result.Filter engine.Process must report. It differs
	// from filter only where the pipeline legitimately does not use the
	// filtered view; the reason is in the comment next to the case.
	process string
}

var corpus = []corpusCase{
	{"node", "tsc-noemit-errors", false, "tsc", "passthrough"},              // plain tsc is already dense: kept verbatim, nothing to save
	{"node", "tsc-noemit-types-node-incompat", false, "tsc", "passthrough"}, // same
	{"node", "tsc-noemit-errors-pretty-color", false, "tsc", "tsc"},
	{"node", "eslint-many-problems", false, "eslint", "eslint"},
	{"node", "eslint-problems-color", false, "eslint", "eslint"},
	{"node", "npm-install-deprecations", false, "npm-install", "npm-install"},
	{"node", "npm-install-eresolve-error", false, "npm-install", "passthrough"}, // every npm error line is kept; dropping the bare "npm error" separators saves <10%
	{"node", "npm-install-no-lockfile", false, "npm-install", "npm-install"},
	{"node", "npm-uninstall", false, "npm-install", "npm-install"},
	{"node", "npm-create-vite", false, "npm-install", "passthrough"}, // under engine.SmallOutput tokens: never filtered
	{"node", "npm-ls", false, "npm-ls", "npm-ls"},
	{"node", "npm-ls-all", false, "npm-ls", "npm-ls"},
	{"node", "npm-audit", false, "npm-audit", "npm-audit"},
	{"node", "npm-outdated", false, "npm-outdated", "npm-outdated"},
	{"node", "npm-run-build-vite", false, "npm-run", "npm-run"},
	{"node", "npm-run-build-vite-tsc-fail", false, "npm-run", "passthrough"}, // under engine.SmallOutput tokens

	{"node", "tsc-build-pretty", true, "tsc", "tsc"},
	{"node", "tsc-build-plain", true, "tsc", "passthrough"},    // plain tsc: kept verbatim
	{"node", "tsc-unknown-option", true, "tsc", "passthrough"}, // one line
	{"node", "eslint-max-warnings", true, "eslint", "eslint"},
	{"node", "eslint-distinct-warnings", true, "eslint", "eslint"},
	{"node", "eslint-parse-error", true, "eslint", "passthrough"}, // four lines
	{"node", "vite-build-unresolved", true, "npm-run", "npm-run"},
	{"node", "npm-run-build-vite-chunk-warning", true, "npm-run", "npm-run"},
	{"node", "npm-run-build-webpack-warnings", true, "npm-run", "npm-run"},
	{"node", "npm-run-build-webpack-error", true, "npm-run", "npm-run"},
	{"node", "npm-run-build-next", true, "npm-run", "npm-run"},
	{"node", "npm-run-build-next-error", true, "npm-run", "normalize"}, // stripping the ANSI code frame colors saves more than the filter
	{"node", "npm-run-lint-eslint-fail", true, "npm-run", "npm-run"},
	{"node", "npm-ci", true, "npm-install", "passthrough"}, // under engine.SmallOutput tokens
	{"node", "pnpm-install", true, "npm-install", "npm-install"},
	{"node", "pnpm-add", true, "npm-install", "npm-install"},
	{"node", "pnpm-add-404", true, "npm-install", "passthrough"}, // only progress lines go: saves <10%
	{"node", "yarn-install", true, "npm-install", "npm-install"},
	{"node", "yarn-add", true, "npm-install", "passthrough"}, // under engine.SmallOutput tokens
	{"node", "npm-ls-problems", true, "npm-ls", "npm-ls"},
	{"node", "npm-ls-all-problems", true, "npm-ls", "npm-ls"},
	{"node", "pnpm-ls", true, "npm-ls", "passthrough"}, // under engine.SmallOutput tokens
	{"node", "pnpm-ls-depth", true, "npm-ls", "npm-ls"},
	{"node", "yarn-list", true, "npm-ls", "npm-ls"},
	{"node", "pnpm-outdated", true, "npm-outdated", "npm-outdated"},
	{"node", "yarn-outdated", true, "npm-outdated", "npm-outdated"},
	{"node", "pnpm-audit", true, "npm-audit", "npm-audit"},
	{"node", "yarn-audit", true, "npm-audit", "npm-audit"},

	// Captured for the adversarial review: newer/other versions, flags and
	// failure shapes.
	{"node", "eslint9-stylish", true, "eslint", "eslint"},
	{"node", "eslint9-quiet", true, "eslint", "eslint"},
	{"node", "eslint9-max-warnings-only", true, "eslint", "passthrough"}, // under engine.SmallOutput tokens
	{"node", "npm-ls-workspaces", true, "npm-ls", "passthrough"},         // under engine.SmallOutput tokens
	{"node", "npm-ls-all-workspaces", true, "npm-ls", "npm-ls"},
	{"node", "npm-outdated-workspaces", true, "npm-outdated", "passthrough"}, // under engine.SmallOutput tokens
	{"node", "npm-install-e404", true, "npm-install", "passthrough"},         // under engine.SmallOutput tokens
	{"node", "npm-install-etarget", true, "npm-install", "passthrough"},      // under engine.SmallOutput tokens
	{"node", "npm-ci-out-of-sync", true, "npm-install", "npm-install"},
	{"node", "npm-install-verbose", true, "npm-install", "npm-install"},
	{"node", "npm-install-verbose-large", true, "npm-install", "npm-install"},
	{"node", "npm-run-typecheck-plain", true, "npm-run", "passthrough"}, // plain tsc: kept verbatim
	{"node", "tsc-pretty-related", true, "tsc", "tsc"},
	{"node", "pnpm-r-build-tsc-fail", true, "npm-run", "passthrough"},       // under engine.SmallOutput tokens
	{"node", "pnpm-build-script-recursive", true, "npm-run", "passthrough"}, // pnpm's prefixed lines are kept verbatim: nothing to save
	{"node", "yarn-typecheck-fail", true, "npm-run", "passthrough"},         // plain tsc: kept verbatim
	{"node", "yarn-lint-fail", true, "npm-run", "npm-run"},
	{"node", "vite-build-syntax-error", true, "npm-run", "npm-run"},
	{"node", "npm-run-lint-eslint9-maxwarn", true, "npm-run", "npm-run"},
}

// corpusBails are real captures a filter matches but must not claim: the
// output is not in a shape it recognizes, so the generic reducer (or plain
// passthrough) takes over.
var corpusBails = []corpusCase{
	{"node", "eslint9-no-config", true, "eslint", "passthrough"},    // ESLint crash report ("Oops! Something went wrong!")
	{"node", "npm-run-lint-oxlint", true, "npm-run", "passthrough"}, // oxlint, a linter lx does not parse
}

func TestCorpusBails(t *testing.T) {
	for _, tc := range corpusBails {
		t.Run(tc.name, func(t *testing.T) {
			fc := tc.load(t)
			c := fc.Context()
			f := engine.Find(c)
			if f == nil || f.Name() != tc.filter {
				t.Fatalf("engine.Find = %v, want %s", f, tc.filter)
			}
			if got, ok := f.Apply(c, fc.Clean()); ok {
				t.Fatalf("filter claimed output it does not recognize:\n%s", got)
			}
			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.Filter != tc.process || res.FilterPanic != "" {
				t.Errorf("Process used %q (panic %q), want %q", res.Filter, res.FilterPanic, tc.process)
			}
		})
	}
}

func (tc corpusCase) load(t *testing.T) fixture.Case {
	t.Helper()
	if !tc.local {
		return fixture.Load(t, tc.category, tc.name)
	}
	fc, err := fixture.Read("testdata", tc.category, tc.name)
	if err != nil {
		t.Fatal(err)
	}
	return fc
}

func TestCorpus(t *testing.T) {
	var total [2]int
	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			fc := tc.load(t)
			c := fc.Context()
			f := engine.Find(c)
			if f == nil || f.Name() != tc.filter {
				t.Fatalf("engine.Find = %v, want %s", f, tc.filter)
			}
			clean := fc.Clean()
			got, ok := f.Apply(c, clean)
			if !ok {
				t.Fatal("filter bailed on real output")
			}
			fixture.Golden(t, "jstools", tc.name, got)
			checkFidelity(t, tc.filter, c, clean, got)

			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("guard re-added %d error lines:\n%s", res.GuardAdded, res.Output)
			}
			if res.Filter != tc.process {
				t.Errorf("Process used %q, want %q", res.Filter, tc.process)
			}
			if res.FilterPanic != "" {
				t.Errorf("filter panicked: %s", res.FilterPanic)
			}
			raw, filtered := tokens.Count(fc.Raw), tokens.Count(got)
			total[0] += raw
			total[1] += res.OutTokens
			t.Logf("savings %-34s raw %6d → filter %6d (%5.1f%%) → lx %6d (%5.1f%%) [%s]", tc.name, raw, filtered,
				pct(raw, filtered), res.OutTokens, pct(raw, res.OutTokens), res.Filter)
		})
	}
	t.Logf("savings total: %d → %d (%.1f%%)", total[0], total[1], pct(total[0], total[1]))
}

func pct(in, out int) float64 { return 100 * (1 - float64(out)/float64(max(in, 1))) }

// checkFidelity asserts that nothing error-class was lost. The engine's
// check (every error-class line survives, whitespace aside) is applied
// as is, except for the documented reformattings, which are verified
// structurally instead:
//   - eslint: repeated messages factored into "×N: l:c l:c" lines;
//   - npm-ls: tree lines re-drawn with indentation (package names such as
//     http-errors are error-class words, not errors);
//   - npm-audit: advisory URLs shortened to the GHSA id.
func checkFidelity(t *testing.T, filter string, c *engine.Context, clean, got string) {
	t.Helper()
	missing := fixture.ErrorLinesMissing(clean, got)
	if filter == "tsc" || filter == "npm-run" {
		// A tsc --pretty code frame quotes source ("throw new
		// Error('failed')"); frames are dropped on purpose, the diagnostic
		// they illustrate is kept.
		frames := tscFrameLines(clean, got)
		missing = without(missing, func(ln string) bool { return frames[ln] })
	}
	switch filter {
	case "eslint", "npm-run":
		missing = checkESLintFactored(t, c, clean, got, missing)
		// File headers are shown relative to the working directory; a
		// path under examples/error/ is not an error line.
		missing = without(missing, func(ln string) bool {
			rel := engine.Relativize(c, ln)
			return !strings.HasPrefix(ln, " ") && rel != ln && strings.Contains(got, rel)
		})
	case "tsc":
		// tsc's "Errors  Files" table header is dropped with the table when
		// every file it lists is already named by a diagnostic; the header
		// itself says nothing ("Errors" is what makes it error-class).
		if !strings.Contains(got, "Errors  Files") {
			table := tscTableLines(clean)
			missing = without(missing, func(ln string) bool { return table[ln] })
		}
	case "npm-ls":
		missing = without(missing, func(ln string) bool {
			m := lsTreeRe.FindStringSubmatch(ln)
			if m == nil {
				m = lsYarnTreeRe.FindStringSubmatch(ln)
			}
			return m != nil && (strings.Contains(got, m[2]) || lsDedupRe.MatchString(m[2]))
		})
	case "npm-audit":
		missing = without(missing, func(ln string) bool {
			short := auditGHSARe.ReplaceAllString(ln, " - $1")
			return short != ln && strings.Contains(got, short)
		})
	case "npm-install":
		missing = without(missing, func(ln string) bool { return installFoldedOnPurpose(clean, got, ln) })
	}
	for _, m := range missing {
		t.Errorf("error line missing: %q", m)
	}
	if c.Exit != 0 {
		for _, m := range fixture.LocationsMissing(clean, got) {
			// engine.FoldStacks folds runs of library frames (node_modules)
			// into "… N library frames (…)"; those locations are the
			// bundler's internals, not the user's code.
			if strings.Contains(m, "node_modules/") && strings.Contains(got, " library frames (") {
				continue
			}
			t.Errorf("location missing: %q", m)
		}
	}
}

var (
	frameSrcRe   = regexp.MustCompile(`^\s*\d+ `)
	frameUnderRe = regexp.MustCompile(`^\s+~+$`)
)

// tscFrameLines returns the source lines of tsc --pretty code frames in
// clean (a "12 source" gutter line directly above a "   ~~~" underline)
// whose diagnostic header ("file:l:c - error TS…", the nearest header
// above) is in got; keys are trimmed like ErrorLinesMissing's results.
func tscFrameLines(clean, got string) map[string]bool {
	frames := map[string]bool{}
	lines := strings.Split(clean, "\n")
	header := ""
	for i, ln := range lines {
		if strings.Contains(ln, " - error TS") || strings.Contains(ln, " - warning TS") {
			header = ln
		}
		if i+1 < len(lines) && header != "" && strings.Contains(got, header) &&
			frameSrcRe.MatchString(ln) && frameUnderRe.MatchString(lines[i+1]) {
			frames[strings.TrimSpace(ln)] = true
		}
	}
	return frames
}

// installFoldedOnPurpose reports whether ln, an error-class line of clean
// missing from got, is one the npm-install filter folds on purpose, checked
// on the input's structure rather than with the filter's own code:
//   - a line of npm's usage text: between "npm error Options:" and the
//     next `npm error Run "npm help <cmd>" for more info` line of an EUSAGE
//     failure, every line in between an npm error line, with the counted
//     marker in got;
//   - a successful "npm http fetch" (2xx/304) or "npm http cache" line
//     (package names such as http-errors), with the counted marker in got.
func installFoldedOnPurpose(clean, got, ln string) bool {
	if strings.HasPrefix(ln, "npm http ") {
		f := strings.Fields(ln)
		ok := len(f) > 3 && (f[2] == "cache" || f[2] == "fetch" && len(f) > 4 && (f[4] == "304" || len(f[4]) == 3 && f[4][0] == '2'))
		return ok && strings.Contains(got, "[npm http: ")
	}
	if !strings.Contains(clean, "npm error code EUSAGE\n") || !strings.Contains(got, "[npm usage text: ") {
		return false
	}
	lines := strings.Split(clean, "\n")
	for i, l := range lines {
		if l != "npm error Options:" {
			continue
		}
		for j := i; j < len(lines) && strings.HasPrefix(lines[j], "npm error"); j++ {
			if strings.HasPrefix(lines[j], `npm error Run "npm help `) {
				break
			}
			if strings.TrimSpace(lines[j]) == ln {
				return true
			}
		}
	}
	return false
}

func without(lines []string, drop func(string) bool) []string {
	var r []string
	for _, ln := range lines {
		if !drop(ln) {
			r = append(r, ln)
		}
	}
	return r
}

var (
	factoredRe   = regexp.MustCompile(`^  (error|warning)  (.*)  ×\d+: (.*)$`)
	groupHeadRe  = regexp.MustCompile(`^(error|warning)  (.*)  ×\d+ in \d+ files?:$`)
	groupFileRe  = regexp.MustCompile(`^  (\S.*?)(?: ×(\d+))?: (\S.*)$`)
	groupOneRe   = regexp.MustCompile(`^(error|warning)  (.*)  (\S+):(\d+:\d+)$`)
	moreSuffixRe = regexp.MustCompile(` … \+\d+ more$`)
)

// checkESLintFactored accounts for the stylish message lines listed in
// missing. Each must be represented in the output by
//   - a factored line under its file header ("  error  text  rule  ×N: l:c
//     l:c …") listing its line:col, or
//   - a grouped entry ("error  text  rule  ×N in M files:" then
//     "  file ×n: l:c …", or "error  text  rule  file:l:c" for a message
//     seen once) listing its line:col, or, when the positions were capped
//     ("… +K more"), declaring exactly as many occurrences as the input has
//     for that file and message.
//
// It returns the lines it could not account for.
func checkESLintFactored(t *testing.T, c *engine.Context, clean, got string, missing []string) []string {
	t.Helper()
	have := map[string]bool{}  // file, severity, text+rule, position
	capped := map[string]int{} // file, severity, text+rule → declared count
	file, group := "", []string(nil)
	outLines := strings.Split(got, "\n")
	for i, ln := range outLines {
		if m := groupHeadRe.FindStringSubmatch(ln); m != nil {
			group = []string{m[1], squash(m[2])}
			continue
		}
		if m := groupOneRe.FindStringSubmatch(ln); m != nil {
			have[m[3]+"\x00"+m[1]+"\x00"+squash(m[2])+"\x00"+m[4]] = true
			group = nil
			continue
		}
		if group != nil && strings.HasPrefix(ln, "  ") && !strings.Contains(ln, ": ") {
			// "  a.js ×12, b.js ×3": counts only.
			for _, e := range strings.Split(strings.TrimSpace(ln), ", ") {
				if i := strings.LastIndex(e, " ×"); i > 0 {
					n, _ := strconv.Atoi(e[i+len(" ×"):])
					capped[e[:i]+"\x00"+group[0]+"\x00"+group[1]] = n
				}
			}
			continue
		}
		if group != nil {
			if m := groupFileRe.FindStringSubmatch(ln); m != nil {
				k := m[1] + "\x00" + group[0] + "\x00" + group[1]
				pos := moreSuffixRe.ReplaceAllString(m[3], "")
				if pos != m[3] {
					n := 1
					if m[2] != "" {
						n, _ = strconv.Atoi(m[2])
					}
					capped[k] = n
				}
				for _, p := range strings.Fields(pos) {
					have[k+"\x00"+p] = true
				}
				continue
			}
			group = nil
		}
		if ln != "" && ln[0] != ' ' && i+1 < len(outLines) && strings.HasPrefix(outLines[i+1], " ") {
			file = ln
			continue
		}
		m := factoredRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		pos := strings.Fields(m[3])
		for k := i + 1; k < len(outLines) && strings.HasPrefix(outLines[k], "      "); k++ {
			pos = append(pos, strings.Fields(outLines[k])...)
		}
		for _, p := range pos {
			have[file+"\x00"+m[1]+"\x00"+squash(m[2])+"\x00"+p] = true
		}
	}
	// Occurrences per file and message in the input.
	type occ struct{ key, pos, line string }
	var occs []occ
	counts := map[string]int{}
	file = ""
	for _, ln := range strings.Split(clean, "\n") {
		if ln != "" && ln[0] != ' ' {
			file = engine.Relativize(c, ln)
			continue
		}
		m, ok := parseESMsg(ln)
		if !ok {
			continue
		}
		k := file + "\x00" + m.sev + "\x00" + squash(joinNonEmpty("  ", m.text, m.rule))
		counts[k]++
		occs = append(occs, occ{k, m.pos, strings.TrimSpace(ln)})
	}
	missingSet := map[string]bool{}
	for _, m := range missing {
		missingSet[m] = true
	}
	unaccounted := map[string]bool{}
	for _, o := range occs {
		if !missingSet[o.line] {
			continue
		}
		if have[o.key+"\x00"+o.pos] {
			continue
		}
		if n, ok := capped[o.key]; ok && n == counts[o.key] {
			continue
		}
		unaccounted[o.line] = true
		t.Logf("unaccounted: %q in %q", o.line, strings.SplitN(o.key, "\x00", 2)[0])
	}
	var rest []string
	for _, m := range missing {
		if _, isMsg := parseESMsg(" " + m); !isMsg || unaccounted[m] {
			rest = append(rest, m)
		}
	}
	return rest
}

// TestCorpusCleanIsNormalized guards the assumption that fixtures reach
// Apply the way the engine hands them over.
func TestCorpusCleanIsNormalized(t *testing.T) {
	for _, tc := range corpus {
		fc := tc.load(t)
		if fc.Clean() != textutil.Clean(fc.Raw) {
			t.Fatalf("%s: Clean differs from textutil.Clean", tc.name)
		}
	}
}

// TestNoStrayMatches: across the whole shared corpus, these filters claim
// only the captures listed in corpus (npm test, jest, vitest, mocha, … go
// to other filters).
func TestNoStrayMatches(t *testing.T) {
	mine := map[string]bool{}
	for _, tc := range corpus {
		if !tc.local {
			mine[tc.category+"/"+tc.name] = true
		}
	}
	for _, fc := range fixture.All(t) {
		f := engine.Find(fc.Context())
		if f != nil && !mine[fc.Category+"/"+fc.Name] {
			t.Errorf("%s/%s (%v) matched by %s", fc.Category, fc.Name, fc.Meta.Argv, f.Name())
		}
	}
}
