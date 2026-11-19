package lnw

import (
	"testing"
	"time"
)

var bkk = time.FixedZone("ICT", 7*60*60)

func TestParseScore(t *testing.T) {
	tests := []struct {
		in         string
		home, away int
		ok         bool
	}{
		{"2 - 1", 2, 1, true},
		{"0 - 0", 0, 0, true},
		{"6 - 0", 6, 0, true},
		// Before kick-off upstream sends "vs", not an empty string or 0-0.
		// A caller that reads this as 0-0 shows a drawn game that has not
		// started, so ok must be false.
		{"vs", 0, 0, false},
		{"", 0, 0, false},
		{"2 - ", 0, 0, false},
	}
	for _, tc := range tests {
		h, a, ok := ParseScore(tc.in)
		if h != tc.home || a != tc.away || ok != tc.ok {
			t.Errorf("ParseScore(%q) = %d,%d,%v; want %d,%d,%v", tc.in, h, a, ok, tc.home, tc.away, tc.ok)
		}
	}
}

func TestParseMinute(t *testing.T) {
	tests := map[string]int{
		"30'":    30,
		"45'":    45,
		"90'":    90,
		"45+2'":  45,
		"HT":     0,
		"FT":     0,
		"รอแข่ง": 0,
		"":       0,
		"999'":   0,
	}
	for in, want := range tests {
		if got := ParseMinute(in); got != want {
			t.Errorf("ParseMinute(%q) = %d; want %d", in, got, want)
		}
	}
}

func TestResolveKickoff(t *testing.T) {
	// Fetched 19 Nov 2026 20:36 Bangkok.
	now := time.Date(2026, 11, 19, 20, 36, 0, 0, bkk)

	got, ok := ResolveKickoff("today", "22:00", now, bkk)
	if !ok || !got.Equal(time.Date(2026, 11, 19, 22, 0, 0, 0, bkk)) {
		t.Fatalf(`today 22:00 = %v,%v`, got, ok)
	}

	got, ok = ResolveKickoff("tomorrow", "02:45", now, bkk)
	if !ok || !got.Equal(time.Date(2026, 11, 20, 2, 45, 0, 0, bkk)) {
		t.Fatalf(`tomorrow 02:45 = %v,%v`, got, ok)
	}

	// A "today" row timed before the fetch is in the past, not rolled
	// forward: upstream means the calendar day, not "the next 00:45".
	got, ok = ResolveKickoff("today", "00:45", now, bkk)
	if !ok || !got.Equal(time.Date(2026, 11, 19, 0, 45, 0, 0, bkk)) {
		t.Fatalf(`today 00:45 = %v,%v`, got, ok)
	}

	// Resolution is relative to the fetch clock, so the same row fetched a
	// day later means a different instant. This is why rows must never be
	// stored raw and converted afterwards.
	later := now.AddDate(0, 0, 1)
	again, _ := ResolveKickoff("today", "22:00", later, bkk)
	if again.Equal(time.Date(2026, 11, 19, 22, 0, 0, 0, bkk)) {
		t.Error("same row resolved a day later should not yield the original instant")
	}

	if _, ok := ResolveKickoff("yesterday", "22:00", now, bkk); ok {
		t.Error(`day "yesterday" is not a value upstream sends; want not ok`)
	}
	if _, ok := ResolveKickoff("today", "", now, bkk); ok {
		t.Error("empty time must not resolve to midnight")
	}
}

func TestStandingKey(t *testing.T) {
	tests := map[string]string{
		"/standings#epl":    "epl",
		"/standings#thai-1": "thai-1",
		"/standings":        "",
		"":                  "",
		"/standings#":       "",
	}
	for in, want := range tests {
		if got := StandingKey(in); got != want {
			t.Errorf("StandingKey(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestParseResultTime(t *testing.T) {
	got, ok := ParseResultTime("2026-11-18 18:00:00", bkk)
	if !ok || !got.Equal(time.Date(2026, 11, 18, 18, 0, 0, 0, bkk)) {
		t.Fatalf("ParseResultTime = %v,%v", got, ok)
	}
	if _, ok := ParseResultTime("2026-11-18T18:00:00Z", bkk); ok {
		t.Error("RFC3339 is not this endpoint's format; want not ok")
	}
}
