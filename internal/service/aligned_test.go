package service

import (
	"testing"
	"time"
)

func TestNextAligned(t *testing.T) {
	hour, half := time.Hour, 30*time.Minute
	cases := []struct{ now, want string }{
		{"2026-09-11T10:00:00Z", "2026-09-11T10:30:00Z"},
		{"2026-09-11T10:29:59Z", "2026-09-11T10:30:00Z"},
		{"2026-09-11T10:30:00Z", "2026-09-11T11:30:00Z"}, // strictly after now
		{"2026-09-11T10:45:00Z", "2026-09-11T11:30:00Z"},
	}
	for _, c := range cases {
		now, _ := time.Parse(time.RFC3339, c.now)
		want, _ := time.Parse(time.RFC3339, c.want)
		if got := nextAligned(now, hour, half); !got.Equal(want) {
			t.Errorf("nextAligned(%s) = %s, want %s", c.now, got.Format(time.RFC3339), c.want)
		}
	}
}
