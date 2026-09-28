package tokens

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestCalibration(t *testing.T) {
	stats, corpus := os.Getenv("LX_TOKSTATS"), os.Getenv("LX_CORPUS")
	if stats == "" || corpus == "" {
		t.Skip("LX_TOKSTATS/LX_CORPUS not set")
	}
	raw, err := os.ReadFile(stats)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	ansi := regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
	var sumAbs, n float64
	worst := 0.0
	for _, row := range rows {
		b, _ := os.ReadFile(filepath.Join(corpus, row[0].(string)))
		s := ansi.ReplaceAllString(string(b), "")
		if len(s) < 200 {
			continue
		}
		cl := row[2].(float64)
		e := math.Abs(float64(Count(s))-cl) / cl
		sumAbs += e
		n++
		worst = max(worst, e)
	}
	mean := sumAbs / n
	fmt.Printf("files=%.0f mean|err|=%.1f%% max=%.1f%%\n", n, 100*mean, 100*worst)
	if mean > 0.08 {
		t.Errorf("mean error %.1f%% > 8%%", 100*mean)
	}
}
