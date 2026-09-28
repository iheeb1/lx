package jstools

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/app", Home: "/home/user"}
}

func byName(name string) engine.Filter {
	for _, f := range engine.Filters() {
		if f.Name() == name {
			return f
		}
	}
	return nil
}

func TestMatch(t *testing.T) {
	cases := []struct {
		argv string
		want string
	}{
		{"tsc --noEmit", "tsc"},
		{"/usr/local/bin/tsc -p .", "tsc"},
		{"node_modules/.bin/tsc -b", "tsc"},
		{"npx tsc --noEmit", "tsc"},
		{"npx -y -p typescript tsc --noEmit", "tsc"},
		{"npx tsc@5.4.2 --noEmit", "tsc"},
		{"npx --package=typescript -- tsc", "tsc"},
		{"npm exec -- tsc", "tsc"},
		{"npm x tsc", "tsc"},
		{"pnpm exec tsc", "tsc"},
		{"pnpm tsc --noEmit", "tsc"},
		{"pnpm dlx tsc", "tsc"},
		{"yarn tsc", "tsc"},
		{"yarn run tsc", "tsc"},
		{"bunx tsc", "tsc"},
		{"bun x tsc", "tsc"},
		{"node node_modules/typescript/bin/tsc", "tsc"},
		{"vue-tsc --noEmit", "tsc"},
		{"tsc --watch", "tsc"},
		{"tsc --version", ""},
		{"tsc --showConfig", ""},
		{"tsc --init", ""},
		{"npx -c tsc", ""},
		{"eslint .", "eslint"},
		{"npx eslint src --ext .ts", "eslint"},
		{"npx eslint --format=stylish .", "eslint"},
		{"npx eslint -f stylish .", "eslint"},
		{"npx eslint -f json .", ""},
		{"eslint --format unix .", ""},
		{"eslint -o report.txt .", ""},
		{"eslint --print-config x.js", ""},
		{"yarn eslint .", "eslint"},
		{"node node_modules/eslint/bin/eslint.js .", "eslint"},
		{"npm install", "npm-install"},
		{"npm i -D typescript", "npm-install"},
		{"npm --prefix sub install", "npm-install"},
		{"npm ci", "npm-install"},
		{"npm uninstall codecov", "npm-install"},
		{"npm rm codecov", "npm-install"},
		{"npm update", "npm-install"},
		{"npm create vite@latest app -- --template react-ts", "npm-install"},
		{"npm init vite@latest", "npm-install"},
		{"npm init -y", ""},
		{"npm install --json", ""},
		{"pnpm install", "npm-install"},
		{"pnpm -C packages/a add lodash", "npm-install"},
		{"pnpm remove lodash", "npm-install"},
		{"yarn", "npm-install"},
		{"yarn --frozen-lockfile", "npm-install"},
		{"yarn --cwd app add lodash", "npm-install"},
		{"yarn --version", ""},
		{"bun install", "npm-install"},
		{"bun add zod", "npm-install"},
		{"npm ls", "npm-ls"},
		{"npm ls --all", "npm-ls"},
		{"npm list --depth=2", "npm-ls"},
		{"npm ls --json", ""},
		{"npm ls --parseable", ""},
		{"pnpm ls --depth 2", "npm-ls"},
		{"yarn list --depth=0", "npm-ls"},
		{"npm audit", "npm-audit"},
		{"npm audit fix", "npm-audit"},
		{"npm audit --json", ""},
		{"npm audit signatures", ""},
		{"pnpm audit", "npm-audit"},
		{"yarn audit", "npm-audit"},
		{"npm outdated", "npm-outdated"},
		{"pnpm outdated", "npm-outdated"},
		{"yarn outdated", "npm-outdated"},
		{"npm outdated --json", ""},
		{"npm run build", "npm-run"},
		{"npm run-script build", "npm-run"},
		{"npm run lint", "npm-run"},
		{"npm run typecheck", "npm-run"},
		{"npm run build:prod", "npm-run"},
		{"npm run lint:fix", "npm-run"},
		{"npm -w web run build", "npm-run"},
		{"npm run build -- --watch", ""},
		{"npm run build:watch", ""},
		{"npm run dev", ""},
		{"npm run test", ""},
		{"npm test", ""},
		{"npm start", ""},
		{"pnpm build", "npm-run"},
		{"pnpm run lint", "npm-run"},
		{"yarn build", "npm-run"},
		{"yarn run typecheck", "npm-run"},
		{"yarn test", ""},
		{"bun run build", "npm-run"},
		{"bun build ./index.ts --outdir out", ""},
		{"vite build", "npm-run"},
		{"npx vite build", "npm-run"},
		{"vite", ""},
		{"vite build --watch", ""},
		{"npx next build", "npm-run"},
		{"next dev", ""},
		{"webpack --mode production", "npm-run"},
		{"npx webpack", "npm-run"},
		{"webpack serve", ""},
		{"jest", ""},
		{"npx vitest run", ""},
		{"git status", ""},
		{"", ""},
	}
	for _, tc := range cases {
		f := engine.Find(ctx(0, strings.Fields(tc.argv)...))
		got := ""
		if f != nil {
			got = f.Name()
		}
		if got != tc.want {
			t.Errorf("%q: filter %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestStream(t *testing.T) {
	for argv, want := range map[string]bool{"tsc -w": true, "npx tsc --watch --noEmit": true, "tsc --noEmit": false} {
		c := ctx(0, strings.Fields(argv)...)
		if got := (tsc{}).Stream(c); got != want {
			t.Errorf("%q: Stream = %v, want %v", argv, got, want)
		}
	}
}

func TestBails(t *testing.T) {
	localizedTSC := "src/a.ts(1,7): erreur TS2304: Impossible de trouver le nom 'foo'.\nsrc/b.ts(2,1): erreur TS2304: Impossible de trouver le nom 'bar'."
	cases := []struct {
		filter, argv, in string
	}{
		{"tsc", "tsc", ""},
		{"tsc", "tsc", "hello"},
		{"tsc", "tsc", localizedTSC},
		{"tsc", "npx tsc", "This is not the tsc command you are looking for\n\nTo get access to the TypeScript compiler, tsc, from the command line either:\n- Use npm install typescript to first add TypeScript to your project before using npx"},
		{"eslint", "eslint .", ""},
		{"eslint", "eslint .", "single line"},
		{"eslint", "eslint .", "Oops! Something went wrong! :(\n\nESLint: 8.57.0\n\nESLint couldn't find a configuration file."},
		{"eslint", "eslint .", "/home/user/src/app/a.js\n  1:1  Fehler  Unerwartetes var  no-var\n\n✖ 1 Problem"},
		{"npm-install", "npm install", ""},
		{"npm-install", "npm install", "some unrelated text\nand more"},
		{"npm-ls", "npm ls", "(empty)"},
		{"npm-outdated", "npm outdated", "Paquet  Actuel  Voulu"},
		{"npm-audit", "yarn audit", "┌──────┐\n│ weird │ table │\n└──────┘"},
		{"npm-run", "npm run build", "\n> app@1.0.0 build\n> custom-bundler\n\nbundled 3 files"},
		{"npm-run", "npm run build", ""},
	}
	for _, tc := range cases {
		f := byName(tc.filter)
		if got, ok := f.Apply(ctx(1, strings.Fields(tc.argv)...), tc.in); ok {
			t.Errorf("%s on %q: ok=true, want bail; got\n%s", tc.filter, tc.in, got)
		}
	}
}

func TestNoInventedVerdicts(t *testing.T) {
	cases := []struct {
		filter, argv, in string
	}{
		{"npm-run", "npm run build", "\n> app@1.0.0 build\n> vite build\n\nvite v5.4.0 building for production...\ntransforming...\n✓ 12 modules transformed.\ndist/index.html  0.46 kB │ gzip: 0.30 kB\ndist/assets/index-abc.js  143.2 kB │ gzip: 46.1 kB\n✓ built in 1.2s\nnpm error Lifecycle script `build` failed with error:\nnpm error code 1\nnpm error command failed\nnpm error command sh -c vite build && node scripts/postbuild.js"},
		{"npm-install", "npm install", "added 12 packages in 2s\n\nnpm error code 1\nnpm error path /home/user/src/app/node_modules/sharp\nnpm error command failed\nnpm error command sh -c node install/check\nnpm error gyp ERR! stack Error: `make` failed with exit code: 2"},
		{"tsc", "tsc", "src/a.ts(1,7): error TS2304: Cannot find name 'foo'.\n\nFound 0 errors. Watching for file changes."},
		{"eslint", "eslint .", "/home/user/src/app/a.js\n  1:1  warning  Unexpected console statement  no-console\n\n✖ 1 problem (0 errors, 1 warning)\n\nESLint found too many warnings (maximum: 0)."},
	}
	for _, tc := range cases {
		f := byName(tc.filter)
		c := ctx(1, strings.Fields(tc.argv)...)
		got, ok := f.Apply(c, tc.in)
		if !ok {
			t.Fatalf("%s bailed", tc.filter)
		}
		for _, word := range []string{"passed", "success", "No errors", "no problems", "OK"} {
			if strings.Contains(got, word) && !strings.Contains(tc.in, word) {
				t.Errorf("%s invented %q:\n%s", tc.filter, word, got)
			}
		}
		if m := fixture.ErrorLinesMissing(tc.in, got); len(m) > 0 {
			t.Errorf("%s dropped error lines %q:\n%s", tc.filter, m, got)
		}
	}
}

func TestTSCPrettyShapes(t *testing.T) {
	in := strings.Join([]string{
		"src/a.ts:10:10 - error TS2322: Type '{ a: number; b: number; }' is not assignable to type 'string'.",
		"",
		" 10   return {",
		"             ~",
		" 11     a: 1,",
		"    ~~~~~~~~~",
		"...",
		" 18     throw new Error('failed')",
		"    ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~",
		" 19   }",
		"    ~~~",
		"",
		"  src/b.ts:3:5",
		"    3   x: string",
		"        ~",
		"    The expected type comes from property 'x' which is declared here on type 'T'",
		"    A second, file-less related message.",
		"",
		"src/c.ts:1:1 - error TS1128: Declaration or statement expected.",
		"",
		"1",
		"",
		"",
		"",
		"Found 2 errors in 2 files.",
		"",
		"Errors  Files",
		"     1  src/a.ts:10",
		"     1  src/c.ts:1",
	}, "\n")
	got, ok := tsc{}.Apply(ctx(2, "tsc", "--pretty"), in)
	if !ok {
		t.Fatal("bailed")
	}
	want := strings.Join([]string{
		"src/a.ts:10:10 - error TS2322: Type '{ a: number; b: number; }' is not assignable to type 'string'.",
		"  related src/b.ts:3:5 - The expected type comes from property 'x' which is declared here on type 'T'",
		"    A second, file-less related message.",
		"src/c.ts:1:1 - error TS1128: Declaration or statement expected.",
		"",
		"Found 2 errors in 2 files.",
	}, "\n")
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	in2 := "src/a.ts:1:1 - error TS1128: Declaration or statement expected.\n\n1 x\n  ~\n\nFound 2 errors in 2 files.\n\nErrors  Files\n     1  src/a.ts:1\n     1  src/other.ts:4"
	got, _ = tsc{}.Apply(ctx(2, "tsc"), in2)
	if !strings.Contains(got, "src/other.ts:4") || !strings.Contains(got, "Errors  Files") {
		t.Errorf("table with an unnamed file was dropped:\n%s", got)
	}
}

func TestTSCPlainIsVerbatim(t *testing.T) {
	in := "npm warn exec The following package was not found and will be installed: typescript@5.4.2\nsrc/a.ts(1,7): error TS2304: Cannot find name 'foo'.\nsrc/b.ts(2,1): error TS2322: Type 'string' is not assignable to type 'number'.\n  Type 'x' is not assignable to type 'y'.\nerror TS5023: Unknown compiler option 'foo'."
	got, ok := tsc{}.Apply(ctx(2, "npx", "tsc"), in)
	if !ok || got != in {
		t.Errorf("ok=%v, got\n%s", ok, got)
	}
}

func TestTSCFactorsRepeatsAboveThreshold(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "src/f%d.ts(%d,5): error TS2304: Cannot find name 'x'.\n", i%7, i+1)
	}
	b.WriteString("src/z.ts(1,1): error TS2322: Type 'a' is not assignable to type 'b'.\n")
	in := strings.TrimSpace(b.String())
	got, ok := tsc{}.Apply(ctx(2, "tsc"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(got, "error TS2304: Cannot find name 'x'. [×100, every location:]") {
		t.Errorf("not factored:\n%s", got)
	}
	if m := fixture.LocationsMissing(in, got); len(m) > 0 {
		t.Errorf("locations missing: %v", m)
	}
	for i := 0; i < 100; i++ {
		if loc := fmt.Sprintf("src/f%d.ts(%d,5)", i%7, i+1); !strings.Contains(got, loc) {
			t.Errorf("location %s missing", loc)
		}
	}
	if !strings.Contains(got, "src/z.ts(1,1): error TS2322") {
		t.Error("unique diagnostic not verbatim")
	}
}

func TestESLintShapes(t *testing.T) {
	in := strings.Join([]string{
		"",
		"/home/user/src/app/src/a.js",
		"   1:1   error    Unexpected var, use let or const instead  no-var",
		"   2:1   error    Unexpected var, use let or const instead  no-var",
		"   3:1   error    Unexpected var, use let or const instead  no-var",
		"   4:1   error    Unexpected var, use let or const instead  no-var",
		"   5:10  error    'error' is defined but never used         no-unused-vars",
		"   6:1   warning  Unexpected console statement              no-console",
		"",
		"/home/user/src/app/src/b.js",
		"  1:9  error  Parsing error: Unexpected token ;",
		"",
		"✖ 7 problems (6 errors, 1 warning)",
		"  4 errors and 0 warnings potentially fixable with the `--fix` option.",
		"",
	}, "\n")
	got, ok := eslint{}.Apply(ctx(1, "eslint", "."), in)
	if !ok {
		t.Fatal("bailed")
	}
	want := strings.Join([]string{
		"src/a.js",
		"  error  Unexpected var, use let or const instead  no-var  ×4: 1:1 2:1 3:1 4:1",
		"   5:10  error    'error' is defined but never used         no-unused-vars",
		"   6:1   warning  Unexpected console statement              no-console",
		"",
		"src/b.js",
		"  1:9  error  Parsing error: Unexpected token ;",
		"",
		"✖ 7 problems (6 errors, 1 warning)",
		"  4 errors and 0 warnings potentially fixable with the `--fix` option.",
	}, "\n")
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestESLintHugeIsGrouped(t *testing.T) {
	var b strings.Builder
	for f := 0; f < 400; f++ {
		fmt.Fprintf(&b, "/home/user/src/app/src/module%03d/index.js\n", f)
		for l := 1; l <= 12; l++ {
			fmt.Fprintf(&b, "  %d:1  error  Unexpected var, use let or const instead  no-var\n", l*3)
		}
		if f%100 == 0 {
			fmt.Fprintf(&b, "  99:5  error  'unused%d' is defined but never used  no-unused-vars\n", f)
		}
		b.WriteString("\n")
	}
	b.WriteString("✖ 4804 problems (4804 errors, 0 warnings)\n")
	in := b.String()
	c := ctx(1, "eslint", ".")
	got, ok := eslint{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(got, "problems grouped by message") {
		t.Fatalf("not grouped:\n%s", got[:min(len(got), 2000)])
	}
	missing := checkESLintFactored(t, c, in, got, fixture.ErrorLinesMissing(in, got))
	missing = without(missing, func(ln string) bool {
		return !strings.HasPrefix(ln, " ") && strings.Contains(got, engine.Relativize(c, ln))
	})
	for _, m := range missing {
		t.Errorf("unaccounted: %q", m)
	}
	if !strings.Contains(got, "✖ 4804 problems (4804 errors, 0 warnings)") {
		t.Error("summary not verbatim")
	}
	res := engine.Process(c, in, engine.Options{})
	if res.OutTokens > engine.DefaultBudget || strings.Contains(res.Output, "lines omitted") {
		t.Errorf("grouped output still hits the budget: %d tokens", res.OutTokens)
	}
}

func TestESLintHugeUnique(t *testing.T) {
	var b strings.Builder
	for f := 0; f < 300; f++ {
		fmt.Fprintf(&b, "/home/user/src/app/src/m%d.js\n", f)
		for l := 1; l <= 5; l++ {
			fmt.Fprintf(&b, "  %d:5  error  'v%d_%d' is defined but never used  no-unused-vars\n", l, f, l)
		}
		b.WriteString("\n")
	}
	b.WriteString("✖ 1500 problems (1500 errors, 0 warnings)\n")
	in := b.String()
	c := ctx(1, "eslint", ".")
	got, ok := eslint{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	missing := checkESLintFactored(t, c, in, got, fixture.ErrorLinesMissing(in, got))
	missing = without(missing, func(ln string) bool {
		return !strings.HasPrefix(ln, " ") && strings.Contains(got, engine.Relativize(c, ln))
	})
	if len(missing) > 0 {
		t.Errorf("%d unaccounted, e.g. %q", len(missing), missing[0])
	}
	res := engine.Process(c, in, engine.Options{})
	if !strings.Contains(res.Output, "✖ 1500 problems") || !strings.Contains(res.Output, "lines omitted") {
		t.Errorf("expected a budget-trimmed view keeping the summary")
	}
}

func TestInstallShapes(t *testing.T) {
	block := func(pkg string) string {
		return strings.Join([]string{
			"npm warn ERESOLVE overriding peer dependency",
			"npm warn While resolving: " + pkg,
			"npm warn Found: react@18.2.0",
			"npm warn node_modules/react",
			"npm warn   react@\"^18.2.0\" from the root project",
			"npm warn",
			"npm warn Could not resolve dependency:",
			"npm warn peer react@\"^17.0.0\" from " + pkg,
			"npm warn node_modules/" + strings.Split(pkg, "@")[0],
			"npm warn   " + pkg + " from the root project",
		}, "\n")
	}
	in := strings.Join([]string{
		block("old-a@1.0.0"), block("old-b@2.0.0"), block("old-c@3.0.0"),
		"npm warn deprecated left-pad@1.3.0: use String.prototype.padStart()",
		"npm warn deprecated har-validator@5.1.5: this library is no longer supported",
		"npm warn deprecated crashy@1.0.0: fails with TypeError on node 20",
		"npm warn EBADENGINE Unsupported engine {",
		"npm warn EBADENGINE   package: 'x@1.0.0',",
		"npm warn EBADENGINE }",
		"",
		"added 3 packages, and audited 4 packages in 1s",
		"",
		"1 package is looking for funding",
		"  run `npm fund` for details",
		"",
		"found 0 vulnerabilities",
		"npm notice",
		"npm notice New major version of npm available! 10.0.0 -> 11.0.0",
	}, "\n")
	got, ok := npmInstall{}.Apply(ctx(0, "npm", "install"), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{
		block("old-a@1.0.0"),
		`[+2 more "npm warn ERESOLVE overriding peer dependency" blocks, while resolving: old-b@2.0.0, old-c@3.0.0]`,
		"npm warn deprecated har-validator@5.1.5: this library is no longer supported",

		"npm warn deprecated left-pad@1.3.0: use String.prototype.padStart()",
		"npm warn deprecated crashy@1.0.0: fails with TypeError on node 20",
		"npm warn EBADENGINE Unsupported engine {",
		"added 3 packages, and audited 4 packages in 1s",
		"found 0 vulnerabilities",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	for _, gone := range []string{"npm notice", "looking for funding", "old-b@2.0.0 from the root project"} {
		if strings.Contains(got, gone) {
			t.Errorf("kept %q:\n%s", gone, got)
		}
	}
	if m := fixture.ErrorLinesMissing(in, got); len(m) > 0 {
		t.Errorf("error lines missing: %q", m)
	}
}

func TestYarnBerryAndBun(t *testing.T) {
	berry := "➤ YN0000: ┌ Resolution step\n➤ YN0002: │ my-app@workspace:. doesn't provide react (p1a2b3), requested by react-dom\n➤ YN0000: └ Completed in 0s 245ms\n➤ YN0000: ┌ Fetch step\n➤ YN0013: │ 2 packages were added to the project (+ 1.2 MiB).\n➤ YN0000: └ Completed in 1s 2ms\n➤ YN0000: · Done with warnings in 1s 300ms"
	got, ok := npmInstall{}.Apply(ctx(0, "yarn", "install"), berry)
	if !ok || strings.Contains(got, "┌ Resolution step") || !strings.Contains(got, "YN0002") || !strings.Contains(got, "Done with warnings") {
		t.Errorf("yarn berry: ok=%v\n%s", ok, got)
	}
	bun := "bun install v1.1.30 (7996d06b)\n\n + typescript@5.6.2\n\n 3 packages installed [812.00ms]"
	got, ok = npmInstall{}.Apply(ctx(0, "bun", "install"), bun)
	if !ok || got != strings.Replace(bun, "\n\n", "\n\n", -1) {
		t.Errorf("bun: ok=%v\n%s", ok, got)
	}
}

func TestAuditManyPaths(t *testing.T) {
	in := "# npm audit report\n\nsemver  <5.7.2\nSeverity: moderate\nsemver vulnerable to Regular Expression Denial of Service - https://github.com/advisories/GHSA-c2qf-rxjj-qqgw\nfix available via `npm audit fix`\nnode_modules/a/node_modules/semver\nnode_modules/b/node_modules/semver\nnode_modules/c/node_modules/semver\nnode_modules/d/node_modules/semver\nnode_modules/semver\n\n1 moderate severity vulnerability\n\nTo address all issues, run:\n  npm audit fix"
	got, ok := npmAudit{}.Apply(ctx(1, "npm", "audit"), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{
		"semver  <5.7.2", "Severity: moderate",
		"semver vulnerable to Regular Expression Denial of Service - GHSA-c2qf-rxjj-qqgw",
		"fix available via `npm audit fix`",
		"node_modules/a/node_modules/semver\nnode_modules/b/node_modules/semver\n[+3 more paths]",
		"1 moderate severity vulnerability", "  npm audit fix",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestNextRouteTableWithSizes(t *testing.T) {
	in := strings.Join([]string{
		"   ▲ Next.js 14.2.3",
		"",
		"   Creating an optimized production build ...",
		" ✓ Compiled successfully",
		"",
		"Route (app)                              Size     First Load JS",
		"┌ ○ /                                    5.2 kB         89.4 kB",
		"├ ○ /_not-found                          871 B          85.1 kB",
		"└ ƒ /api/hello                           0 B                0 B",
		"+ First Load JS shared by all            84.2 kB",
		"  ├ chunks/184-d3d1a7ff3ed2d5d4.js       28.9 kB",
		"  ├ chunks/30b509c0-7e3aa6ff6c3c4d4e.js  53.3 kB",
		"  └ other shared chunks (total)          1.95 kB",
		"",
		"",
		"○  (Static)   prerendered as static content",
		"ƒ  (Dynamic)  server-rendered on demand",
		"",
	}, "\n")
	got, ok := npmRun{}.Apply(ctx(0, "npx", "next", "build"), in)
	want := "   ▲ Next.js 14.2.3\n\n   Creating an optimized production build ...\n ✓ Compiled successfully\n\n[Route (app) table hidden: 3 routes; ○ Static ×2, ƒ Dynamic ×1]\n+ First Load JS shared by all            84.2 kB"
	if !ok || got != want {
		t.Errorf("ok=%v got\n%s\nwant\n%s", ok, got, want)
	}
}

func TestLsNamedKeepsDeduped(t *testing.T) {
	in := "app@1.0.0 /home/user/src/app\n├─┬ eslint@8.57.0\n│ └── debug@4.3.4 deduped\n└─┬ express@4.19.2\n  └── debug@2.6.9"
	got, ok := npmLs{}.Apply(ctx(0, "npm", "ls", "debug"), in)
	want := "app@1.0.0 /home/user/src/app\n  eslint@8.57.0\n    debug@4.3.4 deduped\n  express@4.19.2\n    debug@2.6.9"
	if !ok || got != want {
		t.Errorf("ok=%v got\n%s", ok, got)
	}
	got, _ = npmLs{}.Apply(ctx(0, "npm", "ls", "--all"), in)
	if !strings.Contains(got, "eslint@8.57.0 [+1 deduped]") || strings.Contains(got, "debug@4.3.4") {
		t.Errorf("--all did not fold:\n%s", got)
	}
}

func TestEmptyAndTinyInputs(t *testing.T) {
	for _, f := range engine.Filters() {
		for _, in := range []string{"", "\n", "x", "✖", "┌", "│ a │", "> a@1 build", "npm error"} {
			c := ctx(1, "npm", "run", "build")
			a1, ok1 := f.Apply(c, in)
			a2, ok2 := f.Apply(c, in)
			if a1 != a2 || ok1 != ok2 {
				t.Errorf("%s: not deterministic on %q", f.Name(), in)
			}
		}
	}
}

func TestHuge(t *testing.T) {
	gen := func(n int, line func(i int) string) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(line(i))
			b.WriteByte('\n')
		}
		return b.String()
	}
	cases := []struct {
		filter string
		argv   []string
		in     string
	}{
		{"tsc", []string{"tsc"}, gen(50000, func(i int) string {
			return fmt.Sprintf("src/f%d.ts(%d,3): error TS2554: Expected %d arguments, but got 1.", i%500, i, i%9)
		})},
		{"tsc", []string{"tsc", "--pretty"}, gen(50000, func(i int) string {
			switch i % 5 {
			case 0:
				return fmt.Sprintf("src/f%d.ts:%d:3 - error TS2304: Cannot find name 'x%d'.", i%300, i, i)
			case 2:
				return fmt.Sprintf("%d const y = x%d", i, i)
			case 3:
				return "      ~~"
			}
			return ""
		})},
		{"eslint", []string{"eslint", "."}, gen(50000, func(i int) string {
			if i%50 == 0 {
				return fmt.Sprintf("/home/user/src/app/src/f%d.js", i)
			}
			sev := "error"
			if i%3 == 0 {
				sev = "warning"
			}
			return fmt.Sprintf("  %d:%d  %s  Message number %d here  rule-%d", i, i%80, sev, i%97, i%13)
		})},
		{"npm-install", []string{"npm", "install"}, gen(50000, func(i int) string {
			return fmt.Sprintf("npm warn deprecated pkg%d@1.0.%d: message %d", i, i, i%10)
		})},
		{"npm-ls", []string{"npm", "ls", "--all"}, gen(50000, func(i int) string {
			return strings.Repeat("│ ", i%20) + "├── pkg" + fmt.Sprint(i) + "@1.0.0 deduped"
		})},
		{"npm-audit", []string{"npm", "audit"}, "# npm audit report\n\n" + gen(10000, func(i int) string {
			return fmt.Sprintf("pkg%d  <1.0.0\nSeverity: high\nbad thing %d - https://github.com/advisories/GHSA-aaaa-bbbb-%04d\nfix available via `npm audit fix`\nnode_modules/pkg%d\n", i, i, i, i)
		})},
		{"npm-audit", []string{"pnpm", "audit"}, gen(5000, func(i int) string {
			return "┌──────────┬──────────┐\n│ high     │ bad " + fmt.Sprintf("%04d", i) + " │\n├──────────┼──────────┤\n│ Package  │ pkg      │\n├──────────┼──────────┤\n│ Paths    │ .>a>pkg  │\n└──────────┴──────────┘"
		})},
		{"npm-run", []string{"npm", "run", "build"}, gen(50000, func(i int) string {
			return fmt.Sprintf("dist/assets/chunk-%d.js  %d.%02d kB │ gzip: 1.00 kB", i, i%900, i%100)
		}) + "✓ built in 3s"},
	}
	for _, tc := range cases {
		f := byName(tc.filter)
		c := ctx(1, tc.argv...)
		start := time.Now()
		got, ok := f.Apply(c, tc.in)
		el := time.Since(start)
		if !ok {
			t.Errorf("%s %v: bailed on huge input", tc.filter, tc.argv)
		}
		if el > 3*time.Second && !raceEnabled {
			t.Errorf("%s %v: %v on 50k lines", tc.filter, tc.argv, el)
		}
		res := engine.Process(c, tc.in, engine.Options{})
		if res.FilterPanic != "" {
			t.Errorf("%s: panic %s", tc.filter, res.FilterPanic)
		}
		t.Logf("%-12s %-14v %6d lines → %5d lines in %v; lx %d tokens [%s]", tc.filter, tc.argv, strings.Count(tc.in, "\n"),
			strings.Count(got, "\n")+1, el.Round(time.Millisecond), res.OutTokens, res.Filter)
	}
}
