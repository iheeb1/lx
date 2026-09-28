package jstools

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func TestLsFoldsOnlyWhenNotFaithful(t *testing.T) {
	fc, err := fixture.Read("testdata", "node", "npm-ls-workspaces")
	if err != nil {
		t.Fatal(err)
	}
	in := fc.Clean()
	for _, argv := range [][]string{
		{"npm", "ls"}, {"npm", "ls", "--all"}, {"npm", "ls", "-a"}, {"npm", "ls", "--depth=3"},
		{"npm", "ls", "--depth", "2"}, {"npm", "ls", "debug", "--all"}, {"npm", "list", "-w", "pkg-a"},
		{"pnpm", "ls"}, {"yarn", "list"},
	} {
		c := ctx(0, argv...)
		got, ok := npmLs{}.Apply(c, in)
		if !ok {
			t.Fatalf("%v: bailed", argv)
		}
		folded := strings.Contains(got, "[+")
		if faithful := (npmLs{}).Faithful(c); faithful == folded {
			t.Errorf("%v: Faithful=%v but folded=%v:\n%s", argv, faithful, folded, got)
		}
		if !folded && strings.Count(got, " deduped") != strings.Count(in, " deduped") {
			t.Errorf("%v: deduped lines lost without a fold note:\n%s", argv, got)
		}
	}
	res := engine.Process(ctx(0, "npm", "ls"), strings.Repeat(fc.Raw+"\n", 3), engine.Options{})
	if res.Lossy != strings.Contains(res.Output, "[+") {
		t.Errorf("Process: Lossy=%v for\n%s", res.Lossy, res.Output)
	}
}

func TestInstallListsAreCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "npm warn deprecated pkg%d@1.0.%d: message %d\n", i, i, i%10)
		if i%2 == 0 {
			fmt.Fprintf(&b, "npm warn deprecated sec%d@2.0.0: has a security vulnerability\n", i)
		}
	}
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "npm warn ERESOLVE overriding peer dependency\nnpm warn While resolving: p%d@1.0.0\nnpm warn Found: react@18.2.0\n", i)
	}
	b.WriteString("\nadded 20000 packages in 30s\n")
	c := ctx(0, "npm", "install")
	got, ok := npmInstall{}.Apply(c, b.String())
	if !ok {
		t.Fatal("bailed")
	}
	for _, ln := range strings.Split(got, "\n") {
		if tokens.Count(ln) > 1000 {
			t.Errorf("line of %d tokens: %.200s…", tokens.Count(ln), ln)
		}
	}
	for _, want := range []string{
		"… +19960 more [20000 deprecated packages, messages hidden]",
		"… +9960 more: has a security vulnerability",
		"while resolving: p1@1.0.0, p2@1.0.0",
		"… +459 more]",
		"added 20000 packages in 30s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	res := engine.Process(c, b.String(), engine.Options{})
	if !strings.Contains(res.Output, "added 20000 packages") || !strings.Contains(res.Output, "messages hidden") {
		t.Errorf("Process lost the view:\n%s", res.Output)
	}
}

func TestSilentFailureIsFlagged(t *testing.T) {
	cases := []struct {
		name     string
		exit     int
		argv     string
		in       string
		wantNote string
	}{
		{"webpack success line, exit 1", 1, "npx webpack",
			"asset main.js 1 KiB [emitted] (name: main)\n./src/index.js 20 bytes [built] [code generated]\nwebpack 5.90.0 compiled successfully in 100 ms",
			"[lx: npx webpack exited 1, but no line above reports an error]"},
		{"vite killed", 137, "npm run build",
			"\n> app@1.0.0 build\n> vite build\n\nvite v5.4.0 building for production...\ntransforming...\n✓ 1200 modules transformed.\nrendering chunks...",
			"[lx: npm run build exited 137, killed by signal 9, but no line above reports an error]"},
		{"install interrupted", 130, "npm ci",
			"npm warn deprecated left-pad@1.3.0: use String.prototype.padStart()\n\nadded 3 packages in 1s",
			"[lx: npm ci exited 130, killed by signal 2, but no line above reports an error]"},
		{"eslint warnings only, no max-warnings line", 1, "eslint .",
			"/home/user/src/app/a.js\n  1:1  warning  Unexpected console statement  no-console\n\n✖ 1 problem (0 errors, 1 warning)",
			"[lx: eslint . exited 1, but no line above reports an error]"},
		{"eslint max-warnings says why", 1, "eslint . --max-warnings 0",
			"/home/user/src/app/a.js\n  1:1  warning  Unexpected console statement  no-console\n\n✖ 1 problem (0 errors, 1 warning)\n\nESLint found too many warnings (maximum: 0).",
			""},
		{"tsc errors are evidence", 2, "tsc",
			"src/a.ts(1,7): error TS2304: Cannot find name 'foo'.", ""},
		{"exit 0", 0, "npx webpack",
			"asset main.js 1 KiB [emitted] (name: main)\nwebpack 5.90.0 compiled successfully in 100 ms", ""},
		{"npm error lines are evidence", 1, "npm install",
			"npm error code E404\nnpm error 404 Not Found - GET https://registry.npmjs.org/nope", ""},
	}
	for _, tc := range cases {
		c := ctx(tc.exit, strings.Fields(tc.argv)...)
		f := engine.Find(c)
		if f == nil {
			t.Fatalf("%s: no filter", tc.name)
		}
		got, ok := f.Apply(c, tc.in)
		if !ok {
			t.Fatalf("%s: %s bailed", tc.name, f.Name())
		}
		hasNote := strings.Contains(got, "[lx: ")
		switch {
		case tc.wantNote == "" && hasNote:
			t.Errorf("%s: unexpected note:\n%s", tc.name, got)
		case tc.wantNote != "" && !strings.HasSuffix(got, "\n"+tc.wantNote):
			t.Errorf("%s: want final line %q, got\n%s", tc.name, tc.wantNote, got)
		}
	}
}

func TestWebpackResolveTraceCount(t *testing.T) {
	in := strings.Join([]string{
		"ERROR in ./src/broken.js 1:0-18",
		"Module not found: Error: Can't resolve './nope.js' in '/home/user/src/app/src'",
		"resolve './nope.js' in '/home/user/src/app/src'",
		"  using description file: /home/user/src/app/package.json (relative path: ./src)",
		"    Failed to alias from extension alias with mapping '.js' to '.js' for './nope.js': undefined",
		"      /home/user/src/app/src/nope.js doesn't exist",
		" @ ./src/index.js 4:0-20",
		"",
		"webpack 5.111.1 compiled with 1 error in 248 ms",
	}, "\n")
	got, ok := npmRun{}.Apply(ctx(1, "npx", "webpack"), in)
	if !ok {
		t.Fatal("bailed")
	}

	if !strings.Contains(got, "[resolve trace: 3 lines hidden]\n    Failed to alias") {
		t.Errorf("got\n%s", got)
	}
}

func TestEUsageFold(t *testing.T) {
	fc, err := fixture.Read("testdata", "node", "npm-ci-out-of-sync")
	if err != nil {
		t.Fatal(err)
	}
	in := fc.Clean()
	got, ok := npmInstall{}.Apply(fc.Context(), in)
	if !ok {
		t.Fatal("bailed")
	}
	lines := strings.Split(in, "\n")
	opt, run := -1, -1
	for i, ln := range lines {
		switch {
		case ln == "npm error Options:":
			opt = i
		case strings.HasPrefix(ln, `npm error Run "npm help ci"`):
			run = i
		}
	}
	if want := fmt.Sprintf("[npm usage text: %d option lines hidden; `npm help ci` shows them]", run-opt); !strings.Contains(got, want) {
		t.Errorf("want %q in\n%s", want, got)
	}
	for i, ln := range lines {
		if (i < opt || i >= run) && strings.TrimSpace(strings.TrimPrefix(ln, "npm error")) != "" && !strings.Contains(got, ln) {
			t.Errorf("line outside the option list dropped: %q", ln)
		}
	}

	plain := strings.Replace(in, "npm error code EUSAGE", "npm error code EOTHER", 1)
	got, _ = npmInstall{}.Apply(fc.Context(), plain)
	if strings.Contains(got, "[npm usage text") || !strings.Contains(got, "npm error   --omit") {
		t.Errorf("folded without EUSAGE:\n%s", got)
	}
}

func TestVerboseInstallFold(t *testing.T) {
	in := strings.Join([]string{
		"npm verbose cli /usr/local/bin/node /usr/local/bin/npm",
		"npm http fetch GET 200 https://registry.npmjs.org/http-errors 70ms (cache miss)",
		"npm http fetch GET 304 https://registry.npmjs.org/accepts 12ms (cache revalidated)",
		"npm http cache es6-error@https://registry.npmjs.org/es6-error/-/es6-error-4.1.1.tgz 0ms (cache hit)",
		"npm http fetch GET 404 https://registry.npmjs.org/nope 50ms (cache skip)",
		"npm silly placeDep ROOT ms@2.1.3 OK for: ws-app@1.0.0 want: 2.1.3",
		"npm silly reify moves {}",
		"npm silly audit report error: fetch failed",
		"npm timing reify Completed in 700ms",
		"",
		"added 5 packages in 693ms",
	}, "\n")
	got, ok := npmInstall{}.Apply(ctx(0, "npm", "install", "--loglevel", "silly"), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{
		"[npm http: 3 fetch/cache lines (status 2xx/304) hidden]",
		"npm http fetch GET 404 https://registry.npmjs.org/nope 50ms (cache skip)",
		"[npm silly/timing: 4 lines hidden]",
		"npm silly audit report error: fetch failed",
		"npm verbose cli /usr/local/bin/node /usr/local/bin/npm",
		"added 5 packages in 693ms",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "http-errors") || strings.Contains(got, "placeDep") {
		t.Errorf("chatter kept:\n%s", got)
	}
}

func TestOutdatedPartialTableBails(t *testing.T) {
	in := "Package  Current  Wanted  Latest  Location              Depended by\n" +
		"lodash   4.17.20  4.17.21  4.17.21  node_modules/lodash  app\n" +
		"odd      1.0.0    1.0.0    2.0.0    node_modules/odd  app  extra\n" +
		"bar      1.0.0    1.0.0    2.0.0    node_modules/bar  app"
	if got, ok := (npmOutdated{}).Apply(ctx(1, "npm", "outdated"), in); ok {
		t.Errorf("claimed a table it did not fully parse:\n%s", got)
	}

	ok2 := in[:strings.Index(in, "odd")] + "npm warn config production Use `--omit=dev` instead."
	if got, ok := (npmOutdated{}).Apply(ctx(1, "npm", "outdated"), ok2); !ok || !strings.Contains(got, "npm warn config") {
		t.Errorf("ok=%v\n%s", ok, got)
	}
}

func TestDeprecationOrder(t *testing.T) {
	in := "npm warn deprecated a@1.0.0: use b\n" +
		"npm warn deprecated c@1.0.0: has a security hole\n" +
		"npm warn deprecated d@1.0.0: use e\n" +
		"npm warn deprecated f@1.0.0: no longer supported\n\nadded 4 packages in 1s"
	got, _ := npmInstall{}.Apply(ctx(0, "npm", "install"), in)
	want := "npm warn deprecated a@1.0.0, d@1.0.0 [2 deprecated packages, messages hidden]\n" +
		"npm warn deprecated c@1.0.0: has a security hole\n" +
		"npm warn deprecated f@1.0.0: no longer supported\n\nadded 4 packages in 1s"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFailuresInUnusualPlaces(t *testing.T) {
	cases := []struct {
		name, argv string
		exit       int
		in         string
		want       []string
	}{
		{"tsc crash after diagnostics", "tsc", 1,
			"src/a.ts(1,7): error TS2304: Cannot find name 'foo'.\n/home/user/src/app/node_modules/typescript/lib/tsc.js:118813\n      throw e;\n      ^\n\nRangeError: Maximum call stack size exceeded\n    at getTypeOfSymbol (/home/user/src/app/node_modules/typescript/lib/tsc.js:5000:10)",
			[]string{"RangeError: Maximum call stack size exceeded", "src/a.ts(1,7): error TS2304"}},
		{"tsc pretty interleaved with stderr", "tsc --pretty", 2,
			"src/a.ts:1:1 - error TS1005: ';' expected.\nnpm warn exec The following package was not found and will be installed: typescript@5.6.2\n1 x y\n  ~\n\nFound 1 error in src/a.ts:1",
			[]string{"src/a.ts:1:1 - error TS1005: ';' expected.", "npm warn exec", "Found 1 error in src/a.ts:1"}},
		{"eslint crash after the report", "eslint .", 2,
			"\n/home/user/src/app/a.js\n  3:1  warning  Unexpected console statement  no-console\n\n✖ 1 problem (0 errors, 1 warning)\n\nTypeError: Cannot read properties of undefined (reading 'loc')\nOccurred while linting /home/user/src/app/b.js:12",
			[]string{"TypeError: Cannot read properties of undefined (reading 'loc')", "Occurred while linting /home/user/src/app/b.js:12"}},
		{"eslint stderr inside a file block", "eslint .", 1,
			"/home/user/src/app/a.js\n  1:1  error  Unexpected var  no-var\n(node:123) ESLintRCWarning: deprecated config\n  2:1  error  Unexpected var  no-var\n\n✖ 2 problems (2 errors, 0 warnings)",
			[]string{"1:1  error  Unexpected var  no-var", "2:1  error  Unexpected var  no-var", "✖ 2 problems (2 errors, 0 warnings)"}},
		{"vite success then postbuild failure", "npm run build", 1,
			"\n> app@1.0.0 build\n> vite build && node check-size.js\n\nvite v5.4.0 building for production...\n✓ 12 modules transformed.\ndist/index.html  0.46 kB │ gzip: 0.30 kB\ndist/assets/index-abc.js  143.20 kB │ gzip: 46.10 kB\n✓ built in 1.20s\nError: bundle too large: 143.2 kB > 100 kB\n    at Object.<anonymous> (/home/user/src/app/check-size.js:9:9)",
			[]string{"Error: bundle too large: 143.2 kB > 100 kB", "check-size.js:9:9"}},
		{"webpack errors after a big asset", "npx webpack", 1,
			"asset main.js 507 KiB [emitted] [minimized] [big] (name: main)\norphan modules 511 KiB [orphan] 2 modules\n\nERROR in ./src/a.js 3:4\nModule parse failed: Unexpected token (3:4)\n\nwebpack 5.90.0 compiled with 1 error in 100 ms",
			[]string{"ERROR in ./src/a.js 3:4", "Module parse failed: Unexpected token (3:4)", "compiled with 1 error"}},
		{"pnpm error after progress", "pnpm install", 1,
			"Progress: resolved 1, reused 0, downloaded 0, added 0\nProgress: resolved 9, reused 0, downloaded 0, added 0\n ERR_PNPM_OUTDATED_LOCKFILE  Cannot install with \"frozen-lockfile\" because pnpm-lock.yaml is not up to date with package.json",
			[]string{"ERR_PNPM_OUTDATED_LOCKFILE"}},
		{"yarn error after steps", "yarn install --frozen-lockfile", 1,
			"yarn install v1.22.22\n[1/4] Resolving packages...\nerror Your lockfile needs to be updated, but yarn was run with `--frozen-lockfile`.\ninfo Visit https://yarnpkg.com/en/docs/cli/install for documentation about this command.",
			[]string{"error Your lockfile needs to be updated"}},
		{"npm lifecycle failure after added", "npm install", 1,
			"added 12 packages in 2s\nnpm error code 1\nnpm error path /home/user/src/app/node_modules/sharp\nnpm error command failed",
			[]string{"npm error code 1", "npm error command failed"}},
	}
	for _, tc := range cases {
		c := ctx(tc.exit, strings.Fields(tc.argv)...)
		f := engine.Find(c)
		if f == nil {
			t.Fatalf("%s: no filter", tc.name)
		}
		got, ok := f.Apply(c, tc.in)
		if !ok {
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s (%s): %q missing from\n%s", tc.name, f.Name(), w, got)
			}
		}
		if m := fixture.ErrorLinesMissing(tc.in, got); len(m) > 0 {
			t.Errorf("%s (%s): error lines dropped %q", tc.name, f.Name(), m)
		}
		for _, word := range []string{"passed", "No errors", "no problems", "Success"} {
			if strings.Contains(got, word) && !strings.Contains(tc.in, word) {
				t.Errorf("%s: invented %q", tc.name, word)
			}
		}
	}
}

func TestVariants(t *testing.T) {
	cases := []struct {
		name, argv string
		exit       int
		in         string
		wantOK     bool
		want       []string
	}{
		{"windows CRLF normalized, backslash paths", "tsc --pretty", 2,
			"C:\\proj\\src\\a.ts:1:7 - error TS2304: Cannot find name 'foo'.\n\n1 foo\n  ~~~\n\n\nFound 1 error in C:\\proj\\src\\a.ts:1",
			true, []string{"C:\\proj\\src\\a.ts:1:7 - error TS2304: Cannot find name 'foo'.", "Found 1 error in C:\\proj\\src\\a.ts:1"}},
		{"tsc path with spaces", "tsc", 2,
			"src/my dir/a b.ts(3,1): error TS1128: Declaration or statement expected.",
			true, []string{"src/my dir/a b.ts(3,1): error TS1128"}},
		{"tsc localized category", "tsc", 2,
			"src/a.ts(1,7): Fehler TS2304: Der Name \"foo\" wurde nicht gefunden.", false, nil},
		{"eslint localized severity", "eslint .", 1,
			"/home/user/src/app/a.js\n  1:1  Fehler  Unerwartetes var  no-var", false, nil},
		{"eslint stdin", "eslint --stdin", 1,
			"<text>\n  1:1  error  Unexpected var, use let or const instead  no-var\n\n✖ 1 problem (1 error, 0 warnings)",
			true, []string{"<text>", "1:1  error  Unexpected var"}},
		{"eslint compact format is not claimed", "eslint -f compact .", 1, "", false, nil},

		{"npm 6 ERR! lines", "npm install", 1,
			"npm ERR! code E404\nnpm ERR! 404 Not Found - GET https://registry.npmjs.org/nope - Not found\n\nnpm ERR! A complete log of this run can be found in:\nnpm ERR!     /home/user/.npm/_logs/2020-01-01T00_00_00_000Z-debug.log",
			true, []string{"npm ERR! code E404", "npm ERR!     /home/user/.npm/_logs/2020-01-01T00_00_00_000Z-debug.log"}},
		{"bun install", "bun install", 0,
			"bun install v1.1.30 (7996d06b)\n\n + typescript@5.6.2\n\n 3 packages installed [812.00ms]",
			true, []string{"3 packages installed [812.00ms]"}},
		{"vite 4 asset lines", "npx vite build", 0,
			"vite v4.5.0 building for production...\n✓ 34 modules transformed.\ndist/index.html                  0.46 kB │ gzip:  0.30 kB\ndist/assets/index-d526a0c5.css   1.42 kB │ gzip:  0.74 kB\ndist/assets/index-e92ae01e.js  143.61 kB │ gzip: 46.11 kB\n✓ built in 1.03s",
			true, []string{"[3 asset lines hidden (145.49 kB, largest dist/assets/index-e92ae01e.js 143.61 kB)]", "✓ built in 1.03s"}},
	}
	for _, tc := range cases {
		c := ctx(tc.exit, strings.Fields(tc.argv)...)
		f := engine.Find(c)
		if f == nil {
			if tc.wantOK {
				t.Errorf("%s: no filter", tc.name)
			}
			continue
		}
		got, ok := f.Apply(c, tc.in)
		if ok != tc.wantOK {
			t.Errorf("%s (%s): ok=%v, want %v\n%s", tc.name, f.Name(), ok, tc.wantOK, got)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing from\n%s", tc.name, w, got)
			}
		}
	}
}

func TestApplyFast(t *testing.T) {
	gen := func(n int, line func(i int) string) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(line(i))
			b.WriteByte('\n')
		}
		return b.String()
	}
	cases := map[string]struct {
		argv []string
		in   string
	}{
		"tsc": {[]string{"tsc"}, gen(50000, func(i int) string {
			return fmt.Sprintf("src/f%d.ts(%d,3): error TS2554: Expected %d arguments, but got 1.", i%500, i, i%9)
		})},
		"eslint": {[]string{"eslint", "."}, gen(50000, func(i int) string {
			if i%50 == 0 {
				return fmt.Sprintf("/home/user/src/app/src/f%d.js", i)
			}
			return fmt.Sprintf("  %d:%d  error  Unexpected var, use let or const instead  no-var", i, i%80)
		})},
		"npm-install": {[]string{"npm", "install"}, gen(50000, func(i int) string {
			return fmt.Sprintf("npm http fetch GET 200 https://registry.npmjs.org/pkg%d 12ms (cache miss)", i)
		}) + "added 50000 packages in 3m"},
	}
	for name, tc := range cases {
		f := byName(name)
		c := ctx(1, tc.argv...)
		start := time.Now()
		if _, ok := f.Apply(c, tc.in); !ok {
			t.Errorf("%s bailed", name)
		}
		el := time.Since(start)
		t.Logf("%s: 50k lines in %v", name, el.Round(time.Millisecond))
		if el > time.Second && !raceEnabled {
			t.Errorf("%s: %v for 50k lines", name, el)
		}
	}
}
