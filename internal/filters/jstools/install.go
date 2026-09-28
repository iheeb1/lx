package jstools

import (
	"fmt"
	"slices"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type npmInstall struct{}

func (npmInstall) Name() string { return "npm-install" }

func (npmInstall) Match(c *engine.Context) bool {
	_, sub, rest, ok := manager(c)
	if !ok {
		return false
	}
	switch sub {
	case "install", "ci", "uninstall", "update", "create":
		return !hasArg(rest, "--json", "--help", "-h", "--parseable")
	}
	return false
}

func (npmInstall) GuardsErrors() bool { return true }

func (npmInstall) Apply(c *engine.Context, s string) (string, bool) {
	lines := strings.Split(s, "\n")
	recognized := false
	for _, ln := range lines {
		if installMarkRe.MatchString(ln) {
			recognized = true
			break
		}
	}
	if !recognized {
		return "", false
	}
	var o out
	installLines(lines, &o)
	if note := failNote(c, o.lines, nil); note != "" {
		o.add(note)
	}
	return o.String(), true
}

var (
	installMarkRe = lazyre.New(`^(?:npm (?:warn|WARN|error|notice)\b|npm ERR! |(?:added|removed|changed|audited) \d+ packages?` +
		`|up to date|found \d+ vulnerabilit|\d+ (?:\w+ severity )?vulnerabilit|Progress: resolved \d+|Packages: [+-]\d` +
		`|Already up to date|Lockfile is up to date|Done in |yarn (?:install|add|remove|upgrade|create) v\d|\[\d+/\d+\] ` +
		`|success |➤ YN\d{4}|bun (?:install|add|remove|update|create) v\d|\s*\d+ packages? installed|Checked \d+ installs?` +
		`| ERR_PNPM_|[\s\x{2009}]*WARN[\s\x{2009}])`)

	npmDeprecRe  = lazyre.New(`^(npm (?:warn|WARN) deprecated |[\s\x{2009}]*WARN[\s\x{2009}]+deprecated )(\S+?@[^:\s]+): (.*)$`)
	yarnDeprecRe = lazyre.New(`^(warning )((?:\S+ > )*\S+?@[^:\s]+): (.*)$`)
	yarnPhaseRe  = lazyre.New(`^\[(\d+)/\d+\] `)
	yarnAllRe    = lazyre.New(`^info All dependencies$`)
	yarnTreeRe   = lazyre.New(`^[│ ]*[├└]─ \S`)

	fundingRe    = lazyre.New("^(?:\\d+ packages? (?:is|are) looking for funding|  run `npm fund` for details)$")
	auditHintRe  = lazyre.New(`^To address (?:issues that do not require attention|all issues.*), run:$`)
	auditFixRe   = lazyre.New(`^  npm audit fix(?: --force)?$`)
	npmNoticeRe  = lazyre.New(`^npm (?:notice|NOTICE)\b`)
	eresolveRe   = lazyre.New(`^npm (?:warn|WARN) ERESOLVE overriding peer dependency$`)
	eresolveCont = lazyre.New(`^npm (?:warn|WARN)(?:$| (?:While resolving|Found|node_modules/| |Could not resolve|Conflicting peer|peer|peerOptional|dev|optional|bundled|overridden|\d+ more))`)
	whileResRe   = lazyre.New(`^npm (?:warn|WARN) While resolving: (\S+)`)
	bareNpmRe    = lazyre.New(`^npm (?:warn|WARN|error|ERR!|notice)(?: allow-scripts)?$`)
	pnpmProgRe   = lazyre.New(`^Progress: resolved \d+`)
	pnpmBarRe    = lazyre.New(`^[+\-]+$`)
	updBoxTopRe  = lazyre.New(`^\s*╭─+╮$`)
	updBoxBotRe  = lazyre.New(`^\s*╰─+╯$`)
	yarnStepRe   = lazyre.New(`^\[\d+/\d+\] `)
	yarnNoiseRe  = lazyre.New(`^info Visit https://yarnpkg\.com/|^➤ YN0000: [┌└] `)
	npmLogPathRe = lazyre.New(`^npm (?:error|ERR!) A complete log of this run can be found in:`)

	npmHTTPOkRe = lazyre.New(`^npm (?:http|HTTP) (?:fetch [A-Z]+ (?:2\d\d|304) |cache )`)
	npmSillyRe  = lazyre.New(`^npm (?:sill|silly|timing) `)

	npmUsageCodeRe = lazyre.New(`^npm (?:error|ERR!) code EUSAGE$`)
	npmUsageOptRe  = lazyre.New(`^npm (?:error|ERR!) Options:$`)
	npmUsageEndRe  = lazyre.New(`^npm (?:error|ERR!) Run "npm help ([\w-]+)" for more info$`)
	npmUsageLineRe = lazyre.New(`^npm (?:error|ERR!)(?: |$)`)
)

const maxUsageLines = 400

func usageBlock(lines []string, i int) (int, string) {
	if !npmUsageOptRe.MatchString(lines[i]) {
		return -1, ""
	}
	for j := i + 1; j < len(lines) && j <= i+maxUsageLines; j++ {
		if m := npmUsageEndRe.FindStringSubmatch(lines[j]); m != nil {
			return j, m[1]
		}
		if !npmUsageLineRe.MatchString(lines[j]) {
			return -1, ""
		}
	}
	return -1, ""
}

var keepDeprecWords = []string{"secur", "vulnerab", "leak", "malware", "compromis", "unsupported",
	"no longer supported", "not supported"}

func keepDeprecation(msg string) bool {
	low := strings.ToLower(msg)
	for _, w := range keepDeprecWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

type deprecation struct {
	pkgs  []string
	msg   string
	first int
}

const maxListed = 40

func capList(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(", … +%d more", len(items)-max)
}

func installLines(lines []string, o *out) {
	var (
		kept      []*deprecation
		byMsg     = map[string]*deprecation{}
		hidden    []deprecation
		seenPkg   = map[string]bool{}
		firstDep  = -1
		lastProg  = -1
		deprecIdx = map[int]bool{}
	)
	prefix := ""
	yarnPhase := ""
	msgErr := map[string]bool{}
	httpOK, silly, usage := 0, 0, false
	for i, ln := range lines {
		if pnpmProgRe.MatchString(ln) {
			lastProg = i
		}
		if strings.HasPrefix(ln, "npm ") {
			switch {
			case npmHTTPOkRe.MatchString(ln):
				httpOK++
			case npmSillyRe.MatchString(ln):
				silly++
			case npmUsageCodeRe.MatchString(ln):
				usage = true
			}
		}
		if m := yarnPhaseRe.FindStringSubmatch(ln); m != nil {
			yarnPhase = m[1]
		}
		m := npmDeprecRe.FindStringSubmatch(ln)
		if m == nil && yarnPhase == "1" {
			m = yarnDeprecRe.FindStringSubmatch(ln)
		}
		if m == nil {
			continue
		}

		errMsg, ok := msgErr[m[3]]
		if !ok {
			errMsg = engine.IsError(m[3])
			msgErr[m[3]] = errMsg
		}
		if errMsg || engine.IsError(m[2]) {
			continue
		}
		deprecIdx[i] = true
		if firstDep < 0 {
			firstDep, prefix = i, m[1]
		}
		pkg, msg := m[2], m[3]
		if keepDeprecation(msg) {
			d := byMsg[msg]
			if d == nil {
				d = &deprecation{msg: msg, first: i}
				byMsg[msg] = d
				kept = append(kept, d)
			}
			if !seenPkg[pkg+"\x00"+msg] {
				seenPkg[pkg+"\x00"+msg] = true
				d.pkgs = append(d.pkgs, pkg)
			}
			continue
		}
		if !seenPkg[pkg] {
			seenPkg[pkg] = true
			hidden = append(hidden, deprecation{pkgs: []string{pkg}, msg: msg, first: i})
		}
	}

	var (
		eresolveExtra []string
		eresolveAt    = -1
		eresolveSeen  = false
		inBox         = false
		boxLines      []string
		httpShown     = false
		sillyShown    = false
	)
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if usage {
			if end, cmd := usageBlock(lines, i); end > i {
				o.add(fmt.Sprintf("[npm usage text: %s hidden; `npm help %s` shows them]",
					engine.Plural(end-i, "option line", "option lines"), cmd))
				i = end - 1
				continue
			}
		}
		switch {
		case deprecIdx[i]:
			if i == firstDep {
				groups := make([]deprecation, 0, len(kept)+1)
				for _, d := range kept {
					groups = append(groups, deprecation{msg: prefix + capList(d.pkgs, maxListed) + ": " + d.msg, first: d.first})
				}
				switch len(hidden) {
				case 0:
				case 1:
					groups = append(groups, deprecation{msg: prefix + hidden[0].pkgs[0] + ": " + hidden[0].msg, first: hidden[0].first})
				default:
					names := make([]string, len(hidden))
					for k, d := range hidden {
						names[k] = d.pkgs[0]
					}
					groups = append(groups, deprecation{first: hidden[0].first, msg: fmt.Sprintf("%s%s [%s, messages hidden]",
						prefix, capList(names, maxListed), engine.Plural(len(hidden), "deprecated package", "deprecated packages"))})
				}
				slices.SortStableFunc(groups, func(a, b deprecation) int { return a.first - b.first })
				for _, g := range groups {
					o.add(g.msg)
				}
			}
		case eresolveRe.MatchString(ln):
			j := i + 1
			for j < len(lines) && eresolveCont.MatchString(lines[j]) {
				j++
			}
			if !eresolveSeen {
				eresolveSeen = true
				o.add(lines[i:j]...)
				eresolveAt = len(o.lines)
				o.add("")
			} else {
				target := "?"
				for _, b := range lines[i:j] {
					if m := whileResRe.FindStringSubmatch(b); m != nil {
						target = m[1]
						break
					}
				}
				eresolveExtra = append(eresolveExtra, target)
				for _, b := range lines[i:j] {
					o.drop(b)
				}
			}
			i = j - 1
		case inBox:
			boxLines = append(boxLines, ln)
			if updBoxBotRe.MatchString(ln) {
				inBox = false
				if !strings.Contains(strings.Join(boxLines, "\n"), "Update available") {
					o.add(boxLines...)
				} else {
					for _, b := range boxLines {
						o.drop(b)
					}
				}
				boxLines = nil
			}
		case updBoxTopRe.MatchString(ln):
			inBox, boxLines = true, []string{ln}
		case httpOK > 1 && npmHTTPOkRe.MatchString(ln):
			if !httpShown {
				httpShown = true
				o.add(fmt.Sprintf("[npm http: %s (status 2xx/304) hidden]", engine.Plural(httpOK, "fetch/cache line", "fetch/cache lines")))
			}
		case silly > 1 && npmSillyRe.MatchString(ln):
			if !sillyShown {
				sillyShown = true
				o.add(fmt.Sprintf("[npm silly/timing: %s hidden]", engine.Plural(silly, "line", "lines")))
			}
			o.drop(ln)
		case npmNoticeRe.MatchString(ln), fundingRe.MatchString(ln), bareNpmRe.MatchString(ln),
			yarnStepRe.MatchString(ln), yarnNoiseRe.MatchString(ln),
			pnpmBarRe.MatchString(ln) && i > 0 && strings.HasPrefix(lines[i-1], "Packages: "),
			pnpmProgRe.MatchString(ln) && i != lastProg:
			o.drop(ln)
		case yarnAllRe.MatchString(ln):
			j := i + 1
			for j < len(lines) && yarnTreeRe.MatchString(lines[j]) {
				j++
			}
			if n := j - i - 1; n > 5 {
				o.add(fmt.Sprintf("%s: %d packages [list hidden]", ln, n))
				for _, b := range lines[i+1 : j] {
					o.drop(b)
				}
			} else {
				o.add(lines[i:j]...)
			}
			i = j - 1
		case auditHintRe.MatchString(ln):
			o.drop(ln)
			if i+1 < len(lines) && auditFixRe.MatchString(lines[i+1]) {
				i++
				o.drop(lines[i])
			}
			if i+1 < len(lines) && lines[i+1] == "" {
				i++
			}
		case npmLogPathRe.MatchString(ln) && o.saw(squash(ln)),
			len(o.lines) > 0 && ln == o.lines[len(o.lines)-1] && strings.HasPrefix(ln, "npm "):

		default:
			o.add(ln)
		}
	}
	if len(boxLines) > 0 {
		o.add(boxLines...)
	}
	if eresolveAt >= 0 && len(eresolveExtra) > 0 {
		o.lines[eresolveAt] = fmt.Sprintf("[+%d more \"npm warn ERESOLVE overriding peer dependency\" blocks, while resolving: %s]",
			len(eresolveExtra), capList(eresolveExtra, maxListed))
	}
}
