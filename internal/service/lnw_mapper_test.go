package service

import (
	"testing"
	"time"

	"github.com/flip1688/livescore/internal/lnw"
	"github.com/flip1688/livescore/internal/model"
)

func TestMatchStatusFrom(t *testing.T) {
	tests := []struct {
		raw    string
		isLive bool
		want   model.MatchStatus
	}{
		{"รอแข่ง", false, model.MatchScheduled},
		{"30'", true, model.MatchLive},
		{"HT", true, model.MatchHalftime},
		{"FT", false, model.MatchFinished},
		// isLive alone is enough: upstream has been seen to send a live row
		// whose label has not caught up yet.
		{"", true, model.MatchLive},
		// An unknown label on a match that is not live must not be read as
		// live — scheduled is the safe default.
		{"ยกเลิกชั่วคราว", false, model.MatchScheduled},
	}
	for _, tc := range tests {
		if got := matchStatusFrom(tc.raw, tc.isLive); got != tc.want {
			t.Errorf("matchStatusFrom(%q,%v) = %q; want %q", tc.raw, tc.isLive, got, tc.want)
		}
	}
}

func TestResultStateFrom(t *testing.T) {
	tests := map[string]model.MatchStatus{
		"NS":              model.MatchScheduled,
		"INPLAY_1ST_HALF": model.MatchLive,
		"INPLAY_2ND_HALF": model.MatchLive,
		"HT":              model.MatchHalftime,
		"FT":              model.MatchFinished,
		"FT_PEN":          model.MatchFinished,
		"AET":             model.MatchFinished,
		"POSTP":           model.MatchPostponed,
		"CANCL":           model.MatchCanceled,
		"SOMETHING_NEW":   model.MatchScheduled,
	}
	for in, want := range tests {
		if got := resultStateFrom(in); got != want {
			t.Errorf("resultStateFrom(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestMapLnwMatch(t *testing.T) {
	fetchedAt := time.Date(2026, 11, 19, 20, 36, 0, 0, Bangkok)

	// A live row, with a channel carrying a tokenized stream URL.
	in := lnw.Match{
		ID: "rb_4344928", Time: "20:00", Day: "today",
		League: "พีเอฟแอล", TeamHome: "เซอร์คอน เทอร์เมซ", TeamAway: "เอฟซี แอนดิจอน",
		IsLive: true, Score: "1 - 0", MatchStatus: "30'",
		Channels: []lnw.Channel{{
			ID: "rb_4344928_1", Name: "TyC Sports HD",
			StreamURL: "https://example/api/streams/rb_4344928?channel=x",
		}},
	}
	got, ok := mapLnwMatch(in, fetchedAt)
	if !ok {
		t.Fatal("mapLnwMatch returned not ok")
	}
	if !got.KickoffAt.Equal(time.Date(2026, 11, 19, 20, 0, 0, 0, Bangkok)) {
		t.Errorf("KickoffAt = %v", got.KickoffAt.In(Bangkok))
	}
	if got.MatchDate != "2026-11-19" {
		t.Errorf("MatchDate = %q; want 2026-11-19", got.MatchDate)
	}
	if got.Status != model.MatchLive || got.Minute != 30 {
		t.Errorf("Status/Minute = %q/%d", got.Status, got.Minute)
	}
	if got.HomeScore != 1 || got.AwayScore != 0 || !got.HasScore {
		t.Errorf("score = %d-%d has=%v", got.HomeScore, got.AwayScore, got.HasScore)
	}
	// The channel survives; its tokenized URL must not, since the vendor's
	// own docs forbid caching it beyond 15 seconds.
	if len(got.Channels) != 1 || got.Channels[0].Name != "TyC Sports HD" {
		t.Fatalf("channels = %+v", got.Channels)
	}
	if got.RawDay != "today" || got.RawTime != "20:00" {
		t.Error("raw day/time must be preserved so a bad resolution can be spotted")
	}

	// A 03:00 kick-off belongs to the PREVIOUS match day under the 04:00
	// ICT cutoff, which is what makes the two vendors comparable day by day.
	early := in
	early.Time = "03:00"
	early.Day = "tomorrow"
	got, ok = mapLnwMatch(early, fetchedAt)
	if !ok {
		t.Fatal("not ok")
	}
	if got.MatchDate != "2026-11-19" {
		t.Errorf("03:00 tomorrow -> MatchDate %q; want 2026-11-19 (04:00 cutoff)", got.MatchDate)
	}

	// Pre-kick-off rows read "vs"; that must not become a 0-0 draw.
	pre := in
	pre.Score = "vs"
	pre.IsLive = false
	pre.MatchStatus = "รอแข่ง"
	got, _ = mapLnwMatch(pre, fetchedAt)
	if got.HasScore {
		t.Error(`"vs" must not be stored as a score`)
	}
	if got.Status != model.MatchScheduled {
		t.Errorf("pre-match status = %q", got.Status)
	}

	// A row without a resolvable time is dropped, not stored at midnight.
	bad := in
	bad.Time = ""
	if _, ok := mapLnwMatch(bad, fetchedAt); ok {
		t.Error("row with no time must be skipped")
	}
}

func TestMapLnwResultPrefixesID(t *testing.T) {
	league := lnw.ResultLeague{LeagueID: 570, LeagueName: "Copa Del Rey"}
	in := lnw.ResultMatch{
		ID: 19893334, StartingAt: "2026-11-18 18:00:00", State: "FT_PEN",
		HomeTeam: lnw.ResultTeam{Name: "Portugalete"},
		AwayTeam: lnw.ResultTeam{Name: "Real Unión Club"},
	}
	got, ok := mapLnwResult(league, in)
	if !ok {
		t.Fatal("not ok")
	}
	// The prefix is what keeps this feed's integer ids from colliding with
	// the analysis feed's, which share no namespace.
	if got.ID != "sm_19893334" {
		t.Errorf("ID = %q; want sm_19893334", got.ID)
	}
	if got.Status != model.MatchFinished {
		t.Errorf("Status = %q", got.Status)
	}
	if got.MatchDate != "2026-11-18" {
		t.Errorf("MatchDate = %q", got.MatchDate)
	}
}

func TestMapLnwStandingNeedsAnchor(t *testing.T) {
	ok1 := lnw.Standing{LeagueName: "พรีเมียร์ลีก อังกฤษ", LeagueURL: "/standings#epl"}
	got, ok := mapLnwStanding(ok1, time.Now(), true)
	if !ok || got.ID != "epl" {
		t.Fatalf("got %q ok=%v; want epl", got.ID, ok)
	}
	if !got.IsStale {
		t.Error("upstream's stale flag must be carried into storage")
	}
	// No anchor means no stable key; the table is dropped rather than keyed
	// by a display name that upstream may reword.
	if _, ok := mapLnwStanding(lnw.Standing{LeagueName: "x", LeagueURL: "/standings"}, time.Now(), false); ok {
		t.Error("table without an anchor must be skipped")
	}
}
