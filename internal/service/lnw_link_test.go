package service

import (
	"testing"
	"time"

	"github.com/flip1688/livescore/internal/model"
)

func TestNormalizeTeam(t *testing.T) {
	// Club-type words differ between feeds for the same club, so they must
	// not decide a match.
	if normalizeTeam("เอฟซี แอนดิจอน") != normalizeTeam("แอนดิจอน") {
		t.Error("Thai club-type word should not change the key")
	}
	if normalizeTeam("Arsenal FC") != normalizeTeam("arsenal") {
		t.Error("English club-type word should not change the key")
	}
	if normalizeTeam("Real Unión Club") == "" {
		t.Error("accented names must survive normalization")
	}
	// Different clubs must not collapse together.
	if normalizeTeam("Arsenal") == normalizeTeam("Aston Villa") {
		t.Error("distinct clubs collapsed to one key")
	}
}

func TestLinkResultsByTimeOnlyWhenUnambiguous(t *testing.T) {
	day := "2026-11-19"
	base := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)

	match := model.LnwMatch{ID: "rb_1", HomeName: "อาร์เซน่อล", AwayName: "เชลซี", KickoffAt: base}
	key := linkID(day, match.HomeName, match.AwayName)

	newIndex := func() map[string]*model.LnwMatchLink {
		return map[string]*model.LnwMatchLink{
			key: {ID: key, MatchDate: day, MatchID: "rb_1", Method: model.LnwLinkExactName, Confidence: 1},
		}
	}

	// One fixture in the window: the result attaches, but the link is
	// downgraded because the two feeds share no name to agree on.
	idx := newIndex()
	var cov model.LnwCoverage
	linkResultsByTime(
		[]model.LnwResult{{ID: "sm_1", KickoffAt: base.Add(5 * time.Minute)}},
		[]model.LnwMatch{match}, idx, day, time.Now(), &cov)
	if idx[key].ResultID != "sm_1" {
		t.Fatalf("expected result to link; got %q", idx[key].ResultID)
	}
	if idx[key].Method != model.LnwLinkTimeWindow || idx[key].Confidence >= 1 {
		t.Errorf("time-only link must be downgraded; got %q conf=%v",
			idx[key].Method, idx[key].Confidence)
	}

	// Two fixtures inside the window: ambiguous, so nothing is attached.
	// Guessing here would quietly corrupt the comparison this whole
	// subsystem exists to produce.
	other := model.LnwMatch{ID: "rb_2", HomeName: "ลิเวอร์พูล", AwayName: "เอฟเวอร์ตัน", KickoffAt: base.Add(10 * time.Minute)}
	otherKey := linkID(day, other.HomeName, other.AwayName)
	idx = newIndex()
	idx[otherKey] = &model.LnwMatchLink{ID: otherKey, MatchDate: day, MatchID: "rb_2"}
	linkResultsByTime(
		[]model.LnwResult{{ID: "sm_1", KickoffAt: base.Add(5 * time.Minute)}},
		[]model.LnwMatch{match, other}, idx, day, time.Now(), &cov)
	if idx[key].ResultID != "" || idx[otherKey].ResultID != "" {
		t.Error("ambiguous window must leave the result unlinked")
	}

	// Far outside the window: no link.
	idx = newIndex()
	linkResultsByTime(
		[]model.LnwResult{{ID: "sm_1", KickoffAt: base.Add(5 * time.Hour)}},
		[]model.LnwMatch{match}, idx, day, time.Now(), &cov)
	if idx[key].ResultID != "" {
		t.Error("result outside the window must not link")
	}
}

func TestLinkIDIsFeedAgnostic(t *testing.T) {
	// The same fixture seen through two feeds that spell the club slightly
	// differently must converge on one crosswalk row.
	a := linkID("2026-11-19", "เอฟซี แอนดิจอน", "เซอร์คอน เทอร์เมซ")
	b := linkID("2026-11-19", "แอนดิจอน", "เซอร์คอน เทอร์เมซ")
	if a != b {
		t.Errorf("link ids differ: %q vs %q", a, b)
	}
	// Reversing the fixture is a different match (home and away matter).
	if linkID("2026-11-19", "A", "B") == linkID("2026-11-19", "B", "A") {
		t.Error("home/away order must be significant")
	}
}
