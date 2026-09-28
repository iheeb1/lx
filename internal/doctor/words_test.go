package doctor

import (
	"reflect"
	"strings"
	"testing"
)

func TestShellWords(t *testing.T) {
	const home = "/Users/me"
	ok := []struct {
		in   string
		want []string
	}{
		{"lx hook claude", []string{"lx", "hook", "claude"}},
		{"  /usr/local/bin/lx\thook  claude ", []string{"/usr/local/bin/lx", "hook", "claude"}},
		{"'/Users/John Doe/bin/lx' hook claude", []string{"/Users/John Doe/bin/lx", "hook", "claude"}},
		{`"/opt/my lx/lx" hook claude`, []string{"/opt/my lx/lx", "hook", "claude"}},
		{"'/a b'/lx hook claude", []string{"/a b/lx", "hook", "claude"}},
		{"~/go/bin/lx hook claude", []string{"/Users/me/go/bin/lx", "hook", "claude"}},
		{"$HOME/go/bin/lx hook claude", []string{"/Users/me/go/bin/lx", "hook", "claude"}},
		{"${HOME}/go/bin/lx hook claude", []string{"/Users/me/go/bin/lx", "hook", "claude"}},
		{`"$HOME/go/bin/lx" hook claude`, []string{"/Users/me/go/bin/lx", "hook", "claude"}},
		{"lx hook claude --prefix=/x/lx --readonly", []string{"lx", "hook", "claude", "--prefix=/x/lx", "--readonly"}},
		{"'~'/lx hook", []string{"~/lx", "hook"}},
		{"a~b", []string{"a~b"}},
		{"/ünï/lx hook claude", []string{"/ünï/lx", "hook", "claude"}},
		{`'/it'\''s/lx' hook claude`, []string{"/it's/lx", "hook", "claude"}},
		{`/a\ b/lx hook \;`, []string{"/a b/lx", "hook", ";"}},
		{`\~/lx`, []string{"~/lx"}},
		{"/Users/Jürgen/lx hook claude", []string{"/Users/Jürgen/lx", "hook", "claude"}},
		{`'/Users/Jürgen O'\''Brien/lx' hook claude`, []string{"/Users/Jürgen O'Brien/lx", "hook", "claude"}},
	}
	for _, c := range ok {
		got, good := shellWords(c.in, home)
		if !good || !reflect.DeepEqual(got, c.want) {
			t.Errorf("shellWords(%q) = %q, %v; want %q", c.in, got, good, c.want)
		}
	}
	bad := []string{
		"", "   ",
		"lx hook claude; touch x", "lx hook claude && touch x", "lx hook claude || touch x",
		"lx hook claude | tee x", "lx hook claude & ", "lx hook claude > x", "lx hook claude < x",
		"lx hook claude 2>&1", "lx hook claude $(touch x)", "lx hook claude `touch x`",
		`lx hook "$(touch x)"`, `lx hook "claude` + "`x`" + `"`, "lx hook claude\ntouch x",
		"lx hook claude #x", "lx hook $X", "$LX hook claude", "${LX} hook claude", "$HOMEBIN/lx hook claude",
		"lx hook cl*de", "lx hook cl?ude", "lx hook [c]laude", "lx {hook,x} claude", `lx hook claude\`,
		"lx hook 'claude", `lx hook "claude`, "~root/lx hook claude", "(lx hook claude)", "lx hook claude!",
		`lx hook "claude\"`, `"$(id)" hook claude`,

		"lx hook claude --prefix=ぁ\\;./evil", "lx hook claude --prefix ぁ\\;x", "lx hook claude --prefix \\ぁ",
		"/opt/é\\ x/lx hook claude",
	}
	for _, in := range bad {
		if got, good := shellWords(in, home); good {
			t.Errorf("shellWords(%q) accepted: %q", in, got)
		}
	}

	if _, good := shellWords("$HOME/lx hook claude", "/Users/John Doe"); good {
		t.Error("unquoted $HOME with a space was accepted")
	}
	if w, good := shellWords(`"$HOME/lx" hook claude`, "/Users/John Doe"); !good || w[0] != "/Users/John Doe/lx" {
		t.Errorf("quoted $HOME with a space: %q, %v", w, good)
	}
	if w, good := shellWords("~/lx hook claude", "/Users/John Doe"); !good || w[0] != "/Users/John Doe/lx" {
		t.Errorf("~ with a space in home (tilde results are not split): %q, %v", w, good)
	}
}

func TestClassifyNeverVerifiesInjection(t *testing.T) {
	const home = "/Users/me"
	verified := []string{
		"lx hook claude",
		"/Users/me/.local/bin/lx hook claude",
		"'/Users/John Doe/bin/lx' hook claude",
		"~/go/bin/lx hook claude --readonly",
		"/usr/local/bin/lx hook claude --prefix /usr/local/bin/lx",
		"/usr/local/bin/lx hook claude --prefix='/a b/lx' --readonly",
	}
	for _, c := range verified {
		if _, kind := classify(c, home); kind != lxVerified {
			t.Errorf("classify(%q) = %v, want verified", c, kind)
		}
	}
	base := "/usr/local/bin/lx hook claude"
	injections := []string{
		"touch $SENTINEL; " + base,
		base + "; touch $SENTINEL",
		base + " && touch $SENTINEL",
		base + " || touch $SENTINEL",
		base + " | tee $SENTINEL",
		base + " > $SENTINEL",
		base + " >> /tmp/SENTINEL",
		base + " $(touch SENTINEL)",
		base + " `touch SENTINEL`",
		base + "\ntouch SENTINEL",
		"SENTINEL=1 " + base,
		"LD_PRELOAD=/tmp/x.so " + base,
		"env X=1 " + base,
		"timeout 5 " + base,
		"sh -c 'touch SENTINEL' " + base,
		"a=b/lx hook claude",
		"/tmp/x/lx=y hook claude",
		"(" + base + ")",
		"{ " + base + "; }",
		base + " &",
		base + " --prefix",
		base + " --prefix=",
		base + " --verbose",
		base + " extra",
		"/usr/local/bin/lx hook copilot",
		"/usr/local/bin/lx hook",
		"/usr/local/bin/lx-dev hook claude",
		"/usr/local/bin/notlx hook claude",
		"lx hook claude \"$(touch SENTINEL)\"",
		"exec " + base,
		"eval " + base,
		"source /tmp/x; " + base,
		". /tmp/x && " + base,
		base + " --prefix=ぁ\\;./evil",
	}
	for _, c := range injections {
		if _, kind := classify(c, home); kind == lxVerified {
			t.Errorf("classify(%q) = verified: doctor would execute it", c)
		}
	}

	for _, c := range injections[:15] {
		if _, kind := classify(c, home); kind != lxOther {
			t.Errorf("classify(%q) = %v, want lxOther", c, kind)
		}
	}
	for _, c := range []string{"rtk hook claude", "/opt/hooks/check.sh", "echo lxhook", "python3 -m lxml"} {
		if _, kind := classify(c, home); kind != notLx {
			t.Errorf("classify(%q) = %v, want notLx", c, kind)
		}
	}
}

func TestParseLxHookFlags(t *testing.T) {
	h, kind := classify("/b/lx hook claude --readonly --prefix '/x y/lx'", "/h")
	if kind != lxVerified || !h.readOnly || !h.hasPrefix || h.prefix != "/x y/lx" || h.bin != "/b/lx" {
		t.Errorf("got %+v %v", h, kind)
	}
	h, kind = classify("/b/lx hook claude --prefix=lx", "/h")
	if kind != lxVerified || !h.hasPrefix || h.prefix != "lx" || h.readOnly {
		t.Errorf("got %+v %v", h, kind)
	}
}

func TestBroadLxRule(t *testing.T) {
	broad := []string{
		"Bash(lx:*)", "Bash(lx *)", "Bash(lx*)", "Bash( lx  :* )", "Bash(lx  *)",
		"Bash(/usr/local/bin/lx:*)", "Bash(~/go/bin/lx *)", "Bash('/a b/lx':*)",
		"Bash(lx -r:*)", "Bash(lx --raw *)", "Bash(lx -b 3000:*)", "Bash(lx --budget=10 *)",
		"Bash(lx -- *)", "Bash(lx * status)", "Bash(lx -v *:*)",
		"Bash(lx --live:*)", "Bash(lx -b3000:*)",
		"Bash(lx bash:*)", "Bash(lx env *)", "Bash(lx sudo:*)", "Bash(lx /bin/sh -c:*)", "Bash(lx lx:*)",
		"Bash(lx timeout 5 *)", "Bash(lx nice -n 10:*)", "Bash(lx env A=1 *)", "Bash(lx xargs:*)",
	}
	for _, r := range broad {
		if !broadLxRule(r) {
			t.Errorf("broadLxRule(%q) = false", r)
		}
	}
	fine := []string{
		"Bash(lx)", "Bash(lx git status:*)", "Bash(lx git status)", "Bash(lx go test *)",
		"Bash(lx -r git log:*)", "Bash(git:*)", "Bash(*)", "Bash", "Read(lx:*)", "Bash(lxc:*)",
		"Bash(flx:*)", "Bash(lx-dev:*)", "", "Bash(",
		"Bash(lx env FOO=1 make:*)", "Bash(lx timeout 5 make:*)", "Bash(lx bash build.sh)", "Bash(lx sudo -u root:*)",
		"Bash(lx bash)",
	}
	for _, r := range fine {
		if broadLxRule(r) {
			t.Errorf("broadLxRule(%q) = true", r)
		}
	}
}

func TestMatcherCoversBash(t *testing.T) {
	cases := map[string]bool{
		"": true, "*": true, "Bash": true, "Bash|Edit": true, "Edit|Bash": true, ".*": true, "B.*": true,
		"Edit": false, "Edit|Write": false, "bash": false, "Ba": false, "Notebook.*": false, "[": false,
	}
	for m, want := range cases {
		h := hookEntry{Matcher: m, HasMatcher: true}
		if got := h.coversBash(); got != want {
			t.Errorf("matcher %q covers Bash = %v, want %v", m, got, want)
		}
	}
	if !(hookEntry{}).coversBash() {
		t.Error("a missing matcher must cover Bash")
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/lx": "/usr/bin/lx",
		"/a b/lx":     "'/a b/lx'",
		"it's":        `'it'\''s'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}

		if w, ok := shellWords(shellQuote(in), "/h"); !ok || strings.Join(w, " ") != in {
			t.Errorf("shellWords(shellQuote(%q)) = %q, %v", in, w, ok)
		}
	}
}
