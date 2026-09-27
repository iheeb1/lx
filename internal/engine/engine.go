// Package engine turns a command's raw output into what an agent should read.
//
// Pipeline (Process):
//
//	raw ─► normalize (ANSI, \r frames, overstrike) ─► command filter or generic
//	    ─► error guard (every error line survives) ─► budget ─► never-worse gate
//	    ─► receipt line
//
// Invariants, each covered by tests over real captured output:
//
//	I1  The child's exit code is never changed (enforced by the caller).
//	I2  Error-class lines are never silently removed: a filter keeps them, the
//	    guard re-adds them, or the budget stage keeps them before anything else.
//	I3  Never worse: if filtering does not save at least MinSavings of the
//	    tokens, the normalized output is returned instead.
//	I4  Nothing is lost for good: whenever lines are dropped the full output is
//	    stored and the receipt says how to get it back (lx show <id>).
//	I5  A panicking filter never loses output; Process falls back to generic.
package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

// Context describes the command whose output is being processed.
type Context struct {
	Argv []string // exactly what was executed; Argv[0] may be a path
	Exit int      // child's exit status (128+N when killed by signal N)
	Cwd  string   // working directory, used to relativize paths
	Home string   // $HOME, rendered as ~

	// Budget is the output token budget in effect (set by Process), so a
	// filter that windows large output can size itself to it.
	Budget int
}

// Name is the base name of the executable ("git", "go", "pytest").
func (c *Context) Name() string {
	if len(c.Argv) == 0 {
		return ""
	}
	return filepath.Base(c.Argv[0])
}

// Args returns argv without the executable.
func (c *Context) Args() []string {
	if len(c.Argv) < 2 {
		return nil
	}
	return c.Argv[1:]
}

// Sub returns the first positional (non-flag) argument, skipping flags that
// take a value for the handful of tools where that matters (git -C x, go -C x).
func (c *Context) Sub() string {
	args := c.Args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--prefix" ||
			a == "--namespace" || a == "--config-env" || a == "--exec-path" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// HasFlag reports whether any argument equals one of names, or starts with
// name+"=" for long flags.
func (c *Context) HasFlag(names ...string) bool {
	for _, a := range c.Args() {
		if a == "--" {
			return false
		}
		for _, n := range names {
			if a == n || (strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"=")) {
				return true
			}
		}
	}
	return false
}

// Failed reports a non-zero exit.
func (c *Context) Failed() bool { return c.Exit != 0 }

// Filter is a command-specific reducer. Apply receives normalized output
// (no ANSI, no \r frames, no trailing whitespace) and returns what the agent
// should see. Returning ok=false means "I could not parse this, use the
// generic reducer" — filters must bail rather than guess.
type Filter interface {
	Name() string
	Match(c *Context) bool
	Apply(c *Context, out string) (result string, ok bool)
}

// Faithful is optionally implemented by filters whose view keeps all the
// information of the original in a denser rendering (git status → short
// format). When it reports true and no later stage trimmed anything, lx
// stores no copy and prints no receipt: there is nothing to recover.
type Faithful interface {
	Faithful(c *Context) bool
}

// Streamer is optionally implemented by filters for commands that must not be
// buffered (watchers, servers, followers). Such commands run in passthrough.
type Streamer interface {
	Stream(c *Context) bool
}

var (
	regMu    sync.RWMutex
	registry []Filter
)

// Register adds a filter. Filters register from init(); first match wins,
// in registration order within a package and package init order across them.
func Register(f Filter) {
	regMu.Lock()
	defer regMu.Unlock()
	registry = append(registry, f)
}

// Filters returns all registered filters sorted by name.
func Filters() []Filter {
	regMu.RLock()
	defer regMu.RUnlock()
	out := append([]Filter(nil), registry...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Find returns the first filter matching c, or nil.
func Find(c *Context) Filter {
	regMu.RLock()
	defer regMu.RUnlock()
	for _, f := range registry {
		if f.Match(c) {
			return f
		}
	}
	return nil
}

// Options tune Process. Zero value is the default behavior.
type Options struct {
	Budget     int     // max output tokens before budget trimming (0 = DefaultBudget)
	MinSavings float64 // fraction of tokens that must be saved to keep the filtered version
	NoGuard    bool    // disable the error guard (tests only)

	// MaxChars caps the view in bytes (0 = no cap): the host's output limit
	// minus room for the receipt line. A host such as Claude Code replaces
	// a longer tool result with a short preview, so over the cap reduction
	// is mandatory (the never-worse gate is skipped) and len(Output) <=
	// max(MaxChars, MinMaxChars) holds. MachineReadable output is the one
	// exception: it stays byte-exact and uncapped, and the host may spill it.
	MaxChars int
}

const (
	// DefaultBudget is the output token budget. Tokens alone don't bound a
	// view's length: prose and path lists run 4–5 chars/token, so 8,000
	// tokens can pass Claude Code's 30,000-character Bash limit.
	// Options.MaxChars is the character bound.
	DefaultBudget = 8000
	// DefaultMinSavings: below this, the filtered view is not worth the
	// fidelity risk and the normalized output is shown instead.
	DefaultMinSavings = 0.10
	// SmallOutput: outputs at or under this many tokens are only normalized.
	SmallOutput = 150
)

// Result of processing.
type Result struct {
	Output      string // what to print (without receipt)
	Filter      string // filter name, "generic", or "passthrough"
	RawTokens   int    // tokens of the raw output as the agent would have seen it
	OutTokens   int
	RawLines    int
	OutLines    int
	Lossy       bool // information was dropped/summarized; full output should be stored
	GuardAdded  int  // error lines re-added by the guard
	FilterPanic string
}

// Saved returns the fraction of tokens saved.
func (r Result) Saved() float64 {
	if r.RawTokens == 0 {
		return 0
	}
	return 1 - float64(r.OutTokens)/float64(r.RawTokens)
}

// Process runs the full pipeline over raw output.
func Process(c *Context, raw string, opt Options) (res Result) {
	if opt.Budget <= 0 {
		opt.Budget = DefaultBudget
	}
	if opt.MinSavings == 0 {
		opt.MinSavings = DefaultMinSavings
	}
	c.Budget = opt.Budget
	res.RawTokens = tokens.Count(raw)
	res.RawLines = countLines(raw)
	clean := textutil.Clean(raw)
	cleanTokens := res.RawTokens
	if clean != raw {
		cleanTokens = tokens.Count(clean)
	}

	finish := func(out, name string, lossy bool) Result {
		res.Output, res.Filter, res.Lossy = out, name, lossy
		res.OutTokens = tokens.Count(out)
		res.OutLines = countLines(out)
		if name == "passthrough" {
			// The raw bytes are replayed as-is; nothing was saved.
			res.OutTokens, res.OutLines = res.RawTokens, res.RawLines
		}
		return res
	}
	natural := clean == textutil.TrimTrailingSpace(raw)

	if MachineReadableAny(c) {
		// Output meant for a program reaches it byte-for-byte.
		return finish(strings.TrimRight(raw, "\n"), "passthrough", false)
	}
	// Over the host's character cap the whole output is not an option: the
	// host would swap it for a preview and the receipt would be lost.
	overCap := opt.MaxChars > 0 && len(clean) > opt.MaxChars
	if natural && opt.MaxChars > 0 && len(raw) > opt.MaxChars {
		// "passthrough" replays the raw bytes; trailing blanks alone must
		// not push them over the cap, so print the (fitting) clean text.
		natural = false
	}
	if cleanTokens <= SmallOutput && !overCap {
		if natural {
			return finish(clean, "passthrough", false)
		}
		return finish(clean, "normalize", false)
	}

	out, name := clean, "generic"
	guard, errorsFirst, faithful := !opt.NoGuard, true, false
	if f, fc := Resolve(c); f != nil {
		if r, ok, perr := safeApply(f, fc, clean); perr != "" {
			res.FilterPanic = perr
		} else if ok {
			out, name = r, f.Name()
			if fa, ok := f.(Faithful); ok && fa.Faithful(fc) {
				faithful = true
			}
			if g, ok := f.(Guarded); ok && g.GuardsErrors() {
				guard = false
			}
			if ct, ok := f.(Content); ok && ct.IsContent() {
				guard, errorsFirst = false, false
			}
		}
	}
	if name == "generic" {
		var shape string
		out, shape = GenericShape(c, clean)
		if shape == "json" || shape == "paths" {
			// Data: "error" in an issue title or a file name is content.
			guard, errorsFirst = false, false
		}
	}
	if guard {
		var added int
		out, added = Guard(clean, out)
		res.GuardAdded = added
	}
	before := out
	out = BudgetFit(out, opt.Budget, opt.MaxChars, c.Failed(), errorsFirst)
	lossy := !faithful || out != before || res.GuardAdded > 0

	// Never worse — except over the cap, where the alternative to a
	// reduced view is the host's preview, not the whole output.
	if !overCap && float64(tokens.Count(out)) > float64(cleanTokens)*(1-opt.MinSavings) {
		// Not worth it: show everything, just normalized.
		if !natural {
			return finish(clean, "normalize", false)
		}
		return finish(clean, "passthrough", false)
	}
	return finish(out, name, lossy)
}

func safeApply(f Filter, c *Context, in string) (out string, ok bool, panicMsg string) {
	defer func() {
		if r := recover(); r != nil {
			out, ok, panicMsg = "", false, fmt.Sprint(r)
		}
	}()
	out, ok = f.Apply(c, in)
	return out, ok, ""
}

// Receipt is the one line appended to lossy output so the agent knows the
// view is condensed and exactly how to get everything back.
func Receipt(r Result, id string) string {
	pct := int(r.Saved()*100 + 0.5)
	s := fmt.Sprintf("[lx: %s→%s lines (−%d%%)", humanInt(r.RawLines), humanInt(r.OutLines), pct)
	if id != "" {
		s += " · full output: lx show " + id
	}
	return s + "]"
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func humanInt(n int) string {
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
