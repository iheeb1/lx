package jstools

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type npmRun struct{}

func (npmRun) Name() string { return "npm-run" }

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

	if !ok || (sub != "run" && sub != "script") || pm == "bun" && sub == "script" {
		return false
	}
	i := skipFlags(rest)
	if i < 0 {
		return false
	}
	for _, a := range rest[i+1:] {
		if a == "--watch" || a == "-w" || strings.HasPrefix(a, "--watch=") {
			return false
		}
	}
	return isBuildScript(rest[i])
}

func (npmRun) GuardsErrors() bool { return true }

var (
	viteAssetRe = lazyre.New(`^(\S+)\s+([\d.,]+) (B|kB|KB|MB|GB)(?:\s+│\s+gzip:\s+[\d.,]+ (?:B|kB|KB|MB))?(?:\s+│\s+map:\s+[\d.,]+ (?:B|kB|KB|MB))?$`)
	viteNoiseRe = lazyre.New(`^(?:transforming|rendering chunks|computing gzip size)\.\.\.$`)
	viteLimitRe = lazyre.New(`^\(!\) Some chunks are larger than ([\d.]+) (kB|KB|MB)`)
	wpAssetRe   = lazyre.New(`^asset (\S+) ([\d.]+) (bytes|KiB|MiB|GiB)\b(.*)$`)
	wpModuleRe  = lazyre.New(`^\s*(?:(?:orphan|runtime|cacheable|javascript|asset|css|json) modules|modules by (?:path|layer|type)|\./\S+|\S+ \+ \d+ modules?) .*\b(?:\d+ modules?|\[built\]|\[code generated\]|bytes|KiB|MiB)(?: .*)?$`)
	buildMarkRe = lazyre.New(`^(?:vite v\d|✓ built in|✓ \d+ modules transformed|webpack(?: \d[\d.]*)? compiled|\s*▲ Next\.js|\s*✓ Compiled successfully|Route \((?:app|pages)\)|error during build:|[✗x] Build failed)`)

	nextRouteRe    = lazyre.New(`^[┌├└]\s+(?:([○●ƒλ◐])\s+)?/\S*`)
	nextSubRouteRe = lazyre.New(`^[│ ]\s*[├└]\s+(?:([○●ƒλ◐])\s+)?(?:/\S*|\[\+(\d+) more paths?\])`)
	nextSharedRe   = lazyre.New(`^\+ First Load JS shared by all`)
	nextChunkRe    = lazyre.New(`^\s+[├└] (?:chunks/\S+|other shared chunks \(total\)|css/\S+)\s+[\d.]+ (?:B|kB|MB)$`)
	nextLegendRe   = lazyre.New(`^([○●ƒλ◐])\s+\(([^)]+)\)`)
	nextProgressRe = lazyre.New(`^\s+Generating static pages .*\(\d+/\d+\)(?: \.\.\.)?$`)

	wpNotFoundRe = lazyre.New(`^Module not found: Error: Can't resolve '.+' in '.+'$`)
	wpResolveRe  = lazyre.New(`^resolve '.+' in '.+'$`)
)

func (npmRun) Apply(c *engine.Context, s string) (string, bool) {
	lines := strings.Split(s, "\n")
	limit := -1.0
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
		if end, r := tscRegion(c, lines, i); end > i {
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

func keepIfError(ln string, o *out) {
	if engine.IsError(ln) {
		o.add(ln)
	}
}
