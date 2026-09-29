package laya

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/laya"
)

func TestScriptProtocol(t *testing.T) {
	if !strings.Contains(Script, fmt.Sprintf("\nVERSION = %d\n", laya.Protocol)) {
		t.Fatalf("lx_laya.py doesn't speak protocol %d", laya.Protocol)
	}
}

func TestDaemonUnittest(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-B", "-m", "unittest", "-q", "test_lx_laya").CombinedOutput()
	if err != nil {
		t.Fatalf("python3 -m unittest test_lx_laya: %v\n%s", err, out)
	}
}
