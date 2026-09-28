package hook

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const readOnlyReason = "; read-only command, auto-approved as Claude Code does without lx (--readonly)"

const maxGlobMatches = 256

type roArg struct {
	val     string
	glob    bool
	special bool
}

func (a roArg) plain() bool { return !a.glob && !a.special }

func readOnly(argv []string, cwd, root string, extraDirs []string) (ok bool, why string) {
	c, why := newROCtx(cwd, root, extraDirs)
	if c == nil {
		return false, why
	}
	args := make([]roArg, len(argv))
	for i, v := range argv {
		args[i] = roArg{val: v, glob: strings.ContainsAny(v, "*?["), special: specialLiteral(v, c.zsh)}
	}
	return c.check(args)
}

func specialLiteral(v string, zsh bool) bool {
	if v == "" {
		return false
	}
	if v[0] == '~' || v[0] == '=' || strings.ContainsAny(v, "$`") ||
		strings.Contains(v, "=~") || strings.Contains(v, ":~") {
		return true
	}
	if zsh && (strings.ContainsAny(v, "^#") || zshBraceCCL(v) ||
		strings.ContainsAny(v, "*?[") && strings.Contains(v, "~")) {
		return true
	}
	return braceExpands(v)
}

func shellMayBeZsh() bool {
	for _, v := range []string{"CLAUDE_CODE_SHELL", "SHELL"} {
		if s := os.Getenv(v); s != "" {
			return filepath.Base(s) != "bash"
		}
	}
	return true
}

func zshBraceCCL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' || i > 0 && (s[i-1] == '@' || s[i-1] == '$') {
			continue
		}
		if j := strings.IndexByte(s[i:], '}'); j > 1 {
			return true
		}
	}
	return false
}

func (a *analysis) readOnlyParity(cwd, root string, extraDirs []string) (bool, string) {
	switch {
	case len(a.unsafe) > 0:
		return false, a.unsafe[0]
	case a.lx.broken:
		return false, "unterminated quote"
	case a.sc.syntaxErr:
		return false, "shell syntax error"
	case len(a.segs) == 0:
		return false, "empty command"
	}
	for _, l := range a.sc.lists {
		if l.bg {
			return false, "backgrounded command"
		}
	}
	c, why := newROCtx(cwd, root, extraDirs)
	if c == nil {
		return false, why
	}

	state := c.cwds
	for _, l := range a.sc.lists {
		if len(l.ops) != len(l.pipes)-1 {
			return false, "shell syntax error"
		}
		var succ, fail []string
		for k, p := range l.pipes {
			in := state
			if k > 0 {
				if in = fail; l.ops[k-1] == "&&" {
					in = succ
				}
			}
			s, f, ok, why := a.roPipeline(c, p, in)
			if !ok {
				return false, why
			}
			switch {
			case k == 0:
				succ, fail = s, f
			case l.ops[k-1] == "&&":
				succ, fail = s, union(fail, f)
			default:
				succ, fail = union(succ, s), f
			}
		}
		if state = union(succ, fail); len(state) > 8 {
			return false, "too many cd"
		}
	}
	return true, ""
}

func (a *analysis) roPipeline(c *roCtx, p pipeline, in []string) (succ, fail []string, ok bool, why string) {
	if len(in) == 0 {
		return nil, nil, true, ""
	}
	c.cwds = in
	for _, cmd := range p.cmds {
		s := a.bySimp[cmd]
		if a.permSegment(s).neutral {
			if s.argv[0] == "cd" {
				next, ok, why := c.cd(s.words[1])
				if !ok {
					return nil, nil, false, why
				}
				if len(p.cmds) == 1 {
					return next, in, true, ""
				}
			}
			continue
		}
		if s.cmdIdx != 0 || len(s.envNames) > 0 {
			return nil, nil, false, "environment assignment or wrapper"
		}
		if !redirsOK(s.simple) {
			return nil, nil, false, "redirection"
		}
		for _, w := range s.words {
			if w.expand {
				return nil, nil, false, "$ expansion"
			}
		}
		if ok, why := c.check(c.roArgs(s.words)); !ok {
			return nil, nil, false, why
		}
	}
	return in, in, true, ""
}

func union(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, x := range b {
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func (c *roCtx) roArgs(ws []token) []roArg {
	out := make([]roArg, len(ws))
	for i, w := range ws {
		g, sp := wordShape(w.text, c.zsh)
		out[i] = roArg{val: w.val, glob: g, special: sp}
	}
	return out
}

func wordShape(raw string, zsh bool) (glob, special bool) {
	tilde := false
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; c {
		case '\\':
			i++
		case '\'':
			j := strings.IndexByte(raw[i+1:], '\'')
			if j < 0 {
				return glob, true
			}
			i += j + 1
		case '"':
			for i++; i < len(raw) && raw[i] != '"'; i++ {
				if raw[i] == '\\' {
					i++
				}
			}
		case '$':
			if i+1 < len(raw) && raw[i+1] == '\'' {
				for i += 2; i < len(raw) && raw[i] != '\''; i++ {
					if raw[i] == '\\' {
						i++
					}
				}
			}
		case '*', '?', '[':
			glob = true
		case '{':
			if braceExpands(raw[i:]) ||
				zsh && strings.IndexByte(raw[i:], '}') > 1 && (i == 0 || raw[i-1] != '@') {
				special = true
			}
		case '~':
			if i == 0 || raw[i-1] == '=' || raw[i-1] == ':' {
				special = true
			} else {
				tilde = true
			}
		case '=':
			if i == 0 {
				special = true
			}
		case '^', '#':
			if zsh {
				special = true
			}
		}
	}
	if zsh && glob && tilde {
		special = true
	}
	return glob, special
}

func braceExpands(s string) bool {
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return false
	}
	j := strings.IndexByte(s[i:], '}')
	if j < 0 {
		return false
	}
	inner := s[i : i+j]
	return strings.Contains(inner, ",") || strings.Contains(inner, "..") || braceExpands(s[i+1:])
}

type roCtx struct {
	roots []string
	cwds  []string
	zsh   bool
}

func newROCtx(cwd, root string, extraDirs []string) (*roCtx, string) {
	if runtime.GOOS == "windows" {
		return nil, "read-only parity is not supported on Windows"
	}
	if cwd == "" || !filepath.IsAbs(cwd) {
		return nil, "no absolute working directory"
	}
	if root == "" {
		root = cwd
	}
	if !filepath.IsAbs(root) {
		return nil, "project directory is not absolute"
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "project directory does not exist"
	}
	homes := homeDirs()
	if tooBroad(rr, homes) {
		return nil, "project directory is / or your home directory"
	}
	c := &roCtx{roots: []string{rr}, zsh: shellMayBeZsh()}
	for _, d := range extraDirs {
		if !filepath.IsAbs(d) {
			continue
		}
		r, err := filepath.EvalSymlinks(d)
		if err != nil || tooBroad(r, homes) {
			continue
		}
		c.roots = append(c.roots, r)
	}
	cr, err := filepath.EvalSymlinks(cwd)
	if err != nil || !c.inside(cr) {
		return nil, "working directory is outside the project"
	}
	c.cwds = []string{cr}
	return c, ""
}

func homeDirs() []string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" || !filepath.IsAbs(h) {
		return nil
	}
	h = filepath.Clean(h)
	out := []string{h}
	if r, err := filepath.EvalSymlinks(h); err == nil && r != h {
		out = append(out, r)
	}
	return out
}

func tooBroad(dir string, homes []string) bool {
	if dir == "/" || filepath.Dir(dir) == dir {
		return true
	}
	fi, err := os.Stat(dir)
	for _, h := range homes {
		if dir == h || strings.HasPrefix(h, dir+"/") {
			return true
		}
		for a := h; err == nil; a = filepath.Dir(a) {
			if ai, aerr := os.Stat(a); aerr == nil && os.SameFile(fi, ai) {
				return true
			}
			if filepath.Dir(a) == a {
				break
			}
		}
	}
	return false
}

func (c *roCtx) inside(p string) bool {
	for _, r := range c.roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

func (c *roCtx) cd(w token) ([]string, bool, string) {
	if os.Getenv("CDPATH") != "" {
		return nil, false, "CDPATH is set"
	}
	g, sp := wordShape(w.text, c.zsh)
	if g || sp || w.expand {
		return nil, false, "cd with an expansion"
	}
	var next []string
	for _, cwd := range c.cwds {
		r, err := filepath.EvalSymlinks(cwd + "/" + w.val)
		if err != nil || !c.inside(r) {
			return nil, false, "cd leaves the project or its directory does not exist"
		}
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			return nil, false, "cd to something that is not a directory"
		}
		next = union(next, []string{r})
	}
	return next, true, ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func resolvePath(p string) (string, bool) {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, true
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", false
		}
	}
	dir, rest := filepath.Clean(p), ""
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest), true
		}
	}
}

func (c *roCtx) paths(ps []roArg) (bool, string) {
	for _, p := range ps {
		if ok, why := c.path(p); !ok {
			return false, why
		}
	}
	return true, ""
}

func (c *roCtx) path(a roArg) (bool, string) {
	v := a.val
	switch {
	case a.special:
		return false, "shell expansion in " + v
	case v == "":
		return false, "empty path"
	case strings.ContainsAny(v, "$`"):
		return false, "$ in a path"
	case v[0] == '~' || v[0] == '=':
		return false, v + " is expanded by the shell"
	}
	if a.glob {
		return c.globPath(v)
	}
	for _, cwd := range c.cwds {
		p := v
		if !filepath.IsAbs(p) {
			p = cwd + "/" + v
		}
		if r, ok := resolvePath(p); !ok || !c.inside(r) {
			return false, v + " is outside the project"
		}
	}
	return true, ""
}

func (c *roCtx) globPath(v string) (bool, string) {
	if strings.Contains(v, "**") || strings.ContainsAny(v, "[\\") {
		return false, "wildcard lx does not model in " + v
	}
	i := strings.IndexAny(v, "*?")
	if i < 0 {
		return false, "wildcard lx does not model in " + v
	}

	wild := 0
	for _, part := range strings.Split(v[strings.LastIndexByte(v[:i], '/')+1:], "/") {
		if part == "." || part == ".." {
			return false, "wildcard followed by . or .. in " + v
		}
		if strings.ContainsAny(part, "*?") {
			wild++
		}
	}
	if wild > 2 {
		return false, "too many wildcard levels in " + v
	}
	if dir := v[:strings.LastIndexByte(v[:i], '/')+1]; dir != "" {
		if ok, why := c.path(roArg{val: dir}); !ok {
			return false, why
		}
	}
	leading := !filepath.IsAbs(v) && (v[0] == '*' || v[0] == '?')
	for _, cwd := range c.cwds {

		pat := foldCase(v)
		if !filepath.IsAbs(v) {
			pat = escapeGlob(cwd) + "/" + pat
		}

		pat = strings.TrimRight(pat, "/")
		ms, err := filepath.Glob(pat)
		if err != nil {
			return false, "bad wildcard " + v
		}
		if len(ms) > maxGlobMatches {
			return false, v + " matches too many files to check"
		}
		for _, m := range ms {

			if leading && strings.HasPrefix(strings.TrimPrefix(m, cwd+"/"), "-") {
				return false, v + " matches a file name starting with -"
			}
			if r, ok := resolvePath(m); !ok || !c.inside(r) {
				return false, v + " matches a path outside the project"
			}
		}
	}
	return true, ""
}

func foldCase(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		ch := p[i]
		lo, up := ch|0x20, ch&^0x20
		if lo >= 'a' && lo <= 'z' {
			b.WriteByte('[')
			b.WriteByte(lo)
			b.WriteByte(up)
			b.WriteByte(']')
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func escapeGlob(s string) string {
	if !strings.ContainsAny(s, `*?[\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(`*?[\`, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (c *roCtx) check(args []roArg) (bool, string) {
	if len(args) == 0 {
		return false, "empty command"
	}
	name := args[0]
	if !name.plain() {
		return false, "command name is expanded by the shell"
	}
	rest := args[1:]
	switch name.val {
	case "git":
		return c.git(rest)
	case "ls":
		return c.ls(rest)
	case "tree":
		return c.tree(rest)
	case "du":
		return c.du(rest)
	case "find":
		return c.find(rest)
	case "grep", "egrep", "fgrep":
		return c.search(rest, grepSpec)
	case "rg":
		return c.search(rest, rgSpec)
	}
	return false, name.val + " is not in the read-only table"
}

func isOpt(v string) bool { return len(v) > 1 && v[0] == '-' }

var gitDiffDanger = []string{"--output", "--ext-diff", "--textconv", "--no-index", "--orderfile"}

func (c *roCtx) git(args []roArg) (bool, string) {
	for _, a := range args {
		if !a.plain() {
			return false, "unquoted wildcard or expansion in a git argument"
		}
	}
	i := 0
	for i < len(args) && (args[i].val == "--no-pager" || args[i].val == "-P") {
		i++
	}
	if i >= len(args) {
		return false, "git without a subcommand"
	}
	sub, rest := args[i].val, args[i+1:]
	switch sub {
	case "status":
		return true, ""
	case "diff", "log", "show":
		return c.gitRevs(rest, gitDiffDanger, "O")
	case "blame":
		return c.gitRevs(rest, []string{"--contents", "--ignore-revs-file"}, "S")
	case "branch":
		return gitBranch(rest)
	}
	if strings.HasPrefix(sub, "-") {
		return false, "git option " + sub + " before the subcommand"
	}
	return false, "git " + sub + " is not in the read-only table"
}

func (c *roCtx) gitRevs(args []roArg, longDanger []string, shortDanger string) (bool, string) {
	opts := true
	for _, a := range args {
		v := a.val
		if opts && v == "--" {
			opts = false
			continue
		}
		if opts && isOpt(v) {
			if strings.HasPrefix(v, "--") {
				name, _, _ := strings.Cut(v, "=")
				for _, d := range longDanger {
					if name == d || (len(name) > 2 && name != "--text" && strings.HasPrefix(d, name)) {
						return false, "git " + d
					}
				}
				continue
			}
			if strings.ContainsAny(v[1:], shortDanger) {
				return false, "git -" + shortDanger
			}
			continue
		}
		if ok, why := c.path(a); !ok {
			return false, why
		}
	}
	return true, ""
}

var (
	branchBool = roSet("-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "-l", "--list",
		"--show-current", "--no-color", "--no-column")
	branchOptValue = roSet("--merged", "--no-merged", "--contains", "--no-contains", "--points-at")
)

func gitBranch(args []roArg) (bool, string) {
	list, value, opts := false, false, true
	for _, a := range args {
		v := a.val
		if opts && v == "--" {
			opts, value = false, false
			continue
		}
		if opts && isOpt(v) {
			value = false
			if strings.HasPrefix(v, "--") {
				name, _, eq := strings.Cut(v, "=")
				switch {
				case branchBool[name] && !eq:
					list = list || name == "--list"
				case name == "--color" || name == "--column":
				case name == "--sort" && eq:
				case branchOptValue[name]:
					value = !eq
				default:
					return false, "git branch " + name
				}
				continue
			}
			for _, ch := range v[1:] {
				switch ch {
				case 'a', 'r', 'v':
				case 'l':
					list = true
				default:
					return false, "git branch -" + string(ch)
				}
			}
			continue
		}
		if value {
			value = false
			continue
		}
		if !list {
			return false, "git branch with an argument creates or changes a branch"
		}
	}
	return true, ""
}

var (
	lsBool = roSet("--all", "--almost-all", "--human-readable", "--classify", "--group-directories-first",
		"--reverse", "--recursive", "--directory", "--inode", "--size", "--dereference", "--full-time")
	lsValue = roSet("--sort", "--time", "--time-style")
)

func (c *roCtx) ls(args []roArg) (bool, string) {
	var ops []roArg
	recursive, deref, opts := false, false, true
	for _, a := range args {
		v := a.val
		if opts && v == "--" {
			opts = false
			continue
		}
		if !opts || !isOpt(v) {
			ops = append(ops, a)
			continue
		}
		if !a.plain() {
			return false, "unquoted wildcard in an ls flag"
		}
		if c.fileFlag(v) {
			return false, v + " is also a file name"
		}
		if strings.HasPrefix(v, "--") {
			name, _, eq := strings.Cut(v, "=")
			switch {
			case lsBool[name] && !eq:
			case name == "--color":
			case lsValue[name] && eq:
			default:
				return false, "ls " + name
			}
			recursive = recursive || name == "--recursive"
			deref = deref || name == "--dereference"
			continue
		}
		for _, ch := range v[1:] {
			if !isAlnum(byte(ch)) {
				return false, "ls " + v
			}
		}
		recursive = recursive || strings.ContainsRune(v, 'R')
		deref = deref || strings.ContainsRune(v, 'L')
	}
	if recursive && deref {
		return false, "recursive listing that follows symlinks"
	}
	return c.paths(ops)
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (c *roCtx) fileFlag(v string) bool {
	for _, cwd := range c.cwds {
		if _, err := os.Lstat(cwd + "/" + v); err == nil {
			return true
		}
	}
	return false
}

type flagSpec struct {
	short      string
	shortValue string
	digits     bool
	long       map[string]bool
	longValue  map[string]bool
	longOpt    map[string]bool

	nextWord bool
}

func (c *roCtx) parseFlags(args []roArg, sp *flagSpec, value func(flag string, v roArg) (bool, string)) ([]roArg, bool, string) {
	var ops []roArg
	opts := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		v := a.val
		if opts && v == "--" {
			opts = false
			continue
		}
		if !opts || !isOpt(v) {
			ops = append(ops, a)
			continue
		}
		if !a.plain() {
			return nil, false, "unquoted wildcard or expansion in flag " + v
		}
		if c.fileFlag(v) {
			return nil, false, v + " is also a file name"
		}
		if strings.HasPrefix(v, "--") {
			name, val, eq := strings.Cut(v, "=")
			switch {
			case sp.long[name] && !eq, sp.longOpt[name]:
				if ok, why := value(name, roArg{}); !ok {
					return nil, false, why
				}
			case sp.longValue[name]:
				arg := roArg{val: val}
				if !eq {
					if i+1 >= len(args) {
						return nil, false, name + " without a value"
					}
					i++
					arg = args[i]
				}
				if ok, why := value(name, arg); !ok {
					return nil, false, why
				}
			default:
				return nil, false, "flag " + name + " is not in the read-only table"
			}
			continue
		}
		for j := 1; j < len(v); j++ {
			ch := v[j]
			if sp.digits && ch >= '0' && ch <= '9' {
				continue
			}
			if strings.IndexByte(sp.shortValue, ch) >= 0 {
				if rest := v[j+1:]; sp.nextWord && rest != "" && !isDigits(rest) {
					return nil, false, "-" + string(ch) + " does not end its flag cluster " + v
				}
				arg := roArg{val: v[j+1:]}
				if arg.val == "" {
					if i+1 >= len(args) {
						return nil, false, "-" + string(ch) + " without a value"
					}
					i++
					arg = args[i]
				}
				if ok, why := value("-"+string(ch), arg); !ok {
					return nil, false, why
				}
				break
			}
			if strings.IndexByte(sp.short, ch) < 0 {
				return nil, false, "flag -" + string(ch) + " is not in the read-only table"
			}
		}
	}
	return ops, true, ""
}

func plainValue(flag string, v roArg) (bool, string) {
	if flag != "" && v.val != "" && !v.plain() {
		return false, "unquoted wildcard or expansion in the value of " + flag
	}
	return true, ""
}

var treeSpec = &flagSpec{
	short:      "adfxiqNQpugshDFvtcUrnCXJSA",
	shortValue: "LPIHT",
	long: roSet("--dirsfirst", "--filesfirst", "--noreport", "--prune", "--matchdirs", "--ignore-case",
		"--gitignore", "--du", "--si", "--inodes", "--device", "--metafirst", "--info", "--help", "--version"),
	longValue: roSet("--charset", "--filelimit", "--timefmt", "--sort"),
	nextWord:  true,
}

func (c *roCtx) tree(args []roArg) (bool, string) {
	ops, ok, why := c.parseFlags(args, treeSpec, plainValue)
	if !ok {
		return false, why
	}
	return c.paths(ops)
}

var duSpec = &flagSpec{
	short:      "abchkmsxHPlS0AgnrD",
	shortValue: "dBtI",
	long: roSet("--all", "--apparent-size", "--bytes", "--total", "--human-readable", "--si", "--summarize",
		"--one-file-system", "--inodes", "--count-links", "--separate-dirs", "--null", "--no-dereference",
		"--dereference-args"),
	longValue: roSet("--max-depth", "--block-size", "--threshold", "--time-style", "--exclude"),
	longOpt:   roSet("--time"),
}

func (c *roCtx) du(args []roArg) (bool, string) {
	ops, ok, why := c.parseFlags(args, duSpec, plainValue)
	if !ok {
		return false, why
	}
	return c.paths(ops)
}

var findDanger = roSet("-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf",
	"-fls", "-follow", "-files0-from")

func (c *roCtx) find(args []roArg) (bool, string) {
	i := 0

leading:
	for ; i < len(args); i++ {
		v := args[i].val
		switch {
		case isAny(v, "-H", "-P", "-E", "-X", "-d", "-s", "-x"),
			len(v) > 2 && v[:2] == "-O" && isDigits(v[2:]):
		case v == "--":
			i++
			break leading
		case isAny(v, "-L", "-D", "-f"):
			return false, "find " + v
		default:
			break leading
		}
	}
	var starts []roArg
	for ; i < len(args); i++ {
		if v := args[i].val; v != "" && strings.IndexByte("-(!,", v[0]) >= 0 {
			break
		}
		starts = append(starts, args[i])
	}
	if ok, why := c.paths(starts); !ok {
		return false, why
	}
	for ; i < len(args); i++ {
		a := args[i]
		if !a.plain() {
			return false, "unquoted wildcard or expansion in a find expression"
		}
		v := a.val
		if findDanger[v] {
			return false, "find " + v
		}

		fileArg := isAny(v, "-newer", "-anewer", "-cnewer", "-mnewer", "-Bnewer", "-samefile") ||
			len(v) == 8 && strings.HasPrefix(v, "-newer") && v[7] != 't'
		if fileArg && i+1 < len(args) {
			i++
			if ok, why := c.path(args[i]); !ok {
				return false, why
			}
		}
	}
	return true, ""
}

type searchSpec struct {
	flags     *flagSpec
	pathFlags map[string]bool
	patFlags  map[string]bool
	noPattern map[string]bool

	counts map[string]bool
}

var grepSpec = &searchSpec{
	flags: &flagSpec{

		short:      "EFGPiyvwxcLloqsbHhnTZzaUrIVuJOp",
		shortValue: "ABCdDefm",
		digits:     true,
		long: roSet("--extended-regexp", "--fixed-strings", "--basic-regexp", "--perl-regexp", "--ignore-case",
			"--no-ignore-case", "--invert-match", "--word-regexp", "--line-regexp", "--count",
			"--files-with-matches", "--files-without-match", "--only-matching", "--quiet", "--silent",
			"--no-messages", "--byte-offset", "--with-filename", "--no-filename", "--line-number",
			"--initial-tab", "--null", "--null-data", "--text", "--binary", "--recursive", "--line-buffered",
			"--no-group-separator", "--unix-byte-offsets", "--version", "--help"),
		longValue: roSet("--regexp", "--file", "--include", "--exclude", "--exclude-dir",
			"--after-context", "--before-context", "--max-count", "--label", "--binary-files", "--devices",
			"--directories", "--group-separator"),

		longOpt: roSet("--color", "--colour", "--context"),
	},
	pathFlags: roSet("-f", "--file"),
	patFlags:  roSet("-e", "--regexp", "-f", "--file"),
	counts:    roSet("-A", "-B", "-C", "-m", "--after-context", "--before-context", "--max-count"),
}

var rgSpec = &searchSpec{
	flags: &flagSpec{

		short:      "abcFHhIilNnoPpqSsUuVvwx0.",
		shortValue: "ABCeEfgjmMrtTd",
		long: roSet("--binary", "--block-buffered", "--byte-offset", "--case-sensitive", "--column",
			"--no-column", "--count", "--count-matches", "--crlf", "--no-crlf", "--files",
			"--files-with-matches", "--files-without-match", "--fixed-strings", "--no-fixed-strings",
			"--glob-case-insensitive", "--no-glob-case-insensitive", "--heading", "--no-heading", "--hidden",
			"--no-hidden", "--ignore-case", "--ignore-file-case-insensitive", "--include-zero",
			"--invert-match", "--json", "--no-json", "--line-buffered", "--line-number", "--no-line-number",
			"--line-regexp", "--max-columns-preview", "--mmap", "--no-mmap", "--multiline", "--no-multiline",
			"--multiline-dotall", "--no-config", "--no-ignore", "--no-ignore-dot", "--no-ignore-exclude",
			"--no-ignore-files", "--no-ignore-global", "--no-ignore-parent", "--no-ignore-vcs",
			"--no-ignore-messages", "--no-messages", "--no-require-git", "--no-unicode", "--null",
			"--null-data", "--one-file-system", "--only-matching", "--passthru", "--pcre2", "--no-pcre2",
			"--pretty", "--quiet", "--smart-case", "--stats", "--text", "--no-text", "--trim", "--no-trim",
			"--type-list", "--unrestricted", "--vimgrep", "--with-filename", "--no-filename",
			"--word-regexp", "--version", "--help"),
		longValue: roSet("--after-context", "--before-context", "--context", "--color", "--colors",
			"--context-separator", "--encoding", "--engine", "--field-context-separator",
			"--field-match-separator", "--file", "--glob", "--iglob", "--ignore-file", "--max-columns",
			"--max-count", "--max-depth", "--max-filesize", "--path-separator", "--regexp", "--replace",
			"--sort", "--sortr", "--threads", "--type", "--type-not", "--dfa-size-limit",
			"--regex-size-limit"),
	},
	pathFlags: roSet("-f", "--file", "--ignore-file"),
	patFlags:  roSet("-e", "--regexp", "-f", "--file"),
	noPattern: roSet("--files", "--type-list"),
	counts: roSet("-A", "-B", "-C", "-m", "--after-context", "--before-context", "--context",
		"--max-count"),
}

func (c *roCtx) search(args []roArg, sp *searchSpec) (bool, string) {
	patterned, noPattern := false, false
	ops, ok, why := c.parseFlags(args, sp.flags, func(flag string, v roArg) (bool, string) {
		patterned = patterned || sp.patFlags[flag]
		noPattern = noPattern || sp.noPattern[flag]
		if sp.pathFlags[flag] {
			return c.path(v)
		}
		if sp.counts[flag] && !isDigits(v.val) {
			return false, flag + " needs a number, not " + v.val
		}
		return plainValue(flag, v)
	})
	if !ok {
		return false, why
	}
	if !patterned && !noPattern && len(ops) > 0 {
		if !ops[0].plain() {
			return false, "unquoted wildcard or expansion in the pattern"
		}
		ops = ops[1:]
	}
	return c.paths(ops)
}

func isAny(s string, list ...string) bool {
	for _, x := range list {
		if s == x {
			return true
		}
	}
	return false
}

func roSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
