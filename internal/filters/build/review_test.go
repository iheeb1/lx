package build

// Regression tests for the issues found in the adversarial review of this
// package. Each test names the failure it guards against.

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
)

// Fake filters for delegation tests. Neither name clashes with a real
// command: "git lxfake-show" and "node lxfake.js".
type fakeContent struct{}

func (fakeContent) Name() string    { return "lxfake-content" }
func (fakeContent) IsContent() bool { return true }
func (fakeContent) Match(c *engine.Context) bool {
	return len(c.Argv) > 1 && c.Name() == "git" && c.Argv[1] == "lxfake-show"
}

// Apply keeps only the first line, as a data filter may cut long data.
func (fakeContent) Apply(c *engine.Context, out string) (string, bool) {
	first, _, _ := strings.Cut(out, "\n")
	return first + "\n[fake: rest of the data cut]", true
}

type fakeGuarded struct{}

func (fakeGuarded) Name() string       { return "lxfake-guarded" }
func (fakeGuarded) GuardsErrors() bool { return true }
func (fakeGuarded) Match(c *engine.Context) bool {
	return len(c.Argv) > 1 && c.Name() == "node" && c.Argv[1] == "lxfake.js"
}

// Apply summarizes whatever it gets in one line, trusting it is its own.
func (fakeGuarded) Apply(c *engine.Context, out string) (string, bool) {
	return fmt.Sprintf("[fake: %d lines of script output]", strings.Count(out, "\n")+1), true
}

func init() {
	engine.Register(fakeContent{})
	engine.Register(fakeGuarded{})
}

// assertLines fails unless every line of want occurs in got verbatim and
// the self-guard never had to re-add anything.
func assertLines(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "[lx: error lines from the full output]") {
		t.Errorf("self-guard had to re-add lines:\n%s", got)
	}
}

// Issue 1 (high): the generic reducer folded compiler diagnostics without
// an error word ("undefined: lookup", Kotlin "Type mismatch") into "… N
// similar lines …", hiding every location but the first and last.
func TestReviewDiagnosticsNeverFoldedAsSimilar(t *testing.T) {
	var gobuild, kotlin, gradleJavac []string
	for i := 1; i <= 8; i++ {
		gobuild = append(gobuild, fmt.Sprintf("./main.go:%d:11: undefined: lookup", 10+i))
		kotlin = append(kotlin, fmt.Sprintf("e: file:///home/user/src/demo/app/src/main/kotlin/A.kt:%d:5 Type mismatch: inferred type is String but Int was expected", 10*i))
		gradleJavac = append(gradleJavac, fmt.Sprintf("/home/user/src/demo/src/main/java/A.java:%d: warning: [rawtypes] found raw type: List", 10+i))
	}
	cases := []struct {
		name string
		exit int
		argv []string
		in   string
		want []string
	}{
		{"make, silent go build", 2, []string{"make"},
			"  GO    bin/app\n# example.com/x\n" + strings.Join(gobuild, "\n") + "\nmake: *** [build] Error 1", gobuild},
		{"gradle, kotlin", 1, []string{"./gradlew", "build"},
			"> Task :app:compileKotlin FAILED\n" + strings.Join(kotlin, "\n") + "\n\nFAILURE: Build failed with an exception.\n\nBUILD FAILED in 2s", kotlin},
		{"gradle, javac warnings in gcc format", 0, []string{"gradle", "build"},
			"> Task :compileJava\n" + strings.Join(gradleJavac, "\n") + "\n8 warnings\n\nBUILD SUCCESSFUL in 1s", gradleJavac},
		{"cargo, build script output", 101, []string{"cargo", "build"},
			"   Compiling a v0.1.0\n" + strings.Join(gobuild, "\n") + "\nerror: could not compile `a` (build script)", gobuild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := apply(t, tc.exit, tc.argv, tc.in)
			if !ok {
				t.Fatal("bailed")
			}
			if strings.Contains(got, "similar lines") {
				t.Errorf("diagnostics folded:\n%s", got)
			}
			assertLines(t, got, tc.want...)
		})
	}
	// Non-diagnostic similar lines are still folded.
	var logs []string
	for i := 10; i < 30; i++ {
		logs = append(logs, fmt.Sprintf("generated file %d of 40", i))
	}
	got, _ := apply(t, 0, []string{"make"}, "cc -c a.c\n"+strings.Join(logs, "\n"))
	if !strings.Contains(got, "similar lines") {
		t.Errorf("plain similar lines no longer folded:\n%s", got)
	}
}

// Issue 2 (medium): long runs of include-chain or gcc context lines with no
// diagnostic header after them were re-scanned from every line: quadratic
// (10k lines took 16 s).
func TestReviewPrefixRunsLinear(t *testing.T) {
	for _, unit := range []string{"In file included from a.h:1:\n", "a.c: In function 'f':\n", "src/x.cpp:9:12:   required from here\n"} {
		in := strings.Repeat(unit, 50000) + "make: *** [x.o] Error 1"
		start := time.Now()
		got, ok := apply(t, 2, []string{"make"}, in)
		if el := time.Since(start); el > testenv.Scale(2*time.Second) {
			t.Errorf("%q ×50000 took %v", strings.TrimSpace(unit), el)
		}
		if !ok || !strings.HasSuffix(got, "make: *** [x.o] Error 1") {
			t.Errorf("%q: make error lost (ok=%v)", strings.TrimSpace(unit), ok)
		}
	}
	// The skip must not hide a diagnostic that follows a failed prefix run.
	in := "In file included from a.h:1:\nIn file included from b.h:2:\nnot a header\nIn file included from c.h:3:\nd.h:4:1: error: boom\n1 error generated."
	got, _ := apply(t, 1, []string{"cc", "-c", "a.c"}, in)
	assertLines(t, got, "In file included from c.h:3:", "d.h:4:1: error: boom")
}

// Issue 3 (medium): with more than one "test result:" line lx added its
// own total, which read "0 failed" when a test binary crashed before its
// result line (exit 101): pass-like text on a failed run.
func TestReviewCargoTotalNeverPassLikeOnFailure(t *testing.T) {
	ok2 := "test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s"
	ok1 := "test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s"
	crash := "     Running unittests src/lib.rs (target/debug/deps/demo-1)\n\nrunning 2 tests\ntest a ... ok\ntest b ... ok\n\n" + ok2 + "\n\n" +
		"     Running tests/api.rs (target/debug/deps/api-2)\n\nrunning 3 tests\ntest x ... ok\n\nthread 'y' has overflowed its stack\nfatal runtime error: stack overflow\n" +
		"error: test failed, to rerun pass `--test api`\n\nCaused by:\n  process didn't exit successfully: `/home/user/src/demo/target/debug/deps/api-2` (signal: 6, SIGABRT: process abort signal)\n" +
		"     Running tests/cli.rs (target/debug/deps/cli-3)\n\nrunning 1 test\ntest c ... ok\n\n" + ok1
	got, ok := apply(t, 101, []string{"cargo", "test", "--no-fail-fast"}, crash)
	if !ok {
		t.Fatal("bailed")
	}
	if strings.Contains(got, "[lx: total") {
		t.Errorf("pass-like total on a failed run:\n%s", got)
	}
	assertLines(t, got, "fatal runtime error: stack overflow", "error: test failed, to rerun pass `--test api`", "(signal: 6, SIGABRT: process abort signal)")

	// On a passing run, and on a failed run whose results report failures,
	// the total stays.
	pass := strings.Replace(crash, "\nthread 'y' has overflowed its stack\nfatal runtime error: stack overflow\nerror: test failed, to rerun pass `--test api`\n\nCaused by:\n  process didn't exit successfully: `/home/user/src/demo/target/debug/deps/api-2` (signal: 6, SIGABRT: process abort signal)\n", "\n"+ok1+"\n\n", 1)
	if got, _ := apply(t, 0, []string{"cargo", "test"}, pass); !strings.Contains(got, "[lx: total of 3 test result lines: 4 passed; 0 failed;") {
		t.Errorf("total missing on a passing run:\n%s", got)
	}
	failed := strings.Replace(pass, ok1, "test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s", 1)
	if got, _ := apply(t, 101, []string{"cargo", "test"}, failed); !strings.Contains(got, "1 failed;") || !strings.Contains(got, "[lx: total") {
		t.Errorf("total missing on a run with failures:\n%s", got)
	}
}

// Issue 4 (medium): make handed the lines after an echoed recipe to that
// tool's filter and trusted it with them, even for data (Content) filters,
// and even when a silent recipe's compiler errors followed.
func TestReviewDelegationTrust(t *testing.T) {
	t.Run("content filters are not delegated to", func(t *testing.T) {
		in := "git lxfake-show\nv1.2.3\nconfigure: error: C compiler cannot create executables\nsee config.log\nmake: *** [deps] Error 1"
		got, ok := apply(t, 2, []string{"make"}, in)
		if !ok || strings.Contains(got, "[fake:") {
			t.Fatalf("delegated to a content filter (ok=%v):\n%s", ok, got)
		}
		assertLines(t, got, "configure: error: C compiler cannot create executables", "make: *** [deps] Error 1")
	})
	t.Run("delegated body ends at C toolchain output", func(t *testing.T) {
		in := "node lxfake.js\nwrote 3 files\nsrc/x.c:3:1: error: unknown type name 'u8'\n    3 | u8 v;\n      | ^\n1 error generated.\nmake: *** [x.o] Error 1"
		got, ok := apply(t, 2, []string{"make"}, in)
		if !ok {
			t.Fatal("bailed")
		}
		assertLines(t, got, "[fake: 1 lines of script output]", "src/x.c:3:1: error: unknown type name 'u8'", "    3 | u8 v;", "make: *** [x.o] Error 1")
		for _, ln := range []string{"collect2: error: ld returned 1 exit status", "/usr/bin/ld: cannot find -lfoo", "cc1: all warnings being treated as errors"} {
			got, _ := apply(t, 2, []string{"make"}, "node lxfake.js\nwrote 3 files\n"+ln+"\nmake: *** [x] Error 1")
			assertLines(t, got, ln)
		}
	})
	t.Run("python-style diagnostics still reach the delegate", func(t *testing.T) {
		// Only C-family files end the body: a script's own "x.py:3: error:"
		// lines are its output.
		got, _ := apply(t, 2, []string{"make"}, "node lxfake.js\nx.py:3: error: bad\nmake: *** [lint] Error 1")
		assertLines(t, got, "[fake: 1 lines of script output]")
	})
}

// Issue 5 (low): cc matched machine-readable diagnostics (gcc JSON/SARIF,
// clang SARIF) and AST dumps.
func TestReviewCcMachineOutput(t *testing.T) {
	for argv, want := range map[string]bool{
		"gcc -fdiagnostics-format=json -c a.c":              false,
		"gcc -fdiagnostics-format=json-stderr -c a.c":       false,
		"clang -fdiagnostics-format=sarif -c a.c":           false,
		"gcc -fdiagnostics-format=sarif-file -c a.c":        true, // written to a file
		"clang -fsyntax-only -Xclang -ast-dump a.c":         false,
		"clang -fdiagnostics-format=clang -c a.c":           true,
		"clang++ -fsyntax-only -fcolor-diagnostics a.cpp":   true,
		"x86_64-w64-mingw32-gcc -fdiagnostics-color -c a.c": true,
	} {
		f := engine.Find(ctx(0, strings.Fields(argv)...))
		if got := f != nil && f.Name() == "cc"; got != want {
			t.Errorf("%q: cc matches = %v, want %v", argv, got, want)
		}
	}
}

// Issue 6 (medium): any "libtool: …" line counted as a hidden recipe
// command, so "libtool:   error: cannot find the library …" disappeared
// (exempt from the guard) unless it happened to precede make's error.
func TestReviewLibtoolReportsAreNotCommands(t *testing.T) {
	in := "libtool: compile:  gcc -DHAVE_CONFIG_H -I. -g -O2 -c foo.c  -fno-common -DPIC -o .libs/foo.o\n" +
		"libtool: link: gcc -shared -o .libs/libfoo.so .libs/foo.o\n" +
		"libtool:   error: cannot find the library '/usr/lib/libbar.la' or unhandled argument '/usr/lib/libbar.la'\n" +
		"libtool: warning: '/usr/lib/libbaz.la' seems to be moved\n" +
		"make[1]: *** [libfoo.la] Error 1\nmake: *** [all] Error 2"
	got, _ := apply(t, 2, []string{"make"}, in)
	assertLines(t, got, "libtool:   error: cannot find the library '/usr/lib/libbar.la' or unhandled argument '/usr/lib/libbar.la'",
		"libtool: warning: '/usr/lib/libbaz.la' seems to be moved", "[lx: hidden: 1 recipe command (libtool)]")
}

// Issue 7 (low): Gradle chatter patterns hid (and exempted) error-class
// lines of the same shape.
func TestReviewGradleErrorShapedChatterKept(t *testing.T) {
	in := "> Task :app:compileJava UP-TO-DATE\nDownload https://repo.example.com/x.pom failed: 403 Forbidden\n\nFAILURE: Build failed with an exception.\n\n* What went wrong:\nExecution failed for task ':app:compileJava'.\n\nBUILD FAILED in 2s"
	got, _ := apply(t, 1, []string{"./gradlew", "build"}, in)
	assertLines(t, got, "Download https://repo.example.com/x.pom failed: 403 Forbidden")
}

// Issue 8 (low): "Nothing to be done" lines of sub-makes are counted, but
// were not exempt, so an error-word target name made the guard re-add them.
// "cc1: some warnings being treated as errors" (gcc -Werror=…) was not
// recognized as the compiler's failure line.
func TestReviewMakeSmallShapes(t *testing.T) {
	got, _ := apply(t, 0, []string{"make"}, "make[1]: Nothing to be done for 'error-pages'.\nmake[1]: Nothing to be done for 'all'.\ncc -c a.c\nmake: Nothing to be done for 'install'.")
	assertLines(t, got, "2 \"Nothing to be done\" lines")
	in := "gcc -Werror=format -c a.c\ngcc -Werror=format -c b.c\nb.c:3:5: warning: format '%d' expects argument of type 'int' [-Wformat=]\ncc1: some warnings being treated as errors\nmake: *** [b.o] Error 1"
	got, _ = apply(t, 2, []string{"make"}, in)
	assertLines(t, got, "gcc -Werror=format -c b.c\nb.c:3:5: warning", "cc1: some warnings being treated as errors")
}

// Improvement: javac warnings under Maven were kept one by one (a hundred
// "[WARNING] …:[l,c] found raw type" lines); now each message is kept once
// with its detail lines and a location list.
func TestReviewMavenJavacWarningsGrouped(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "[WARNING] /home/user/src/demo/src/main/java/A.java:[%d,12] found raw type: java.util.List\n  missing type arguments for generic class java.util.List<E>\n", i)
		if i == 5 {
			b.WriteString("[WARNING] /home/user/src/demo/src/main/java/B.java:[3,4] found raw type: java.util.List\n  missing type arguments for generic class java.util.List<E>\n")
		}
	}
	// Same message, other detail lines: not merged.
	b.WriteString("[WARNING] /home/user/src/demo/src/main/java/C.java:[5,6] found raw type: java.util.List\n  other detail\n")
	// An exact repeat: counted on the first line.
	b.WriteString("[WARNING] /home/user/src/demo/src/main/java/A.java:[1,12] found raw type: java.util.List\n  missing type arguments for generic class java.util.List<E>\n")
	// Error-class warnings are never merged.
	b.WriteString("[WARNING] /home/user/src/demo/src/main/java/D.java:[1,1] could not resolve annotation X\n[WARNING] /home/user/src/demo/src/main/java/D.java:[2,1] could not resolve annotation X\n")
	b.WriteString("[INFO] BUILD SUCCESS")
	got, ok := apply(t, 0, []string{"mvn", "compile"}, b.String())
	if !ok {
		t.Fatal("bailed")
	}
	assertLines(t, got,
		"[WARNING] /home/user/src/demo/src/main/java/A.java:[1,12] found raw type: java.util.List [×2]\n  missing type arguments for generic class java.util.List<E>\n"+
			"[lx: same warning at 30 more locations: src/main/java/A.java:[2,12], [3,12], [4,12], [5,12], src/main/java/B.java:[3,4], src/main/java/A.java:[6,12], [7,12],",
		", … +18 more]",
		"[WARNING] /home/user/src/demo/src/main/java/C.java:[5,6] found raw type: java.util.List\n  other detail",
		"[WARNING] /home/user/src/demo/src/main/java/D.java:[1,1] could not resolve annotation X",
		"[WARNING] /home/user/src/demo/src/main/java/D.java:[2,1] could not resolve annotation X")
	if strings.Count(got, "missing type arguments") != 1 {
		t.Errorf("detail lines repeated:\n%s", got)
	}
}

// Improvement: stack traces Maven prints under its [ERROR] prefix (surefire
// fork crashes, plugin exceptions) are folded like unprefixed ones, and a
// block surefire prints twice is shown once.
func TestReviewMavenErrorPrefixedTraces(t *testing.T) {
	var b strings.Builder
	block := "[ERROR] The forked VM terminated without properly saying goodbye. VM crash or System.exit called?\n[ERROR] Command was /bin/sh -c cd '/x' && java -jar surefirebooter.jar\n[ERROR] Process Exit Code: 3\n[ERROR] Crashed tests:\n[ERROR] com.example.ExitTest\n"
	b.WriteString("[INFO] BUILD FAILURE\n[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.1.2:test (default-test) on project demo:\n")
	b.WriteString(block)
	b.WriteString("[ERROR] org.apache.maven.surefire.booter.SurefireBooterForkException: The forked VM terminated without properly saying goodbye. VM crash or System.exit called?\n")
	b.WriteString(strings.Join(strings.Split(block, "\n")[1:], "\n"))
	for _, f := range []string{"org.apache.maven.plugin.surefire.booterclient.ForkStarter.fork(ForkStarter.java:643)",
		"org.apache.maven.plugin.surefire.booterclient.ForkStarter.run(ForkStarter.java:285)",
		"org.apache.maven.lifecycle.internal.MojoExecutor.execute(MojoExecutor.java:212)",
		"org.apache.maven.cli.MavenCli.main(MavenCli.java:207)",
		"com.example.build.Custom.run(Custom.java:12)",
		"org.codehaus.plexus.classworlds.launcher.Launcher.main(Launcher.java:314)",
		"org.codehaus.plexus.classworlds.launcher.Launcher.launch(Launcher.java:201)",
		"org.codehaus.plexus.classworlds.launcher.Launcher.mainWithExitCode(Launcher.java:362)"} {
		b.WriteString("[ERROR] \tat " + f + "\n")
	}
	b.WriteString("[ERROR] -> [Help 1]")
	got, ok := apply(t, 1, []string{"mvn", "test"}, b.String())
	if !ok {
		t.Fatal("bailed")
	}
	assertLines(t, got,
		"[ERROR] \tat org.apache.maven.plugin.surefire.booterclient.ForkStarter.fork(ForkStarter.java:643)",
		"[ERROR] \tat com.example.build.Custom.run(Custom.java:12)", // application frame kept
		"library frames",
		"[lx: 4 [ERROR] lines repeated verbatim from above]",
		"[ERROR] org.apache.maven.surefire.booter.SurefireBooterForkException: The forked VM terminated")
	if strings.Count(got, "Command was") != 1 {
		t.Errorf("repeated block not collapsed:\n%s", got)
	}
	// Short repeats (fewer than 3 lines) stay as they are.
	in := "[ERROR] a.java:[1,1] boom\n[ERROR] x\n[ERROR] Failed to execute goal g on project p: Compilation failure\n[ERROR] a.java:[1,1] boom\n[ERROR] y"
	got, _ = apply(t, 1, []string{"mvn", "compile"}, in)
	if strings.Count(got, "a.java:[1,1] boom") != 2 {
		t.Errorf("short repeat collapsed:\n%s", got)
	}
}

// Streamer: dev servers and continuous builds must not be buffered.
func TestReviewStream(t *testing.T) {
	for argv, want := range map[string]bool{
		"./gradlew bootRun": true, "gradle :app:run": true, "./gradlew build --continuous": true, "gradle -t test": true,
		"./gradlew build": false, "gradle test": false,
		"mvn spring-boot:run": true, "./mvnw quarkus:dev": true, "mvn jetty:run": true, "mvn package": false,
		"make dev": true, "make serve": true, "make watch": true, "make": false, "make test": false, "make -C web dev": true,
	} {
		f := engine.Find(ctx(0, strings.Fields(argv)...))
		s, ok := f.(engine.Streamer)
		if f == nil || !mine(f.Name()) || !ok {
			t.Errorf("%q: no build filter with Stream (found %v)", argv, f)
			continue
		}
		if got := s.Stream(ctx(0, strings.Fields(argv)...)); got != want {
			t.Errorf("%q: Stream = %v, want %v", argv, got, want)
		}
	}
}

// False-pass hunt: runs that failed must never read as a success; the
// failure is in view (or the filter bails and the generic reducer shows
// the tail).
func TestReviewFailedRunsShowTheFailure(t *testing.T) {
	cases := []struct {
		name string
		exit int
		argv []string
		in   string
		want string // "" = the filter must bail
	}{
		{"make: go test passes, a later recipe fails silently", 2, []string{"make", "check"},
			"go test ./...\nok  \texample.com/x\t0.01s\nok  \texample.com/y\t0.01s\n./scripts/verify.sh\nmake: *** [check] Error 3", "make: *** [check] Error 3"},
		{"make: compiler killed (OOM), no diagnostic", 2, []string{"make", "-j8"},
			"cc -c a.c\ncc -c b.c\ncc: fatal error: Killed signal terminated program cc1\ncompilation terminated.\nmake: *** [b.o] Error 1", "cc: fatal error: Killed signal terminated program cc1"},
		{"make: killed by a signal, nothing but commands", 143, []string{"make"},
			"cc -c a.c\ncc -c b.c\ncc -c c.c", ""},
		{"make: error count summary only", 2, []string{"make"},
			"cc -c a.c\n10 errors generated.\nmake: *** [a.o] Error 1", "10 errors generated."},
		{"ninja: nothing to do but exit 1", 1, []string{"ninja"}, "ninja: no work to do.", ""},
		{"cargo: Finished, then a failed install step", 101, []string{"cargo", "install", "--path", "."},
			"   Compiling demo v0.1.0 (/home/user/src/demo)\n    Finished `release` profile [optimized] target(s) in 3.00s\n  Installing /home/user/.cargo/bin/demo\nerror: failed to move `/home/user/.cargo/bin/cargo-installAbc/demo` to `/home/user/.cargo/bin/demo`\n\nCaused by:\n  Permission denied (os error 13)",
			"error: failed to move"},
		{"cargo test: all ok results, binary killed", 101, []string{"cargo", "test"},
			"     Running unittests src/lib.rs (target/debug/deps/demo-1)\n\nrunning 1 test\ntest a ... ok\n\ntest result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n\n     Running tests/b.rs (target/debug/deps/b-2)\n\nrunning 1 test\nerror: test failed, to rerun pass `--test b`\n\nCaused by:\n  process didn't exit successfully: `target/debug/deps/b-2` (signal: 9, SIGKILL: kill)",
			"(signal: 9, SIGKILL: kill)"},
		{"gradle: BUILD SUCCESSFUL but exit 1", 1, []string{"gradle", "build"},
			"> Task :compileJava UP-TO-DATE\n\nBUILD SUCCESSFUL in 1s\n1 actionable task: 1 up-to-date", ""},
		{"gradle: failure only in What went wrong", 1, []string{"gradle", "build"},
			"> Task :compileJava UP-TO-DATE\n\nFAILURE: Build failed with an exception.\n\n* What went wrong:\nCould not resolve all files for configuration ':compileClasspath'.\n> Could not find com.example:lib:1.0.\n\nBUILD FAILED in 1s",
			"> Could not find com.example:lib:1.0."},
		{"maven: passing counts, crashed fork", 1, []string{"mvn", "test"},
			"[INFO] Tests run: 3, Failures: 0, Errors: 0, Skipped: 0\n[INFO] BUILD FAILURE\n[ERROR] Crashed tests:\n[ERROR] com.example.ExitTest",
			"[ERROR] Crashed tests:"},
		{"maven: BUILD SUCCESS text but exit 1", 1, []string{"mvn", "verify"},
			"[INFO] Tests run: 3, Failures: 0, Errors: 0, Skipped: 0\n[INFO] BUILD SUCCESS", ""},
		{"cc: link failure without an error word", 1, []string{"cc", "-o", "app", "a.c"},
			"ld: unknown options: -E\nclang: error: linker command failed with exit code 1 (use -v to see invocation)", "ld: unknown options: -E"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := apply(t, tc.exit, tc.argv, tc.in)
			if tc.want == "" {
				if ok {
					t.Fatalf("want a bail, got:\n%s", got)
				}
				return
			}
			if !ok {
				t.Fatal("bailed")
			}
			assertLines(t, got, tc.want)
			// Nothing lx adds may read as a pass.
			for _, ln := range strings.Split(got, "\n") {
				if strings.HasPrefix(ln, "[lx:") && (strings.Contains(ln, " 0 failed") || strings.Contains(ln, "success")) {
					t.Errorf("pass-like lx line on a failed run: %q", ln)
				}
			}
		})
	}
}

// Variants: Windows line endings and ANSI colors are normalized before
// filters run; the filters must still recognize the shapes.
func TestReviewCRLFAndColor(t *testing.T) {
	raw := "cc -c a.c\r\n\x1b[1ma.c:3:5: \x1b[0m\x1b[0;1;31merror: \x1b[0m\x1b[1muse of undeclared identifier 'x'\x1b[0m\r\n    3 |   x = 1;\r\n      |   ^\r\n1 error generated.\r\nmake: *** [a.o] Error 1\r\n"
	c := ctx(2, "make")
	res := engine.Process(c, strings.Repeat(raw, 20), engine.Options{})
	if res.Filter != "make" || res.GuardAdded != 0 {
		t.Fatalf("filter %s guard %d", res.Filter, res.GuardAdded)
	}
	if !strings.Contains(res.Output, "a.c:3:5: error: use of undeclared identifier 'x' [×20]") || strings.Contains(res.Output, "\r") || strings.Contains(res.Output, "\x1b") {
		t.Fatalf("got:\n%s", res.Output)
	}
}

// isErrorLine must agree with engine.IsError: on every line of every
// fixture, and on warning lines built around error words.
func TestIsErrorLineAgrees(t *testing.T) {
	var lines []string
	for _, f := range loadFixtures(t) {
		lines = append(lines, strings.Split(f.Clean(), "\n")...)
	}
	for _, msg := range []string{"could not open 'x'", "no warnings", "warnings: 0 errors: 3", "no such file", "0 errors", "failed to link",
		"unused variable 'error'", "error: nested", "warning: nested", "no warnings such file or directory", "'-Werror' ignored", ""} {
		lines = append(lines, "a.c:1:2: warning: "+msg, "warning: "+msg, "warning[E0001]: "+msg, "[WARNING] "+msg, "[WARNING] /x/A.java:[1,2] "+msg)
	}
	for _, ln := range lines {
		if got, want := isErrorLine(ln), engine.IsError(ln); got != want {
			t.Errorf("isErrorLine(%q) = %v, engine.IsError = %v", ln, got, want)
		}
	}
}

// go test ./internal/filters/build -run '^$' -fuzz FuzzIsErrorLine -fuzztime 20s
func FuzzIsErrorLine(f *testing.F) {
	for _, s := range []string{"could not open", "no warnings", "0 errors", "error", "fatal", "undefined reference to `x'", "✗ x"} {
		f.Add("a.c:1:2: ", s)
		f.Add("", s)
	}
	f.Fuzz(func(t *testing.T, prefix, msg string) {
		for _, ln := range []string{prefix + "warning: " + msg, "warning: " + msg, "[WARNING] " + msg, prefix + ": warning: " + msg} {
			if strings.Contains(ln, "\n") {
				continue
			}
			if got, want := isErrorLine(ln), engine.IsError(ln); got != want {
				t.Fatalf("isErrorLine(%q) = %v, engine.IsError = %v", ln, got, want)
			}
		}
	})
}

// Savings: RUST_BACKTRACE frames of the standard library and runtime are
// folded; every application frame stays; the full format (addresses) is
// understood; an error-class frame is never folded.
func TestReviewRustBacktraceFold(t *testing.T) {
	std := "/rustc/90b35a6239c3d8bdabc530a6a0816f7ff89a0aaf/library"
	in := "     Running unittests src/lib.rs (target/debug/deps/demo-1)\n\nrunning 1 test\ntest a ... FAILED\n\nfailures:\n\n---- a stdout ----\n\n" +
		"thread 'a' panicked at src/lib.rs:9:5:\nboom\nstack backtrace:\n" +
		"   0:        0x1049c8f3c - std::backtrace_rs::backtrace::libunwind::trace::h0\n                               at " + std + "/std/src/../../backtrace/src/backtrace/libunwind.rs:116:5\n" +
		"   1:        0x1049c8f3c - rust_begin_unwind\n                               at " + std + "/std/src/panicking.rs:665:5\n" +
		"   2:        0x1049c8f3c - core::panicking::panic_fmt::h1\n                               at " + std + "/core/src/panicking.rs:74:14\n" +
		"   3:        0x1049c8f3c - demo::a::h2\n                               at ./src/lib.rs:9:5\n" +
		"   4:        0x1049c8f3c - serde_json::de::from_str::h3\n                               at /home/user/.cargo/registry/src/index.crates.io-6f17d22bba15001f/serde_json-1.0.120/src/de.rs:2676:5\n" +
		"   5:        0x1049c8f3c - demo::a::{{closure}}::h4\n                               at ./src/lib.rs:7:10\n" +
		"   6:        0x1049c8f3c - core::ops::function::FnOnce::call_once::h5\n                               at " + std + "/core/src/ops/function.rs:250:5\n" +
		"   7:        0x1049c8f3c - test::run_test_in_process::h6\n                               at " + std + "/test/src/lib.rs:627:18\n" +
		"   8:        0x1049c8f3c - std::sys::backtrace::__rust_begin_short_backtrace::h7\n                               at " + std + "/std/src/sys/backtrace.rs:154:18\n" +
		"\nfailures:\n    a\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s"
	got, ok := apply(t, 101, []string{"cargo", "test"}, in)
	if !ok {
		t.Fatal("bailed")
	}
	assertLines(t, got,
		"thread 'a' panicked at src/lib.rs:9:5:\nboom\nstack backtrace:\n   … 3 library frames (std, core)\n   3:        0x1049c8f3c - demo::a::h2\n                               at ./src/lib.rs:9:5\n",
		// A single library frame between application frames stays.
		"   4:        0x1049c8f3c - serde_json::de::from_str::h3\n",
		"   5:        0x1049c8f3c - demo::a::{{closure}}::h4\n                               at ./src/lib.rs:7:10\n   … 3 library frames (core, test, std)\n",
		"test result: FAILED. 0 passed; 1 failed;")
	if strings.Contains(got, "rust_begin_unwind") || strings.Contains(got, "call_once") {
		t.Errorf("library frames not folded:\n%s", got)
	}
}

// Savings: in a template error storm only the first maxSysExcerpts source
// excerpts from system headers are shown; every header line stays, and
// excerpts of the project's own files are never hidden.
func TestReviewSystemHeaderExcerpts(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&b, "/usr/include/c++/13/bits/stl_algo.h:%d:7: error: no match for call to '(lambda) (int&, int&)'\n %4d |       if (__comp(__i, __first))\n      |           ^~~~~~\n", 100+i, 100+i)
		fmt.Fprintf(&b, "src/app.cpp:%d:12: note: required from here\n %4d |   std::sort(v.begin(), v.end(), cmp);\n      |            ^\n", 10+i, 10+i)
	}
	got, _ := apply(t, 1, []string{"g++", "-c", "src/app.cpp"}, b.String())
	for i := 1; i <= 6; i++ {
		assertLines(t, got, fmt.Sprintf("/usr/include/c++/13/bits/stl_algo.h:%d:7: error: no match for call to '(lambda) (int&, int&)'", 100+i))
	}
	if n := strings.Count(got, "if (__comp(__i, __first))"); n != maxSysExcerpts {
		t.Errorf("%d system excerpts shown, want %d:\n%s", n, maxSysExcerpts, got)
	}
	if !strings.Contains(got, "3 source excerpts from system headers") {
		t.Errorf("hidden excerpts not counted:\n%s", got)
	}
}

// A cargo test run in which no test ran at all (a name filter matching
// nothing) keeps cargo's result lines: "16 filtered out" is the finding.
// When other binaries ran tests, empty ones are still counted and hidden.
func TestReviewCargoNothingRan(t *testing.T) {
	empty := func(bin string, filtered int) string {
		return fmt.Sprintf("     Running unittests %s (target/debug/deps/demo-1)\n\nrunning 0 tests\n\ntest result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; %d filtered out; finished in 0.00s\n\n", bin, filtered)
	}
	got, _ := apply(t, 0, []string{"cargo", "test", "parse_negativ"}, "    Finished `test` profile [unoptimized + debuginfo] target(s) in 1.00s\n"+empty("src/lib.rs", 16))
	assertLines(t, got, "test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 16 filtered out; finished in 0.00s")
	ran := "     Running tests/a.rs (target/debug/deps/a-2)\n\nrunning 1 test\ntest x ... ok\n\ntest result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	got, _ = apply(t, 0, []string{"cargo", "test"}, empty("src/lib.rs", 16)+ran)
	assertLines(t, got, "1 test binary that ran no tests", "test result: ok. 1 passed;")
	if strings.Contains(got, "16 filtered out;") {
		t.Errorf("empty binary shown although others ran tests:\n%s", got)
	}
}

// Issue (medium): a line ending in a backslash (a Windows path) followed by
// an indented error was taken for an echoed multi-line shell recipe: the
// error line was hidden and exempt from the guard.
func TestReviewBackslashLineIsNotARecipe(t *testing.T) {
	got, _ := apply(t, 2, []string{"mingw32-make"}, "cc -c a.c\nCopying to C:\\build\\out\\\n    error: access denied\nmake: *** [install] Error 1")
	assertLines(t, got, "Copying to C:\\build\\out\\", "    error: access denied", "make: *** [install] Error 1")
	// A real echoed recipe still folds, error words in its code included.
	got, _ = apply(t, 0, []string{"make"}, "for d in a b; do \\\n\tif grep -q FAIL $d.log; then \\\n\t\texit 1; \\\n\tfi; \\\n\tdone\nall good")
	assertLines(t, got, "for d in a b; do \\\n[lx: 4 more lines of this echoed recipe hidden]")
}

// Gradle's "* Try:" advice is counted, but an error-class line in it is a
// report and stays.
func TestReviewGradleTryBlock(t *testing.T) {
	in := "> Task :app:compileJava FAILED\n\nFAILURE: Build failed with an exception.\n\n* What went wrong:\nExecution failed for task ':app:compileJava'.\n\n* Try:\n> Run with --stacktrace option to get the stack trace.\n> Could not reach https://repo.example.com: connection refused\n> Run with --scan to get full insights.\n\nBUILD FAILED in 1s"
	got, _ := apply(t, 1, []string{"gradle", "build"}, in)
	assertLines(t, got, "> Could not reach https://repo.example.com: connection refused")
	if strings.Contains(got, "--stacktrace") {
		t.Errorf("advice shown:\n%s", got)
	}
}
