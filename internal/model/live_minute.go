package model

import "time"

// ComputeLiveMinute derives the displayed match minute from the raw thscore
// status code and the kick-off time of the current half. thscore's feeds only
// carry a usable minute at the moment something changes, so a continuously
// ticking clock must be computed from the half start instead.
//
// The result is capped at the end of regulation for each phase (45 for the
// 1st half, 90 for the 2nd); time beyond the cap is stoppage time, surfaced
// separately via Match.InjuryTime. Returns 0 when the match is not in a
// running phase or the half start is unknown.
func ComputeLiveMinute(statusCode int, halfStart, now time.Time) int {
	if halfStart.IsZero() {
		return 0
	}

	elapsed := int(now.Sub(halfStart).Minutes())
	if elapsed < 0 {
		elapsed = 0
	}

	// thscore status codes: 1=first half, 2=halftime, 3=second half, 4=extra time.
	switch statusCode {
	case 1:
		return min(elapsed+1, 45)
	case 2:
		return 45
	case 3:
		return min(45+elapsed+1, 90)
	case 4:
		return 90 + elapsed + 1
	default:
		return 0
	}
}
