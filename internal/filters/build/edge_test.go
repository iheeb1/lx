package build

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/demo", Home: "/home/user"}
}

func mine(name string) bool {
	switch name {
	case "make", "cmake-build", "ninja", "cc", "cargo", "cargo-test", "gradle", "maven":
		return true
	}
	return false
}

func TestMatch(t *testing.T) {
	cases := []struct {
		argv string
		want string
	}{
		{"make", "make"}, {"gmake all", "make"}, {"/usr/bin/make -j8", "make"}, {"make -C lib test", "make"},
		{"make --jobs=4 install", "make"}, {"make VERBOSE=1", "make"}, {"make -Oline", "make"}, {"make -kj4", "make"},
		{"mingw32-make", "make"}, {"make -f build.mk -I inc", "make"},
		{"make -n", ""}, {"make --dry-run", ""}, {"make -p", ""}, {"make -q all", ""}, {"make -np", ""},
		{"make --version", ""}, {"make -d", ""}, {"make --trace", ""},
		{"cmake --build build", "cmake-build"}, {"cmake --build=build", "cmake-build"}, {"cmake -S . -B build", ""},
		{"ninja", "ninja"}, {"ninja -C build", "ninja"}, {"ninja -t targets", ""}, {"ninja -tquery", ""}, {"ninja -n", ""},
		{"cc -c x.c", "cc"}, {"gcc -Wall x.c -o x", "cc"}, {"g++-13 -std=c++20 a.cpp", "cc"}, {"clang++ -c a.cc", "cc"},
		{"/usr/bin/clang -fsyntax-only a.c", "cc"}, {"x86_64-linux-gnu-gcc -c a.c", "cc"}, {"ccache gcc -c a.c", "cc"},
		{"clang-18 a.c", "cc"},
		{"gcc -E a.c", ""}, {"gcc -M a.c", ""}, {"gcc -MM a.c", ""}, {"gcc --version", ""}, {"gcc -S -o - a.c", ""},
		{"clang -print-search-dirs", ""}, {"gcc -dumpmachine", ""}, {"cpp a.c", ""}, {"clang -### a.c", ""},
		{"cargo build", "cargo"}, {"cargo +nightly build --release", "cargo"}, {"cargo --color never check", "cargo"},
		{"cargo b", "cargo"}, {"cargo clippy -- -D warnings", "cargo"}, {"cargo install ripgrep", "cargo"},
		{"cargo fetch", "cargo"}, {"cargo update -p serde", "cargo"}, {"/home/user/.cargo/bin/cargo check", "cargo"},
		{"cargo test", "cargo-test"}, {"cargo t --lib", "cargo-test"}, {"cargo test -- --nocapture", "cargo-test"},
		{"cargo test -- --list", ""}, {"cargo test -- --format json -Z unstable-options", ""}, {"cargo test --no-run", ""},
		{"cargo build --message-format=json", ""}, {"cargo run", ""}, {"cargo doc", ""}, {"cargo fmt", ""},
		{"gradle build", "gradle"}, {"./gradlew test", "gradle"}, {"gradlew.bat build", "gradle"},
		{"./gradlew :app:test --tests CalcTest", "gradle"},
		{"gradle tasks", ""}, {"./gradlew --version", ""}, {"./gradlew dependencies", ""}, {"./gradlew :app:dependencies", ""},
		{"./gradlew build --continuous", "gradle"},
		{"mvn test", "maven"}, {"./mvnw -B package", "maven"}, {"mvn -B -ntp verify", "maven"}, {"mvn -pl api -am install", "maven"},
		{"mvn -v", ""}, {"mvn dependency:tree", ""}, {"mvn help:effective-pom", ""},
	}
	for _, tc := range cases {
		c := ctx(0, strings.Fields(tc.argv)...)
		f := engine.Find(c)
		got := ""
		if f != nil && mine(f.Name()) {
			got = f.Name()
		}
		if got != tc.want {
			t.Errorf("%q: Find = %q, want %q", tc.argv, got, tc.want)
		}
	}
}

var filterArgv = map[string][]string{
	"make":        {"make"},
	"cmake-build": {"cmake", "--build", "build"},
	"ninja":       {"ninja"},
	"cc":          {"cc", "-c", "a.c"},
	"cargo":       {"cargo", "build"},
	"cargo-test":  {"cargo", "test"},
	"gradle":      {"./gradlew", "build"},
	"maven":       {"mvn", "package"},
}

func apply(t *testing.T, exit int, argv []string, in string) (string, bool) {
	t.Helper()
	c := ctx(exit, argv...)
	f := engine.Find(c)
	if f == nil || !mine(f.Name()) {
		t.Fatalf("%v: no build filter", argv)
	}
	return f.Apply(c, in)
}

func TestEmptyAndUnknownBail(t *testing.T) {
	unknown := []string{
		"",
		"\n\n",
		"Bonjour le monde\nTout va bien",
		"Kompiliere foo v1.0\nFertig",
		"Construction réussie en 3 s",
		"Scanning for projects...\nBUILD SUCCESS",
		"{\n  \"name\": \"demo\",\n  \"ok\": true\n}",
	}
	for name, argv := range filterArgv {
		for _, in := range unknown {
			for _, exit := range []int{0, 2} {
				if got, ok := apply(t, exit, argv, in); ok {
					t.Errorf("%s exit %d on %q: want bail, got %q", name, exit, in, got)
				}
			}
		}
	}
}

func TestSingleLine(t *testing.T) {
	cases := []struct {
		name string
		exit int
		in   string
	}{
		{"make", 0, "make: Nothing to be done for `all'."},
		{"make", 2, "make: *** No targets specified and no makefile found.  Stop."},
		{"cc", 1, "a.c:1:10: fatal error: 'missing.h' file not found"},
		{"ninja", 0, "ninja: no work to do."},
		{"cargo", 0, "    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.02s"},
		{"cargo-test", 101, "error: no test target named `nope` in default-run packages"},
		{"gradle", 0, "BUILD SUCCESSFUL in 1s"},
		{"maven", 0, "[INFO] BUILD SUCCESS"},
	}
	for _, tc := range cases {
		got, ok := apply(t, tc.exit, filterArgv[tc.name], tc.in)
		if !ok || got != tc.in {
			t.Errorf("%s: got ok=%v %q, want the line unchanged", tc.name, ok, got)
		}
	}
}

func TestFailedWithPassLookingText(t *testing.T) {
	cases := []struct {
		name string
		exit int
		in   string
	}{
		{"make", 2, "gcc -c -o a.o a.c\ngcc -c -o b.o b.c\ngcc -c -o c.o c.c"},
		{"cmake-build", 2, "[ 50%] Building C object a.o\n[100%] Built target a"},
		{"cargo", 101, "   Compiling a v0.1.0\n   Compiling b v0.1.0\n    Finished `dev` profile [unoptimized] target(s) in 1.00s"},
		{"cargo-test", 101, "     Running unittests src/lib.rs (target/debug/deps/a-1)\n\nrunning 2 tests\ntest a ... ok\ntest b ... ok\n\ntest result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s"},
		{"gradle", 1, "> Task :compileJava UP-TO-DATE\n> Task :test UP-TO-DATE\n\nBUILD SUCCESSFUL in 1s\n2 actionable tasks: 2 up-to-date"},
		{"maven", 1, "[INFO] Scanning for projects...\n[INFO] Tests run: 3, Failures: 0, Errors: 0, Skipped: 0\n[INFO] BUILD SUCCESS"},
	}
	for _, tc := range cases {
		if got, ok := apply(t, tc.exit, filterArgv[tc.name], tc.in); ok {
			t.Errorf("%s exit %d: want bail, got:\n%s", tc.name, tc.exit, got)
		}
	}
}

func TestNativeShapes(t *testing.T) {
	t.Run("command before error kept, others counted", func(t *testing.T) {
		in := "cc -c -o a.o a.c\ncc -c -o b.o b.c\nb.c:3:5: error: expected ';' after expression\n    3 |   x = 1\n      |        ^\n      |        ;\n1 error generated.\nmake: *** [b.o] Error 1"
		got, ok := apply(t, 2, []string{"make"}, in)
		want := "[lx: hidden: 1 recipe command (cc)]\ncc -c -o b.o b.c\n" + strings.SplitN(in, "\n", 3)[2]
		if !ok || got != want {
			t.Fatalf("got ok=%v\n%s\nwant\n%s", ok, got, want)
		}
	})
	t.Run("directory announced for kept lines", func(t *testing.T) {
		in := "make[1]: Entering directory '/home/user/src/demo/lib'\ncc -c x.c\nx.c:1:1: error: unknown type name 'foo'\nmake[1]: *** [x.o] Error 1\nmake[1]: Leaving directory '/home/user/src/demo/lib'\nmake: *** [lib] Error 2"
		got, _ := apply(t, 2, []string{"make"}, in)

		if !strings.HasPrefix(got, "[lx: hidden: 1 directory line]\nmake[1]: Entering directory '/home/user/src/demo/lib'\ncc -c x.c\n") {
			t.Fatalf("directory not announced before the kept command:\n%s", got)
		}
		if strings.Contains(got, "Leaving directory") {
			t.Fatalf("leaving line shown:\n%s", got)
		}
	})
	t.Run("long include chain folded, repeated one hidden", func(t *testing.T) {
		chain := "In file included from a.c:1:\nIn file included from b.h:2:\nIn file included from c.h:3:\nIn file included from d.h:4:\n"
		in := chain + "e.h:5:1: error: unknown type name 'bad'\n" + chain + "e.h:9:1: error: unknown type name 'worse'\n2 errors generated."
		got, _ := apply(t, 1, []string{"cc", "-c", "a.c"}, in)
		want := "[lx: hidden: 1 repeated include chain]\nIn file included from a.c:1:\n… 2 more include lines …\nIn file included from d.h:4:\ne.h:5:1: error: unknown type name 'bad'\ne.h:9:1: error: unknown type name 'worse'\n2 errors generated."
		if got != want {
			t.Fatalf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("header error repeated per translation unit", func(t *testing.T) {
		var b strings.Builder
		for _, tu := range []string{"a", "b", "c"} {
			fmt.Fprintf(&b, "cc -c %s.c\nIn file included from %s.c:1:\ninc/x.h:3:1: error: unknown type name 'u8'\n    3 | u8 v;\n      | ^\n1 error generated.\nmake: *** [%s.o] Error 1\n", tu, tu, tu)
		}
		got, _ := apply(t, 2, []string{"make", "-k"}, b.String())
		if strings.Count(got, "unknown type name 'u8'") != 1 || !strings.Contains(got, "inc/x.h:3:1: error: unknown type name 'u8' [×3]") {
			t.Fatalf("duplicate header error not grouped:\n%s", got)
		}
		for _, tu := range []string{"a", "b", "c"} {
			if !strings.Contains(got, "make: *** ["+tu+".o] Error 1") {
				t.Fatalf("make error for %s lost:\n%s", tu, got)
			}
		}
	})
	t.Run("error-class warning never merged by message", func(t *testing.T) {
		in := "a.c:1:1: warning: could not open 'x.dat' [-Wmissing-file]\na.c:9:1: warning: could not open 'x.dat' [-Wmissing-file]\na.c:20:1: warning: could not open 'x.dat' [-Wmissing-file]\n3 warnings generated."
		got, _ := apply(t, 0, []string{"cc", "-c", "a.c"}, in)
		for _, ln := range strings.Split(in, "\n") {
			if !strings.Contains(got, ln) {
				t.Fatalf("line %q lost:\n%s", ln, got)
			}
		}
	})
	t.Run("warning source excerpts are code, not errors", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(&b, "a.c:%d:5: warning: unused variable 'e%d' [-Wunused-variable]\n %4d |     int e%d; if (err) goto fail;\n      |         ^\n", i, i, i, i)
		}
		got, ok := apply(t, 0, []string{"cc", "-c", "a.c"}, b.String())
		if !ok || strings.Contains(got, "[lx: error lines from the full output]") {
			t.Fatalf("guard re-added source excerpts:\n%s", got)
		}
		if !strings.Contains(got, "a.c:5:5: warning: unused variable 'e5' [-Wunused-variable]") {
			t.Fatalf("distinct warning lost:\n%s", got)
		}
	})
	t.Run("template cap with exact count", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 100; i++ {
			fmt.Fprintf(&b, "a.c:%d:5: warning: unused variable 'v%d' [-Wunused-variable]\n", i, i)
		}
		got, _ := apply(t, 0, []string{"cc", "-c", "a.c"}, b.String())
		if !strings.Contains(got, "[lx: +60 more warnings like the above not shown]") {
			t.Fatalf("cap marker missing:\n%s", got)
		}
	})
	t.Run("plain recipe echo delegated to the go filters", func(t *testing.T) {
		in := "go test ./...\n--- FAIL: TestX (0.00s)\n    x_test.go:9: got 1, want 2\nFAIL\nFAIL\texample.com/x\t0.01s\nFAIL\nmake: *** [test] Error 1"
		got, ok := apply(t, 2, []string{"make", "test"}, in)
		if !ok || !strings.Contains(got, "x_test.go:9: got 1, want 2") || !strings.HasSuffix(got, "make: *** [test] Error 1") {
			t.Fatalf("got ok=%v\n%s", ok, got)
		}
	})
	t.Run("gcc include chain with from-continuations and template context", func(t *testing.T) {
		in := "In file included from inc/a.h:3,\n                 from inc/b.h:7,\n                 from inc/c.h:1,\n                 from src/x.cpp:2:\n" +
			"inc/d.h: In instantiation of 'T f(T) [with T = int]':\nsrc/x.cpp:9:12:   required from here\n" +
			"inc/d.h:5:14: error: no match for 'operator+' (operand types are 'S' and 'int')\n    5 |   return a + 1;\n      |          ~~^~~\n"
		got, ok := apply(t, 1, []string{"g++", "-c", "src/x.cpp"}, in)
		want := "In file included from inc/a.h:3,\n… 2 more include lines …\n                 from src/x.cpp:2:\n" +
			"inc/d.h: In instantiation of 'T f(T) [with T = int]':\nsrc/x.cpp:9:12:   required from here\n" +
			"inc/d.h:5:14: error: no match for 'operator+' (operand types are 'S' and 'int')\n    5 |   return a + 1;\n      |          ~~^~~"
		if !ok || got != want {
			t.Fatalf("got ok=%v\n%s\nwant\n%s", ok, got, want)
		}
	})
	t.Run("standalone notes grouped as notes", func(t *testing.T) {
		in := "a.c:1:1: note: remember 'x'\na.c:2:1: note: remember 'x'\na.c:3:1: note: remember 'x'\nlink.c:1:1: warning: old API [-Wdeprecated]"
		got, _ := apply(t, 0, []string{"cc", "-c", "a.c"}, in)
		if !strings.Contains(got, "[lx: same note at 2 more locations: a.c:2:1, a.c:3:1]") {
			t.Fatalf("got\n%s", got)
		}
	})
	t.Run("nothing to be done in sub-makes counted", func(t *testing.T) {
		in := "make[1]: Nothing to be done for `all'.\nmake[1]: Nothing to be done for `all'.\ncc -c -o a.o a.c\nmake: Nothing to be done for `install'."
		got, _ := apply(t, 0, []string{"make"}, in)
		want := "[lx: hidden: 1 recipe command (cc), 2 \"Nothing to be done\" lines]\nmake: Nothing to be done for `install'."
		if got != want {
			t.Fatalf("got\n%s\nwant\n%s", got, want)
		}
	})
}

func TestCargoShapes(t *testing.T) {
	t.Run("many ignored tests counted", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("     Running unittests src/lib.rs (target/debug/deps/a-1)\n\nrunning 15 tests\n")
		for i := 0; i < 15; i++ {
			fmt.Fprintf(&b, "test t%d ... ignored\n", i)
		}
		b.WriteString("\ntest result: ok. 0 passed; 0 failed; 15 ignored; 0 measured; 0 filtered out; finished in 0.00s\n")
		got, _ := apply(t, 0, []string{"cargo", "test"}, b.String())
		if strings.Count(got, "... ignored") != maxIgnoredShown || !strings.Contains(got, "5 more ignored tests") {
			t.Fatalf("got\n%s", got)
		}
	})
	t.Run("identical warning in lib and lib test built once each", func(t *testing.T) {
		w := "warning: unused import: `std::fmt`\n --> src/lib.rs:1:5\n  |\n1 | use std::fmt;\n  |     ^^^^^^^^\n\n"
		in := "   Compiling a v0.1.0 (/home/user/src/demo)\n" + w + w + "warning: `a` (lib) generated 1 warning\nwarning: `a` (lib test) generated 1 warning (1 duplicate)\n    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.50s"
		got, _ := apply(t, 0, []string{"cargo", "build"}, in)
		if !strings.Contains(got, "warning: unused import: `std::fmt` [×2]") || strings.Count(got, "use std::fmt;") != 1 {
			t.Fatalf("got\n%s", got)
		}
	})
	t.Run("error-named crates hidden without guard re-adds", func(t *testing.T) {
		in := "   Compiling quick-error v2.0.1\n   Compiling failure v0.1.8\n   Compiling error-chain v0.12.4\n    Finished `dev` profile [unoptimized + debuginfo] target(s) in 3.00s"
		got, ok := apply(t, 0, []string{"cargo", "build"}, in)
		if !ok || strings.Contains(got, "error lines from the full output") || strings.Contains(got, "quick-error") {
			t.Fatalf("got ok=%v\n%s", ok, got)
		}
	})
}

func TestSelfGuard(t *testing.T) {
	lines := []string{"gcc -Wfatal-errors -c a.c", "fatal: something broke", "ok line"}
	out := selfGuard(lines, func(i int) bool { return i == 0 }, "ok line")
	if !strings.Contains(out, "[lx: error lines from the full output]\nfatal: something broke") || strings.Contains(out, "gcc -W") {
		t.Fatalf("got\n%s", out)
	}
	if got := selfGuard(lines, nil, "gcc -Wfatal-errors -c a.c [×2]\nfatal: something broke"); strings.Contains(got, "[lx:") {
		t.Fatalf("count suffix not understood:\n%s", got)
	}
}

func TestHuge(t *testing.T) {
	n := 50000
	if v := os.Getenv("LX_HUGE_N"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	build := func(f func(b *strings.Builder, i int)) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			f(&b, i)
		}
		return b.String()
	}
	cases := []struct {
		name string
		exit int
		in   string
	}{
		{"make", 2, build(func(b *strings.Builder, i int) {
			if i%2 == 0 {
				fmt.Fprintf(b, "gcc -O2 -Wfatal-errors -c -o f%d.o f%d.c\n", i, i)
			} else {
				fmt.Fprintf(b, "f%d.c:%d:5: warning: unused variable 'x%d' [-Wunused-variable]\n %4d |     int x%d;\n      |         ^\n", i-1, i%900+1, i, i%900+1, i)
			}
		}) + "f1.c:1:1: error: boom\nmake: *** [f1.o] Error 1"},
		{"cc", 1, build(func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "In file included from m%d.c:1:\ninc/h%d.h:%d:1: error: unknown type name 't%d'\n", i%7, i%13, i%500, i%11)
		})},
		{"cargo", 101, build(func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "   Compiling crate-%d v0.1.%d\n", i, i)
		}) + "error[E0308]: mismatched types\n --> src/main.rs:1:1\n\nerror: could not compile `x` (bin \"x\") due to 1 previous error"},
		{"cargo-test", 101, "     Running unittests src/lib.rs (target/debug/deps/a-1)\n\nrunning 50000 tests\n" + build(func(b *strings.Builder, i int) {
			if i == 777 {
				fmt.Fprintf(b, "test t%d ... FAILED\n", i)
				return
			}
			fmt.Fprintf(b, "test t%d ... ok\n", i)
		}) + "\ntest result: FAILED. 49999 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 9.00s"},
		{"gradle", 0, build(func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "> Task :m%d:compileJava UP-TO-DATE\n", i)
		}) + "BUILD SUCCESSFUL in 30s"},
		{"maven", 1, build(func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "[INFO] --- compiler:3.11.0:compile (default-compile) @ m%d ---\n[WARNING] /x/A%d.java:[1,1] [unchecked] unchecked conversion\n", i, i%100)
		}) + "[INFO] BUILD FAILURE\n[ERROR] Failed to execute goal x on project m1: boom"},
	}
	for _, tc := range cases {

		lines := strings.Split(tc.in, "\n")
		quarter := strings.Join(lines[:len(lines)/4], "\n") + "\n" + lines[len(lines)-1]
		startQ := time.Now()
		apply(t, tc.exit, filterArgv[tc.name], quarter)
		elQ := time.Since(startQ)
		start := time.Now()
		got, ok := apply(t, tc.exit, filterArgv[tc.name], tc.in)
		el := time.Since(start)
		if !ok {
			t.Errorf("%s: bailed on huge output", tc.name)
			continue
		}
		if el > testenv.Scale(30*time.Second) || el > testenv.Scale(200*time.Millisecond) && el > 10*elQ {
			t.Errorf("%s: took %v (a quarter of the input: %v)", tc.name, el, elQ)
		}
		if tokens.Count(got) >= tokens.Count(tc.in) {
			t.Errorf("%s: no savings (%d → %d tokens)", tc.name, tokens.Count(tc.in), tokens.Count(got))
		}
		t.Logf("%-11s %7d lines in %v: %d → %d tokens", tc.name, strings.Count(tc.in, "\n")+1, el.Round(time.Millisecond), tokens.Count(tc.in), tokens.Count(got))
	}
}

func TestMavenShapes(t *testing.T) {
	t.Run("class line without Running keeps earlier output", func(t *testing.T) {
		in := "[ERROR] a.BTest.x -- Time elapsed: 0.1 s <<< FAILURE!\njava.lang.AssertionError: boom\n\tat a.BTest.x(BTest.java:9)\n" +
			"[INFO] Tests run: 1, Failures: 0, Errors: 0, Skipped: 0, Time elapsed: 0.1 s -- in a.CTest\n[INFO] BUILD FAILURE"
		got, _ := apply(t, 1, []string{"mvn", "test"}, in)
		if !strings.Contains(got, "\tat a.BTest.x(BTest.java:9)") {
			t.Fatalf("failure trace dropped:\n%s", got)
		}
	})
	t.Run("warning counts stay on their line when class output is dropped", func(t *testing.T) {
		in := "[INFO] Running a.ATest\nhello\nworld\n[WARNING] flaky clock\n[INFO] Tests run: 2, Failures: 0, Errors: 0, Skipped: 0, Time elapsed: 0.1 s -- in a.ATest\n" +
			"[WARNING] flaky clock\n[WARNING] other\n[INFO] BUILD SUCCESS"
		got, _ := apply(t, 0, []string{"mvn", "test"}, in)
		want := "[lx: hidden: 2 [INFO] progress lines, 2 lines of passing-test output]\n[WARNING] flaky clock [×2]\n[WARNING] other\n[INFO] BUILD SUCCESS"
		if got != want {
			t.Fatalf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("reactor summary shown only with failures", func(t *testing.T) {
		in := "[INFO] Reactor Summary for p 1.0:\n[INFO] \n[INFO] a .......... SUCCESS [  0.1 s]\n[INFO] b .......... SUCCESS [  0.2 s]\n[INFO] BUILD SUCCESS"
		got, _ := apply(t, 0, []string{"mvn", "install"}, in)
		if strings.Contains(got, "[INFO] Reactor Summary") || !strings.HasSuffix(got, "[INFO] BUILD SUCCESS") {
			t.Fatalf("got\n%s", got)
		}
	})
}

func TestGradleShapes(t *testing.T) {
	in := "> Task :a:compileJava UP-TO-DATE\n> Task :a:test\n\nATest > ok() PASSED\n\nATest > bad() FAILED\n    java.lang.AssertionError at ATest.java:5\n\n2 tests completed, 1 failed\n\n> Task :a:test FAILED\n\nFAILURE: Build failed with an exception.\n\n* What went wrong:\nExecution failed for task ':a:test'.\n> There were failing tests.\n\n* Try:\n> Run with --scan to get full insights.\n\nBUILD FAILED in 1s"
	got, ok := apply(t, 1, []string{"gradle", "test"}, in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"> Task :a:test\n", "ATest > bad() FAILED\n    java.lang.AssertionError at ATest.java:5", "2 tests completed, 1 failed", "> Task :a:test FAILED", "* What went wrong:\nExecution failed for task ':a:test'.\n> There were failing tests.", "BUILD FAILED in 1s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	body := got[strings.Index(got, "\n")+1:]
	for _, gone := range []string{"PASSED", "* Try:", "--scan", "compileJava"} {
		if strings.Contains(body, gone) {
			t.Fatalf("%q still shown in\n%s", gone, got)
		}
	}
}
