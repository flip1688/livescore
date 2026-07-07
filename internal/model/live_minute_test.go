package model

import (
	"testing"
	"time"
)

func TestComputeLiveMinute(t *testing.T) {
	now := time.Date(2026, 7, 8, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		statusCode int
		halfStart  time.Time
		want       int
	}{
		{"unknown half start", 1, time.Time{}, 0},
		{"not running", 0, now.Add(-10 * time.Minute), 0},
		{"finished", -1, now.Add(-10 * time.Minute), 0},
		{"first half 10th minute", 1, now.Add(-9 * time.Minute), 10},
		{"first half capped at 45", 1, now.Add(-50 * time.Minute), 45},
		{"halftime pinned at 45", 2, now.Add(-60 * time.Minute), 45},
		{"second half 60th minute", 3, now.Add(-14 * time.Minute), 60},
		{"second half capped at 90", 3, now.Add(-50 * time.Minute), 90},
		{"extra time keeps counting", 4, now.Add(-5 * time.Minute), 96},
		{"half start in the future clamps to start", 1, now.Add(2 * time.Minute), 1},
	}
	for _, c := range cases {
		if got := ComputeLiveMinute(c.statusCode, c.halfStart, now); got != c.want {
			t.Errorf("%s: ComputeLiveMinute(%d, ...) = %d, want %d", c.name, c.statusCode, got, c.want)
		}
	}
}
