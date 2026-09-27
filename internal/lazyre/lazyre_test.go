package lazyre

import (
	"sync"
	"testing"
)

func TestLazyCompileOnceConcurrent(t *testing.T) {
	r := New(`^a(b+)c$`)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m := r.FindStringSubmatch("abbc"); len(m) != 2 || m[1] != "bb" {
				t.Errorf("got %q", m)
			}
		}()
	}
	wg.Wait()
	if r.String() != `^a(b+)c$` {
		t.Fatal(r.String())
	}
}

func TestCompileAllReportsBadPatterns(t *testing.T) {
	New(`(unclosed`)
	if bad := CompileAll(); len(bad) != 1 || bad[0] != `(unclosed` {
		t.Fatalf("bad = %q", bad)
	}
}
