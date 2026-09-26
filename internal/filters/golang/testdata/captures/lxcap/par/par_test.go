package par

import (
	"testing"
	"time"
)

func TestParallel(t *testing.T) {
	cases := []struct {
		name string
		a, b int
		want int
	}{
		{"small", 1, 2, 3},
		{"conflict", 2, 2, 5},
		{"large", 100, 200, 300},
		{"negative", -1, -1, -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			time.Sleep(10 * time.Millisecond)
			t.Logf("adding %d + %d", tc.a, tc.b)
			if got := Add(tc.a, tc.b); got != tc.want {
				t.Errorf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestSequential(t *testing.T) {
	t.Log("sequential runs fine")
}
