package glued

import (
	"fmt"
	"testing"
)

func TestNoNewline(t *testing.T) {
	fmt.Print("progress: 3/5")
	t.Error("stopped early")
}

func TestOK(t *testing.T) {
	fmt.Print("ok-no-newline")
}
