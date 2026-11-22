package service

import "testing"

func TestDisciplineOf(t *testing.T) {
	// Real programme titles from the fighting feed on 2026-11-22.
	cases := map[string]string{
		"Muay Thai Alliance 2026 Ep.44":           "muay-thai",
		"Rajadamnern Knockout (23 November 2026)": "muay-thai",
		"THE GLOBAL FIGHT 2026 Ep.233":            "unknown",
		"ONE Championship 180":                    "muay-thai",
		"UFC 320 main card":                       "unknown",
		"World Boxing Championship":               "boxing",
		"Kickboxing Grand Prix":                   "kickboxing",
		"MMA Cage Night":                          "mma",
	}
	for title, want := range cases {
		if got := disciplineOf(title); got != want {
			t.Errorf("disciplineOf(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestDisciplineOfRefusesToGuess(t *testing.T) {
	// The vendor stamps every combat broadcast "fighting" and the title is
	// the only hint there is. A title that says nothing must stay unknown:
	// the page shows unknown rows under every filter, which is wrong in a
	// way a reader can see, unlike filing them under a sport at random.
	if got := disciplineOf("Ep.233"); got != "unknown" {
		t.Errorf("disciplineOf = %q, want unknown", got)
	}
}
