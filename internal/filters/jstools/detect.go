package jstools

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() {
	engine.RegisterDetector(engine.Detector{Name: "tsc", Detect: detectTSC, Filter: tsc{}, Argv: []string{"tsc"}})
	engine.RegisterDetector(engine.Detector{Name: "eslint", Detect: detectESLint, Filter: eslint{}, Argv: []string{"eslint"}})
}

func detectTSC(s string) bool {
	if engine.HasLine(s, "): error TS", func(ln string) bool { m, ok := scanTSCPlain(ln); return ok && tscPathLike(m[0]) }) {
		return true
	}
	return engine.HasLine(s, " - error TS", func(ln string) bool { m, ok := scanTSCPretty(ln); return ok && tscPathLike(m[0]) }) &&
		engine.HasLinePrefix(s, "Found ", tscFoundRe.MatchString)
}

func tscPathLike(p string) bool {
	if p == "" || strings.ContainsAny(p, " \t") || strings.IndexByte("+-<>|#", p[0]) >= 0 {
		return false
	}
	i := strings.IndexByte(p, ':')
	if i < 0 {
		return true
	}
	drive := i == 1 && ('A' <= p[0] && p[0] <= 'Z' || 'a' <= p[0] && p[0] <= 'z') && len(p) > 2 && (p[2] == '\\' || p[2] == '/')
	return drive && strings.IndexByte(p[2:], ':') < 0
}

func detectESLint(s string) bool {
	if !engine.HasLinePrefix(s, "✖ ", eslintSummaryRe.MatchString) {
		return false
	}

	prev := ""
	for rest := s; rest != ""; {
		var ln string
		ln, rest, _ = strings.Cut(rest, "\n")
		if prev != "" && prev[0] != ' ' && prev[0] != '\t' && len(prev) <= engine.MaxSignatureLine &&
			len(ln) <= engine.MaxSignatureLine {
			if _, _, _, _, ok := scanESMsg(ln); ok {
				return true
			}
		}
		prev = ln
	}
	return false
}
