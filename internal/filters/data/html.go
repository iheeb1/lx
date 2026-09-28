package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"html"
	"regexp"
	"strings"
)

var (
	htmlSniffRe = lazyre.New(`(?i)^(?:\x{feff})?\s*(?:<!doctype\s+html|<html[\s>]|<head[\s>]|<!--[\s\S]*?-->\s*<!doctype\s+html)`)
	htmlTitleRe = lazyre.New(`(?is)<title[^>]*>(.*?)</title\s*>`)
	htmlMainRe  = lazyre.New(`(?is)<main\b[^>]*>(.*)</main\s*>`)

	htmlDropEls = []string{"script", "style", "noscript", "template", "svg", "head", "iframe", "object",
		"canvas", "math", "select", "nav", "footer", "aside", "button", "dialog"}
	htmlDropRes = func() []*regexp.Regexp {
		res := []*regexp.Regexp{regexp.MustCompile(`(?s)<!--.*?-->`)}
		for _, el := range htmlDropEls {
			res = append(res, regexp.MustCompile(`(?is)<`+el+`\b[^>]*>.*?</`+el+`\s*>`))
		}
		return res
	}()
	htmlPreRe = lazyre.New(`(?is)<pre\b[^>]*>(.*?)</pre\s*>`)

	htmlBlockRe   = lazyre.New(`(?i)<(?:/?(?:p|div|br|hr|li|ul|ol|dl|dt|dd|tr|table|thead|tbody|section|article|header|main|blockquote|figure|figcaption|form|fieldset|details|summary|h[1-6]|option|label)\b[^>]*)>`)
	htmlHeadingRe = lazyre.New(`(?i)<h([1-6])\b[^>]*>`)
	htmlLiRe      = lazyre.New(`(?i)<li\b[^>]*>`)
	htmlCellRe    = lazyre.New(`(?i)</t[dh]\s*>`)
	htmlTagRe     = lazyre.New(`(?s)<[^>]*>`)
	spaceRunRe    = lazyre.New(`[ \t\x{a0}]+`)
	preMarkRe     = lazyre.New("^\x00pre(\\d+)\x00$")
)

func isHTML(body, contentType string) bool {
	if strings.Contains(strings.ToLower(contentType), "html") {
		return true
	}
	head := body
	if len(head) > 2048 {
		head = head[:2048]
	}
	return htmlSniffRe.MatchString(head)
}

func htmlText(doc string, maxTokens int) (title string, lines []string) {

	lower := strings.ToLower(doc)
	if strings.Contains(lower, "<title") {
		if m := htmlTitleRe.FindStringSubmatch(doc); m != nil {
			title = strings.Join(strings.Fields(html.UnescapeString(htmlTagRe.ReplaceAllString(m[1], ""))), " ")
		}
	}
	s := doc
	if strings.Contains(lower, "<main") {
		if m := htmlMainRe.FindStringSubmatch(doc); m != nil {
			s = m[1]
			lower = strings.ToLower(s)
		}
	}
	for k, re := range htmlDropRes {
		open := "<!--"
		if k > 0 {
			open = "<" + htmlDropEls[k-1]
		}
		if strings.Contains(lower, open) {
			s = re.ReplaceAllString(s, " ")
			lower = strings.ToLower(s)
		}
	}
	var pres [][]string
	has := func(tag string) bool { return strings.Contains(lower, tag) }
	if has("<pre") {
		s = htmlPreRe.ReplaceAllStringFunc(s, func(p string) string {
			inner := htmlPreRe.FindStringSubmatch(p)[1]
			text := html.UnescapeString(htmlTagRe.ReplaceAllString(inner, ""))
			var pl []string
			for _, ln := range strings.Split(strings.Trim(text, "\n"), "\n") {
				pl = append(pl, strings.TrimRight(ln, " \t"))
			}
			pres = append(pres, pl)
			return fmt.Sprintf("\n\x00pre%d\x00\n", len(pres)-1)
		})
	}
	if has("<h") {
		s = htmlHeadingRe.ReplaceAllStringFunc(s, func(t string) string {
			n := int(t[2] - '0')
			if n < 1 || n > 6 {
				n = 1
			}
			return "\n" + strings.Repeat("#", n) + " "
		})
	}
	if has("<li") {
		s = htmlLiRe.ReplaceAllString(s, "\n- ")
	}
	if has("</t") {
		s = htmlCellRe.ReplaceAllString(s, " | ")
	}
	s = htmlBlockRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, "")

	var all []string
	for _, ln := range strings.Split(s, "\n") {
		if m := preMarkRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			var k int
			fmt.Sscan(m[1], &k)
			if k < len(pres) {
				all = append(all, pres[k]...)
			}
			continue
		}
		ln = strings.TrimSpace(spaceRunRe.ReplaceAllString(html.UnescapeString(ln), " "))
		ln = strings.TrimSuffix(ln, " |")
		if ln == "" || ln == "-" || ln == "|" || strings.Trim(ln, "#- ") == "" {
			continue
		}
		if len(all) > 0 && all[len(all)-1] == ln {
			continue
		}
		all = append(all, ln)
	}
	used := 0
	for i, ln := range all {
		c := countTokens(ln) + 1
		if used+c > maxTokens {
			lines = append(lines, all[:i]...)
			lines = append(lines, fmt.Sprintf("[lx: … +%s of page text not shown]", pluralInt(len(all)-i, "more line", "more lines")))
			return title, lines
		}
		used += c
	}
	return title, all
}
