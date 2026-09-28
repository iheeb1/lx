package git

import (
	"fmt"
	"path"
	"strings"

	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	jsonKeyRe     = lazyre.New(`^([ +-])\s*"([^"]+)": \{$`)
	jsonNameRe    = lazyre.New(`^([ +-])\s*"name": "([^"]+)",?$`)
	jsonVersionRe = lazyre.New(`^([ +-])\s*"version": "([^"]+)",?$`)

	tomlNameRe    = lazyre.New(`^([ +-])name = "([^"]+)"$`)
	tomlVersionRe = lazyre.New(`^([ +-])version = "([^"]+)"$`)

	yarnKeyRe     = lazyre.New(`^([ +-])([^\s#].*):$`)
	yarnVersionRe = lazyre.New(`^([ +-])  version:? "?([^"\s]+)"?$`)

	pnpmKeyRe = lazyre.New(`^([+-])  '?/?((?:@[^/@\s']+/)?[^/@\s']+)[@/](\d[^:'(\s]*)[^:]*'?:$`)

	goSumRe = lazyre.New(`^([+-])(\S+) (v\S+?)(?:/go\.mod)? h1:\S+$`)
)

var jsonSkipKeys = map[string]bool{
	"dependencies": true, "devDependencies": true, "peerDependencies": true, "optionalDependencies": true,
	"peerDependenciesMeta": true, "requires": true, "engines": true, "bin": true, "funding": true,
	"packages": true, "": true, "scripts": true, "autoload": true, "require": true, "require-dev": true,
	"dist": true, "source": true, "support": true, "extra": true, "license": true,
}

type verChanges struct {
	order    []string
	old, new map[string][]string
	unknown  int
}

func newVerChanges() *verChanges {
	return &verChanges{old: map[string][]string{}, new: map[string][]string{}}
}

func (v *verChanges) set(side byte, name, ver string) {
	if name == "" {
		v.unknown++
		return
	}
	_, o := v.old[name]
	_, n := v.new[name]
	if !o && !n {
		v.order = append(v.order, name)
	}
	if side == '-' {
		v.old[name] = append(v.old[name], ver)
	} else {
		v.new[name] = append(v.new[name], ver)
	}
}

type sideKeys struct{ old, new string }

func (k *sideKeys) set(side byte, name string) {
	if side != '+' {
		k.old = name
	}
	if side != '-' {
		k.new = name
	}
}

func (k *sideKeys) get(side byte) string {
	if side == '-' {
		return k.old
	}
	return k.new
}

func lockfileSummary(f *fileDiff) []string {
	v := newVerChanges()
	base := path.Base(f.name)
	for _, h := range f.hunks {
		var k sideKeys
		for _, ln := range h.body {
			if ln == "" {
				continue
			}
			side := ln[0]
			switch base {
			case "go.sum", "go.work.sum":
				if m := goSumRe.FindStringSubmatch(ln); m != nil {
					v.set(m[1][0], m[2], m[3])
				}
			case "package-lock.json", "npm-shrinkwrap.json", "composer.lock":
				if m := jsonKeyRe.FindStringSubmatch(ln); m != nil {
					name := m[2]
					if i := strings.LastIndex(name, "node_modules/"); i >= 0 {
						name = name[i+len("node_modules/"):]
					}
					if jsonSkipKeys[m[2]] {
						name = ""
					}
					k.set(side, name)
				} else if m := jsonNameRe.FindStringSubmatch(ln); m != nil && base == "composer.lock" {
					k.set(side, m[2])
				} else if m := jsonVersionRe.FindStringSubmatch(ln); m != nil && side != ' ' {
					v.set(side, k.get(side), m[2])
				}
			case "Cargo.lock", "poetry.lock", "uv.lock":
				if m := tomlNameRe.FindStringSubmatch(ln); m != nil {
					k.set(side, m[2])
				} else if strings.HasPrefix(ln[1:], "[") {
					k.set(side, "")
				} else if m := tomlVersionRe.FindStringSubmatch(ln); m != nil && side != ' ' {
					v.set(side, k.get(side), m[2])
				}
			case "yarn.lock":
				if m := yarnKeyRe.FindStringSubmatch(ln); m != nil {
					k.set(side, yarnName(m[2]))
				} else if m := yarnVersionRe.FindStringSubmatch(ln); m != nil && side != ' ' {
					v.set(side, k.get(side), m[2])
				}
			case "pnpm-lock.yaml":
				if m := pnpmKeyRe.FindStringSubmatch(ln); m != nil {
					v.set(side, m[2], m[3])
				}
			}
		}
	}
	head := fmt.Sprintf("[lockfile: %s lines in %s not shown", countDesc(f), plural(len(f.hunks), "hunk"))
	changed, added, removed := v.lists()
	if len(changed)+len(added)+len(removed) == 0 {
		return []string{head + "]"}
	}
	out := []string{head + "; package versions:"}
	for _, l := range []struct {
		label string
		items []string
	}{{"changed", changed}, {"added", added}, {"removed", removed}} {
		if len(l.items) == 0 {
			continue
		}
		items := capItems(l.items, 40)
		items[0] = fmt.Sprintf("%s (%d): %s", l.label, len(l.items), items[0])
		out = append(out, wrapList("  ", items, statWidth)...)
	}
	if v.unknown > 0 {
		out = append(out, fmt.Sprintf("  (+%s outside the shown context)", plural(v.unknown, "version line")))
	}
	out[len(out)-1] += "]"
	return out
}

func (v *verChanges) lists() (changed, added, removed []string) {
	for _, name := range v.order {
		oldV, newV := uniq(v.old[name]), uniq(v.new[name])
		gone, came := minus(oldV, newV), minus(newV, oldV)
		if len(gone) == 1 && len(came) == 1 {
			changed = append(changed, name+" "+gone[0]+"→"+came[0])
			continue
		}
		for _, x := range came {
			added = append(added, name+"@"+x)
		}
		for _, x := range gone {
			removed = append(removed, name+"@"+x)
		}
	}
	return
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func minus(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, y := range b {
		in[y] = true
	}
	var out []string
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

func yarnName(key string) string {
	first, _, _ := strings.Cut(key, ",")
	first = strings.Trim(strings.TrimSpace(first), `"`)
	if i := strings.IndexByte(first[min(1, len(first)):], '@'); i >= 0 {
		first = first[:i+1]
	}
	return first
}

func plural(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}
