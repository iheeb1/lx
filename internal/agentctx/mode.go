package agentctx

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

const intentChars = 400

var (
	verifyRe = lazyre.New(`\bverif(?:y|ying|ication)\b|\bmake sure\b|\bconfirm(?:s|ing)?\b|\bsanity[- ]check|\bdouble[- ]check` +
		`|\bcheck(?:ing)? (?:that|if|whether)\b[^.?!\n]{0,80}?\b(?:pass(?:es|ing)?|green|succeeds?|works?|compiles?|builds?|fixed|resolved)\b` +
		`|\bsee (?:if|whether)\b[^.?!\n]{0,60}?\b(?:pass(?:es|ing)?|green|succeeds?|works?|compiles?|builds?)\b` +
		`|\b(?:re-?run|run)(?:ning)? (?:the |all |our |them |it |this )?(?:\w+ ){0,3}(?:again|one more time|once more)\b` +
		`|\bre-?run(?:ning)? (?:the |all )?(?:\w+ )?(?:tests?|suites?|specs?|build|checks?)\b` +
		`|\bshould (?:now |all |still )?(?:pass|be green|succeed|compile)\b`)
	debugRe = lazyre.New(`\bdebug(?:ging)?\b|\bwhy\b[^.?!\n]{0,80}?\b(?:fail(?:s|ed|ing|ure)?|broke|breaks|broken|crash(?:es|ed|ing)?|errors?|panics?|hangs?)\b` +
		`|\binvestigat(?:e|es|ing|ion)\b|\broot[- ]cause\b|\btrac(?:e|ing)\b|\breproduc(?:e|es|ing)\b|\brepro\b` +
		`|\bfigure out (?:what|why|how|where|which)\b|\bwhat(?:'s| is) (?:going on|wrong|happening|causing)\b|\bdiagnos(?:e|ing|is)\b`)
	negRe       = lazyre.New(`(?:\bno need to|\bdon'?t|\bdo not|\bnot|\bnever|\bwithout|\bskip(?:ping)?|\bwon'?t|\bno longer|\binstead of|\brather than)(?:\W+\w+){0,2}\W*$`)
	debugNounRe = lazyre.New(`^ (?:build|mode|flag|logs?|level|output|prints?|statements?|symbols|info|config|endpoint|server|port|binary)\b`)
)

func (s *Snapshot) InferMode() (m engine.Mode) {
	if s == nil || len(s.Assistant) == 0 || len(s.textAge) > 0 && s.textAge[0] > 1 {
		return engine.ModeAuto
	}
	defer func() {
		if recover() != nil {
			m = engine.ModeAuto
		}
	}()
	return inferMode(s.Assistant[0])
}

func inferMode(text string) engine.Mode {
	if len(text) > intentChars {
		text = text[len(text)-intentChars:]
	}
	t := strings.ToLower(text)
	v := cue(t, verifyRe, nil)
	d := cue(t, debugRe, debugNounRe)
	switch {
	case v && !d:
		return engine.ModeVerify
	case d && !v:
		return engine.ModeError
	}
	return engine.ModeAuto
}

func cue(t string, re, noun *lazyre.Regexp) bool {
	for _, m := range re.FindAllStringIndex(t, -1) {
		if pre := t[max(0, m[0]-30):m[0]]; negRe.MatchString(pre) && !strings.Contains(pre, "not sure") {
			continue
		}
		if noun != nil && strings.HasPrefix(t[m[0]:], "debug") && noun.MatchString(t[m[1]:]) {
			continue
		}
		return true
	}
	return false
}
