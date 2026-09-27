package runner

import (
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/textutil"
)

// maxPromptLine is the longest unterminated line (in characters, after
// escape codes are removed) that can count as a prompt.
const maxPromptLine = 200

// promptWindow is how many trailing bytes of the capture the detector reads:
// enough for a 200-character prompt wrapped in color codes.
const promptWindow = 4096

// promptRe matches the end of a line that asks the user something: a
// trailing '?', ':' or '>', optionally followed by a bracketed choice or
// default ("Ok to proceed? (y) ", "package name: (app) ", "overwrite?
// (y/n [n]) "); a [y/n]-style choice; a password request; or "press
// enter/any key".
var promptRe = lazyre.New(`(?i)(?:[?:>]\s*(?:[\[(][^\n]{0,40}[\])]\s*)?|[\[(]\s*y(?:es)?\s*/\s*no?\s*[\])]\s*|password:?\s*|\b(?:press|hit)\s+(?:enter|return|any key)\b[^\n]{0,40})$`)

// PromptLine reports whether tail (the end of a command's output) ends in an
// unterminated line that looks like a prompt, and returns that line as the
// user would see it (escape codes removed, carriage-return overwrites
// applied). whole says tail is the entire output, not just its end.
func PromptLine(tail string) (string, bool) {
	return promptLine(tail, true)
}

func promptLine(tail string, whole bool) (string, bool) {
	i := strings.LastIndexByte(tail, '\n')
	if i < 0 && !whole {
		return "", false // the line started before the window: too long
	}
	line := tail[i+1:]
	line = strings.TrimRight(line, "\r")
	if j := strings.LastIndexByte(line, '\r'); j >= 0 {
		line = line[j+1:] // the terminal shows what was written last
	}
	line = textutil.StripANSI(line)
	if strings.TrimSpace(line) == "" || utf8.RuneCountInString(line) > maxPromptLine {
		return "", false
	}
	if strings.ContainsFunc(line, func(r rune) bool { return r < 0x20 && r != '\t' }) {
		return "", false // binary noise, not a question
	}
	if !promptRe.MatchString(line) {
		return "", false
	}
	return line, true
}
