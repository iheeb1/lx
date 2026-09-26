package jstools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// npmRun condenses the output of build/lint/type-check scripts run through
// a package manager (`npm run build`, `pnpm lint`, `yarn typecheck`, …) and
// of `vite build`, `next build` and `webpack` run directly.
//
// The output is a mix of the manager's own lines ("> app@1.0.0 build",
// "npm error Lifecycle script …", all kept) and whatever the script ran:
//   - tsc diagnostics and eslint reports are rendered like the tsc and eslint
//     filters do;
//   - vite: the per-asset "dist/… 12.3 kB │ gzip: …" lines become one counted
//     line with the total and the largest asset (assets over the limit of a
//     "(!) Some chunks are larger than …" warning stay), the transforming…/
//     rendering chunks… progress lines are dropped;
//   - webpack: "asset …" and module-tree lines are counted ("[big]" assets
//     stay); WARNING/ERROR blocks and the "compiled …" line are kept;
//   - next: the route table becomes a count per route kind;
//   - anything else is kept, with long library stack traces folded.
//
// The filter bails when it recognizes none of these tools.
type npmRun struct{}

func (npmRun) Name() string { return "npm-run" }

// buildScripts are script names (or "name:variant" prefixes) whose output
// this filter understands.
var buildScripts = map[string]bool{
	"build": true, "lint": true, "typecheck": true, "type-check": true, "types": true, "tsc": true,
	"check": true, "check-types": true, "compile": true, "eslint": true,
}

func isBuildScript(s string) bool {
	low := strings.ToLower(s)
	for _, w := range []string{"watch", "dev", "serve", "start", "test"} {
		if strings.Contains(low, w) {
			return false
		}
	}
	base, _, _ := strings.Cut(low, ":")
	return buildScripts[base]
}

// webpackNot are webpack-cli commands (and flags) that do not build once.
var webpackNot = map[string]bool{
	"serve": true, "server": true, "s": true, "watch": true, "w": true, "help": true, "version": true,
	"info": true, "init": true, "configtest": true, "plugin": true, "loader": true, "migrate": true,
	"--watch": true, "-w": true, "--help": true, "-h": true, "--version": true, "-v": true,
}

func (npmRun) Match(c *engine.Context) bool {
	t, args := tool(c)
	switch t {
	case "vite", "next":
		i := skipFlags(args)
		return i >= 0 && args[i] == "build" && !hasArg(args, "--watch", "-w")
	case "webpack", "webpack-cli":
		for _, a := range args {
			if webpackNot[a] {
				return false
			}
		}
		return true
	}
	pm, sub, rest, ok := manager(c)
	// "bun build" is bun's bundler, not a script.
	if !ok || (sub != "run" && sub != "script") || pm == "bun" && sub == "script" {
		return false
	}
	i := skipFlags(rest)
	if i < 0 {
		return false
	}
	for _, a := range rest[i+1:] {
		if a == "--watch" || a == "-w" || strings.HasPrefix(a, "--watch=") {
			return false // "npm run build -- --watch" never ends
		}
	}
	return isBuildScript(rest[i])
}

func (npmRun) GuardsErrors() bool { return true }

var (
	viteAssetRe = regexp.MustCompile(`^(\S+)\s+([\d.,]+) (B|kB|KB|MB|GB)(?:\s+│\s+gzip:\s+[\d.,]+ (?:B|kB|KB|MB))?(?:\s+│\s+map:\s+[\d.,]+ (?:B|kB|KB|MB))?$`)
	viteNoiseRe = regexp.MustCompile(`^(?:transforming|rendering chunks|computing gzip size)\.\.\.$`)
	viteLimitRe = regexp.MustCompile(`^\(!\) Some chunks are larger than ([\d.]+) (kB|KB|MB)`)
	wpAssetRe   = regexp.MustCompile(`^asset (\S+) ([\d.]+) (bytes|KiB|MiB|GiB)\b(.*)$`)
	wpModuleRe  = regexp.MustCompile(`^\s*(?:(?:orphan|runtime|cacheable|javascript|asset|css|json) modules|modules by (?:path|layer|type)|\./\S+|\S+ \+ \d+ modules?) .*\b(?:\d+ modules?|\[built\]|\[code generated\]|bytes|KiB|MiB)(?: .*)?$`)
	buildMarkRe = regexp.MustCompile(`^(?:vite v\d|✓ built in|✓ \d+ modules transformed|webpack(?: \d[\d.]*)? compiled|\s*▲ Next\.js|\s*✓ Compiled successfully|Route \((?:app|pages)\)|error during build:|[✗x] Build failed)`)
	// next build's route table: "┌ ○ /about  1.2 kB  89 kB" rows (the
	// symbol is missing on a dynamic segment whose paths are listed under
	// it), "  ├ ● /blog/a" / "  └ ● [+2 more paths]" prerendered paths,
	// and the legend after it.
	nextRouteRe    = regexp.MustCompile(`^[┌├└]\s+(?:([○●ƒλ◐])\s+)?/\S*`)
	nextSubRouteRe = regexp.MustCompile(`^[│ ]\s*[├└]\s+(?:([○●ƒλ◐])\s+)?(?:/\S*|\[\+(\d+) more paths?\])`)
	nextSharedRe   = regexp.MustCompile(`^\+ First Load JS shared by all`)
	nextChunkRe    = regexp.MustCompile(`^\s+[├└] (?:chunks/\S+|other shared chunks \(total\)|css/\S+)\s+[\d.]+ (?:B|kB|MB)$`)
	nextLegendRe   = regexp.MustCompile(`^([○●ƒλ◐])\s+\(([^)]+)\)`)
	nextProgressRe = regexp.MustCompile(`^\s+Generating static pages .*\(\d+/\d+\)(?: \.\.\.)?$`)
	// webpack: "Module not found: Error: Can't resolve 'x' in 'dir'" is
	// followed by the resolver's trace of every path it tried.
	wpNotFoundRe = regexp.MustCompile(`^Module not found: Error: Can't resolve '.+' in '.+'$`)
	wpResolveRe  = regexp.MustCompile(`^resolve '.+' in '.+'$`)
)

func (npmRun) Apply(c *engine.Context, s string) (string, bool) {
	lines := strings.Split(s, "\n")
	limit := -1.0 // vite chunk size warning limit, in kB
	for _, ln := range lines {
		if m := viteLimitRe.FindStringSubmatch(ln); m != nil {
			limit, _ = strconv.ParseFloat(m[1], 64)
			if m[2] == "MB" {
				limit *= 1000
			}
		}
	}
	var o out
	recognized := false
	for i := 0; i < len(lines); {
		ln := lines[i]
		if end, r := tscRegion(lines, i); end > i {
			o.add(r...)
			recognized, i = true, end
			continue
		}
		if end, r := eslintRegion(c, lines, i); end > i {
			o.add(r...)
			recognized, i = true, end
			continue
		}
		if viteAssetRe.MatchString(ln) {
			i = viteAssets(lines, i, limit, &o)
			recognized = true
			continue
		}
		if wpAssetRe.MatchString(ln) || wpModuleRe.MatchString(ln) {
			i = webpackStats(lines, i, &o)
			continue
		}
		if strings.HasPrefix(ln, "Route (") {
			if end, ok := nextRoutes(lines, i, &o); ok {
				recognized, i = true, end
				continue
			}
		}
		if buildMarkRe.MatchString(ln) {
			recognized = true
		}
		if wpNotFoundRe.MatchString(ln) && i+1 < len(lines) && wpResolveRe.MatchString(lines[i+1]) {
			o.add(ln)
			j := i + 2
			for j < len(lines) && strings.HasPrefix(lines[j], "  ") {
				j++
			}
			// The count excludes error-class trace lines, which drop keeps.
			at, hidden := len(o.lines), 0
			o.add("")
			for _, t := range lines[i+1 : j] {
				if !o.drop(t) {
					hidden++
				}
			}
			o.lines[at] = fmt.Sprintf("[resolve trace: %s hidden]", engine.Plural(hidden, "line", "lines"))
			i = j
			continue
		}
		if viteNoiseRe.MatchString(ln) || yarnNoiseRe.MatchString(ln) || nextProgressRe.MatchString(ln) {
			o.drop(ln)
		} else {
			o.add(ln)
		}
		i++
	}
	if !recognized {
		return "", false
	}
	o.lines = engine.FoldStacks(c, o.lines)
	if note := failNote(c, o.lines, eslintMaxWarnRe.MatchString); note != "" {
		o.add(note)
	}
	return o.String(), true
}

// viteAssets folds the run of vite asset lines starting at lines[i].
func viteAssets(lines []string, i int, limit float64, o *out) int {
	var (
		n              int
		total, largest float64
		largestName    string
	)
	j := i
	for ; j < len(lines); j++ {
		m := viteAssetRe.FindStringSubmatch(lines[j])
		if m == nil {
			break
		}
		kb, _ := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", ""), 64)
		switch m[3] {
		case "B":
			kb /= 1000
		case "MB":
			kb *= 1000
		case "GB":
			kb *= 1000 * 1000
		}
		if limit >= 0 && kb > limit || engine.IsError(lines[j]) {
			o.add(lines[j])
			continue
		}
		n++
		total += kb
		if kb > largest {
			largest, largestName = kb, m[1]
		}
	}
	if n == 1 {
		o.add(lines[i : i+1]...)
	} else if n > 1 {
		o.add(fmt.Sprintf("[%d asset lines hidden (%s, largest %s %s)]", n, fmtKB(total), largestName, fmtKB(largest)))
	}
	return j
}

func fmtKB(kb float64) string {
	if kb >= 1000 {
		return fmt.Sprintf("%.2f MB", kb/1000)
	}
	return fmt.Sprintf("%.2f kB", kb)
}

// webpackStats folds webpack's asset and module lines starting at lines[i].
func webpackStats(lines []string, i int, o *out) int {
	assets, modules := 0, 0
	j := i
	for ; j < len(lines); j++ {
		ln := lines[j]
		if m := wpAssetRe.FindStringSubmatch(ln); m != nil {
			if strings.Contains(m[4], "[big]") || engine.IsError(ln) {
				o.add(ln)
			} else {
				assets++
			}
			continue
		}
		if wpModuleRe.MatchString(ln) {
			if engine.IsError(ln) {
				o.add(ln)
			} else {
				modules++
			}
			continue
		}
		break
	}
	var parts []string
	if assets > 0 {
		parts = append(parts, engine.Plural(assets, "asset line", "asset lines"))
	}
	if modules > 0 {
		parts = append(parts, engine.Plural(modules, "module line", "module lines"))
	}
	if len(parts) > 0 {
		o.add("[" + strings.Join(parts, ", ") + " hidden]")
	}
	return j
}

// nextRoutes summarizes a next build route table starting at lines[i]
// ("Route (app)", with or without size columns) as one counted line, drops
// the legend it used for the counts and keeps the shared First Load JS
// line. ok is false when no route row follows.
func nextRoutes(lines []string, i int, o *out) (int, bool) {
	title := strings.Fields(lines[i])
	if len(title) < 2 {
		return i, false
	}
	kinds := map[string]int{}
	var order []string
	count := func(sym string, n int) {
		if sym == "" {
			return
		}
		if kinds[sym] == 0 {
			order = append(order, sym)
		}
		kinds[sym] += n
	}
	routes, paths := 0, 0
	var shared []string
	j := i + 1
	for ; j < len(lines); j++ {
		ln := lines[j]
		if m := nextRouteRe.FindStringSubmatch(ln); m != nil {
			count(m[1], 1)
			routes++
			keepIfError(ln, o)
			continue
		}
		if m := nextSubRouteRe.FindStringSubmatch(ln); m != nil {
			n := 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			count(m[1], n)
			paths += n
			keepIfError(ln, o)
			continue
		}
		if nextSharedRe.MatchString(ln) {
			shared = append(shared, ln)
			continue
		}
		if nextChunkRe.MatchString(ln) {
			continue
		}
		break
	}
	if routes == 0 {
		return i, false
	}
	// Legend: "○  (Static)  prerendered as static content".
	names := map[string]string{}
	end := j
	for k := j; k < len(lines) && k < j+12; k++ {
		if m := nextLegendRe.FindStringSubmatch(lines[k]); m != nil {
			names[m[1]] = m[2]
			end = k + 1
		} else if strings.TrimSpace(lines[k]) != "" {
			break
		}
	}
	if len(names) > 0 {
		j = end
	}
	var parts []string
	for _, k := range order {
		p := k
		if nm := names[k]; nm != "" {
			p += " " + nm
		}
		parts = append(parts, fmt.Sprintf("%s ×%d", p, kinds[k]))
	}
	s := fmt.Sprintf("[%s %s table hidden: %s", title[0], title[1], engine.Plural(routes, "route", "routes"))
	if paths > 0 {
		s += fmt.Sprintf(", %s", engine.Plural(paths, "prerendered path", "prerendered paths"))
	}
	if len(parts) > 0 {
		s += "; " + strings.Join(parts, ", ")
	}
	o.add(s + "]")
	o.add(shared...)
	return j, true
}

// keepIfError keeps a line being folded into a count when it looks
// error-class (a route named /error): the filter is Guarded, so nothing
// else would show it.
func keepIfError(ln string, o *out) {
	if engine.IsError(ln) {
		o.add(ln)
	}
}
