package service

import (
	"testing"
	"time"

	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/thscore"
)

// applyChange merges a livescores/changes delta onto the previous match
// state; the delta payload never carries team/league fields (including the
// denormalized logo URLs), so it must start from prev and leave anything the
// delta doesn't touch — home_logo_url/away_logo_url in particular — exactly
// as it was, never blanking them.
func TestApplyChangePreservesLogoFields(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	prev := model.Match{
		ID:          "123",
		HomeTeamID:  "1",
		AwayTeamID:  "2",
		HomeLogoURL: "https://r2.example/teams/1.png",
		AwayLogoURL: "https://r2.example/teams/2.png",
		HomeScore:   1,
		AwayScore:   0,
	}
	ch := thscore.LivescoreChange{
		MatchID:   123,
		Status:    2,
		HomeScore: 2,
		AwayScore: 0,
	}

	got := applyChange(prev, ch, now)

	if got.HomeLogoURL != prev.HomeLogoURL {
		t.Errorf("HomeLogoURL = %q, want preserved %q", got.HomeLogoURL, prev.HomeLogoURL)
	}
	if got.AwayLogoURL != prev.AwayLogoURL {
		t.Errorf("AwayLogoURL = %q, want preserved %q", got.AwayLogoURL, prev.AwayLogoURL)
	}
	if got.HomeScore != 2 {
		t.Errorf("HomeScore = %d, want 2 (delta should still apply)", got.HomeScore)
	}
}

// analysisSkip must skip a match only when its analysis was fetched inside
// thscore's own 24h upstream cache window — refetching sooner just burns the
// per-match rate limit for a payload that hasn't changed.
func TestAnalysisSkip(t *testing.T) {
	now, err := time.Parse(time.RFC3339, "2026-07-09T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		fetchedAt time.Time
		want      bool
	}{
		{"never fetched (zero time)", time.Time{}, false},
		{"fetched just now", now, true},
		{"fetched 1h ago", now.Add(-1 * time.Hour), true},
		{"fetched 23h59m ago", now.Add(-(24*time.Hour - time.Minute)), true},
		{"fetched exactly 24h ago", now.Add(-24 * time.Hour), false},
		{"fetched 25h ago", now.Add(-25 * time.Hour), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := analysisSkip(now, c.fetchedAt); got != c.want {
				t.Errorf("analysisSkip(%v, %v) = %v, want %v", now, c.fetchedAt, got, c.want)
			}
		})
	}
}

// analysisSyncDates must return the matchdate buckets spanned by the 24h
// lookahead window. Since the lookahead equals one matchdate bucket's width
// (24h) and Bangkok is a fixed offset (no DST), now+24h always lands exactly
// one calendar day after now regardless of time-of-day — so in practice this
// always returns two consecutive dates; the dedup branch for from == to is
// defensive only (unreachable with the current 24h constant, but cheap
// insurance if the lookahead ever changes).
func TestAnalysisSyncDates(t *testing.T) {
	cases := []struct {
		name string
		now  string
		want []string
	}{
		{
			name: "midday, well inside a matchdate",
			now:  "2026-07-09T10:00:00+07:00",
			want: []string{"2026-07-09", "2026-07-10"},
		},
		{
			name: "before the 04:00 cutoff",
			now:  "2026-07-09T02:00:00+07:00",
			want: []string{"2026-07-08", "2026-07-09"},
		},
		{
			name: "exactly on the cutoff boundary",
			now:  "2026-07-09T04:00:00+07:00",
			want: []string{"2026-07-09", "2026-07-10"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, c.now)
			if err != nil {
				t.Fatal(err)
			}
			got := analysisSyncDates(now)
			if len(got) != len(c.want) {
				t.Fatalf("analysisSyncDates(%s) = %v, want %v", c.now, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("analysisSyncDates(%s) = %v, want %v", c.now, got, c.want)
				}
			}
		})
	}
}
