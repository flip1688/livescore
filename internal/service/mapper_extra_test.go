package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/thscore"
)

// thscore freezes the top-level homeScore/awayScore at the 90-minute score
// once a knockout match enters extra time — the live score only moves inside
// extraExplain. The mappers must promote the ET-inclusive aggregate into the
// main score (penalties excluded) and keep the frozen 90' score in Extra.
// Payload shape taken verbatim from World Cup QF 2907403 (2026-07-12).
func TestMatchFromLivescoreExtraTime(t *testing.T) {
	now := time.Date(2026, 7, 12, 6, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"matchId": 2907403, "leagueId": 75, "matchTime": "12-07-2026 08:00:00",
		"status": -1, "homeId": 766, "awayId": 648,
		"homeScore": 1, "awayScore": 1, "homeHalfScore": 1, "awayHalfScore": 0,
		"extraExplain": {
			"kickOff": 2, "minute": 90, "homeScore": 1, "awayScore": 1,
			"extraTimeStatus": 1, "extraHomeScore": 3, "extraAwayScore": 1,
			"penHomeScore": 0, "penAwayScore": 0,
			"twoRoundsHomeScore": 0, "twoRoundsAwayScore": 0, "winner": 1
		}
	}`)
	var m thscore.LivescoreMatch
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got, err := matchFromLivescore(m, now)
	if err != nil {
		t.Fatalf("matchFromLivescore: %v", err)
	}
	if got.HomeScore != 3 || got.AwayScore != 1 {
		t.Errorf("score = %d-%d, want ET-inclusive 3-1", got.HomeScore, got.AwayScore)
	}
	if got.Extra == nil {
		t.Fatal("Extra = nil, want knockout detail")
	}
	if got.Extra.FTHomeScore != 1 || got.Extra.FTAwayScore != 1 {
		t.Errorf("FT score = %d-%d, want frozen 1-1", got.Extra.FTHomeScore, got.Extra.FTAwayScore)
	}
	if got.Extra.Winner != 1 || got.Extra.StatusCode != 1 {
		t.Errorf("winner/status = %d/%d, want 1/1", got.Extra.Winner, got.Extra.StatusCode)
	}
}

// A match decided on penalties keeps the main score at the (ET-inclusive)
// aggregate — shootout goals never count toward it.
func TestApplyExtraPenaltiesExcluded(t *testing.T) {
	m := model.Match{HomeScore: 0, AwayScore: 0}
	applyExtra(&m, thscore.ExtraExplain{
		ExtraTimeStatus: 1,
		PenHomeScore:    4, PenAwayScore: 2,
		Winner: 1,
	})
	if m.HomeScore != 0 || m.AwayScore != 0 {
		t.Errorf("score = %d-%d, want 0-0 (pens excluded)", m.HomeScore, m.AwayScore)
	}
	if m.Extra == nil || m.Extra.PenHomeScore != 4 || m.Extra.PenAwayScore != 2 {
		t.Errorf("Extra = %+v, want pens 4-2", m.Extra)
	}
}

// A normal 90-minute row (no extraExplain object, or the empty-string form
// thscore sends on some endpoints) must leave the match untouched.
func TestApplyExtraAbsent(t *testing.T) {
	var e thscore.ExtraExplain
	if err := json.Unmarshal([]byte(`""`), &e); err != nil {
		t.Fatalf("unmarshal empty-string extraExplain: %v", err)
	}
	m := model.Match{HomeScore: 2, AwayScore: 1}
	applyExtra(&m, e)
	if m.Extra != nil {
		t.Errorf("Extra = %+v, want nil", m.Extra)
	}
	if m.HomeScore != 2 || m.AwayScore != 1 {
		t.Errorf("score = %d-%d, want unchanged 2-1", m.HomeScore, m.AwayScore)
	}
}

// The changes.aspx delta populates only changed fields — a row for a match
// past 90' can arrive without extraExplain. applyChange must not snap the
// score back to the frozen 90-minute value or drop the knockout detail.
func TestApplyChangeKeepsExtraOnPartialDelta(t *testing.T) {
	now := time.Date(2026, 7, 12, 6, 0, 0, 0, time.UTC)
	prev := model.Match{
		ID: "2907403", HomeScore: 3, AwayScore: 1,
		Extra: &model.MatchExtra{StatusCode: 3, FTHomeScore: 1, FTAwayScore: 1},
	}
	ch := thscore.LivescoreChange{
		MatchID: 2907403, Status: 4,
		HomeScore: 1, AwayScore: 1, // frozen 90' score, no extraExplain
	}

	got := applyChange(prev, ch, now)

	if got.HomeScore != 3 || got.AwayScore != 1 {
		t.Errorf("score = %d-%d, want 3-1 kept from prev", got.HomeScore, got.AwayScore)
	}
	if got.Extra == nil {
		t.Error("Extra dropped, want kept from prev")
	}
}

// Rows for long-finished matches re-appear in the changes feed carrying a
// skeletal extraExplain — winner/status set but every score zeroed (observed
// on production, 2026-07-13) — which must not wipe the real knockout detail.
func TestApplyChangeIgnoresSkeletalExtraDelta(t *testing.T) {
	now := time.Date(2026, 7, 13, 0, 45, 0, 0, time.UTC)
	prev := model.Match{
		ID: "2907403", HomeScore: 3, AwayScore: 1,
		Extra: &model.MatchExtra{StatusCode: 1, FTHomeScore: 1, FTAwayScore: 1, Winner: 1},
	}
	ch := thscore.LivescoreChange{
		MatchID: 2907403, Status: -1,
		HomeScore: 1, AwayScore: 1, // frozen 90' score
		Winner:       1,
		ExtraExplain: thscore.ExtraExplain{ExtraTimeStatus: 1, Winner: 1}, // scores all zero
	}

	got := applyChange(prev, ch, now)

	if got.HomeScore != 3 || got.AwayScore != 1 {
		t.Errorf("score = %d-%d, want 3-1 kept from prev", got.HomeScore, got.AwayScore)
	}
	if got.Extra == nil || got.Extra.FTHomeScore != 1 || got.Extra.FTAwayScore != 1 {
		t.Errorf("Extra = %+v, want prev's ft 1-1 kept", got.Extra)
	}
}

// A genuinely scoreless tie (0-0 through 120 minutes) still records its
// detail on the first delta that reports it — pens make the payload
// trustworthy even with the ET aggregate at zero.
func TestApplyChangeScorelessPensApplied(t *testing.T) {
	now := time.Date(2026, 7, 13, 0, 45, 0, 0, time.UTC)
	prev := model.Match{ID: "1", HomeScore: 0, AwayScore: 0}
	ch := thscore.LivescoreChange{
		MatchID: 1, Status: -1,
		ExtraExplain: thscore.ExtraExplain{ExtraTimeStatus: 1, PenHomeScore: 4, PenAwayScore: 2, Winner: 1},
	}

	got := applyChange(prev, ch, now)

	if got.Extra == nil || got.Extra.PenHomeScore != 4 {
		t.Errorf("Extra = %+v, want pens 4-2 recorded", got.Extra)
	}
	if got.HomeScore != 0 || got.AwayScore != 0 {
		t.Errorf("score = %d-%d, want 0-0 (pens excluded)", got.HomeScore, got.AwayScore)
	}
}

// When the delta does carry extraExplain, it wins over prev.
func TestApplyChangeAppliesExtraDelta(t *testing.T) {
	now := time.Date(2026, 7, 12, 6, 0, 0, 0, time.UTC)
	prev := model.Match{ID: "2907403", HomeScore: 1, AwayScore: 1}
	ch := thscore.LivescoreChange{
		MatchID: 2907403, Status: 4,
		HomeScore: 1, AwayScore: 1,
		ExtraExplain: thscore.ExtraExplain{
			ExtraTimeStatus: 3, HomeScore: 1, AwayScore: 1,
			ExtraHomeScore: 2, ExtraAwayScore: 1,
		},
	}

	got := applyChange(prev, ch, now)

	if got.HomeScore != 2 || got.AwayScore != 1 {
		t.Errorf("score = %d-%d, want live ET aggregate 2-1", got.HomeScore, got.AwayScore)
	}
	if got.Extra == nil || got.Extra.StatusCode != 3 {
		t.Errorf("Extra = %+v, want in-ET detail", got.Extra)
	}
}
