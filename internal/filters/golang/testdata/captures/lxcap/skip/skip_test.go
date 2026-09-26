package skip

import "testing"

func TestNeedsDocker(t *testing.T)  { t.Skip("docker not available") }
func TestNeedsNetwork(t *testing.T) { t.Skip("set LX_NET=1 to run") }
func TestLogs(t *testing.T) {
	for i := 0; i < 3; i++ {
		t.Logf("step %d ok", i)
	}
}
