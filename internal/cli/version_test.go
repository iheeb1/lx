package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestVersionString(t *testing.T) {
	plat := runtime.Version() + ", " + runtime.GOOS + "/" + runtime.GOARCH
	vcs := func(kv ...string) []debug.BuildSetting {
		var s []debug.BuildSetting
		for i := 0; i+1 < len(kv); i += 2 {
			s = append(s, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return s
	}
	const rev = "3f2a1c9b8e7d6c5b4a39281706f5e4d3c2b1a098"
	cases := []struct {
		name    string
		version string // cli.Version, as -ldflags -X would set it
		info    *debug.BuildInfo
		want    string
	}{
		{
			name:    "ldflags version wins over module version and keeps vcs parts",
			version: "v0.2.0",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.1.9"},
				Settings: vcs("vcs.revision", rev, "vcs.time", "2026-09-26T10:11:12Z", "vcs.modified", "false")},
			want: "lx v0.2.0 (3f2a1c9, 2026-09-26, " + plat + ")",
		},
		{
			name:    "go install module@v0.2.0: module version, no vcs",
			version: devVersion,
			info:    &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}},
			want:    "lx v0.2.0 (" + plat + ")",
		},
		{
			name:    "(devel) with revision and time",
			version: devVersion,
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"},
				Settings: vcs("vcs", "git", "vcs.revision", rev, "vcs.time", "2026-09-26T23:59:59Z")},
			want: "lx dev (3f2a1c9, 2026-09-26, " + plat + ")",
		},
		{
			name:    "modified tree is +dirty",
			version: devVersion,
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"},
				Settings: vcs("vcs.revision", rev, "vcs.time", "2026-09-26T10:11:12Z", "vcs.modified", "true")},
			want: "lx dev (3f2a1c9+dirty, 2026-09-26, " + plat + ")",
		},
		{
			name:    "no build info",
			version: devVersion,
			info:    nil,
			want:    "lx dev (" + plat + ")",
		},
		{
			name:    "empty ldflags version (make build VERSION=) falls through",
			version: "",
			info:    &debug.BuildInfo{Main: debug.Module{Version: "v0.3.1"}},
			want:    "lx v0.3.1 (" + plat + ")",
		},
		{
			name:    "empty module version",
			version: devVersion,
			info:    &debug.BuildInfo{},
			want:    "lx dev (" + plat + ")",
		},
		{
			name:    "go 1.24+ vcs-stamped pseudo-version shown as is",
			version: devVersion,
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260926101112-3f2a1c9b8e7d+dirty"},
				Settings: vcs("vcs.revision", rev, "vcs.time", "2026-09-26T10:11:12Z", "vcs.modified", "true")},
			want: "lx v0.0.0-20260926101112-3f2a1c9b8e7d+dirty (3f2a1c9+dirty, 2026-09-26, " + plat + ")",
		},
		{
			name:    "short revision kept whole; dirty without revision dropped",
			version: "v1.0.0",
			info:    &debug.BuildInfo{Settings: vcs("vcs.revision", "abc", "vcs.modified", "true")},
			want:    "lx v1.0.0 (abc+dirty, " + plat + ")",
		},
		{
			name:    "modified without revision adds nothing",
			version: "v1.0.0",
			info:    &debug.BuildInfo{Settings: vcs("vcs.modified", "true", "vcs.time", "2026-01-02T03:04:05Z")},
			want:    "lx v1.0.0 (2026-01-02, " + plat + ")",
		},
		{
			name:    "time without T is kept as given",
			version: "v1.0.0",
			info:    &debug.BuildInfo{Settings: vcs("vcs.time", "2026-01-02")},
			want:    "lx v1.0.0 (2026-01-02, " + plat + ")",
		},
	}
	oldV, oldR := Version, readBuildInfo
	t.Cleanup(func() { Version, readBuildInfo = oldV, oldR })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			Version = c.version
			info := c.info
			readBuildInfo = func() (*debug.BuildInfo, bool) { return info, info != nil }
			if got := versionString(); got != c.want {
				t.Errorf("versionString()\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// devVersion must stay equal to Version's default in cli.go (go test sets no
// -ldflags -X), or an unstamped build would print that default as if it were
// a release version.
func TestVersionDefaultIsDev(t *testing.T) {
	if Version != devVersion {
		t.Errorf("Version defaults to %q but devVersion is %q; keep them equal", Version, devVersion)
	}
}

// The real build info of the test binary must still give a well-formed line.
func TestVersionStringRealBuildInfo(t *testing.T) {
	got := versionString()
	suffix := runtime.Version() + ", " + runtime.GOOS + "/" + runtime.GOARCH + ")"
	if !strings.HasPrefix(got, "lx ") || !strings.HasSuffix(got, suffix) || strings.Contains(got, "\n") {
		t.Errorf("versionString() = %q, want \"lx <version> (…%s\"", got, suffix)
	}
}

// `lx version`, `lx --version` and `lx -V` print versionString().
func TestVersionCommand(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-V"} {
		out, code := relCaptureStdout(t, func() int { return Main([]string{arg}) })
		if code != 0 || out != versionString()+"\n" {
			t.Errorf("lx %s = %q (exit %d), want %q (exit 0)\n"+
				"(pending LEAD EDIT from the release item? cli.go Main must print versionString())",
				arg, out, code, versionString()+"\n")
		}
	}
}

func relCaptureStdout(t *testing.T, f func() int) (out string, code int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	done := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); done <- b }()
	old := os.Stdout
	os.Stdout = w
	func() {
		defer func() { os.Stdout = old; w.Close() }()
		code = f()
	}()
	return string(<-done), code
}

// ---- install.sh ------------------------------------------------------------
//
// These run the real install.sh against fake releases served over file:// (or
// by a fake curl/wget that copies from a local directory), with fake
// uname/sysctl, so they need no network, no Go toolchain and no real binary.
// Without LXTEST_SERVE the fake curl and wget fail loudly, so a test that
// reached the network would fail rather than download.

type relFakeRelease struct {
	dir string // served as LX_BASE_URL=file://dir, or by the fake curl/wget
}

const relFakeLX = "#!/bin/sh\necho 'lx v9.9.9 (test, fake/arch)'\n"

// relNewRelease writes dir/<archive> holding lx (left out when lx is ""),
// LICENSE and README.md, and a SHA256SUMS listing it. sums, when non-nil,
// replaces the SHA256SUMS text; each %s in it becomes the archive's digest.
func relNewRelease(t *testing.T, archive, lx string, sums *string) relFakeRelease {
	t.Helper()
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		mode int64
		body string
	}{{"lx", 0o755, lx}, {"LICENSE", 0o644, "MIT\n"}, {"README.md", 0o644, "# lx\n"}} {
		if f.name == "lx" && lx == "" {
			continue
		}
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	digest := hex.EncodeToString(sum[:])
	text := digest + "  lx_other_os.tar.gz\n" + digest + "  " + archive + "\n"
	if sums != nil {
		text = strings.ReplaceAll(*sums, "%s", digest)
	}
	relWrite(t, filepath.Join(dir, archive), buf.String(), 0o644)
	relWrite(t, filepath.Join(dir, "SHA256SUMS"), text, 0o644)
	return relFakeRelease{dir: dir}
}

// corrupt appends a byte to the release's archive.
func (rel relFakeRelease) corrupt(t *testing.T, archive string) relFakeRelease {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(rel.dir, archive), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return rel
}

func relWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

type relInstallRun struct {
	uname      [2]string // uname -s, uname -m
	translated string    // sysctl -n sysctl.proc_translated
	env        []string  // extra KEY=VALUE, applied last (a later key wins)
	home       string
	tmp        string
	bin        string   // fake tools dir, first on PATH
	shell      []string // the shell that runs install.sh; default sh
	cwd        string   // working directory; default the test's
	keepTmp    bool     // TMPDIR may be left non-empty
}

func relNewRun(t *testing.T, bin, s, m string) *relInstallRun {
	return &relInstallRun{uname: [2]string{s, m}, translated: "0", home: t.TempDir(), tmp: t.TempDir(), bin: bin}
}

// relFakeTools writes the stand-ins install.sh finds first on PATH: uname
// and sysctl answer from the environment; curl and wget copy the URL's last
// path element from $LXTEST_SERVE (logging their arguments to
// $LXTEST_SERVE/log), and without LXTEST_SERVE fail loudly. They are shared
// by every run because macOS vets each new executable on its first exec
// (~0.3 s, serialized).
func relFakeTools(t *testing.T) string {
	bin := t.TempDir()
	relWrite(t, filepath.Join(bin, "uname"),
		"#!/bin/sh\ncase $1 in -s) printf '%s\\n' \"$LXTEST_UNAME_S\" ;; -m) printf '%s\\n' \"$LXTEST_UNAME_M\" ;; *) exit 2 ;; esac\n", 0o755)
	relWrite(t, filepath.Join(bin, "sysctl"), "#!/bin/sh\nprintf '%s\\n' \"${LXTEST_TRANSLATED:-0}\"\n", 0o755)
	for _, net := range []string{"curl", "wget"} {
		relWrite(t, filepath.Join(bin, net), `#!/bin/sh
[ -n "${LXTEST_SERVE:-}" ] || { echo 'network use in a test' >&2; exit 97; }
printf '%s\n' "`+net+` $*" >>"$LXTEST_SERVE/log"
o= u=
while [ $# -gt 0 ]; do case $1 in -o | -O) o=$2; shift ;; -*) ;; *) u=$1 ;; esac; shift; done
exec cp "$LXTEST_SERVE/${u##*/}" "$o"
`, 0o755)
	}
	return bin
}

// relMinimalPath returns a directory to use as the whole PATH: symlinks to
// the real tools install.sh needs besides a downloader and a hasher, plus the
// fake uname and sysctl, the named fakes from bin, and those of the named
// extra real tools that exist. It is how a test takes curl, sha256sum and the
// like away.
func relMinimalPath(t *testing.T, bin string, fakes []string, real ...string) string {
	t.Helper()
	dir := t.TempDir()
	link := func(from, name string) {
		if err := os.Symlink(from, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range append([]string{"uname", "sysctl"}, fakes...) {
		link(filepath.Join(bin, f), f)
	}
	for _, tool := range []string{"mktemp", "cp", "awk", "cut", "mkdir", "tar", "gzip", "install", "mv", "rm"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			if tool == "gzip" { // only GNU tar runs gzip
				continue
			}
			t.Skipf("%s not found", tool)
		}
		link(p, tool)
	}
	for _, tool := range real {
		if p, err := exec.LookPath(tool); err == nil {
			link(p, tool)
		}
	}
	return dir
}

func (r *relInstallRun) run(t *testing.T) (stdout, stderr string, code int) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	shell := r.shell
	if len(shell) == 0 {
		shell = []string{"sh"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell[0], append(shell[1:], script)...)
	cmd.Dir = r.cwd
	cmd.Env = append([]string{
		"PATH=" + r.bin + ":" + os.Getenv("PATH"),
		"HOME=" + r.home,
		"TMPDIR=" + r.tmp,
		"SHELL=/bin/zsh",
		"LXTEST_UNAME_S=" + r.uname[0],
		"LXTEST_UNAME_M=" + r.uname[1],
		"LXTEST_TRANSLATED=" + r.translated,
	}, r.env...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil && cmd.ProcessState == nil {
		t.Fatalf("install.sh did not run: %v", err)
	}
	if code = cmd.ProcessState.ExitCode(); code < 0 {
		t.Fatalf("install.sh was killed (timeout?)\n%s", e.String())
	}
	if strings.Contains(e.String(), "network use in a test") {
		t.Fatalf("install.sh tried to use the network:\n%s", e.String())
	}
	if left, _ := os.ReadDir(r.tmp); len(left) != 0 && !r.keepTmp {
		t.Errorf("install.sh left %d entries in TMPDIR (first: %s)", len(left), left[0].Name())
	}
	return o.String(), e.String(), code
}

func relRequireTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	for _, tool := range []string{"sh", "tar", "install", "awk", "mktemp", "cut"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		if _, err := exec.LookPath("shasum"); err != nil {
			t.Skip("no sha256sum or shasum")
		}
	}
}

// relAssertNoInstall checks that a refused install did not even create dir.
func relAssertNoInstall(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(dir)
		t.Errorf("refused install created %s (entries: %d)", dir, len(entries))
	}
}

// relAssertInstalled checks dir holds exactly one file, lx, with the fake
// release's content and mode 0755.
func relAssertInstalled(t *testing.T, dir string) {
	t.Helper()
	lx := filepath.Join(dir, "lx")
	if got := relRead(t, lx); got != relFakeLX {
		t.Errorf("installed lx = %q, want the archive's", got)
	}
	if fi, err := os.Stat(lx); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("installed lx mode = %v (%v), want 0755", fi, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("install dir holds %d entries, want only lx", len(entries))
	}
}

func relRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func relStr(s string) *string { return &s }

// It only runs subprocesses (it touches no package state), so it runs
// alongside the package's other parallel tests: on macOS most of its time is
// the system vetting each newly installed executable.
func TestInstallScript(t *testing.T) {
	t.Parallel()
	relRequireTools(t)
	bin := relFakeTools(t)
	for _, c := range []struct {
		name string
		f    func(*testing.T, string)
	}{
		{"installs", relTestInstalls},
		{"platforms", relTestPlatforms},
		{"unsupported", relTestUnsupported},
		{"refuses", relTestRefuses},
		{"replaces and guides", relTestReplacesAndGuides},
		{"needs a destination", relTestNeedsADestination},
		{"destination is a directory", relTestDestinationIsADirectory},
		{"downloads", relTestDownloads},
		{"tool fallbacks", relTestToolFallbacks},
		{"relative install dir", relTestRelativeDir},
		{"odd TMPDIR", relTestOddTmpdir},
		{"cleanup failure keeps exit 0", relTestCleanupFailure},
		{"shells", relTestShells},
	} {
		t.Run(c.name, func(t *testing.T) { c.f(t, bin) })
	}
}

func relTestInstalls(t *testing.T, bin string) {
	rel := relNewRelease(t, "lx_darwin_arm64.tar.gz", relFakeLX, nil)
	r := relNewRun(t, bin, "Darwin", "arm64")
	dest := filepath.Join(r.home, ".local", "bin")
	r.env = []string{"LX_BASE_URL=file://" + rel.dir + "/"} // trailing slash is fine
	out, errOut, code := r.run(t)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	relAssertInstalled(t, dest)
	for _, want := range []string{
		"lx v9.9.9 installed to " + filepath.Join(dest, "lx") + "\n",
		"is not on your PATH; add this line to ~/.zshrc:",
		`export PATH="$HOME/.local/bin:$PATH"`,
		"next:  lx discover",
		"lx init",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func relTestPlatforms(t *testing.T, bin string) {
	cases := []struct {
		s, m, translated, archive string
	}{
		{"Darwin", "arm64", "0", "lx_darwin_arm64.tar.gz"},
		{"Darwin", "x86_64", "0", "lx_darwin_amd64.tar.gz"},
		{"Darwin", "x86_64", "1", "lx_darwin_arm64.tar.gz"}, // Rosetta shell on Apple silicon
		{"Linux", "x86_64", "1", "lx_linux_amd64.tar.gz"},   // proc_translated is a macOS thing
		{"Linux", "amd64", "0", "lx_linux_amd64.tar.gz"},
		{"Linux", "aarch64", "0", "lx_linux_arm64.tar.gz"},
		{"Linux", "arm64", "0", "lx_linux_arm64.tar.gz"},
	}
	for _, c := range cases {
		t.Run(c.s+"/"+c.m+"/"+c.translated, func(t *testing.T) {
			t.Parallel()
			// Only the expected archive exists, so a wrong mapping fails
			// to download. The archive has no lx, so a right one gets as far
			// as extracting (and stops before running a new binary, which is
			// slow on macOS).
			rel := relNewRelease(t, c.archive, "", nil)
			r := relNewRun(t, bin, c.s, c.m)
			r.translated = c.translated
			r.env = []string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + filepath.Join(r.home, "bin")}
			want := "cannot extract lx from " + c.archive
			if out, errOut, code := r.run(t); code != 1 || !strings.Contains(errOut, want) {
				t.Errorf("exit %d, stderr %q; want exit 1 mentioning %q\n%s", code, errOut, want, out)
			}
		})
	}
}

func relTestUnsupported(t *testing.T, bin string) {
	cases := []struct{ s, m, want string }{
		{"MINGW64_NT-10.0-22631", "x86_64", "download the zip from https://github.com/iheeb1/lx/releases"},
		{"CYGWIN_NT-10.0", "x86_64", "download the zip from https://github.com/iheeb1/lx/releases"},
		{"FreeBSD", "amd64", "go install github.com/iheeb1/lx/cmd/lx@latest"},
		{"Linux", "riscv64", "no prebuilt lx for riscv64"},
		{"Linux", "armv7l", "no prebuilt lx for armv7l"},
	}
	for _, c := range cases {
		t.Run(c.s+"/"+c.m, func(t *testing.T) {
			t.Parallel()
			rel := relNewRelease(t, "lx_linux_amd64.tar.gz", relFakeLX, nil)
			r := relNewRun(t, bin, c.s, c.m)
			dest := filepath.Join(r.home, "bin")
			r.env = []string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + dest}
			_, errOut, code := r.run(t)
			if code != 1 || !strings.Contains(errOut, c.want) {
				t.Errorf("exit %d, stderr %q; want exit 1 mentioning %q", code, errOut, c.want)
			}
			relAssertNoInstall(t, dest)
		})
	}
}

// Every way the download can be wrong must install nothing and leave an
// existing lx untouched.
func relTestRefuses(t *testing.T, bin string) {
	const archive = "lx_linux_amd64.tar.gz"
	cases := []struct {
		name    string
		release func(t *testing.T) relFakeRelease
		env     []string
		want    string // in stderr
		// The binary is tried from a temp name inside the destination (it
		// must be able to run from there), so that failure can leave an
		// empty destination directory behind; every earlier one must not.
		madeDir bool
		// newOnly: skip the over-an-existing-lx variant, which the case
		// above it already covers on the same path (each run of a new
		// binary costs ~0.3 s on macOS).
		newOnly bool
	}{
		{
			name: "corrupted archive",
			release: func(t *testing.T) relFakeRelease {
				return relNewRelease(t, archive, relFakeLX, nil).corrupt(t, archive)
			},
			want: "checksum mismatch for " + archive,
		},
		{
			name: "archive not listed, only a longer name with its digest",
			release: func(t *testing.T) relFakeRelease {
				return relNewRelease(t, archive, relFakeLX, relStr("%s  x"+archive+"\n%s  "+archive+".sig\n"))
			},
			want: "no valid entry in SHA256SUMS",
		},
		{
			name:    "empty SHA256SUMS",
			release: func(t *testing.T) relFakeRelease { return relNewRelease(t, archive, relFakeLX, relStr("")) },
			want:    "no valid entry in SHA256SUMS",
		},
		{
			name: "truncated digest",
			release: func(t *testing.T) relFakeRelease {
				rel := relNewRelease(t, archive, relFakeLX, nil)
				sums := relRead(t, filepath.Join(rel.dir, "SHA256SUMS"))
				d := strings.Fields(sums)[0]
				relWrite(t, filepath.Join(rel.dir, "SHA256SUMS"), d[:63]+"  "+archive+"\n", 0o644)
				return rel
			},
			want: "no valid entry in SHA256SUMS",
		},
		{
			name: "digest of another file",
			release: func(t *testing.T) relFakeRelease {
				return relNewRelease(t, archive, relFakeLX, relStr(strings.Repeat("ab", 32)+"  "+archive+"\n"))
			},
			want: "checksum mismatch",
		},
		{
			name: "missing SHA256SUMS",
			release: func(t *testing.T) relFakeRelease {
				rel := relNewRelease(t, archive, relFakeLX, nil)
				os.Remove(filepath.Join(rel.dir, "SHA256SUMS"))
				return rel
			},
			want: "cannot read file://",
		},
		{
			name: "missing archive",
			release: func(t *testing.T) relFakeRelease {
				rel := relNewRelease(t, archive, relFakeLX, nil)
				os.Remove(filepath.Join(rel.dir, archive))
				return rel
			},
			want: "cannot read file://",
		},
		{
			name:    "archive without lx",
			release: func(t *testing.T) relFakeRelease { return relNewRelease(t, archive, "", nil) },
			want:    "cannot extract lx",
		},
		{
			name:    "binary that does not run",
			madeDir: true,
			release: func(t *testing.T) relFakeRelease {
				return relNewRelease(t, archive, "#!/bin/sh\necho 'exec format error' >&2\nexit 3\n", nil)
			},
			want: "does not run on this machine: exec format error",
		},
		{
			name:    "binary that runs but is not lx",
			madeDir: true,
			newOnly: true,
			release: func(t *testing.T) relFakeRelease {
				return relNewRelease(t, archive, "#!/bin/sh\necho 'usage: something else'\n", nil)
			},
			want: "unexpected version line: usage: something else",
		},
		{
			name:    "plain http base",
			release: func(t *testing.T) relFakeRelease { return relNewRelease(t, archive, relFakeLX, nil) },
			env:     []string{"LX_BASE_URL=http://example.invalid/lx"},
			want:    "must start with https:// or file://",
		},
		{
			name:    "LX_VERSION with a path in it",
			release: func(t *testing.T) relFakeRelease { return relNewRelease(t, archive, relFakeLX, nil) },
			env:     []string{"LX_BASE_URL=", "LX_VERSION=v1/../../evil"},
			want:    "bad LX_VERSION",
		},
	}
	for _, c := range cases {
		for _, existing := range []bool{false, true} {
			if existing && c.newOnly {
				continue
			}
			name := c.name
			if existing {
				name += "/over existing lx"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				rel := c.release(t)
				r := relNewRun(t, bin, "Linux", "x86_64")
				dest := filepath.Join(r.home, "bin")
				const old = "#!/bin/sh\necho 'lx v0.0.1 (old)'\n"
				if existing {
					relWrite(t, filepath.Join(dest, "lx"), old, 0o755)
				}
				r.env = append([]string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + dest}, c.env...)
				out, errOut, code := r.run(t)
				if code == 0 || !strings.Contains(errOut, c.want) {
					t.Errorf("exit %d, stderr %q; want failure mentioning %q\nstdout: %s", code, errOut, c.want, out)
				}
				if strings.Contains(out, "installed to") {
					t.Errorf("stdout claims an install: %q", out)
				}
				if existing {
					if got := relRead(t, filepath.Join(dest, "lx")); got != old {
						t.Errorf("existing lx was changed to %q", got)
					}
					if entries, _ := os.ReadDir(dest); len(entries) != 1 {
						t.Errorf("install dir holds %d entries, want only the old lx", len(entries))
					}
				} else if !c.madeDir {
					relAssertNoInstall(t, dest)
				} else if entries, _ := os.ReadDir(dest); len(entries) != 0 {
					t.Errorf("refused install left %d entries in %s", len(entries), dest)
				}
			})
		}
	}
}

func relTestReplacesAndGuides(t *testing.T, bin string) {
	// Each case installs and runs a new binary (~0.3 s on macOS), so each
	// covers several branches. zsh is covered by relTestInstalls.
	rel := relNewRelease(t, "lx_linux_arm64.tar.gz", relFakeLX, nil)
	mac := relNewRelease(t, "lx_darwin_arm64.tar.gz", relFakeLX, nil)
	star := relNewRelease(t, "lx_linux_arm64.tar.gz", relFakeLX, relStr("%s *lx_linux_arm64.tar.gz\n"))
	outside := t.TempDir()
	cases := []struct {
		name    string
		rel     relFakeRelease
		os      string // uname -s; default Linux
		shell   string
		dest    func(r *relInstallRun) string
		onPath  bool
		old     string // existing lx content, "" for none
		want    []string
		notWant []string
	}{
		{name: "bash on linux, replacing an older lx", rel: rel, shell: "/bin/bash", old: "#!/bin/sh\necho 'lx v0.0.1 (old)'\n",
			want: []string{"add this line to ~/.bashrc:", `export PATH="$HOME/.local/bin:$PATH"`}},
		{name: "bash on macOS", rel: mac, os: "Darwin", shell: "/bin/bash", want: []string{"add this line to ~/.bash_profile:"}},
		{name: "fish, binary-mode SHA256SUMS line", rel: star, shell: "/opt/homebrew/bin/fish",
			want: []string{"~/.config/fish/config.fish", `fish_add_path "$HOME/.local/bin"`}},
		{name: "other shell, dir outside HOME spelled out", rel: rel, shell: "/bin/ksh",
			dest: func(*relInstallRun) string { return filepath.Join(outside, "tools") },
			want: []string{"~/.profile", `export PATH="` + filepath.Join(outside, "tools") + `:$PATH"`}},
		{name: "on PATH: no hint", rel: rel, shell: "/bin/zsh", onPath: true, notWant: []string{"not on your PATH", "note:"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			osName := c.os
			if osName == "" {
				osName = "Linux"
			}
			r := relNewRun(t, bin, osName, "aarch64")
			dest := filepath.Join(r.home, ".local", "bin")
			if c.dest != nil {
				dest = c.dest(r)
			}
			if c.old != "" {
				relWrite(t, filepath.Join(dest, "lx"), c.old, 0o700)
			}
			r.env = []string{"LX_BASE_URL=file://" + c.rel.dir, "LX_INSTALL_DIR=" + dest, "SHELL=" + c.shell}
			if c.onPath {
				r.env = append(r.env, "PATH="+r.bin+":"+dest+":"+os.Getenv("PATH"))
			}
			out, errOut, code := r.run(t)
			if code != 0 {
				t.Fatalf("exit %d\n%s%s", code, out, errOut)
			}
			relAssertInstalled(t, dest)
			for _, w := range append([]string{"lx v9.9.9 installed to " + filepath.Join(dest, "lx")}, c.want...) {
				if !strings.Contains(out, w) {
					t.Errorf("stdout lacks %q:\n%s", w, out)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(out, w) {
					t.Errorf("stdout has %q:\n%s", w, out)
				}
			}
		})
	}
}

func relTestNeedsADestination(t *testing.T, bin string) {
	rel := relNewRelease(t, "lx_linux_amd64.tar.gz", relFakeLX, nil)
	r := relNewRun(t, bin, "Linux", "x86_64")
	r.env = []string{"LX_BASE_URL=file://" + rel.dir, "HOME="}
	_, errOut, code := r.run(t)
	if code == 0 || !strings.Contains(errOut, "set LX_INSTALL_DIR") {
		t.Errorf("exit %d, stderr %q; want a failure asking for LX_INSTALL_DIR", code, errOut)
	}
}

func relTestDestinationIsADirectory(t *testing.T, bin string) {
	rel := relNewRelease(t, "lx_linux_amd64.tar.gz", relFakeLX, nil)
	r := relNewRun(t, bin, "Linux", "x86_64")
	dest := filepath.Join(r.home, "bin")
	if err := os.MkdirAll(filepath.Join(dest, "lx"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.env = []string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + dest}
	_, errOut, code := r.run(t)
	if code == 0 || !strings.Contains(errOut, "is a directory") {
		t.Errorf("exit %d, stderr %q; want a refusal", code, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(dest, "lx")); len(entries) != 0 {
		t.Errorf("wrote into the lx directory: %v", entries)
	}
}

// The real download path: the URLs install.sh builds, curl's https-only
// flags, and the wget fallback. The fake curl/wget serve a local release.
func relTestDownloads(t *testing.T, bin string) {
	const archive = "lx_linux_amd64.tar.gz"
	const latest = "https://github.com/iheeb1/lx/releases/latest/download/"
	cases := []struct {
		name    string
		lx      string // archive's lx; "" stops the install at extraction
		env     []string
		minimal []string // fakes on a minimal PATH (no real curl/wget); nil: full PATH
		want    []string // in the downloader log
		errWant string
	}{
		{name: "latest release with curl", lx: relFakeLX,
			want: []string{
				"curl --proto =https --tlsv1.2 -fsSL -o ",
				" " + latest + "SHA256SUMS\n",
				" " + latest + archive + "\n",
			}},
		{name: "LX_VERSION without v", env: []string{"LX_VERSION=0.2.0"}, errWant: "cannot extract lx",
			want: []string{
				" https://github.com/iheeb1/lx/releases/download/v0.2.0/SHA256SUMS\n",
				" https://github.com/iheeb1/lx/releases/download/v0.2.0/" + archive + "\n",
			}},
		{name: "wget when there is no curl", minimal: []string{"wget"}, errWant: "cannot extract lx",
			want: []string{"wget -q -O ", " " + latest + "SHA256SUMS\n", " " + latest + archive + "\n"}},
		{name: "neither curl nor wget", minimal: []string{}, errWant: "need curl or wget"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rel := relNewRelease(t, archive, c.lx, nil)
			r := relNewRun(t, bin, "Linux", "x86_64")
			dest := filepath.Join(r.home, "bin")
			r.env = append([]string{"LX_BASE_URL=", "LX_INSTALL_DIR=" + dest, "LXTEST_SERVE=" + rel.dir}, c.env...)
			if c.minimal != nil {
				r.env = append(r.env, "PATH="+relMinimalPath(t, bin, c.minimal, "shasum", "sha256sum"))
			}
			out, errOut, code := r.run(t)
			if c.errWant == "" {
				if code != 0 {
					t.Fatalf("exit %d\n%s%s", code, out, errOut)
				}
				relAssertInstalled(t, dest)
			} else {
				if code == 0 || !strings.Contains(errOut, c.errWant) {
					t.Errorf("exit %d, stderr %q; want failure mentioning %q", code, errOut, c.errWant)
				}
				relAssertNoInstall(t, dest)
			}
			log, _ := os.ReadFile(filepath.Join(rel.dir, "log"))
			for _, w := range c.want {
				if !strings.Contains(string(log), w) {
					t.Errorf("downloader log lacks %q:\n%s", w, log)
				}
			}
			if c.want == nil && len(log) != 0 {
				t.Errorf("downloader ran:\n%s", log)
			}
		})
	}
}

// sha256sum, then shasum, then openssl; with none of them nothing installs.
func relTestToolFallbacks(t *testing.T, bin string) {
	const archive = "lx_linux_amd64.tar.gz"
	cases := []struct {
		name    string
		real    []string
		corrupt bool
		want    string
	}{
		{name: "shasum without sha256sum", real: []string{"shasum"}, want: "cannot extract lx"},
		{name: "openssl alone", real: []string{"openssl"}, want: "cannot extract lx"},
		{name: "openssl alone, corrupted archive", real: []string{"openssl"}, corrupt: true, want: "checksum mismatch"},
		{name: "no hasher", want: "need sha256sum, shasum or openssl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			for _, tool := range c.real {
				if _, err := exec.LookPath(tool); err != nil {
					t.Skipf("%s not found", tool)
				}
			}
			// The archive has no lx: getting to extraction proves the
			// checksum was computed and matched.
			rel := relNewRelease(t, archive, "", nil)
			if c.corrupt {
				rel.corrupt(t, archive)
			}
			r := relNewRun(t, bin, "Linux", "x86_64")
			dest := filepath.Join(r.home, "bin")
			r.env = []string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + dest,
				"PATH=" + relMinimalPath(t, bin, nil, c.real...)}
			out, errOut, code := r.run(t)
			if code == 0 || !strings.Contains(errOut, c.want) {
				t.Errorf("exit %d, stderr %q; want failure mentioning %q", code, errOut, c.want)
			}
			if strings.Contains(out, "installed to") {
				t.Errorf("stdout claims an install: %q", out)
			}
			relAssertNoInstall(t, dest)
		})
	}
}

// A relative LX_INSTALL_DIR is relative to the working directory: an exported
// CDPATH must not send it elsewhere, and "-" is a directory, not $OLDPWD.
// dash, when present, is the shell where both went wrong.
func relTestRelativeDir(t *testing.T, bin string) {
	shell := []string{"sh"}
	if p, err := exec.LookPath("dash"); err == nil {
		shell = []string{p}
	}
	cases := []struct {
		name, dir string
		env       func(elsewhere string) []string
	}{
		{"CDPATH", "bin/", func(e string) []string { return []string{"CDPATH=" + e} }},
		{"dash as a name", "-", func(e string) []string { return []string{"OLDPWD=" + filepath.Join(e, "-")} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rel := relNewRelease(t, "lx_linux_amd64.tar.gz", relFakeLX, nil)
			r := relNewRun(t, bin, "Linux", "x86_64")
			r.shell = shell
			r.cwd = t.TempDir()
			elsewhere := t.TempDir()
			for _, d := range []string{"bin", "-"} {
				if err := os.Mkdir(filepath.Join(elsewhere, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			r.env = append([]string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + c.dir}, c.env(elsewhere)...)
			out, errOut, code := r.run(t)
			if code != 0 {
				t.Fatalf("exit %d\n%s%s", code, out, errOut)
			}
			want := filepath.Join(r.cwd, strings.TrimSuffix(c.dir, "/"))
			relAssertInstalled(t, want)
			if !strings.Contains(out, "installed to ") || !strings.Contains(out, "/"+filepath.Base(want)+"/lx\n") {
				t.Errorf("stdout does not name %s/lx:\n%s", want, out)
			}
			for _, d := range []string{"bin", "-"} {
				if entries, _ := os.ReadDir(filepath.Join(elsewhere, d)); len(entries) != 0 {
					t.Errorf("installed into %s instead of %s", filepath.Join(elsewhere, d), want)
				}
			}
		})
	}
}

// GNU sha256sum escapes a file name holding a backslash and marks its digest
// with a leading '\'; hashing stdin keeps a good download from being
// reported as corrupted.
func relTestOddTmpdir(t *testing.T, bin string) {
	rel := relNewRelease(t, "lx_linux_amd64.tar.gz", "", nil)
	r := relNewRun(t, bin, "Linux", "x86_64")
	r.tmp = filepath.Join(t.TempDir(), `back\slash and space`)
	if err := os.Mkdir(r.tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	r.env = []string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + filepath.Join(r.home, "bin")}
	if _, errOut, code := r.run(t); code != 1 || !strings.Contains(errOut, "cannot extract lx") {
		t.Errorf("exit %d, stderr %q; want the checksum to pass (then no lx in the archive)", code, errOut)
	}
}

// If removing the temp dir fails after a good install, the script must still
// exit 0: its exit code is what `curl … | sh && …` and CI act on.
func relTestCleanupFailure(t *testing.T, bin string) {
	rel := relNewRelease(t, "lx_linux_amd64.tar.gz", relFakeLX, nil)
	r := relNewRun(t, bin, "Linux", "x86_64")
	r.keepTmp = true
	path := relMinimalPath(t, bin, nil, "shasum", "sha256sum")
	realRm, err := os.Readlink(filepath.Join(path, "rm"))
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(path, "rm"))
	relWrite(t, filepath.Join(path, "rm"), "#!/bin/sh\n[ \"$1\" != -rf ] || exit 1\nexec '"+realRm+"' \"$@\"\n", 0o755)
	dest := filepath.Join(r.home, "bin")
	r.env = []string{"LX_BASE_URL=file://" + rel.dir, "LX_INSTALL_DIR=" + dest, "PATH=" + path}
	out, errOut, code := r.run(t)
	if code != 0 || !strings.Contains(out, "installed to") {
		t.Errorf("exit %d after a good install whose cleanup failed; want 0\n%s%s", code, out, errOut)
	}
	relAssertInstalled(t, dest)
}

// curl … | sh runs whatever sh is: dash (Debian, Ubuntu), bash (macOS,
// Fedora), busybox ash (Alpine), ksh. Each must install and refuse alike.
func relTestShells(t *testing.T, bin string) {
	var shells [][]string
	shPath, _ := exec.LookPath("sh")
	shPath, _ = filepath.EvalSymlinks(shPath)
	for _, name := range []string{"dash", "bash", "ksh", "mksh", "yash", "busybox"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if real, _ := filepath.EvalSymlinks(p); real == shPath {
			continue // sh itself (dash on Debian and Ubuntu, busybox on Alpine) runs every other test
		}
		if name == "busybox" {
			shells = append(shells, []string{p, "sh"})
		} else {
			shells = append(shells, []string{p})
		}
	}
	if len(shells) == 0 {
		t.Skip("no other shells installed")
	}
	const archive = "lx_linux_amd64.tar.gz"
	for _, sh := range shells {
		name := filepath.Base(sh[0])
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			good := relNewRelease(t, archive, relFakeLX, nil)
			r := relNewRun(t, bin, "Linux", "x86_64")
			r.shell = sh
			dest := filepath.Join(r.home, ".local", "bin")
			r.env = []string{"LX_BASE_URL=file://" + good.dir}
			out, errOut, code := r.run(t)
			if code != 0 || !strings.Contains(out, "lx v9.9.9 installed to "+filepath.Join(dest, "lx")) {
				t.Fatalf("%s: exit %d\n%s%s", name, code, out, errOut)
			}
			relAssertInstalled(t, dest)

			bad := relNewRelease(t, archive, relFakeLX, nil).corrupt(t, archive)
			r = relNewRun(t, bin, "Linux", "x86_64")
			r.shell = sh
			dest = filepath.Join(r.home, "bin")
			r.env = []string{"LX_BASE_URL=file://" + bad.dir, "LX_INSTALL_DIR=" + dest}
			out, errOut, code = r.run(t)
			if code == 0 || !strings.Contains(errOut, "checksum mismatch") || strings.Contains(out, "installed") {
				t.Errorf("%s: corrupted archive: exit %d\n%s%s", name, code, out, errOut)
			}
			relAssertNoInstall(t, dest)
		})
	}
}
