package engine

import (
	"regexp"
	"strings"
)

// Level is the importance class of an output line.
type Level int

const (
	Normal Level = iota
	Warn
	Err
)

var (
	// benign spans are removed before matching so "0 errors", "no failures",
	// "error handling" or a file named errors.go don't count as errors, while
	// "2 failed, 0 errors" still does.
	benignRe = regexp.MustCompile(`(?i)\b(?:0|no|zero|without)\s+(?:errors?|failures?|failed|warnings?|vulnerabilities|problems?|issues?)\b` +
		`|\b(?:errors?|failures?|failed|warnings?)\s*[:=]\s*0\b` +
		`|\berrors?[_-]?(?:handler|handling|handle|boundary|page|codes?|messages?|types?|utils?)\b` +
		`|\b[\w./-]*errors?\.(?:go|ts|tsx|js|jsx|py|rs|rb|java|kt|c|h|cc|cpp)\b` +
		`|\berr\s*!=\s*nil\b|\bif err\b|\bonError\b|\bon_error\b|\bErrorf?\(` +
		`|\bfail[_-]?fast\b|\bexpect\(.*\)\.to(?:Throw|Fail)\w*|\bassertRaises\b|\bpytest\.raises\b` +
		// Paths and URLs (any token with a slash) and command-line flags:
		// "examples/error-pages/", "-Wfatal-errors", "--fail-fast" are names,
		// not status. The error, if any, is elsewhere on the line.
		"|\\S*/\\S*|(?:^|\\s)--?[A-Za-z][\\w=,.:+-]*")

	errRe = regexp.MustCompile(`(?i)(?:\b(?:error|errors|err!|fatal|panic|panicked|exception|traceback|` +
		`fail|failed|failure|failures|failing|assertionerror|segfault|segmentation fault|core dumped|` +
		`aborted|unhandled|uncaught|deadlock|timed out|out of memory|oom|killed|` +
		`cannot|could not|couldn't|unable to|no such file|not found|undefined reference|` +
		`denied|refused|rejected|conflict|unresolved|syntaxerror|typeerror|referenceerror|` +
		`valueerror|keyerror|indexerror|attributeerror|importerror|modulenotfounderror|nameerror|` +
		`runtimeerror|nullpointerexception|data race)\b` +
		`|^\s*E\s{2,}\S|^\s*[✗✘✕×]\s|\bTS\d{4}\b|\berror\[E\d+\]|^\s*npm (?:ERR!|error)|` +
		`^--- FAIL|^FAIL\b|^\s*FAILED\b|^\s*!\s+\[rejected\]|` +
		`^\s*g?make(?:\[\d+\])?: \*\*\*|\bundefined symbols?\b|\bunknown (?:options?|flags?|arguments?|commands?)\b|^\s*e: )`)

	warnRe = regexp.MustCompile(`(?i)\b(?:warn|warning|warnings|deprecated|deprecation)\b|^\s*⚠`)

	// passRe vetoes lines that report success ("✓ handles error input").
	passRe = regexp.MustCompile(`^\s*(?:✓|✔|√|PASS\b|ok\s|--- PASS|\[PASS\]|PASSED\b)|\.\.\.\s*ok$|\s(?:PASSED|passed)\s*(?:\[|$)` +
		// Neutral progress/bookkeeping whose only error-ish word is a name:
		// "=== RUN TestConflict", "go: downloading github.com/pkg/errors",
		// "Compiling quick-error v2.0.1".
		`|^=== (?:RUN|PAUSE|CONT|NAME)\s|^\s*--- SKIP|^go: (?:downloading|finding|extracting) ` +
		`|^\s*(?:Compiling|Checking|Downloaded|Downloading|Fresh|Installing|Documenting)\s+\S+ v\d`)
)

// Classify returns the importance level of one line.
//
// It is classifySlow made fast: the cheap stem prefilter first, then an
// exact tokenized equivalent of errRe, and the expensive pass-veto and
// benign-span stripping only for lines that hold an error or warning
// candidate. Stripping benign spans only removes matches (every benign
// alternative starts and ends on a boundary), so a line with no candidate
// in its original text has none after stripping either.
func Classify(line string) Level {
	if !mayClassify(line) {
		return Normal
	}
	if strings.ContainsAny(line, "\u212a\u017f") {
		return classifySlow(line) // case folding outside ASCII: use the reference
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

// errSpecialRe is errRe minus its \b(word|…)\b alternation, which errMatch
// checks by tokenizing instead (the alternation is the slow part: ~50µs a
// line through the regexp engine, ~1µs tokenized).
var errSpecialRe = regexp.MustCompile(`(?i)^\s*E\s{2,}\S|^\s*[✗✘✕×]\s|\bTS\d{4}\b|\berror\[E\d+\]|^\s*npm (?:ERR!|error)|` +
	`^--- FAIL|^FAIL\b|^\s*FAILED\b|^\s*!\s+\[rejected\]|` +
	`^\s*g?make(?:\[\d+\])?: \*\*\*|\bundefined symbols?\b|\bunknown (?:options?|flags?|arguments?|commands?)\b|^\s*e: `)

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

// errPhrases are errRe's multi-word alternatives, words separated by
// exactly one space.
var errPhrases = [][]string{
	{"segmentation", "fault"}, {"core", "dumped"}, {"timed", "out"}, {"out", "of", "memory"},
	{"could", "not"}, {"unable", "to"}, {"no", "such", "file"}, {"not", "found"},
	{"undefined", "reference"}, {"data", "race"},
}

// errMatch reports errRe.MatchString(s) without running errRe.
func errMatch(s string) bool {
	if errSpecialRe.MatchString(s) {
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
		// couldn't: "couldn" ' "t"
		if w == "couldn" && k+1 < len(toks) && s[t.end:toks[k+1].start] == "'" && low(toks[k+1]) == "t" {
			return true
		}
		// err! directly followed by a word character (\berr!\b).
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

// classifySlow is the reference classifier; Classify only adds a prefilter.
func classifySlow(line string) Level {
	if strings.TrimSpace(line) == "" {
		return Normal
	}
	if passRe.MatchString(line) {
		return Normal
	}
	// Benign spans can only cancel a match, never create one: a stripped
	// leading token must not let "^\s*E\s{2,}" match where the line itself
	// didn't. So a class needs a match in the original AND after stripping.
	s := benignRe.ReplaceAllString(line, " ")
	if errRe.MatchString(line) && errRe.MatchString(s) {
		return Err
	}
	if warnRe.MatchString(line) && warnRe.MatchString(s) {
		return Warn
	}
	return Normal
}

// classifyStems: every match of errRe or warnRe contains one of these
// letter runs (ASCII case-insensitive), and every match of benignRe does
// too (err, fail, warn, vulnerabilit, problem, issue, expect, assertraises,
// raises). Blanking benign spans only inserts spaces, so any letter run in
// the blanked line also occurs in the original line.
var classifyStems = []string{
	"err", "fatal", "panic", "exception", "traceback", "fail", "segfault", "segmentation",
	"dumped", "abort", "unhandled", "uncaught", "deadlock", "timed", "memory", "oom",
	"killed", "cannot", "could", "unable", "such", "found", "undefined", "denied",
	"refused", "rejected", "conflict", "unresolved", "race", "warn", "deprecat",
	"vulnerabilit", "problem", "issue", "expect", "assertraises", "raises",
	"unknown", "symbol",
}

// pytestELineRe is errRe's `^\s*E\s{2,}\S` alternative (errRe is (?i)).
var pytestELineRe = regexp.MustCompile(`^\s*[eE]\s{2,}\S`)

// kotlinELineRe is errRe's `^\s*e: ` alternative (Kotlin, apt "E: …").
var kotlinELineRe = regexp.MustCompile(`^\s*[eE]: `)

// mayClassify is a cheap necessary condition for Classify returning Warn or
// Err. It returns false only when no errRe/warnRe alternative can match:
// no stem (so benignRe cannot match either and the line is matched as is),
// no status symbol, no pytest "E   " start and no "TS"+digit. Lines holding
// the two non-ASCII runes that case-fold to ASCII letters (U+212A KELVIN
// SIGN, U+017F LONG S) always take the full path. This keeps Classify fast
// on large outputs (minified bundles, long logs) without changing results.
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
	for i := strings.Index(lower, "ts"); i >= 0; {
		if i+2 < len(lower) && lower[i+2] >= '0' && lower[i+2] <= '9' {
			return true
		}
		j := strings.Index(lower[i+1:], "ts")
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return false
}

// asciiLower lowercases ASCII letters only, allocating only when needed.
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

// IsError reports whether a line is error-class (P0).
func IsError(line string) bool { return Classify(line) == Err }

// IsWarning reports whether a line is warning-class (P1).
func IsWarning(line string) bool { return Classify(line) == Warn }
