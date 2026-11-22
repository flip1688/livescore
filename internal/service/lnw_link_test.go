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

func TestHasThaiSeparatesComparableNames(t *testing.T) {
	if !hasThai("ปาโดวา") {
		t.Error("Thai name not recognized")
	}
	// Real row from 2026-11-22: the vendor mixes scripts inside one fixture.
	if hasThai("arezzo") {
		t.Error("Latin name must not count as Thai")
	}
}

func TestLinkRemainingByTimeRefusesToGuess(t *testing.T) {
	day := "2026-11-22"
	base := time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC)

	// A Latin-named fixture: no name comparison with thscore is possible,
	// so this pass is its only chance of being linked.
	m := model.LnwMatch{
		ID: "rb_1", HomeName: "arezzo", AwayName: "ปาโดวา",
		KickoffAt: base, HasScore: true, HomeScore: 2, AwayScore: 1,
	}
	key := linkID(day, m.HomeName, m.AwayName)
	newIndex := func() map[string]*model.LnwMatchLink {
		return map[string]*model.LnwMatchLink{
			key: {ID: key, MatchDate: day, MatchID: "rb_1"},
		}
	}

	// One candidate in the window: linked, but only at time-window
	// confidence — nothing here proves it is the same fixture.
	idx := newIndex()
	var cov model.LnwCoverage
	linkRemainingByTime(
		[]model.LnwMatch{m},
		[]model.Match{{ID: "th_1", KickoffAt: base.Add(2 * time.Minute)}},
		idx, day, base, &cov,
	)
	if got := idx[key].ThscoreID; got != "th_1" {
		t.Fatalf("unique candidate not linked: %q", got)
	}
	if idx[key].Method != model.LnwLinkTimeWindow || idx[key].Confidence != 0.5 {
		t.Errorf("link advertised too strongly: %s/%v", idx[key].Method, idx[key].Confidence)
	}

	// Two candidates, one of which has the same score: the score breaks the
	// tie and the link says so.
	idx = newIndex()
	cov = model.LnwCoverage{}
	linkRemainingByTime(
		[]model.LnwMatch{m},
		[]model.Match{
			{ID: "th_1", KickoffAt: base, HomeScore: 0, AwayScore: 0},
			{ID: "th_2", KickoffAt: base, HomeScore: 2, AwayScore: 1},
		},
		idx, day, base, &cov,
	)
	if idx[key].ThscoreID != "th_2" || idx[key].Method != model.LnwLinkTimeScore {
		t.Errorf("score tie-break failed: %+v", idx[key])
	}
	if cov.LinkedByScore != 1 {
		t.Errorf("LinkedByScore = %d, want 1", cov.LinkedByScore)
	}

	// Two candidates with the same score as each other: unresolvable, and
	// the row must stay unlinked rather than pick one.
	idx = newIndex()
	cov = model.LnwCoverage{}
	linkRemainingByTime(
		[]model.LnwMatch{m},
		[]model.Match{
			{ID: "th_1", KickoffAt: base, HomeScore: 2, AwayScore: 1},
			{ID: "th_2", KickoffAt: base, HomeScore: 2, AwayScore: 1},
		},
		idx, day, base, &cov,
	)
	if idx[key].ThscoreID != "" {
		t.Error("ambiguous pairing was guessed")
	}
	if cov.UnlinkedAmbiguous != 1 || cov.UnlinkedScript != 1 {
		t.Errorf("unlinked reason not recorded: ambiguous=%d script=%d",
			cov.UnlinkedAmbiguous, cov.UnlinkedScript)
	}

	// Nothing in the window at all.
	idx = newIndex()
	cov = model.LnwCoverage{}
	linkRemainingByTime(
		[]model.LnwMatch{m},
		[]model.Match{{ID: "th_1", KickoffAt: base.Add(time.Hour)}},
		idx, day, base, &cov,
	)
	if idx[key].ThscoreID != "" || cov.UnlinkedNoCandidate != 1 {
		t.Errorf("out-of-window candidate was used: %+v", idx[key])
	}
}
