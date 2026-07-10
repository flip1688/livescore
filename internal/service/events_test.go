package service

import (
	"testing"

	"github.com/flip1688/livescore/internal/thscore"
)

// mergeEvents must union two per-match lists by EventID, with incoming
// winning on a conflict — syncEventsStats relies on this to let a later
// (today) fetch override an earlier (yesterday) one for the same event.
func TestMergeEvents(t *testing.T) {
	cases := []struct {
		name             string
		existing         []thscore.EventItem
		incoming         []thscore.EventItem
		wantIDs          []int
		wantIncomingWins int // EventID whose PlayerName must come from incoming
	}{
		{
			name:     "disjoint sets union",
			existing: []thscore.EventItem{{EventID: 1, PlayerName: "A"}},
			incoming: []thscore.EventItem{{EventID: 2, PlayerName: "B"}},
			wantIDs:  []int{1, 2},
		},
		{
			name:             "conflicting id: incoming wins",
			existing:         []thscore.EventItem{{EventID: 1, PlayerName: "old"}},
			incoming:         []thscore.EventItem{{EventID: 1, PlayerName: "new"}},
			wantIDs:          []int{1},
			wantIncomingWins: 1,
		},
		{
			name:     "empty existing",
			existing: nil,
			incoming: []thscore.EventItem{{EventID: 5}},
			wantIDs:  []int{5},
		},
		{
			name:     "empty incoming",
			existing: []thscore.EventItem{{EventID: 7}},
			incoming: nil,
			wantIDs:  []int{7},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeEvents(c.existing, c.incoming)
			if len(got) != len(c.wantIDs) {
				t.Fatalf("mergeEvents() = %d events, want %d", len(got), len(c.wantIDs))
			}
			byID := map[int]thscore.EventItem{}
			for _, e := range got {
				byID[e.EventID] = e
			}
			for _, id := range c.wantIDs {
				if _, ok := byID[id]; !ok {
					t.Errorf("mergeEvents() missing EventID %d in %+v", id, got)
				}
			}
			if c.wantIncomingWins != 0 {
				if byID[c.wantIncomingWins].PlayerName != "new" {
					t.Errorf("mergeEvents() EventID %d = %+v, want incoming copy (PlayerName=new)",
						c.wantIncomingWins, byID[c.wantIncomingWins])
				}
			}
		})
	}
}

// sortEvents must order by numeric minute first, then EventID, and must not
// be confused by stoppage-time suffixes like "45+2".
func TestSortEvents(t *testing.T) {
	events := []thscore.EventItem{
		{EventID: 20, Minute: "90"},
		{EventID: 5, Minute: "45+2"},
		{EventID: 2, Minute: "10"},
		{EventID: 1, Minute: "10"}, // same minute as above — tiebreak by lower EventID
		{EventID: 99, Minute: "bogus"},
	}
	sortEvents(events)

	wantOrder := []int{1, 2, 5, 20, 99}
	gotOrder := make([]int, len(events))
	for i, e := range events {
		gotOrder[i] = e.EventID
	}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("sortEvents() len = %d, want %d", len(gotOrder), len(wantOrder))
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Errorf("sortEvents() order = %v, want %v", gotOrder, wantOrder)
		}
	}
}

// eventsDigest must be stable regardless of input order (so comparing a
// freshly-merged list against a previously-stored one, which may have been
// persisted in a different order, doesn't false-positive as "changed"), and
// must change when a meaningful field changes.
func TestEventsDigest(t *testing.T) {
	a := []thscore.EventItem{
		{EventID: 1, Minute: "10", Type: 1, HomeEvent: true, PlayerName: "A"},
		{EventID: 2, Minute: "45", Type: 11, HomeEvent: false, PlayerName: "B"},
	}
	// same events, different input order
	b := []thscore.EventItem{
		{EventID: 2, Minute: "45", Type: 11, HomeEvent: false, PlayerName: "B"},
		{EventID: 1, Minute: "10", Type: 1, HomeEvent: true, PlayerName: "A"},
	}
	if eventsDigest(a) != eventsDigest(b) {
		t.Errorf("eventsDigest() differs for reordered-but-identical event sets")
	}

	// a genuinely different event set (extra substitution, e.g. the 2907400
	// production bug this fix targets) must produce a different digest.
	c := append(append([]thscore.EventItem(nil), a...), thscore.EventItem{EventID: 3, Minute: "60", Type: 11})
	if eventsDigest(a) == eventsDigest(c) {
		t.Errorf("eventsDigest() unchanged after adding an event")
	}

	// a field-level change on an existing event (e.g. corrected minute) must
	// also produce a different digest.
	d := []thscore.EventItem{
		{EventID: 1, Minute: "11", Type: 1, HomeEvent: true, PlayerName: "A"},
		{EventID: 2, Minute: "45", Type: 11, HomeEvent: false, PlayerName: "B"},
	}
	if eventsDigest(a) == eventsDigest(d) {
		t.Errorf("eventsDigest() unchanged after a field edit on an existing event")
	}

	// OprTime must NOT affect the digest — it's thscore's own record-modified
	// timestamp and can tick without any real content change.
	e := []thscore.EventItem{
		{EventID: 1, Minute: "10", Type: 1, HomeEvent: true, PlayerName: "A", OprTime: "09-07-2026 10:00:00"},
		{EventID: 2, Minute: "45", Type: 11, HomeEvent: false, PlayerName: "B", OprTime: "09-07-2026 10:05:00"},
	}
	if eventsDigest(a) != eventsDigest(e) {
		t.Errorf("eventsDigest() must be insensitive to OprTime-only differences")
	}
}
