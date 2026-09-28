// Package lazyre compiles regular expressions on first use.
package lazyre

import (
	"regexp"
	"sync"
)

type Regexp struct {
	expr string
	once sync.Once
	re   *regexp.Regexp
}

var (
	allMu sync.Mutex
	all   []*Regexp
)

func New(expr string) *Regexp {
	r := &Regexp{expr: expr}
	allMu.Lock()
	all = append(all, r)
	allMu.Unlock()
	return r
}

func CompileAll() (bad []string) {
	allMu.Lock()
	list := append([]*Regexp(nil), all...)
	allMu.Unlock()
	for _, r := range list {
		func() {
			defer func() {
				if recover() != nil {
					bad = append(bad, r.expr)
				}
			}()
			r.Get()
		}()
	}
	return bad
}

func Count() int {
	allMu.Lock()
	defer allMu.Unlock()
	return len(all)
}

func (r *Regexp) Get() *regexp.Regexp {
	r.once.Do(func() { r.re = regexp.MustCompile(r.expr) })
	return r.re
}

func (r *Regexp) String() string                         { return r.expr }
func (r *Regexp) MatchString(s string) bool              { return r.Get().MatchString(s) }
func (r *Regexp) FindString(s string) string             { return r.Get().FindString(s) }
func (r *Regexp) FindStringIndex(s string) []int         { return r.Get().FindStringIndex(s) }
func (r *Regexp) FindStringSubmatch(s string) []string   { return r.Get().FindStringSubmatch(s) }
func (r *Regexp) FindStringSubmatchIndex(s string) []int { return r.Get().FindStringSubmatchIndex(s) }
func (r *Regexp) FindAllString(s string, n int) []string {
	return r.Get().FindAllString(s, n)
}
func (r *Regexp) FindAllStringIndex(s string, n int) [][]int {
	return r.Get().FindAllStringIndex(s, n)
}
func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.Get().FindAllStringSubmatch(s, n)
}
func (r *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return r.Get().FindAllStringSubmatchIndex(s, n)
}
func (r *Regexp) ReplaceAllString(src, repl string) string {
	return r.Get().ReplaceAllString(src, repl)
}
func (r *Regexp) ReplaceAllLiteralString(src, repl string) string {
	return r.Get().ReplaceAllLiteralString(src, repl)
}
func (r *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	return r.Get().ReplaceAllStringFunc(src, repl)
}
func (r *Regexp) Split(s string, n int) []string { return r.Get().Split(s, n) }
func (r *Regexp) SubexpNames() []string          { return r.Get().SubexpNames() }
func (r *Regexp) SubexpIndex(name string) int    { return r.Get().SubexpIndex(name) }
func (r *Regexp) NumSubexp() int                 { return r.Get().NumSubexp() }
