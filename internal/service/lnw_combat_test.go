package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flip1688/livescore/internal/lnw"
)

func TestMapLnwCombatBout(t *testing.T) {
	fetched := time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC)
	// A real row from /api/public/wuachon on 2026-11-22.
	b := lnw.CombatBout{
		ID:          68539,
		RoundName:   "22/11/69 สนามชนไก่ดาวอังคาร คู่ที่ 33",
		Type:        "cock",
		TypeLabel:   "ไก่ชน",
		RedCorner:   lnw.CombatCorner{Name: "ไก่แดง"},
		BlueCorner:  lnw.CombatCorner{Name: "ไก่น้ำเงิน"},
		Stadium:     "สนามชนไก่ดาวอังคาร",
		DateTime:    "2026-11-22T11:33:00+07:00",
		Status:      "ended",
		StatusLabel: "จบแล้ว",
		Result:      "tie",
		ResultLabel: "เสมอ / ถอน",
		Stakes:      380000,
		IsLive:      true,
		Stream:      json.RawMessage(`{"url":"https://example.invalid/s?stk=secret"}`),
	}

	got, ok := mapLnwCombatBout(b, lnw.SportWuachon, fetched)
	if !ok {
		t.Fatal("bout rejected")
	}
	// The id must carry the feed: the two combat feeds number independently.
	if got.ID != "wuachon:68539" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.BoutNo != 33 {
		t.Errorf("BoutNo = %d, want 33 from %q", got.BoutNo, b.RoundName)
	}
	// 11:33 +07:00 is 04:33 UTC, and under the 04:00 cutoff it belongs to
	// the same calendar day rather than the previous one.
	if !got.StartAt.Equal(time.Date(2026, 11, 22, 4, 33, 0, 0, time.UTC)) {
		t.Errorf("StartAt = %v", got.StartAt)
	}
	if got.MatchDate != "2026-11-22" {
		t.Errorf("MatchDate = %q", got.MatchDate)
	}
	// Upstream contradicts itself here; both values must survive so the
	// disagreement can be counted rather than silently resolved.
	if got.Status != "ended" || !got.IsLive {
		t.Errorf("status/live not preserved: %q/%v", got.Status, got.IsLive)
	}
	if !got.HasStream {
		t.Error("stream presence lost")
	}

	// A bout with no readable start time is skipped: there is no clock to
	// fall back on and a zero instant would pollute every day's query.
	b.DateTime = ""
	if _, ok := mapLnwCombatBout(b, lnw.SportWuachon, fetched); ok {
		t.Error("bout with no start time was stored")
	}
}

func TestMapLnwCombatBoutKeepsUnmappedResult(t *testing.T) {
	fetched := time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC)
	// 10 of 212 bouts came back with result "green", which is upstream
	// leaking an internal value. It must not be renamed into a Thai
	// meaning, and it must be visible that we did not understand it.
	b := lnw.CombatBout{
		ID:       1,
		DateTime: "2026-11-22T11:33:00+07:00",
		Result:   "green",
	}
	got, ok := mapLnwCombatBout(b, lnw.SportWuachon, fetched)
	if !ok {
		t.Fatal("bout rejected")
	}
	if got.ResultKnown {
		t.Error("unmapped result reported as known")
	}
	if got.Result != "" || got.RawResult != "green" {
		t.Errorf("Result/RawResult = %q/%q", got.Result, got.RawResult)
	}
}

func TestBoxingProgramHasEvents(t *testing.T) {
	// Every programme observed so far looks like this.
	empty := lnw.BoxingProgram{TotalEvents: 0, Events: json.RawMessage(`[]`)}
	if empty.HasEvents() {
		t.Error("empty card reported as present")
	}
	// Either signal alone is enough: the count and the array have never
	// disagreed, but a card must not be missed if they ever do.
	if !(lnw.BoxingProgram{TotalEvents: 3}).HasEvents() {
		t.Error("totalEvents ignored")
	}
	if !(lnw.BoxingProgram{Events: json.RawMessage(`[{"id":1}]`)}).HasEvents() {
		t.Error("non-empty events array ignored")
	}
}
