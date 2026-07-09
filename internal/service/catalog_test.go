package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The matchdate cutoff is 04:00 ICT: a match at 03:00 on Jul 8 belongs to
// Jul 7's programme.
func TestCurrentMatchDate(t *testing.T) {
	cases := []struct {
		now  string
		want string
	}{
		{"2026-07-08T03:00:00+07:00", "2026-07-07"}, // late-night match → previous day
		{"2026-07-08T03:59:59+07:00", "2026-07-07"},
		{"2026-07-08T04:00:00+07:00", "2026-07-08"}, // cutoff boundary
		{"2026-07-08T15:00:00+07:00", "2026-07-08"},
		{"2026-07-08T23:59:00+07:00", "2026-07-08"},
		{"2026-07-07T21:00:00+00:00", "2026-07-08"}, // 04:00 ICT expressed in UTC
	}
	for _, c := range cases {
		now, err := time.Parse(time.RFC3339, c.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := CurrentMatchDate(now); got != c.want {
			t.Errorf("CurrentMatchDate(%s) = %s, want %s", c.now, got, c.want)
		}
	}
}

// Match/MatchEvents/MatchStats/Standings must reject an empty id before
// touching the store or cache — validated here on a zero-value Catalog (nil
// store/cache) so a bug that moved the check after an I/O call would panic
// instead of silently passing.
func TestMatchDetailRejectsEmptyID(t *testing.T) {
	c := &Catalog{}
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
	}{
		{"Match", func() error { _, err := c.Match(ctx, ""); return err }},
		{"MatchEvents", func() error { _, err := c.MatchEvents(ctx, ""); return err }},
		{"MatchStats", func() error { _, err := c.MatchStats(ctx, ""); return err }},
		{"Standings", func() error { _, err := c.Standings(ctx, ""); return err }},
		{"MatchAnalysis", func() error { _, err := c.MatchAnalysis(ctx, ""); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, ErrBadInput) {
				t.Errorf("%s(\"\") error = %v, want ErrBadInput", tc.name, err)
			}
		})
	}
}
