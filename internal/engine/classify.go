package engine

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"
)

type Level int

const (
	Normal Level = iota
	Warn
	Err
)

var (
	benignRe = lazyre.New(`(?i)\b(?:0|no|zero|without)\s+(?:errors?|failures?|failed|warnings?|vulnerabilities|problems?|issues?)\b` +
		`|\b(?:errors?|failures?|failed|warnings?)\s*[:=]\s*0\b` +
		`|\berrors?[_-]?(?:handler|handling|handle|boundary|page|codes?|messages?|types?|utils?)\b` +
		`|\b[\w./-]*errors?\.(?:go|ts|tsx|js|jsx|py|rs|rb|java|kt|c|h|cc|cpp)\b` +
		`|\berr\s*!=\s*nil\b|\bif err\b|\bonError\b|\bon_error\b|\bErrorf?\(` +
		`|\bfail[_-]?fast\b|\bexpect\(.*\)\.to(?:Throw|Fail)\w*|\bassertRaises\b|\bpytest\.raises\b` +
		"|\\S*/\\S*|(?:^|\\s)--?[A-Za-z][\\w=,.:+-]*")

	errRe = lazyre.New(`(?i)(?:\b(?:error|errors|err!|fatal|panic|panicked|exception|traceback|` +
		`fail|failed|failure|failures|failing|assertionerror|segfault|segmentation fault|core dumped|` +
		`aborted|unhandled|uncaught|deadlock|timed out|out of memory|oom|killed|` +
		`cannot|could not|couldn't|unable to|no such file|not found|undefined reference|` +
		`denied|refused|rejected|conflict|unresolved|syntaxerror|typeerror|referenceerror|` +
		`valueerror|keyerror|indexerror|attributeerror|importerror|modulenotfounderror|nameerror|` +
		`runtimeerror|nullpointerexception|data race)\b` +
		`|^\s*E\s{2,}\S|^\s*[✗✘✕×]\s|\bTS\d{4}\b|\berror\[E\d+\]|^\s*npm (?:ERR!|error)|` +
		`^--- FAIL|^FAIL\b|^\s*FAILED\b|^\s*!\s+\[rejected\]|` +
		`^\s*g?make(?:\[\d+\])?: \*\*\*|\bundefined symbols?\b|\bunknown (?:options?|flags?|arguments?|commands?)\b|^\s*e: )`)

	warnRe = lazyre.New(`(?i)\b(?:warn|warning|warnings|deprecated|deprecation)\b|^\s*⚠`)

	passRe = lazyre.New(`^\s*(?:✓|✔|√|PASS\b|ok\s|--- PASS|\[PASS\]|PASSED\b)|\.\.\.\s*ok$|\s(?:PASSED|passed)\s*(?:\[|$)` +
		`|^=== (?:RUN|PAUSE|CONT|NAME)\s|^\s*--- SKIP|^go: (?:downloading|finding|extracting) ` +
		`|^\s*(?:Compiling|Checking|Downloaded|Downloading|Fresh|Installing|Documenting)\s+\S+ v\d`)
)

func Classify(line string) Level {
	if !mayClassify(line) {
		return Normal
	}
	if strings.ContainsAny(line, "\u212a\u017f") {
		return classifySlow(line)
	}
	if !errMatch(line) && !warnRe.MatchString(line) {
		return Normal
	}
	if strings.TrimSpace(line) == "" || passRe.MatchString(line) {
		return Normal
	}
	s := benignRe.ReplaceAllString(line, " ")
	if errMatch(line) && errMatch(s) {
		return Err
	}
	if warnRe.MatchString(line) && warnRe.MatchString(s) {
		return Warn
	}
	return Normal
}

var (
	errLeadRe = lazyre.New(`(?i)^(?:\s*(?:E\s{2,}\S|[✗✘✕×]\s|npm (?:ERR!|error)|FAILED\b|!\s+\[rejected\]|g?make(?:\[\d+\])?: \*\*\*|e: )|--- FAIL|FAIL\b)`)
	errMidRe  = lazyre.New(`(?i)\bTS\d{4}\b|\berror\[E\d+\]|\bundefined symbols?\b|\bunknown (?:options?|flags?|arguments?|commands?)\b`)
)

func errSpecial(s string) bool {
	if errLeadRe.MatchString(s) {
		return true
	}
	low := asciiLower(s)
	if !strings.ContainsAny(s, "\u212a\u017f") && !tsCode(low) && !strings.Contains(low, "error[e") && !strings.Contains(low, "undefined symbol") && !strings.Contains(low, "unknown ") {
		return false
	}
	return errMidRe.MatchString(s)
}

func tsCode(low string) bool {
	for i := strings.Index(low, "ts"); i >= 0; {
		if i+2 < len(low) && low[i+2] >= '0' && low[i+2] <= '9' {
			return true
		}
		j := strings.Index(low[i+1:], "ts")
		if j < 0 {
			return false
		}
		i += 1 + j
	}
	return false
}

var errWords = map[string]bool{
	"error": true, "errors": true, "fatal": true, "panic": true, "panicked": true, "exception": true,
	"traceback": true, "fail": true, "failed": true, "failure": true, "failures": true, "failing": true,
	"assertionerror": true, "segfault": true, "aborted": true, "unhandled": true, "uncaught": true,
	"deadlock": true, "oom": true, "killed": true, "cannot": true, "denied": true, "refused": true,
	"rejected": true, "conflict": true, "unresolved": true, "syntaxerror": true, "typeerror": true,
	"referenceerror": true, "valueerror": true, "keyerror": true, "indexerror": true,
	"attributeerror": true, "importerror": true, "modulenotfounderror": true, "nameerror": true,
	"runtimeerror": true, "nullpointerexception": true,
}

var errPhrases = [][]string{
	{"segmentation", "fault"}, {"core", "dumped"}, {"timed", "out"}, {"out", "of", "memory"},
	{"could", "not"}, {"unable", "to"}, {"no", "such", "file"}, {"not", "found"},
	{"undefined", "reference"}, {"data", "race"},
}

func errMatch(s string) bool {
	if errSpecial(s) {
		return true
	}
	type tok struct{ start, end int }
	var toks []tok
	for i := 0; i < len(s); {
		if !isWordByte(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && isWordByte(s[j]) {
			j++
		}
		toks = append(toks, tok{i, j})
		i = j
	}
	low := func(t tok) string { return asciiLower(s[t.start:t.end]) }
	for k, t := range toks {
		w := low(t)
		if errWords[w] {
			return true
		}

		if w == "couldn" && k+1 < len(toks) && s[t.end:toks[k+1].start] == "'" && low(toks[k+1]) == "t" {
			return true
		}

		if w == "err" && t.end+1 < len(s) && s[t.end] == '!' && isWordByte(s[t.end+1]) {
			return true
		}
		for _, ph := range errPhrases {
			if w != ph[0] || k+len(ph) > len(toks) {
				continue
			}
			ok := true
			for m := 1; m < len(ph) && ok; m++ {
				ok = s[toks[k+m-1].end:toks[k+m].start] == " " && low(toks[k+m]) == ph[m]
			}
			if ok {
				return true
			}
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func classifySlow(line string) Level {
	if strings.TrimSpace(line) == "" {
		return Normal
	}
	if passRe.MatchString(line) {
		return Normal
	}

	s := benignRe.ReplaceAllString(line, " ")
	if errRe.MatchString(line) && errRe.MatchString(s) {
		return Err
	}
	if warnRe.MatchString(line) && warnRe.MatchString(s) {
		return Warn
	}
	return Normal
}

var classifyStems = []string{
	"err", "fatal", "panic", "exception", "traceback", "fail", "segfault", "segmentation",
	"dumped", "abort", "unhandled", "uncaught", "deadlock", "timed", "memory", "oom",
	"killed", "cannot", "could", "unable", "such", "found", "undefined", "denied",
	"refused", "rejected", "conflict", "unresolved", "race", "warn", "deprecat",
	"vulnerabilit", "problem", "issue", "expect", "assertraises", "raises",
	"unknown", "symbol",
}

var pytestELineRe = lazyre.New(`^\s*[eE]\s{2,}\S`)

var kotlinELineRe = lazyre.New(`^\s*[eE]: `)

func mayClassify(line string) bool {
	if strings.ContainsAny(line, "✗✘✕×⚠\u212a\u017f") || pytestELineRe.MatchString(line) ||
		strings.Contains(line, "***") || kotlinELineRe.MatchString(line) {
		return true
	}
	lower := asciiLower(line)
	for _, st := range classifyStems {
		if strings.Contains(lower, st) {
			return true
		}
	}
	return tsCode(lower)
}

func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; c >= 'A' && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

func IsError(line string) bool { return Classify(line) == Err }

func IsWarning(line string) bool { return Classify(line) == Warn }
