package engine

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ShortenLine caps one line at max runes, keeping the start and the end
// (where file:line suffixes and verdicts usually are) and saying how much
// was cut. Error lines get 3x the room.
func ShortenLine(line string, max int) string {
	// Length first: classifying is far more expensive than counting runes,
	// and short lines never need it.
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

// Relativize rewrites absolute paths under the working directory to
// relative ones and the home directory to ~. Paths stay valid because lx
// runs in the agent's cwd.
func Relativize(c *Context, s string) string {
	if c.Cwd != "" && c.Cwd != "/" {
		cwd := strings.TrimRight(c.Cwd, "/") + "/"
		// Some tools resolve /tmp to /private/tmp on macOS.
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

// replacePath replaces old with repl except inside URLs (file:///x/y must
// stay a valid URL) and where old is only the tail of a longer path.
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

// CollapseRuns replaces runs of identical consecutive lines with one copy
// plus a count, and runs of blank lines with a single blank line.
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

var progressRe = regexp.MustCompile(`^\s*\d{1,3}(?:\.\d+)?%\s*(?:[|\[]|$)` +
	`|\[[=#>\-. ]{8,}\]` +
	`|[█▓▒░■□━▏▎▍▌▋▊▉]{4,}` +
	`|\d+(?:\.\d+)?\s?[KMG]i?B\s*/\s*\d+(?:\.\d+)?\s?[KMG]i?B` +
	`|^\s*[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]\s` +
	`|^(?:remote: )?(?:Enumerating|Counting|Compressing|Receiving|Resolving|Writing) objects:` +
	`|^(?:remote: )?Resolving deltas:|^Updating files:` +
	`|^Progress: resolved \d+` +
	`|^[0-9a-f]{12}: (?:Pulling fs layer|Waiting|Downloading|Verifying Checksum|Download complete|Extracting|Pull complete|Already exists)`)

// IsProgress reports whether a line is a progress bar / transfer meter /
// spinner frame that carries no information once the command has finished.
func IsProgress(line string) bool {
	return progressRe.MatchString(line) && !IsError(line)
}

// DropProgress removes progress lines, returning the survivors and the count
// removed.
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

// Plural formats "1 file" / "3 files".
func Plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
