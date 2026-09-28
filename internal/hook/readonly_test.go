package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func roProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"src/main.go", "src/util.go", "internal/a.go", "node_modules/x/i.js", "sub/k.txt"} {
		writeFile(t, filepath.Join(root, f), "x\n")
	}
	if err := os.Symlink("/", filepath.Join(root, "esc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src", filepath.Join(root, "inner")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CDPATH", "")

	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("CLAUDE_CODE_SHELL", "")
	return root
}

func parity(cmd, cwd, root string, extra ...string) (bool, string) {
	return analyze(cmd).readOnlyParity(cwd, root, extra)
}

func TestReadOnlyTable(t *testing.T) {
	root := roProject(t)
	allowed := []string{

		`git status`, `git status -sb`, `git status --porcelain`, `git --no-pager log -5`, `git -P diff`,
		`git diff HEAD~1 -- src/`, `git diff --cached`, `git diff --stat main...HEAD`, `git diff --no-ext-diff`,
		`git diff --output-indicator-new=+`, `git diff --text`, `git log --oneline -20`, `git log -p -- src`,
		`git log --grep=fix --author "Jane Doe"`, `git show HEAD`, `git show HEAD~2 --stat`, `git show @{u}`,
		`git blame src/main.go`, `git blame -L 1,10 src/main.go`,
		`git branch`, `git branch -vv`, `git branch -a`, `git branch -avv`, `git branch --list 'feat*'`,
		`git branch -l x`, `git branch --merged main`, `git branch --contains HEAD`, `git branch --show-current`,
		`git branch --sort=-committerdate`, `git branch --points-at HEAD -r`,

		`ls`, `ls -la`, `ls -1`, `ls src`, `ls -R src`, `ls --color=auto -la`, `ls -- src`, `ls inner`,
		`ls src/*.go`, `ls ./src/*`, `ls s*/*.go`, `ls ./src/../src`, `ls -lh --sort=size --group-directories-first`,
		`tree`, `tree -L 2`, `tree -L2 -a src`, `tree -a -I node_modules src`, `tree -d --dirsfirst`, `tree -P '*.go' --prune`,
		`du`, `du -sh .`, `du -h -d 1 src`, `du --max-depth=1 .`, `du -sh src/*`, `du -a --exclude='*.o' .`,

		`find . -name '*.go'`, `find src -type f`, `find -H src -maxdepth 2`, `find . -name '*.go' -newer src/main.go`,
		`find . -path ./node_modules -prune -o -print`, `find . -newermt 2024-01-01`, `find`, `find . \( -name a -o -name b \)`,

		`grep -rn TODO internal`, `grep -rn TODO`, `grep -E 'a|b' src/main.go`, `grep -e foo -e bar -r src`,
		`egrep -n x src`, `fgrep -rl x .`, `grep --include='*.go' -rn x .`, `grep -C3 foo src/main.go`,
		`grep -A 2 foo src`, `grep -5 foo src/main.go`, `grep -f src/main.go -r .`, `grep --color=always -i x src`,

		`rg -n foo src`, `rg foo`, `rg -S -tgo foo src`, `rg --files src`, `rg -g '*.go' foo`, `rg -e a -e b`,
		`rg --type go foo`, `rg -uu foo`, `rg --hidden foo .`, `rg -A3 --no-heading 'x y' src internal`,

		`cd src && ls -la | head -20`, `git log | head -5`, `git status 2>&1 | tail -3`, `cd src && ls && ls ..`,
		`git status && git diff`, `git status; ls -la`, `git diff || git log -1`, `ls | cat`,
	}
	refused := []string{

		`find . -delete`, `find . -exec rm {} +`, `find . -execdir ls \;`, `find . -ok rm {} \;`, `find . -fprint out`,
		`find . -fprintf out '%p'`, `find . -fls out`, `find . -follow`, `find . -files0-from x`, `find -L . -name x`,
		`find -f /etc`, `find -D tree .`, `find / -name x`, `find esc`, `find . -name *.go`, `find . -newer /etc/passwd`,
		`find ../.. -name x`,

		`rg --pre ./x foo`, `rg --pre=x foo`, `rg --pre-glob '*' foo`, `rg -z foo`, `rg --search-zip foo`, `rg -L foo`,
		`rg --follow foo`, `rg --hostname-bin x foo`, `rg --type-add 'x:*.x' foo`, `rg --files /etc`, `rg foo /etc`,
		`rg --ignore-file /etc/x foo`, `rg -f /etc/passwd`, `rg foo esc/etc`,

		`grep -R x .`, `grep --dereference-recursive x .`, `grep -S x .`, `grep --exclude-from=x y .`, `grep -rn x /etc`,
		`grep x ../../x`, `grep -r key ~/.ssh`, `grep -f /etc/passwd x`, `grep --file=/etc/passwd x .`, `grep foo* .`,
		`grep -f ~/.ssh/id x`, `grep --exclude-f=x y .`,

		`git -c core.pager=x log`, `git -C /tmp status`, `git -C ../x status`, `git --git-dir=x status`,
		`git --work-tree=x status`, `git --exec-path=x status`, `git --namespace=x status`, `git -P -c x=y log`,
		`git diff --output=/tmp/x`, `git diff --output /tmp/x`, `git diff --out=/tmp/x`, `git diff --no-index a b`,
		`git diff --no-i a b`, `git diff --ext-diff`, `git diff --textconv`, `git log -O/tmp/order`, `git log -pO x`,
		`git diff --orderfile=x`, `git diff /etc/passwd src/main.go`, `git diff ../../x y`, `git diff esc/etc/hosts x`,
		`git show ~/x`, `git show *.go`, `git blame --contents /etc/passwd src/main.go`, `git blame -S revs src/main.go`,
		`git blame --ignore-revs-file x src/main.go`, `git push`, `git commit -m x`, `git checkout x`, `git stash`,
		`git config user.name`, `git`, `git branch -D main`, `git branch newname`, `git branch -v newname`,
		`git branch -m a b`, `git branch -d x`, `git branch -c x`, `git branch -f x`, `git branch -u origin/x`,
		`git branch --set-upstream-to=x`, `git branch --edit-description`, `git branch --format=x`,

		`ls ../../..`, `ls ..`, `ls /etc`, `ls ~`, `ls ~root`, `ls esc`, `ls esc/`, `ls esc/etc`, `ls inner/../..`,
		`ls */`, `ls -RL`, `ls -R --dereference src`, `ls --hide=x`, `ls src/{a,b}`, `ls {/etc,.}`, `ls =ls`,
		`ls "$HOME"`, `ls $HOME`, `ls */*/*`, `ls "s"*/../..`, `ls s*/../..`, `ls src/*/..`, `ls ./*`, `du -sh *`,
		`cd src && ls .. && cd .. && ls`,
		`cd src && ls && cd sub && ls`,
		`tree -o out.txt`, `tree -R`, `tree -l`, `tree --fromfile x`, `tree /`, `tree esc`,
		`du -L`, `du -X file .`, `du --files0-from=x`, `du /`, `du --exclude-from=x .`, `du --dereference .`,

		`git status && npm test`, `git status; rm -rf x`, `git status | sort`, `LD_PRELOAD=x git status`,
		`timeout 5 git status`, `env git status`, `git status > f`, `git status < in`, `git status &`,
		`git status $(x)`, "git status `x`", `cat README.md`, `cd .. && ls`, `cd esc && ls`, `cd src && ls ../..`,
		`cd /tmp && ls`, `./grep x .`, `/usr/bin/git status`, `lx git status`, `sudo ls`, `git status && lx ls`,
		"ls\nrm -rf x", `ls 'unterminated`,
	}
	for _, c := range allowed {
		if ok, why := parity(c, root, root); !ok {
			t.Errorf("refused %q: %s", c, why)
		}
	}
	for _, c := range refused {
		if ok, _ := parity(c, root, root); ok {
			t.Errorf("allowed %q", c)
		}
	}
	if n := len(allowed) + len(refused); n < 80 {
		t.Fatalf("only %d cases", n)
	}
}

func TestReadOnlyArgv(t *testing.T) {
	root := roProject(t)
	for _, c := range []struct {
		argv []string
		want bool
	}{
		{[]string{"git", "status"}, true},
		{[]string{"ls", "-la", "src"}, true},
		{[]string{"rg", "-n", "foo", "src"}, true},
		{[]string{"grep", "-r", "key", "~/.ssh"}, false},
		{[]string{"ls", "../.."}, false},

		{[]string{"find", ".", "-name", "*.go"}, false},
		{[]string{"ls", "$HOME"}, false},
		{[]string{"find", ".", "-name", "a", "-exec", "rm", "{}", "+"}, false},
	} {
		if got, why := readOnly(c.argv, root, root, nil); got != c.want {
			t.Errorf("readOnly(%q) = %v (%s), want %v", c.argv, got, why, c.want)
		}
	}
}

func TestReadOnlyRoot(t *testing.T) {
	root := roProject(t)
	home := filepath.Join(root, "sub")
	t.Setenv("HOME", home)
	if ok, _ := parity("git status", home, home); ok {
		t.Error("root equal to $HOME allowed")
	}
	if ok, _ := parity("git status", home, root); ok {
		t.Error("root containing $HOME allowed")
	}
	t.Setenv("HOME", t.TempDir())
	if ok, _ := parity("ls", "/", "/"); ok {
		t.Error("root / allowed")
	}
	if ok, _ := parity("ls", "src", "src"); ok {
		t.Error("relative cwd allowed")
	}
	if ok, _ := parity("ls", root, filepath.Join(root, "missing")); ok {
		t.Error("missing root allowed")
	}

	if ok, _ := parity("ls", t.TempDir(), root); ok {
		t.Error("cwd outside the root allowed")
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if ok, why := parity("ls src", link, root); !ok {
		t.Errorf("symlinked cwd refused: %s", why)
	}
}

func TestReadOnlyExtraDirs(t *testing.T) {
	root := roProject(t)
	extra := t.TempDir()
	writeFile(t, filepath.Join(extra, "doc.md"), "x")
	if ok, _ := parity("grep -rn x "+extra, root, root); ok {
		t.Error("extra dir allowed without being configured")
	}
	if ok, why := parity("grep -rn x "+extra, root, root, extra); !ok {
		t.Errorf("configured extra dir refused: %s", why)
	}

	home, _ := os.UserHomeDir()
	for _, d := range []string{"/", home, filepath.Dir(home)} {
		if ok, _ := parity("ls /etc", root, root, d); ok {
			t.Errorf("extra dir %s widened the root to /etc", d)
		}
	}
}

func TestReadOnlySymlinkEscapes(t *testing.T) {
	root := roProject(t)

	sub := filepath.Join(root, "deep")
	writeFile(t, filepath.Join(sub, "a", "f"), "x")
	if err := os.Symlink("/etc", filepath.Join(sub, "b")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{`ls deep/*`, `ls deep/*/`, `du -sh deep/*`, `ls deep/?`, `grep -r x deep/b/`, `cd deep && ls b`} {
		if ok, _ := parity(c, root, root); ok {
			t.Errorf("allowed %q (deep/b -> /etc)", c)
		}
	}
	if ok, why := parity(`ls deep/a/*`, root, root); !ok {
		t.Errorf("refused a wildcard inside: %s", why)
	}
}

func TestReadOnlyGlobOptionNames(t *testing.T) {
	root := roProject(t)
	dir := filepath.Join(root, "w")
	writeFile(t, filepath.Join(dir, "--pre=sh"), "x")
	writeFile(t, filepath.Join(dir, "a.txt"), "x")
	for _, c := range []string{`rg foo *`, `ls *`, `grep -r x ?-pre=sh`} {
		if ok, _ := parity(c, dir, root); ok {
			t.Errorf("allowed %q with a file named --pre=sh", c)
		}
	}
	if ok, why := parity(`rg foo ./*`, dir, root); !ok {
		t.Errorf("./* refused: %s", why)
	}
	if ok, why := parity(`ls a*`, dir, root); !ok {
		t.Errorf("a* refused: %s", why)
	}
	many := filepath.Join(root, "many")
	for i := 0; i <= maxGlobMatches; i++ {
		writeFile(t, filepath.Join(many, strings.Repeat("f", 1+i%5)+string(rune('a'+i%26))+"-"+itoa(i)), "")
	}
	if ok, _ := parity(`ls many/*`, root, root); ok {
		t.Error("a wildcard matching too many files was allowed")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestReadOnlyCDPATH(t *testing.T) {
	root := roProject(t)
	t.Setenv("CDPATH", "/etc")
	if ok, _ := parity(`cd src && ls`, root, root); ok {
		t.Error("cd with CDPATH set allowed")
	}
}

func TestWordShape(t *testing.T) {
	for raw, want := range map[string][2]bool{
		`*.go`: {true, false}, `'*.go'`: {false, false}, `"*.go"`: {false, false}, `\*.go`: {false, false},
		`a'*'b*`: {true, false}, `~/x`: {false, true}, `'~/x'`: {false, false}, `a~b`: {false, false},
		`--f=~/x`: {false, true}, `=ls`: {false, true}, `{a,b}`: {false, true}, `'{a,b}'`: {false, false},
		`{1..3}`: {false, true}, `HEAD@{1}`: {false, false}, `$'*'`: {false, false}, `x[1]`: {true, false},
		`"a\"*"`: {false, false}, `HEAD^`: {false, false}, `{e}sc`: {false, false}, `es*~x`: {true, false},
	} {
		g, s := wordShape(raw, false)
		if g != want[0] || s != want[1] {
			t.Errorf("bash: wordShape(%q) = %v, %v, want %v", raw, g, s, want)
		}
	}

	for raw, want := range map[string][2]bool{
		`HEAD^`: {false, true}, `^src/etc`: {false, true}, `e#sc`: {false, true}, `'^x'`: {false, false},
		`"a#b"`: {false, false}, `\^x`: {false, false}, `es*~x`: {true, true}, `HEAD~1`: {false, false},
		`{e}sc`: {false, true}, `HEAD@{1}`: {false, false}, `@{u}`: {false, false}, `{}`: {false, false},
		`'{e}'sc`: {false, false}, `*.go`: {true, false},
	} {
		g, s := wordShape(raw, true)
		if g != want[0] || s != want[1] {
			t.Errorf("zsh: wordShape(%q) = %v, %v, want %v", raw, g, s, want)
		}
	}
}

var fuzzBases = []struct {
	argvs  [][]string
	from   int
	only   bool
	danger []string
}{
	{
		[][]string{{"git", "status"}, {"git", "diff"}, {"git", "log", "--oneline"}, {"git", "branch", "-vv"}},
		1, true,
		[]string{"-c", "-C", "-cx=y", "--git-dir=x", "--work-tree=x", "--exec-path=x", "--namespace=x",
			"--config-env=a=b", "--output=x", "--no-index", "--ext-diff", "-exec", "-delete", "--pre", "-o",
			"-D", "--files0-from=x", "~/x", "../../x", "$HOME"},
	},
	{
		[][]string{{"git", "diff"}, {"git", "diff", "HEAD~1", "src"}, {"git", "log", "--oneline", "-20"},
			{"git", "show", "HEAD"}, {"git", "log", "-p", "src"}, {"git", "--no-pager", "log"}},
		-1, false,
		[]string{"--output=x", "--output", "--out=x", "--no-index", "--no-i", "--ext-diff", "--textconv", "-O",
			"-Ox", "-pO", "--orderfile=x", "~/x", "../../x", "$HOME", "/etc/passwd", "esc/etc", "{a,b}"},
	},
	{
		[][]string{{"git", "branch"}, {"git", "branch", "-vv"}, {"git", "branch", "-a"}, {"git", "branch", "-r", "-v"}},
		2, false,
		[]string{"-D", "-d", "-m", "-M", "-c", "-C", "-f", "newname", "-u", "--set-upstream-to=x", "--delete",
			"--move", "--copy", "--force", "--edit-description", "-o", "--output=x", "-exec", "~/x", "../../x", "$HOME"},
	},
	{
		[][]string{{"find", "."}, {"find", ".", "-name", "x.go"}, {"find", "src", "-type", "f"}, {"find", "-H", "src"}},
		1, false,
		[]string{"-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls",
			"-follow", "-files0-from", "~/x", "$HOME", "{-delete,-print}"},
	},
	{
		[][]string{{"rg", "-efoo"}, {"rg", "-n", "-efoo", "src"}, {"rg", "--regexp=foo", "-tgo", "src"}, {"rg", "--files"}},
		1, false,
		[]string{"--pre", "--pre=x", "--pre-glob=x", "-z", "--search-zip", "-L", "--follow", "--hostname-bin=x",
			"--type-add=x", "-f/etc/passwd", "--file=/etc/passwd", "--ignore-file=/etc/x", "-nz", "--output=x",
			"~/x", "../../x", "$HOME", "/etc", "esc/etc", "--files0-from=x"},
	},
	{
		[][]string{{"grep", "-rn", "--regexp=TODO", "internal"}, {"grep", "-efoo", "src/main.go"}, {"egrep", "-n", "-ex", "."}},
		1, false,
		[]string{"-R", "--dereference-recursive", "--exclude-from=x", "-f/etc/passwd", "--file=/etc/passwd", "-S",
			"--output=x", "--pre", "--files0-from=x", "~/x", "../../x", "$HOME", "/etc/passwd", "esc/etc"},
	},
	{
		[][]string{{"ls"}, {"ls", "-la"}, {"ls", "src"}, {"ls", "--color=auto"}},
		1, false,
		[]string{"~/x", "../../x", "$HOME", "/etc", "esc", "esc/etc", "--hide=x", "--output=x", "--no-index",
			"--files0-from=x", "{a,b}", "=ls"},
	},
	{
		[][]string{{"tree"}, {"tree", "-a"}, {"tree", "-d", "src"}, {"tree", "-L2"}},
		1, false,
		[]string{"-o", "-oout", "-R", "-l", "-al", "--fromfile", "--fromfile=x", "--output=x", "~/x", "../../x", "$HOME", "/", "esc"},
	},
	{
		[][]string{{"du"}, {"du", "-sh", "."}, {"du", "-h", "-d1", "src"}},
		1, false,
		[]string{"-L", "-X", "-Xfile", "--files0-from=x", "--exclude-from=x", "--dereference", "--output=x",
			"-exec", "-o", "~/x", "../../x", "$HOME", "/", "esc"},
	},
}

func FuzzReadOnly(f *testing.F) {
	root := roProject(f)
	for b := range fuzzBases {
		for a := range fuzzBases[b].argvs {
			for w := range fuzzBases[b].danger {
				f.Add(uint8(b), uint8(a), uint8(w), uint8(w+a))
			}
		}
	}
	f.Fuzz(func(t *testing.T, b, a, w, pos uint8) {
		fb := fuzzBases[int(b)%len(fuzzBases)]
		base := fb.argvs[int(a)%len(fb.argvs)]
		word := fb.danger[int(w)%len(fb.danger)]
		from := fb.from
		if from < 0 {
			from = 2
			for from < len(base) && base[from-1] == "--no-pager" {
				from++
			}
		}
		at := from
		if !fb.only {
			at += int(pos) % (len(base) - from + 1)
		}
		argv := append(append(append([]string(nil), base[:at]...), word), base[at:]...)

		if ok, _ := readOnly(base, root, root, nil); !ok {
			t.Fatalf("base %q refused", base)
		}
		if ok, _ := readOnly(argv, root, root, nil); ok {
			t.Fatalf("readOnly(%q) = true", argv)
		}
		if ok, _ := parity(strings.Join(argv, " "), root, root); ok {
			t.Fatalf("parity(%q) = true", strings.Join(argv, " "))
		}
	})
}

func FuzzReadOnlyParity(f *testing.F) {
	root := roProject(f)
	for _, s := range []string{`git status`, `cd src && ls -la | head`, `ls */*`, `find . -name '*.go'`,
		`grep -rn "a b" src`, `rg -e x -- -y`, `ls "s"*/../..`, `git branch --merged main x`, `du -sh -- *`,
		`tree -L 2 -P '*.go'`, `ls {a,b}`, `cd inner && ls .. && ls`,

		`git status | cat -$IFS/etc/passwd`, `git log | cat - -l`, `git log | tail +5`, `grep --context x /etc/passwd`,
		`ls ^src/etc`, `ls e#sc/etc`, `ls es*~x`, `ls {e}sc/etc`, `ls E*/etc`, `tree -Lo 2 out.txt`,
		`cd nonexist; ls`, `cd src || ls ../..`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, _ := parity(s, root, root)
		b, _ := parity(s, root, root)
		if a != b {
			t.Fatalf("parity(%q) not deterministic", s)
		}
		if !a {
			return
		}

		an := analyze(s)
		if len(an.unsafe) > 0 || an.lx.broken {
			t.Fatalf("parity(%q) allowed an unsafe string", s)
		}
		for _, tk := range an.lx.toks {
			if tk.kind == tRedir && tk.text != "2>&" {
				t.Fatalf("parity(%q) allowed redirection %s", s, tk.text)
			}
		}
		for _, sg := range an.segs {
			if sg.cmdIdx != 0 || !isAny(sg.argv[0], "git", "ls", "tree", "du", "find", "grep", "egrep", "fgrep",
				"rg", "cd", "head", "tail", "cat") {
				t.Fatalf("parity(%q) allowed %q", s, sg.argv)
			}

			for _, w := range sg.words {
				if w.expand {
					t.Fatalf("parity(%q) allowed $ expansion in %q", s, w.text)
				}
			}
			if isAny(sg.argv[0], "head", "tail", "cat") && !stdinFilter(sg.argv) {
				t.Fatalf("parity(%q) allowed %q", s, sg.argv)
			}
		}
	})
}

func TestReadOnlyFast(t *testing.T) {
	root := roProject(t)
	a := analyze(`cd src && git status && ls -la | head -20 && rg -n foo . && grep -rn TODO .`)
	const n = 200
	for i := 0; i < n; i++ {
		if ok, why := a.readOnlyParity(root, root, nil); !ok {
			t.Fatal(why)
		}
	}
}

func TestReadOnlyReviewRegressions(t *testing.T) {
	root := roProject(t)

	for _, f := range []string{"5", "+5", "src/deep/x"} {
		writeFile(t, filepath.Join(root, f), "x")
	}
	refused := map[string]string{

		`git status | cat -$IFS/etc/passwd`: "expansion in a pipe filter",
		`git log | head -$IFS/etc/passwd`:   "expansion in a pipe filter",
		`git log | tail -n $N`:              "expansion in a pipe filter",
		`git log | cat -*`:                  "wildcard in a pipe filter",

		`git log | cat - -l`:    "BSD cat: -l after - is a file",
		`git log | cat -- -l`:   "-l after -- is a file",
		`git log | head 5`:      "5 is a file",
		`git log | tail +5`:     "GNU tail: +5 is a file",
		`git log | head -c 5 5`: "the second 5 is a file",
		`git log | head -n`:     "-n without its count",
		`git log | head -n x`:   "-n with a non-count",

		`grep --context x /etc/passwd`:   "BSD grep --context",
		`grep -C x /etc/passwd`:          "count flag with a non-number",
		`grep -A x /etc/passwd`:          "count flag with a non-number",
		`grep -m x /etc/passwd`:          "count flag with a non-number",
		`grep --max-count x /etc/passwd`: "count flag with a non-number",
		`rg -C x /etc/passwd`:            "count flag with a non-number",

		`ls ^src/etc`:    "zsh ^ glob",
		`ls e#sc/etc`:    "zsh # glob",
		`ls es*~x`:       "zsh ~ exclusion",
		`ls {e}sc/etc`:   "zsh BRACE_CCL",
		`grep ^x src`:    "zsh ^ glob in a pattern",
		`git show HEAD^`: "zsh ^ glob in a revision",

		`ls E*/etc`: "case-insensitive glob reaches esc/etc",
		`ls ES*`:    "case-insensitive glob reaches esc",

		`tree -Lo 2 out.txt`:  "tree -L 2 -o out.txt",
		`tree -aLo 2 out.txt`: "tree -a -L 2 -o out.txt",
		`tree -PL x 2`:        "tree -P x -L 2",

		`cd nonexist; ls`:        "cd to a missing directory",
		`cd nonexist || ls`:      "cd to a missing directory",
		`cd src/main.go && ls`:   "cd to a file",
		`cd src; cd sub && ls`:   "cd sub may run in src, where sub is missing",
		`cd src || ls ../..`:     "ls runs where cd src failed",
		`cd src && ls; ls ../..`: "after ;, the shell may be in the root or in src",

		`find . -Bnewer /etc/passwd`: "find -Bnewer",
		`find . -mnewer /etc/passwd`: "find -mnewer",
	}
	for c, why := range refused {
		if ok, _ := parity(c, root, root); ok {
			t.Errorf("allowed %q (%s)", c, why)
		}
	}
	allowed := []string{
		`git log | head -n 5`, `git log | tail -n +30`, `git log | head -20`, `git log | cat -n`,
		`git log | head --lines=5`, `git log | tail -c 1K`, `git log | head -qn5`,
		`grep --context=3 foo src`, `grep -C 3 foo src`, `grep -C3 foo src`, `grep --context 3 foo src`,
		`grep -m 1 foo src`, `rg -C 2 foo src`,
		`tree -L 2`, `tree -L2`, `tree -aL 2 src`, `tree -a -P '*.go' src`,
		`ls S*/*.go`, `git show HEAD@{1}`, `git log @{u}`, `git diff HEAD~1 -- src/`,
		`cd src && ls`, `cd src && ls && cd deep && ls`, `cd src || ls`, `cd src && ls ..`,
		`cd src && cd deep && ls ../..`, `git status && cd src && ls`,
	}
	for _, c := range allowed {
		if ok, why := parity(c, root, root); !ok {
			t.Errorf("refused %q: %s", c, why)
		}
	}

	fl := filepath.Join(root, "fl")
	writeFile(t, filepath.Join(fl, "src", "a"), "x")
	if err := os.Symlink("/etc", filepath.Join(fl, "-l")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{`ls src -l`, `ls -l`, `du -s src -l`, `grep -r x src -l`, `rg -l x src`} {
		if ok, _ := parity(c, fl, root); ok {
			t.Errorf("allowed %q with a file named -l", c)
		}
	}
	if ok, why := parity(`ls -a src`, fl, root); !ok {
		t.Errorf("refused ls -a src: %s", why)
	}

	t.Setenv("SHELL", "/bin/bash")
	for _, c := range []string{`git show HEAD^`, `git log HEAD^..HEAD`, `grep -n ^func src`, `ls {e}sc`} {
		if ok, why := parity(c, root, root); !ok {
			t.Errorf("bash: refused %q: %s", c, why)
		}
	}

	t.Setenv("CLAUDE_CODE_SHELL", "/usr/local/bin/zsh")
	if ok, _ := parity(`git show HEAD^`, root, root); ok {
		t.Error("CLAUDE_CODE_SHELL=zsh: allowed git show HEAD^")
	}

	t.Setenv("CLAUDE_CODE_SHELL", "")
	t.Setenv("SHELL", "")
	if ok, _ := parity(`ls ^src/etc`, root, root); ok {
		t.Error("unknown shell: allowed ls ^src/etc")
	}
}

func TestDecideNeutralFilters(t *testing.T) {
	r := Rules{Allow: []string{"Bash(git status:*)", "Bash(git log:*)"}}
	for _, c := range []string{`git status | cat -$IFS/etc/passwd`, `git log | head 5`, `git log | cat - -x`,
		`git log | cat -- -x`, `git log | tail +5`, `git log | cat -*`, "git log | cat -`x`"} {
		if got := r.Decide(c); got == VerdictAllow {
			t.Errorf("Decide(%q) = allow", c)
		}
	}
	for _, c := range []string{`git log | head -n 5`, `git log 2>&1 | tail -n +30`, `git log | cat`, `git log | head -5`} {
		if got := r.Decide(c); got != VerdictAllow {
			t.Errorf("Decide(%q) = %q, want allow", c, got)
		}
	}
}

func TestReadOnlyHomeOtherCase(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "Home")
	if err := os.MkdirAll(filepath.Join(home, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	upper := filepath.Join(base, "HOME")
	if _, err := os.Stat(upper); err != nil {
		t.Skip("case-sensitive file system")
	}
	if ok, _ := parity("ls src", upper, upper); ok {
		t.Error("the home directory in another case was accepted as the project")
	}
}
