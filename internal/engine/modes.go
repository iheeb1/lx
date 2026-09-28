package engine

import (
	"math"
	"strconv"

	"github.com/iheeb1/lx/internal/tokens"
)

type Mode uint8

const (
	ModeAuto Mode = iota

	ModeError

	ModeDebug

	ModeVerify

	ModeMinimal
)

const MinimalBudget = 2000

const ModeList = "auto, error, debug, verify or minimal"

var modeNames = [...]string{"auto", "error", "debug", "verify", "minimal"}

func (m Mode) String() string {
	if int(m) < len(modeNames) {
		return modeNames[m]
	}
	return "mode(" + strconv.Itoa(int(m)) + ")"
}

func ParseMode(s string) (Mode, bool) {
	for i, n := range modeNames {
		if s == n {
			return Mode(i), true
		}
	}
	return ModeAuto, false
}

func (m Mode) View(failed bool) Mode {
	if m == ModeVerify && failed {
		return ModeError
	}
	return m
}

func (m Mode) Budget(b int, failed bool) int {
	if b <= 0 {
		b = DefaultBudget
	}
	return m.View(failed).knobs().budget(b)
}

type modeKnobs struct {
	num, den  int
	budgetCap int

	errs errKeep

	boundary int

	noTemplates bool

	similarRun int

	receiptGate bool
}

const (
	modeErrContext   = 3
	modeBoundary     = 2
	debugSimilarRun  = 2 * minSimilarRun
	autoErrContext   = 1
	autoBoundary     = 1
	receiptIDExample = "1000000"
)

func (m Mode) knobs() modeKnobs {
	k := modeKnobs{num: 1, den: 1, errs: errKeep{context: autoErrContext}, boundary: autoBoundary, similarRun: minSimilarRun}
	switch m {
	case ModeError:
		k.num, k.den = 3, 2
		k.errs.context, k.boundary = modeErrContext, modeBoundary
	case ModeDebug:
		k.num = 2
		k.errs.context, k.boundary = modeErrContext, modeBoundary
		k.noTemplates, k.similarRun = true, debugSimilarRun
	case ModeVerify:
		k.den = 2
		k.errs.all = true
	case ModeMinimal:
		k.budgetCap, k.receiptGate = MinimalBudget, true
		k.errs.all = true
	}
	return k
}

func (k modeKnobs) budget(b int) int {
	if k.num != k.den {
		if k.num > k.den && b > math.MaxInt/k.num {
			b = math.MaxInt
		} else {
			b = b * k.num / k.den
		}
	}
	if k.budgetCap > 0 {
		b = min(b, k.budgetCap)
	}
	return max(b, 1)
}

func modeOf(c *Context) Mode {
	if c == nil {
		return ModeAuto
	}
	return c.Mode
}

func templateLogs(c *Context, lines []string) ([]string, bool) {
	if modeOf(c).knobs().noTemplates {
		return nil, false
	}
	return TemplateLogs(lines)
}

func modeNote(asked, view Mode) string {
	switch {
	case asked == ModeAuto:
		return ""
	case view != asked && view != ModeAuto:
		return "mode " + asked.String() + "→" + view.String()
	}
	return "mode " + asked.String()
}

func receiptTokens(r Result, out string, outTokens int) int {
	r.OutTokens, r.OutLines = outTokens, countLines(out)
	return tokens.Count(Receipt(r, receiptIDExample)) + 1
}
