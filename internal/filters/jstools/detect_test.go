package jstools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

type detectCase struct {
	fixture.Case
	tool string
}

func loadDetectCases(t *testing.T) []detectCase {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("testdata", "detect", "*.txt"))
	var out []detectCase
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		c, err := fixture.Read("testdata", "detect", name)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join("testdata", "detect", name+".meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Tool string }
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		tool, _, _ := strings.Cut(m.Tool, ",")
		out = append(out, detectCase{c, tool})
	}
	if len(out) < 4 {
		t.Fatalf("only %d detect captures", len(out))
	}
	return out
}

func TestLintDetectorCaptures(t *testing.T) {
	detectors := map[string]struct {
		detect func(string) bool
		filter engine.Filter
		argv   []string
	}{
		"tsc":    {detectTSC, tsc{}, []string{"tsc"}},
		"eslint": {detectESLint, eslint{}, []string{"eslint"}},
	}
	for _, dc := range loadDetectCases(t) {
		t.Run(dc.Name, func(t *testing.T) {
			clean := dc.Clean()
			for name, d := range detectors {
				if name != dc.tool && d.detect(clean) {
					t.Errorf("%s detector fired on %q (%s)", name, dc.Meta.Argv, dc.Meta.Description)
				}
			}
			d, mine := detectors[dc.tool]
			if !mine {
				return
			}
			if !d.detect(clean) {
				t.Fatalf("%s detector did not fire", dc.tool)
			}
			c := dc.Context()
			out, shape := engine.GenericShape(c, clean)
			if shape != "detected:"+dc.tool {
				t.Fatalf("shape %q", shape)
			}
			direct, ok := d.filter.Apply(&engine.Context{Argv: d.argv, Exit: c.Exit, Cwd: c.Cwd, Home: c.Home}, clean)
			if !ok || direct != out {
				t.Fatalf("detected view differs from the %s filter's own view", dc.tool)
			}
			fixture.Golden(t, "detect", dc.Name, out)
			if miss := fixture.ErrorMessagesMissing(clean, out); len(miss) > 0 {
				t.Errorf("error messages missing: %q", miss)
			}
			if miss := fixture.LocationsMissing(clean, out); len(miss) > 0 {
				t.Errorf("locations missing: %q", miss)
			}
			for _, ln := range strings.Split(clean, "\n") {
				if eslintSummaryRe.MatchString(ln) || strings.HasPrefix(ln, "Script ") || strings.HasPrefix(ln, "npx ") ||
					strings.HasPrefix(ln, "Command failed") || ln == "rake aborted!" {
					if !strings.Contains(out, ln) {
						t.Errorf("line not kept verbatim: %q", ln)
					}
				}
			}
		})
	}
}

func TestLintDetectorsReject(t *testing.T) {
	for name, s := range map[string]string{
		"tsc code in prose":      "see error TS2322 in the docs: Type 'string' is not assignable\n",
		"MSBuild C#":             "Program.cs(12,5): error CS1002: ; expected\n",
		"tsc pretty, no Found":   "src/a.ts:3:7 - error TS2322: Type 'string' is not assignable to type 'number'.\n",
		"ts-loader":              "ERROR in src/a.ts:3:7\nTS2322: Type 'string' is not assignable to type 'number'.\n",
		"deno check":             "error: TS2322 [ERROR]: Type 'string' is not assignable to type 'number'.\n    at file:///w/a.ts:3:7\n",
		"eslint without summary": "/w/src/a.js\n  1:5  error  'x' is assigned a value but never used  no-unused-vars\n",
		"eslint summary only":    "✖ 3 problems (3 errors, 0 warnings)\n",
		"eslint unix format":     "/w/src/a.js:1:5: 'x' is assigned a value but never used [Error/no-unused-vars]\n\n1 problem\n",
		"eslint JSON":            `[{"filePath":"/w/src/a.js","messages":[{"ruleId":"no-var","severity":2,"message":"Unexpected var","line":1,"column":1}],"errorCount":1}]` + "\n",
	} {
		if detectTSC(s) || detectESLint(s) {
			t.Errorf("%s: a detector fired on\n%s", name, s)
		}
	}
	plain := "src/a.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'.\n"
	pretty := "src/a.ts:3:7 - error TS2322: Type 'string' is not assignable to type 'number'.\n\n3 const x: number = \"a\";\n        ~\n\n\nFound 1 error in src/a.ts:3\n"
	stylish := "\n/w/src/a.js\n  1:5  error  'x' is assigned a value but never used  no-unused-vars\n\n✖ 1 problem (1 error, 0 warnings)\n"
	if !detectTSC(plain) || !detectTSC(pretty) || detectESLint(plain) {
		t.Error("tsc output: detectors disagree")
	}
	if !detectESLint(stylish) || detectTSC(stylish) {
		t.Error("eslint output: detectors disagree")
	}
}

func TestTSCDetectorNeedsAPathAtLineStart(t *testing.T) {
	diag := "src/a.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'."
	pretty := "src/a.ts:3:7 - error TS2322: Type 'string' is not assignable to type 'number'."
	for name, prefix := range map[string]string{
		"turbo":          "web:typecheck: ",
		"pnpm -r":        "packages/web build: ",
		"unified diff +": "+",
		"unified diff -": "-",
		"grep -n":        "ci.log:12:",
		"compose":        "app-1  | ",
		"CI timestamp":   "2026-09-01T12:00:00.1234567Z ",
		"quote":          "> ",
		"indent":         "    ",
		"tab":            "\t",
		"table":          "| ",
		"comment":        "# ",
	} {
		s := strings.Repeat(prefix+diag+"\n", 3) + prefix + "Found 3 errors.\n"
		if detectTSC(s) {
			t.Errorf("%s: tsc detector fired on %q", name, prefix+diag)
		}
		if p := strings.Repeat(prefix+pretty+"\n", 3) + "Found 3 errors.\n"; detectTSC(p) {
			t.Errorf("%s: tsc detector fired on %q", name, prefix+pretty)
		}
	}
	for _, file := range []string{"src/a.ts", "./src/a.ts", "../pkg/src/a.ts", "/home/user/src/app/src/a.ts",
		`C:\src\app\a.ts`, "C:/src/app/a.ts", "packages/web/src/index.tsx", "a.d.ts"} {
		if !detectTSC(file + "(3,7): error TS2322: Type 'string' is not assignable to type 'number'.\n") {
			t.Errorf("tsc detector missed a diagnostic for %s", file)
		}
	}
	for _, file := range []string{"x:y/a.ts", "C:a.ts", `C:\a.ts:x`} {
		if tscPathLike(file) {
			t.Errorf("tscPathLike(%q) = true", file)
		}
	}
}
