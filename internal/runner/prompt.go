package runner

import (
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/textutil"
)

const maxPromptLine = 200

const promptWindow = 4096

var promptRe = lazyre.New(`(?i)(?:[?:>]\s*(?:[\[(][^\n]{0,40}[\])]\s*)?|[\[(]\s*y(?:es)?\s*/\s*no?\s*[\])]\s*|password:?\s*|\b(?:press|hit)\s+(?:enter|return|any key)\b[^\n]{0,40})$`)

func PromptLine(tail string) (string, bool) {
	return promptLine(tail, true)
}

func promptLine(tail string, whole bool) (string, bool) {
	i := strings.LastIndexByte(tail, '\n')
	if i < 0 && !whole {
		return "", false
	}
	line := tail[i+1:]
	line = strings.TrimRight(line, "\r")
	if j := strings.LastIndexByte(line, '\r'); j >= 0 {
		line = line[j+1:]
	}
	line = textutil.StripANSI(line)
	if strings.TrimSpace(line) == "" || utf8.RuneCountInString(line) > maxPromptLine {
		return "", false
	}
	if strings.ContainsFunc(line, func(r rune) bool { return r < 0x20 && r != '\t' }) {
		return "", false
	}
	if !promptRe.MatchString(line) {
		return "", false
	}
	return line, true
}
