package python

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// pipGlobalValue are pip's general options that take a separate value and
// may come before the subcommand.
var pipGlobalValue = map[string]bool{
	"--proxy": true, "--python": true, "--log": true, "--log-file": true, "--cache-dir": true,
	"--timeout": true, "--retries": true, "--exists-action": true, "--trusted-host": true,
	"--cert": true, "--client-cert": true, "--use-feature": true, "--use-deprecated": true,
	"--root-user-action": true, "--progress-bar": true, "--keyring-provider": true, "--resume-retries": true,
}

// pipSub returns pip's subcommand and the arguments after it.
func pipSub(c *engine.Context) (string, []string) {
	inv := parseInvocation(c)
	if inv.tool != "pip" {
		return "", nil
	}
	args := inv.args
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case pipGlobalValue[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// ---- pip install ----

// pipInstallFilter condenses `pip install`: Collecting / Downloading /
// Using cached / already-satisfied / build-step chatter becomes counts (the
// top-level already-satisfied requirements are listed with their
// versions); "Successfully installed …", "Successfully uninstalled …",
// warnings and notices (once each) are kept, and from the first ERROR line
// or error block on everything is kept verbatim (resolver conflicts,
// subprocess-exited-with-error output, hints).
type pipInstallFilter struct{}

func (pipInstallFilter) Name() string { return "pip-install" }

func (pipInstallFilter) Match(c *engine.Context) bool {
	sub, args := pipSub(c)
	return sub == "install" && !hasArg(args, "--report", "--dry-run", "-h", "--help")
}

var (
	pipCollectingRe = regexp.MustCompile(`^Collecting \S`)
	pipDownloadRe   = regexp.MustCompile(`^\s*Downloading \S+(?: \([^)]*\))?$`)
	pipCachedRe     = regexp.MustCompile(`^\s*Using cached \S+(?: \([^)]*\))?$`)
	pipSatisfiedRe  = regexp.MustCompile(`^Requirement already satisfied: (\S+) in .*?\(([^()]*)\)$`)
	pipObtainRe     = regexp.MustCompile(`^(?:Obtaining|Processing) \S`)
	pipBuildRe      = regexp.MustCompile(`^\s*(?:(?:Installing build dependencies|Getting requirements to build (?:wheel|editable)|Preparing (?:editable )?metadata \([^)]*\)|Checking if build backend supports build_editable|Building (?:editable|wheel) for \S+ \([^)]*\))(?:: (?:started|finished with status 'done')| \.\.\. done)` +
		`|Running setup\.py (?:install|develop) for \S+(?: \.\.\. done)?|Created wheel for \S+: filename=\S+ size=\d+ sha256=[0-9a-f]+|Stored in directory: \S.*|Building wheels for collected packages: .*|Installing backend dependencies: (?:started|finished with status 'done'))$`)
	pipUninstRe = regexp.MustCompile(`^\s*(?:Attempting uninstall: \S+|Found existing installation: \S+ \S+|Uninstalling \S+:)$`)
	pipNoticeRe = regexp.MustCompile(`^(?:\[notice\] |WARNING: |DEPRECATION: |INFO: |You should consider upgrading via )`)
	pipKnownRe  = regexp.MustCompile(`^(?:Collecting |Requirement already satisfied: |Successfully installed |Successfully built |Obtaining |Processing |ERROR: |Installing collected packages: |Looking in indexes: |Looking in links: |Defaulting to user installation)|^\s*(?:Downloading |Using cached )`)
)

func (pipInstallFilter) Apply(c *engine.Context, text string) (string, bool) {
	lines := strings.Split(text, "\n")
	known := false
	for _, ln := range lines {
		if pipKnownRe.MatchString(ln) {
			known = true
			break
		}
	}
	if !known || strings.HasPrefix(strings.TrimSpace(text), "{") {
		return "", false
	}
	var (
		out                                    []string
		marker                                 = -1
		collecting, downloading, cached, build int
		obtaining, uninst, progress            int
		satisfied                              int
		topSatisfied                           []string
		installingAt                           = -1
		installed, errMode                     bool
		seen                                   = map[string]bool{}
		ln0                                    string // the line being read
	)
	hide := func(n *int) bool {
		if engine.IsError(ln0) {
			// "Collecting pytest-error-for-skips": hiding it would make the
			// engine's guard print it again under a heading.
			out = append(out, ln0)
			return false
		}
		*n++
		if marker < 0 {
			marker = len(out)
			out = append(out, "") // filled in below
		}
		return true
	}
	for _, ln := range lines {
		ln0 = ln
		t := strings.TrimSpace(ln)
		if errMode {
			if pipNoticeRe.MatchString(ln) && !strings.HasPrefix(ln, "ERROR") {
				if seen[ln] {
					continue
				}
				seen[ln] = true
			}
			out = append(out, ln)
			if strings.HasPrefix(ln, "Successfully installed ") {
				installed = true
			}
			continue
		}
		switch {
		case t == "":
		case strings.HasPrefix(ln, "ERROR:") || strings.HasPrefix(t, "error: ") || strings.HasPrefix(t, "× "):
			errMode = true
			out = append(out, ln)
		case pipCollectingRe.MatchString(ln):
			hide(&collecting)
		case pipDownloadRe.MatchString(ln):
			hide(&downloading)
		case pipCachedRe.MatchString(ln):
			hide(&cached)
		case pipSatisfiedRe.MatchString(ln):
			m := pipSatisfiedRe.FindStringSubmatch(ln)
			top := !strings.Contains(ln, " (from ") || strings.Contains(ln, " (from -r ") || strings.Contains(ln, " (from -c ")
			if hide(&satisfied) && top {
				topSatisfied = append(topSatisfied, reqName(m[1])+" "+m[2])
			}
		case pipObtainRe.MatchString(ln):
			hide(&obtaining)
		case pipBuildRe.MatchString(ln):
			hide(&build)
		case pipUninstRe.MatchString(ln):
			hide(&uninst)
		case engine.IsProgress(ln):
			hide(&progress)
		case strings.HasPrefix(ln, "Installing collected packages: "):
			installingAt = len(out)
			out = append(out, ln)
		case strings.HasPrefix(ln, "Successfully installed "):
			installed = true
			out = append(out, ln)
		case strings.HasPrefix(t, "Successfully uninstalled "):
			out = append(out, t) // indented under the hidden "Uninstalling x:" line
		case pipNoticeRe.MatchString(ln):
			if !seen[ln] {
				seen[ln] = true
				out = append(out, ln)
			}
		default:
			out = append(out, ln)
		}
	}
	if installed && installingAt >= 0 && !engine.IsError(out[installingAt]) {
		// Redundant with "Successfully installed"; kept when the install
		// stopped halfway, where it says what was being installed.
		out[installingAt] = "\x00"
	}
	if marker >= 0 {
		var parts []string
		add := func(n int, what string) {
			if n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, what))
			}
		}
		add(collecting, "Collecting")
		add(downloading, "Downloading")
		add(cached, "Using cached")
		add(obtaining, "Obtaining/Processing")
		add(build, "build-step")
		add(uninst, "uninstall-step")
		add(progress, "progress")
		var segs []string
		if len(parts) > 0 {
			segs = append(segs, "hid "+strings.Join(parts, ", ")+" lines")
		}
		if satisfied > 0 {
			s := fmt.Sprintf("%d already satisfied", satisfied)
			switch {
			case len(topSatisfied) == 0:
				s = engine.Plural(satisfied, "dependency", "dependencies") + " already satisfied"
			default:
				names := topSatisfied
				more := ""
				if len(names) > 10 {
					more = fmt.Sprintf(" +%d more", len(names)-10)
					names = names[:10]
				}
				s = "already satisfied: " + strings.Join(names, ", ") + more
				if dep := satisfied - len(topSatisfied); dep > 0 {
					s += fmt.Sprintf(" (+%s)", engine.Plural(dep, "dependency", "dependencies"))
				}
			}
			segs = append(segs, s)
		}
		out[marker] = "[lx: " + strings.Join(segs, "; ") + "]"
	}
	var b []string
	for _, ln := range out {
		if ln != "\x00" {
			b = append(b, ln)
		}
	}
	b = trimBlank(b)
	if c.Exit != 0 && !anyError(b) {
		b = append(b, fmt.Sprintf("[lx: pip exited %d]", c.Exit))
	}
	return strings.Join(relativize(c, b), "\n"), true
}

// reqName strips the version specifier and extras from a requirement.
func reqName(spec string) string {
	if i := strings.IndexAny(spec, "<>=!~;[ @"); i > 0 {
		return spec[:i]
	}
	return spec
}

func anyError(lines []string) bool {
	for _, ln := range lines {
		if engine.IsError(ln) {
			return true
		}
	}
	return false
}

// ---- pip uninstall ----

// pipUninstallFilter keeps "Successfully uninstalled …" and anything
// unexpected (warnings, errors, the "Would remove" list), dropping the
// "Found existing installation" / "Uninstalling x:" lines before each.
type pipUninstallFilter struct{}

func (pipUninstallFilter) Name() string { return "pip-uninstall" }

func (pipUninstallFilter) Match(c *engine.Context) bool {
	sub, _ := pipSub(c)
	return sub == "uninstall"
}

func (pipUninstallFilter) Apply(c *engine.Context, text string) (string, bool) {
	var out []string
	known, hidden := false, 0
	for _, ln := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(ln) == "":
		case pipUninstRe.MatchString(ln) && !engine.IsError(ln):
			known = true
			hidden++
		case strings.HasPrefix(strings.TrimSpace(ln), "Successfully uninstalled "):
			known = true
			out = append(out, strings.TrimSpace(ln))
		default:
			out = append(out, ln)
		}
	}
	if !known {
		return "", false
	}
	if c.Exit != 0 && !anyError(out) {
		out = append(out, fmt.Sprintf("[lx: pip exited %d]", c.Exit))
	}
	return strings.Join(relativize(c, out), "\n"), true
}

// ---- pip list / pip show ----

// pipListFilter recognizes the `pip list` table. It is data: package names
// containing "error" are not errors (Content). The table is already
// compact, so apart from relativizing editable locations it is unchanged.
type pipListFilter struct{}

func (pipListFilter) Name() string    { return "pip-list" }
func (pipListFilter) IsContent() bool { return true }

func (pipListFilter) Match(c *engine.Context) bool {
	sub, args := pipSub(c)
	if sub != "list" {
		return false
	}
	for _, f := range argValues(args, "--format", "") {
		if f != "columns" {
			return false // json / freeze: machine output
		}
	}
	return true
}

var pipListHeadRe = regexp.MustCompile(`^Package +Version\b`)

func (pipListFilter) Apply(c *engine.Context, text string) (string, bool) {
	lines := strings.Split(text, "\n")
	// The header may follow warnings/notices.
	for i, ln := range lines {
		if pipListHeadRe.MatchString(ln) {
			if i+1 >= len(lines) || strings.Trim(lines[i+1], "- ") != "" {
				return "", false
			}
			if c.Exit != 0 && !anyError(lines) {
				lines = append(lines, fmt.Sprintf("[lx: pip exited %d]", c.Exit))
			}
			return strings.Join(relativize(c, lines), "\n"), true
		}
	}
	return "", false
}

// pipShowFilter keeps every field of `pip show` and factors the "Files:"
// list of `pip show -f` into a path tree.
type pipShowFilter struct{}

func (pipShowFilter) Name() string    { return "pip-show" }
func (pipShowFilter) IsContent() bool { return true }

func (pipShowFilter) Match(c *engine.Context) bool {
	sub, _ := pipSub(c)
	return sub == "show"
}

var pipFieldRe = regexp.MustCompile(`^[A-Z][\w-]*: ?`)

func (pipShowFilter) Apply(c *engine.Context, text string) (string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return "", false
	}
	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "Name: ") {
			start = i
			break
		}
		if !strings.HasPrefix(ln, "WARNING: ") && strings.TrimSpace(ln) != "" {
			return "", false
		}
	}
	if start < 0 {
		return "", false
	}
	var out []string
	out = append(out, lines[:start]...)
	var files []string
	flushFiles := func() {
		if len(files) > 0 {
			for _, p := range engine.FactorPaths(files) {
				out = append(out, "  "+p)
			}
			files = nil
		}
	}
	inFiles := false
	for _, ln := range lines[start:] {
		switch {
		case inFiles && strings.HasPrefix(ln, "  "):
			files = append(files, strings.TrimSpace(ln))
			continue
		case ln == "---" || pipFieldRe.MatchString(ln) || strings.TrimSpace(ln) == "":
			flushFiles()
			inFiles = ln == "Files:"
			out = append(out, ln)
		default:
			// Continuation of a multi-line field, or an unknown line.
			flushFiles()
			inFiles = false
			out = append(out, ln)
		}
	}
	flushFiles()
	if c.Exit != 0 && !anyError(out) {
		out = append(out, fmt.Sprintf("[lx: pip exited %d]", c.Exit))
	}
	return strings.Join(relativize(c, out), "\n"), true
}
