package engine

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"
	"unicode/utf8"
)

func ShortenLine(line string, max int) string {
	if len(line) <= max {
		return line
	}
	n := utf8.RuneCountInString(line)
	if n <= max {
		return line
	}
	if IsError(line) {
		max *= 3
		if n <= max {
			return line
		}
	}
	r := []rune(line)
	headN := max * 3 / 4
	tailN := max / 5
	return fmt.Sprintf("%s …[+%d chars]… %s", string(r[:headN]), n-headN-tailN, string(r[n-tailN:]))
}

func Relativize(c *Context, s string) string {
	if c.Cwd != "" && c.Cwd != "/" {
		cwd := strings.TrimRight(c.Cwd, "/") + "/"

		if strings.HasPrefix(cwd, "/tmp/") || strings.HasPrefix(cwd, "/var/") {
			s = replacePath(s, "/private"+cwd, "")
		}
		s = replacePath(s, cwd, "")
	}
	if c.Home != "" && c.Home != "/" {
		s = replacePath(s, strings.TrimRight(c.Home, "/")+"/", "~/")
	}
	return s
}

func replacePath(s, old, repl string) string {
	if !strings.Contains(s, old) {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, old)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		inURL := i >= 2 && s[i-2:i] == ":/" || i >= 3 && s[i-3:i] == "://"
		midPath := i > 0 && (isWordByte(s[i-1]) || s[i-1] == '.' || s[i-1] == '-')
		b.WriteString(s[:i])
		if inURL || midPath {
			b.WriteString(old)
		} else {
			b.WriteString(repl)
		}
		s = s[i+len(old):]
	}
}

func CollapseRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		n := j - i
		switch {
		case strings.TrimSpace(lines[i]) == "":
			out = append(out, "")
		case n == 1:
			out = append(out, lines[i])
		default:
			out = append(out, fmt.Sprintf("%s [×%d]", lines[i], n))
		}
		i = j
	}
	return out
}

var progressRe = lazyre.New(`^\s*\d{1,3}(?:\.\d+)?%\s*(?:[|\[]|$)` +
	`|\[[=#>\-. ]{8,}\]` +
	`|[█▓▒░■□━▏▎▍▌▋▊▉]{4,}` +
	`|\d+(?:\.\d+)?\s?[KMG]i?B\s*/\s*\d+(?:\.\d+)?\s?[KMG]i?B` +
	`|^\s*[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]\s` +
	`|^(?:remote: )?(?:Enumerating|Counting|Compressing|Receiving|Resolving|Writing) objects:` +
	`|^(?:remote: )?Resolving deltas:|^Updating files:` +
	`|^Progress: resolved \d+` +
	`|^[0-9a-f]{12}: (?:Pulling fs layer|Waiting|Downloading|Verifying Checksum|Download complete|Extracting|Pull complete|Already exists)`)

func IsProgress(line string) bool {
	return progressRe.MatchString(line) && !IsError(line)
}

func DropProgress(lines []string) ([]string, int) {
	out := lines[:0:0]
	n := 0
	for _, ln := range lines {
		if IsProgress(ln) {
			n++
			continue
		}
		out = append(out, ln)
	}
	return out, n
}

func Plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
