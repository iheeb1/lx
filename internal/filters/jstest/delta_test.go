package jstest

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func rerun(t *testing.T, c *engine.Context, prev, cur string) string {
	t.Helper()
	res := engine.Process(c, cur, engine.Options{})
	delta, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 40", TurnsAgo: 1, Exit: c.Exit, Raw: prev}, cur, res.Output)
	if !ok {
		t.Fatalf("no delta; view:\n%s", res.Output)
	}
	if float64(tokens.Count(delta)) > 0.7*float64(tokens.Count(res.Output)) {
		t.Errorf("delta is not 30%% smaller than the view:\n%s", delta)
	}
	return delta
}

func swap(t *testing.T, s string, pairs ...string) string {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(s, pairs[i]) {
			t.Fatalf("fixture text %q not found", pairs[i])
		}
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return s
}

func cut(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	j := strings.Index(s[i+1:], to)
	if i < 0 || j < 0 {
		t.Fatalf("fixture span %q…%q not found", from, to)
	}
	return s[:i] + s[i+1+j:]
}

func contains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func lacks(t *testing.T, got string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(got, b) {
			t.Errorf("unexpected %q in:\n%s", b, got)
		}
	}
}

const jestNew = `  ● DateTime#toISOWeekDate() pads negative years

    expect(received).toBe(expected) // Object.is equality

    Expected: "-000001-W52-5"
    Received: "-1-W52-5"

      214 | test("DateTime#toISOWeekDate() pads negative years", () => {
    > 215 |   expect(DateTime.fromObject({ year: -1 }).toISOWeekDate()).toBe("-000001-W52-5");
          |                                                             ^
      216 | });

      at Object.toBe (test/datetime/format.test.js:215:61)

`

func TestDeltaJest(t *testing.T) {
	fc := fixture.Load(t, "node", "jest-fail")
	cur := cut(t, fc.Raw, "  ● Info.months lists all the months\n", "  ● Info.monthsFormat lists all the months\n")
	cur = cut(t, cur, "  ● Info.months lists all the months\n", "  ● Info.monthsFormat lists all the months\n")
	summary := strings.Index(cur, "Summary of all failing tests")
	cur = cur[:summary] + swap(t, cur[summary:], "FAIL test/datetime/tokenParse.test.js\n", jestNew+"FAIL test/datetime/tokenParse.test.js\n")
	cur = swap(t, cur,
		"PASS test/datetime/diff.test.js\n", jestNew+"PASS test/datetime/diff.test.js\n",
		"Tests:       10 failed, 1212 passed, 1222 total", "Tests:       10 failed, 1213 passed, 1223 total",
		"Time:        2.137 s", "Time:        2.304 s")
	delta := rerun(t, fc.Context(), fc.Raw, cur)
	contains(t, delta,
		"[lx: same command as lx show 40 (1 turn ago, still in your context) — only what changed:]\n",
		"new:\nFAIL test/datetime/format.test.js\n  ● DateTime#toISOWeekDate() pads negative years\n",
		`    Received: "-1-W52-5"`,
		"fixed: test/info/listers.test.js › Info.months lists all the months\n",
		"  test/datetime/format.test.js › DateTime#toISO() handles negative years (line 126)\n",
		"Test Suites: 3 failed, 55 passed, 58 total\nTests:       10 failed, 1213 passed, 1223 total")
	lacks(t, delta, `"-012345-05-25T09:23:54.123Z"`, "\nchanged:\n")
}

func TestDeltaVitest(t *testing.T) {
	fc := fixture.Load(t, "node", "vitest-fail")
	cur := cut(t, fc.Raw, " FAIL  test/double-slash.test.ts > cleanDoubleSlashes > http://foo.com//\n", " FAIL  test/double-slash.test.ts > cleanDoubleSlashes > http://foo.com/bar//foo/\n")
	cur = swap(t, cur,
		"     × http://foo.com// 0ms\n", "",
		"(5 tests | 4 failed) 7ms", "(5 tests | 3 failed) 6ms",
		"(34 tests | 1 failed) 8ms", "(35 tests | 2 failed) 9ms",
		`     × / with {"str":"&","str2":"%26"} 3ms`, `     × / with {"str":"&","str2":"%26"} 3ms`+"\n     × keeps array params 1ms",
		"\n\n Test Files", `

 FAIL  test/query.test.ts > withQuery > keeps array params
AssertionError: expected '/?a=1' to be '/?a=1&a=2' // Object.is equality

Expected: "/?a=1&a=2"
Received: "/?a=1"

 ❯ test/query.test.ts:88:40
     88|     expect(withQuery("/", { a: [1, 2] })).toBe("/?a=1&a=2");
       |                                        ^

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[7/7]⎯


 Test Files`,
		"Tests  7 failed | 482 passed (489)", "Tests  7 failed | 483 passed (490)",
		"Duration  368ms", "Duration  402ms")
	delta := rerun(t, fc.Context(), fc.Raw, cur)
	contains(t, delta,
		"new:\n FAIL  test/query.test.ts > withQuery > keeps array params\nAssertionError: expected '/?a=1' to be '/?a=1&a=2'",
		"fixed: test/double-slash.test.ts > cleanDoubleSlashes > http://foo.com//\n",
		"  test/encoding.test.ts > encodeQueryValue > a=1&b=2 (line 104)\n",
		" ❯ test/query.test.ts (35 tests | 2 failed) 9ms",
		" Test Files  3 failed | 10 passed (13)\n      Tests  7 failed | 483 passed (490)")
	lacks(t, delta, "Received: \"//foo//bar//\"")
}

const mochaNew = `
  9) app.listen()
       should wrap with an HTTP server:
     TypeError: server.address is not a function
      at Context.<anonymous> (test/app.listen.js:21:18)
      at process.processImmediate (node:internal/timers:504:21)
`

func TestDeltaMocha(t *testing.T) {
	fc := fixture.Load(t, "node", "mocha-fail")
	cur := cut(t, fc.Raw, "  8) res\n", "  9) res\n")
	cur = swap(t, cur,
		"        8) should set the response status code to 800", "        ✔ should set the response status code to 800",
		"        9) should set the response status code to 900", "        8) should set the response status code to 900",
		"    ✔ should wrap with an HTTP server", "    9) should wrap with an HTTP server",
		"  9) res\n", "  8) res\n",
		"  1252 passing (2s)", "  1252 passing (3s)")
	cur = strings.TrimRight(cur, "\n") + "\n" + mochaNew
	delta := rerun(t, fc.Context(), fc.Raw, cur)
	contains(t, delta,
		"new:\n  9) app.listen()\n       should wrap with an HTTP server:\n     TypeError: server.address is not a function\n",
		"fixed: res .status(code) accept valid ranges should set the response status code to 800\n",
		"  res .status(code) accept valid ranges should set the response status code to 900 (test/res.status.js:115)\n",
		"  1252 passing (3s)\n  9 failing")
	lacks(t, delta, "\nchanged:\n", `expected 700 "undefined"`)
}

func TestDeltaNpmTestUsesRunner(t *testing.T) {
	fc := fixture.Load(t, "node", "mocha-fail")
	items, ok := engine.ItemsOf(fc.Context(), fc.Clean())
	if !ok || len(items) != 9 {
		t.Fatalf("npm test → mocha: %d items (ok=%v), want 9", len(items), ok)
	}
	if items[0].Key != "req .subdomains when present should return an array" {
		t.Fatalf("first key %q", items[0].Key)
	}
}

func TestDeltaCaptures(t *testing.T) {
	for _, name := range []string{"jest-mixed", "jest-assertions", "vitest-mixed", "vitest-unhandled-only", "mocha-mixed", "mocha-uncaught"} {
		t.Run(name, func(t *testing.T) {
			fc, err := fixture.Read("testdata/captures", "", name)
			if err != nil {
				t.Fatal(err)
			}
			c := fc.Context()
			items, ok := engine.ItemsOf(c, fc.Clean())
			if !ok || len(items) == 0 {
				t.Fatalf("no items (ok=%v)", ok)
			}
			view := engine.Process(c, fc.Raw, engine.Options{}).Output
			for _, it := range items {
				for _, ln := range strings.Split(it.Block, "\n") {
					if !strings.Contains(view, strings.TrimSpace(ln)) {
						t.Errorf("item %q: line %q is not in the view", it.Key, ln)
					}
				}
			}
			delta, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 5", TurnsAgo: 3, Exit: c.Exit, Raw: fc.Raw}, fc.Raw, view)
			if ok && !strings.HasPrefix(delta, "[lx: output identical to lx show 5 (3 turns ago") {
				t.Fatalf("identical rerun:\n%s", delta)
			}
		})
	}
}
