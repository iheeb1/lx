package fatal2

import (
	"fmt"
	"log"
	"testing"
)

func TestParse(t *testing.T) {
	t.Errorf("Parse(%q) = %d, want %d", "12", 21, 12)
}

func TestLoad(t *testing.T) {
	fmt.Println("loading config from testdata/app.yaml")
	log.Fatalf("open testdata/app.yaml: permission issue")
}

func TestNeverRuns(t *testing.T) {}
