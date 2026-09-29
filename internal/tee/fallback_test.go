//go:build unix

package tee

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func sandboxed(t *testing.T) (fallback string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes through read-only directories")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LX_TEE_DIR", "")
	t.Setenv("LX_TEE", "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return filepath.Join(tmp, "lx-"+strconv.Itoa(os.Getuid()), "runs")
}

func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}

func perm(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestFallbackWhenCacheUnwritable(t *testing.T) {
	fb := sandboxed(t)
	readOnly(t, os.Getenv("HOME"))

	id, err := Save(Meta{Argv: []string{"go", "test", "./..."}}, "--- FAIL: TestX\n")
	if err != nil || id != 1 {
		t.Fatalf("Save = %d, %v", id, err)
	}
	if _, err := os.Stat(Dir()); err == nil {
		t.Fatalf("wrote into the read-only cache dir %s", Dir())
	}
	for p, want := range map[string]os.FileMode{
		filepath.Dir(fb): 0o700, fb: 0o700, filepath.Join(fb, "1.log"): 0o600, filepath.Join(fb, "1.json"): 0o600,
	} {
		if got := perm(t, p); got != want {
			t.Errorf("%s: mode %v, want %v", p, got, want)
		}
	}
	out, m, err := Load(1)
	if err != nil || out != "--- FAIL: TestX\n" || m.Argv[0] != "go" || m.State != StateDone {
		t.Fatalf("Load = %q %+v %v", out, m, err)
	}
	if r := Recent(5); len(r) != 1 || r[0].ID != 1 {
		t.Fatalf("Recent = %+v", r)
	}
	if m, err := ReadMeta(1); err != nil || m.State != StateDone {
		t.Fatalf("ReadMeta = %+v %v", m, err)
	}

	sp, err := Reserve(Meta{Argv: []string{"make"}})
	if err != nil || sp.ID() != 2 {
		t.Fatalf("Reserve = %v, %v", sp, err)
	}
	sp.Write([]byte("building\n"))
	if err := sp.Finish(Meta{Argv: []string{"make"}}, "building\ndone\n"); err != nil {
		t.Fatal(err)
	}
	if out, _, err := Load(2); err != nil || out != "building\ndone\n" {
		t.Fatalf("spooled run: %q %v", out, err)
	}
}

func TestFallbackIDsNeverCollide(t *testing.T) {
	fb := sandboxed(t)
	for i := 1; i <= 3; i++ {
		if id, err := Save(Meta{Argv: []string{"git", "status"}}, "primary "+strconv.Itoa(i)+"\n"); err != nil || id != i {
			t.Fatalf("Save = %d, %v", id, err)
		}
	}
	primary := Dir()
	readOnly(t, primary)
	if id, err := Save(Meta{Argv: []string{"go", "test"}}, "sandboxed\n"); err != nil || id != 4 {
		t.Fatalf("sandboxed Save = %d, %v", id, err)
	}
	if _, err := os.Stat(filepath.Join(fb, "4.log")); err != nil {
		t.Fatal("run 4 is not in the fallback store:", err)
	}
	os.Chmod(primary, 0o700)
	if id, err := Save(Meta{Argv: []string{"git", "diff"}}, "primary again\n"); err != nil || id != 5 {
		t.Fatalf("Save after the sandbox = %d, %v", id, err)
	}
	for id, want := range map[int]string{3: "primary 3\n", 4: "sandboxed\n", 5: "primary again\n"} {
		if out, _, err := Load(id); err != nil || out != want {
			t.Errorf("Load(%d) = %q, %v", id, out, err)
		}
	}
	var got []int
	for _, m := range Recent(10) {
		got = append(got, m.ID)
	}
	if len(got) != 5 || got[0] != 5 || got[1] != 4 || got[4] != 1 {
		t.Fatalf("Recent ids = %v", got)
	}
}

func TestFallbackRefusesUnsafeDirs(t *testing.T) {
	cases := map[string]func(t *testing.T, base string) (victim string){
		"base is a symlink": func(t *testing.T, base string) string {
			real := t.TempDir()
			os.Symlink(real, base)
			return real
		},
		"runs is a symlink": func(t *testing.T, base string) string {
			real := t.TempDir()
			os.Mkdir(base, 0o700)
			os.Symlink(real, filepath.Join(base, "runs"))
			return real
		},
		"base is group-writable": func(t *testing.T, base string) string {
			os.Mkdir(base, 0o700)
			os.Chmod(base, 0o770)
			return filepath.Join(base, "runs")
		},
		"runs is world-readable": func(t *testing.T, base string) string {
			os.MkdirAll(filepath.Join(base, "runs"), 0o700)
			os.Chmod(filepath.Join(base, "runs"), 0o755)
			return filepath.Join(base, "runs")
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			fb := sandboxed(t)
			readOnly(t, os.Getenv("HOME"))
			victim := plant(t, filepath.Dir(fb))
			os.MkdirAll(victim, 0o700)
			os.WriteFile(filepath.Join(victim, "1.log"), []byte("forged output\n"), 0o600)
			os.WriteFile(filepath.Join(victim, "1.json"), []byte(`{"id":1,"argv":["git","status"]}`), 0o600)

			if id, err := Save(Meta{Argv: []string{"go", "test"}}, "secret\n"); err == nil {
				t.Fatalf("Save stored run %d in an unsafe directory", id)
			}
			if _, err := os.Stat(filepath.Join(victim, "2.log")); err == nil {
				t.Fatal("wrote into an unsafe directory")
			}
			if out, _, err := Load(1); err == nil {
				t.Fatalf("Load served a run from an unsafe directory: %q", out)
			}
			if r := Recent(5); len(r) != 0 {
				t.Fatalf("Recent listed runs from an unsafe directory: %+v", r)
			}
		})
	}
}

func TestExplicitTeeDirHasNoFallback(t *testing.T) {
	fb := sandboxed(t)
	dir := filepath.Join(t.TempDir(), "runs")
	readOnly(t, dir)
	t.Setenv("LX_TEE_DIR", dir)
	if id, err := Save(Meta{Argv: []string{"go", "test"}}, "x\n"); err == nil {
		t.Fatalf("Save = %d into a read-only LX_TEE_DIR", id)
	}
	if _, err := os.Stat(filepath.Dir(fb)); err == nil {
		t.Fatal("LX_TEE_DIR is set, yet lx created the fallback store")
	}
}

func TestNoCacheDirUsesPrivateStore(t *testing.T) {
	fb := sandboxed(t)
	t.Setenv("HOME", "")
	if _, err := os.UserCacheDir(); err == nil {
		t.Skip("this platform finds a cache dir without HOME")
	}
	if Dir() != fb {
		t.Fatalf("Dir() = %s, want the private %s", Dir(), fb)
	}
	id, err := Save(Meta{Argv: []string{"go", "test"}}, "ok\n")
	if err != nil {
		t.Fatal(err)
	}
	if out, _, err := Load(id); err != nil || out != "ok\n" {
		t.Fatalf("Load = %q, %v", out, err)
	}
	if perm(t, filepath.Dir(fb)) != 0o700 {
		t.Errorf("%s is not private", filepath.Dir(fb))
	}
}

func TestConcurrentStoresNeverShareAnID(t *testing.T) {
	fb := sandboxed(t)
	primary := Dir()
	if !privateDir(fb, true) {
		t.Fatal("cannot create the fallback store")
	}
	const n = 60
	ids := make(chan int, 2*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		for _, pair := range [][2]string{{primary, fb}, {fb, primary}} {
			wg.Add(1)
			go func(dir, other string) {
				defer wg.Done()
				s, err := reserveIn(dir, []string{other}, Meta{Argv: []string{"true"}}, false)
				if err != nil {
					t.Error(err)
					return
				}
				ids <- s.ID()
			}(pair[0], pair[1])
		}
	}
	wg.Wait()
	close(ids)
	seen := map[int]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("run id %d was given out twice", id)
		}
		seen[id] = true
	}
}
