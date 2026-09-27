package data

import (
	"encoding/json"
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"sort"
	"strings"
)

// lockfileKind names the lockfile format of a base name, or "".
func lockfileKind(base string) string {
	switch base {
	case "package-lock.json", "npm-shrinkwrap.json":
		return "npm"
	case "yarn.lock":
		return "yarn"
	case "pnpm-lock.yaml":
		return "pnpm"
	case "Cargo.lock":
		return "cargo"
	case "go.sum", "go.work.sum":
		return "gosum"
	case "poetry.lock":
		return "poetry"
	case "uv.lock":
		return "uv"
	case "Pipfile.lock":
		return "pipfile"
	case "Gemfile.lock":
		return "gemfile"
	case "composer.lock":
		return "composer"
	}
	return ""
}

type lockPkg struct{ name, version string }

// lockSummary is what a lockfile parser extracts.
type lockSummary struct {
	format  string // "npm, lockfileVersion 3"
	pkgs    []lockPkg
	direct  []lockPkg // direct dependencies, when the format records them
	grepFor string    // grep command template, with <name> for the package
	noun    string    // what is counted ("package" when empty)
}

// maxLockList: the package list is shown only when it costs at most this.
const maxLockList = 1200

// summarizeLockfile renders a one-line summary of a lockfile with its
// package count, the direct dependencies when the format records them, and
// the full package list when it is short. It returns ok=false when the
// content does not parse as the format its name says.
func summarizeLockfile(kind, path, out string) (string, bool) {
	var (
		s  lockSummary
		ok bool
	)
	switch kind {
	case "npm":
		s, ok = parseNpmLock(out)
	case "yarn":
		s, ok = parseYarnLock(out)
	case "pnpm":
		s, ok = parsePnpmLock(out)
	case "cargo", "poetry", "uv":
		s, ok = parseTomlPackages(kind, out)
	case "gosum":
		s, ok = parseGoSum(out)
	case "pipfile":
		s, ok = parsePipfileLock(out)
	case "gemfile":
		s, ok = parseGemfileLock(out)
	case "composer":
		s, ok = parseComposerLock(out)
	}
	if !ok || len(s.pkgs) == 0 {
		return "", false
	}
	// The viewer's own messages ("cat: yarn.lock: Input/output error")
	// come first, verbatim.
	var b strings.Builder
	for _, ln := range strings.Split(out, "\n") {
		if fileDiagRe.MatchString(ln) {
			b.WriteString(ln + "\n")
		}
	}
	noun := s.noun
	if noun == "" {
		noun = "package"
	}
	fmt.Fprintf(&b, "[lx: lockfile %s (%s): %s, %s — content not shown; find one: %s]",
		path, s.format, pluralInt(len(s.pkgs), noun, noun+"s"), humanBytes(len(out)),
		strings.ReplaceAll(s.grepFor, "FILE", shellQuote(path)))
	if len(s.direct) > 0 {
		b.WriteString("\ndirect dependencies (locked versions): " + joinPkgs(s.direct))
	}
	if list := joinPkgs(s.pkgs); countTokens(list) <= maxLockList {
		b.WriteString("\n" + noun + "s: " + list)
	}
	return b.String(), true
}

func pluralInt(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return commaInt(n) + " " + many
}

func joinPkgs(ps []lockPkg) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.name
		if p.version != "" {
			parts[i] += " " + p.version
		}
	}
	return strings.Join(parts, ", ")
}

func parseNpmLock(out string) (lockSummary, bool) {
	var doc struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version         string            `json:"version"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		} `json:"packages"`
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.LockfileVersion == 0 {
		return lockSummary{}, false
	}
	s := lockSummary{
		format:  fmt.Sprintf("npm, lockfileVersion %d", doc.LockfileVersion),
		grepFor: `grep -n -A2 '"node_modules/<name>"' FILE`,
	}
	if len(doc.Packages) > 0 {
		keys := make([]string, 0, len(doc.Packages))
		for k := range doc.Packages {
			if k != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			name := k[strings.LastIndex(k, "node_modules/")+len("node_modules/"):]
			if !strings.Contains(k, "node_modules/") {
				name = k // workspace package
			}
			s.pkgs = append(s.pkgs, lockPkg{name, doc.Packages[k].Version})
		}
		root := doc.Packages[""]
		var direct []string
		for n := range root.Dependencies {
			direct = append(direct, n)
		}
		for n := range root.DevDependencies {
			direct = append(direct, n)
		}
		sort.Strings(direct)
		for _, n := range direct {
			s.direct = append(s.direct, lockPkg{n, doc.Packages["node_modules/"+n].Version})
		}
		return s, true
	}
	// lockfileVersion 1: nested "dependencies" objects.
	var walk func(deps map[string]json.RawMessage, depth int)
	walk = func(deps map[string]json.RawMessage, depth int) {
		if depth > 64 {
			return
		}
		names := make([]string, 0, len(deps))
		for n := range deps {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			var d struct {
				Version      string                     `json:"version"`
				Dependencies map[string]json.RawMessage `json:"dependencies"`
			}
			if json.Unmarshal(deps[n], &d) != nil {
				continue
			}
			s.pkgs = append(s.pkgs, lockPkg{n, d.Version})
			walk(d.Dependencies, depth+1)
		}
	}
	walk(doc.Dependencies, 0)
	return s, true
}

var yarnVersionRe = lazyre.New(`^  version:? "?([^"\s]+)"?$`)

func parseYarnLock(out string) (lockSummary, bool) {
	s := lockSummary{format: "yarn v1", grepFor: `grep -n -A2 '^"\?<name>@' FILE`}
	// yarn v1 files start with "# yarn lockfile v1", berry files have a
	// __metadata entry; anything else is not taken for a lockfile.
	if !strings.Contains(out, "# yarn lockfile v1") && !strings.Contains(out, "\n__metadata:") && !strings.HasPrefix(out, "__metadata:") {
		return s, false
	}
	var cur *lockPkg
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case ln == "__metadata:":
			s.format = "yarn berry"
			cur = nil
		case ln != "" && ln[0] != ' ' && ln[0] != '#' && strings.HasSuffix(ln, ":"):
			spec := strings.Trim(strings.SplitN(strings.TrimSuffix(ln, ":"), ", ", 2)[0], `"`)
			name := spec
			if at := strings.IndexByte(spec[min(1, len(spec)):], '@'); at >= 0 {
				name = spec[:at+1]
			}
			s.pkgs = append(s.pkgs, lockPkg{name: name})
			cur = &s.pkgs[len(s.pkgs)-1]
		case ln != "" && ln[0] != ' ':
			cur = nil
		case cur != nil:
			if m := yarnVersionRe.FindStringSubmatch(ln); m != nil {
				cur.version = m[1]
			}
		}
	}
	return s, len(s.pkgs) > 0
}

var (
	pnpmVersionRe = lazyre.New(`^lockfileVersion: '?([\d.]+)'?`)
	pnpmPkgRe     = lazyre.New(`^  '?/?((?:@[^@/\s']+/)?[^@\s']+)@([^:'(\s]+)[^:]*'?:$`)
)

func parsePnpmLock(out string) (lockSummary, bool) {
	s := lockSummary{format: "pnpm", grepFor: `grep -n '<name>@' FILE`}
	section, importer := "", ""
	var dep *lockPkg
	for _, ln := range strings.Split(out, "\n") {
		if m := pnpmVersionRe.FindStringSubmatch(ln); m != nil {
			s.format = "pnpm, lockfileVersion " + m[1]
			continue
		}
		if ln != "" && ln[0] != ' ' {
			section = strings.TrimSuffix(ln, ":")
			continue
		}
		switch section {
		case "packages":
			if m := pnpmPkgRe.FindStringSubmatch(ln); m != nil {
				s.pkgs = append(s.pkgs, lockPkg{m[1], m[2]})
			}
		case "importers":
			// The root importer's direct dependencies:
			//   .:  /  dependencies:  /  'name':  /  version: 1.2.3
			switch ind := len(ln) - len(strings.TrimLeft(ln, " ")); {
			case ind == 2:
				importer, dep = strings.TrimSuffix(strings.TrimSpace(ln), ":"), nil
			case ind == 6 && importer == "." && strings.HasSuffix(ln, ":"):
				s.direct = append(s.direct, lockPkg{name: strings.Trim(strings.TrimSuffix(strings.TrimSpace(ln), ":"), `'"`)})
				dep = &s.direct[len(s.direct)-1]
			case ind == 8 && dep != nil && strings.HasPrefix(strings.TrimSpace(ln), "version: "):
				v := strings.TrimPrefix(strings.TrimSpace(ln), "version: ")
				if i := strings.IndexByte(v, '('); i > 0 {
					v = v[:i] // peer-dependency suffix
				}
				dep.version = strings.Trim(v, `'"`)
			}
		}
	}
	return s, strings.HasPrefix(s.format, "pnpm, ") && len(s.pkgs) > 0
}

var tomlKVRe = lazyre.New(`^(name|version) = "([^"]*)"$`)

// parseTomlPackages reads Cargo.lock, poetry.lock and uv.lock: one
// [[package]] table per locked package.
func parseTomlPackages(kind, out string) (lockSummary, bool) {
	s := lockSummary{format: map[string]string{"cargo": "Cargo", "poetry": "Poetry", "uv": "uv"}[kind],
		grepFor: `grep -n -A1 'name = "<name>"' FILE`}
	var cur *lockPkg
	for _, ln := range strings.Split(out, "\n") {
		if ln == "[[package]]" {
			s.pkgs = append(s.pkgs, lockPkg{})
			cur = &s.pkgs[len(s.pkgs)-1]
			continue
		}
		if strings.HasPrefix(ln, "[") {
			cur = nil
			continue
		}
		if cur == nil {
			continue
		}
		if m := tomlKVRe.FindStringSubmatch(ln); m != nil {
			if m[1] == "name" && cur.name == "" {
				cur.name = m[2]
			} else if m[1] == "version" && cur.version == "" {
				cur.version = m[2]
			}
		}
	}
	for _, p := range s.pkgs {
		if p.name == "" {
			return s, false
		}
	}
	return s, len(s.pkgs) > 0
}

var goSumRe = lazyre.New(`^(\S+) (v[^/\s]+)(/go\.mod)? h1:\S+$`)

func parseGoSum(out string) (lockSummary, bool) {
	s := lockSummary{format: "Go checksums", grepFor: `grep '<name> ' FILE`, noun: "module version"}
	seen := map[string]bool{}
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		m := goSumRe.FindStringSubmatch(ln)
		if m == nil {
			return s, false
		}
		k := m[1] + " " + m[2]
		if !seen[k] {
			seen[k] = true
			s.pkgs = append(s.pkgs, lockPkg{m[1], m[2]})
		}
	}
	return s, len(s.pkgs) > 0
}

func parsePipfileLock(out string) (lockSummary, bool) {
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(out), &doc) != nil || doc["_meta"] == nil {
		return lockSummary{}, false
	}
	s := lockSummary{format: "Pipfile.lock", grepFor: `grep -n -A2 '"<name>": {' FILE`}
	for _, sec := range []string{"default", "develop"} {
		var pk map[string]struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(doc[sec], &pk) != nil {
			continue
		}
		names := make([]string, 0, len(pk))
		for n := range pk {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s.pkgs = append(s.pkgs, lockPkg{n, strings.TrimPrefix(pk[n].Version, "==")})
		}
	}
	return s, len(s.pkgs) > 0
}

var (
	gemSpecRe   = lazyre.New(`^    (\S+) \(([^)]+)\)$`)
	gemSourceRe = lazyre.New(`(?m)^(?:GEM|PATH|GIT)$`)
)

func parseGemfileLock(out string) (lockSummary, bool) {
	s := lockSummary{format: "Bundler", grepFor: `grep -n '<name> (' FILE`}
	// Bundler lockfiles list gems under a GEM (or PATH/GIT) source's
	// "  specs:" line.
	if !strings.Contains(out, "\n  specs:\n") || !gemSourceRe.MatchString(out) {
		return s, false
	}
	for _, ln := range strings.Split(out, "\n") {
		if m := gemSpecRe.FindStringSubmatch(ln); m != nil {
			s.pkgs = append(s.pkgs, lockPkg{m[1], m[2]})
		}
	}
	return s, len(s.pkgs) > 0
}

func parseComposerLock(out string) (lockSummary, bool) {
	var doc struct {
		Packages    []struct{ Name, Version string } `json:"packages"`
		PackagesDev []struct{ Name, Version string } `json:"packages-dev"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil {
		return lockSummary{}, false
	}
	s := lockSummary{format: "Composer", grepFor: `grep -n -A1 '"name": "<name>"' FILE`}
	for _, p := range append(doc.Packages, doc.PackagesDev...) {
		s.pkgs = append(s.pkgs, lockPkg{p.Name, p.Version})
	}
	return s, len(s.pkgs) > 0
}
