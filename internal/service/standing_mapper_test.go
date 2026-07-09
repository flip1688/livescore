package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flip1688/livescore/internal/thscore"
)

// standingFixtureJSON is a trimmed excerpt of a real /football_th/standing/
// league.aspx payload (Bolivia Division 1, leagueId 593), captured via
// cmd/thscore-smoke on 2026-07-09. It keeps the real field layout — notably
// subLeagueInfos at the top level (not nested in leagueInfo) and the
// teamInfo singular key — plus rows covering every leagueColorInfos zone
// (index 0, 1, 4) and the "no zone" sentinel (-1).
const standingFixtureJSON = `{
  "leagueInfo": {
    "leagueId": 593,
    "name": "ลีกโปรตุเซีย ดิวิชั่นแรก",
    "currentSeason": "2026",
    "color": "#B5A150",
    "shortName": "BOL D1"
  },
  "subLeagueInfos": [
    {
      "subLeagueId": 3533,
      "name": "League",
      "hasScore": true,
      "totalRound": 30,
      "currentRound": 6,
      "hasTwoLegs": false,
      "currentSubLeague": true
    }
  ],
  "teamInfo": [
    {"teamId": 1062, "name": "โบลิวาร์", "area": 0},
    {"teamId": 1995, "name": "เดอะ สตรองเกสต์", "area": 0}
  ],
  "totalStandings": [
    {
      "rank": 1, "teamId": 31073, "winRate": 62.5, "drawRate": 25, "loseRate": 12.5,
      "winAverage": 1.75, "loseAverage": 0.63,
      "recentFirstResult": 0, "recentSecondResult": 0, "recentThirdResult": 0,
      "recentFourthResult": 1, "recentFifthResult": 2, "recentSixthResult": 0,
      "deduction": 0, "deductionExplain": "", "color": 0, "red": 1,
      "totalCount": 8, "winCount": 5, "drawCount": 2, "loseCount": 1,
      "getScore": 14, "loseScore": 5, "goalDifference": 9, "integral": 17, "totalAddScore": 0
    },
    {
      "rank": 3, "teamId": 4478, "winRate": 50, "drawRate": 50, "loseRate": 0,
      "winAverage": 1.75, "loseAverage": 0.88,
      "recentFirstResult": 1, "recentSecondResult": 0, "recentThirdResult": 0,
      "recentFourthResult": 1, "recentFifthResult": 0, "recentSixthResult": 0,
      "deduction": 0, "deductionExplain": "", "color": 1, "red": 3,
      "totalCount": 8, "winCount": 4, "drawCount": 4, "loseCount": 0,
      "getScore": 14, "loseScore": 7, "goalDifference": 7, "integral": 16, "totalAddScore": 0
    },
    {
      "rank": 7, "teamId": 7641, "winRate": 33.3, "drawRate": 22.2, "loseRate": 44.4,
      "winAverage": 1.56, "loseAverage": 2.22,
      "recentFirstResult": 2, "recentSecondResult": 2, "recentThirdResult": 0,
      "recentFourthResult": 1, "recentFifthResult": 0, "recentSixthResult": 0,
      "deduction": 0, "deductionExplain": "", "color": -1, "red": 3,
      "totalCount": 9, "winCount": 3, "drawCount": 2, "loseCount": 4,
      "getScore": 14, "loseScore": 20, "goalDifference": -6, "integral": 11, "totalAddScore": 0
    },
    {
      "rank": 16, "teamId": 44560, "winRate": 12.5, "drawRate": 25, "loseRate": 62.5,
      "winAverage": 0.63, "loseAverage": 2.38,
      "recentFirstResult": 1, "recentSecondResult": 2, "recentThirdResult": 2,
      "recentFourthResult": 0, "recentFifthResult": 2, "recentSixthResult": 2,
      "deduction": 0, "deductionExplain": "", "color": 4, "red": 2,
      "totalCount": 8, "winCount": 1, "drawCount": 2, "loseCount": 5,
      "getScore": 5, "loseScore": 19, "goalDifference": -14, "integral": 5, "totalAddScore": 0
    }
  ],
  "halfStandings": [
    {"rank": 1, "teamId": 4480, "winRate": 50, "drawRate": 50, "loseRate": 0,
     "winAverage": 1.13, "loseAverage": 0.25, "totalCount": 8, "winCount": 4,
     "drawCount": 4, "loseCount": 0, "getScore": 9, "loseScore": 2,
     "goalDifference": 7, "integral": 16, "color": -1}
  ],
  "homeStandings": [
    {"rank": 1, "teamId": 4140, "winRate": 60, "drawRate": 20, "loseRate": 20,
     "winAverage": 1.8, "loseAverage": 1, "totalCount": 5, "winCount": 3,
     "drawCount": 1, "loseCount": 1, "getScore": 9, "loseScore": 5,
     "goalDifference": 4, "integral": 10, "color": -1}
  ],
  "awayStandings": [
    {"rank": 1, "teamId": 1995, "winRate": 75, "drawRate": 25, "loseRate": 0,
     "winAverage": 1.5, "loseAverage": 0.75, "totalCount": 4, "winCount": 3,
     "drawCount": 1, "loseCount": 0, "getScore": 6, "loseScore": 3,
     "goalDifference": 3, "integral": 10, "color": -1}
  ],
  "homeHalfStandings": [
    {"rank": 1, "teamId": 35501, "winRate": 40, "drawRate": 40, "loseRate": 20,
     "winAverage": 1.4, "loseAverage": 0.8, "totalCount": 5, "winCount": 2,
     "drawCount": 2, "loseCount": 1, "getScore": 7, "loseScore": 4,
     "goalDifference": 3, "integral": 8, "color": -1}
  ],
  "awayHalfStandings": [
    {"rank": 1, "teamId": 4480, "winRate": 40, "drawRate": 60, "loseRate": 0,
     "winAverage": 0.8, "loseAverage": 0.4, "totalCount": 5, "winCount": 2,
     "drawCount": 3, "loseCount": 0, "getScore": 4, "loseScore": 2,
     "goalDifference": 2, "integral": 9, "color": -1}
  ],
  "leagueColorInfos": [
    {"color": "#a2e76f", "leagueName": "LIBC CL qualifying"},
    {"color": "#FF9966", "leagueName": "LIBC qualifying"},
    {"color": "#00CCFF", "leagueName": "CON CSA qualifying"},
    {"color": "#ff9999", "leagueName": "Championship Playoff"},
    {"color": "#B1A7A7", "leagueName": "Relegation"}
  ],
  "conference": false
}`

func mustParseFixture(t *testing.T) thscore.StandingResponse {
	t.Helper()
	var r thscore.StandingResponse
	if err := json.Unmarshal([]byte(standingFixtureJSON), &r); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return r
}

// TestStandingFromResponse_LeagueHeader checks the league/subleague header
// mapping, including sourcing total/current round from the active
// subLeagueInfos entry rather than leagueInfo itself (which doesn't carry
// those fields in the real payload).
func TestStandingFromResponse_LeagueHeader(t *testing.T) {
	r := mustParseFixture(t)
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	st := standingFromResponse(r, now)

	if st.ID != "593" {
		t.Errorf("ID = %q, want 593", st.ID)
	}
	if st.Name == "" || st.ShortName != "BOL D1" || st.Color != "#B5A150" || st.CurrentSeason != "2026" {
		t.Errorf("league header mismatch: %+v", st)
	}
	if st.TotalRound != 30 || st.CurrentRound != 6 {
		t.Errorf("TotalRound/CurrentRound = %d/%d, want 30/6 (sourced from the active sub-league)", st.TotalRound, st.CurrentRound)
	}
	if len(st.SubLeagues) != 1 || st.SubLeagues[0].ID != "3533" || !st.SubLeagues[0].CurrentSubLeague {
		t.Errorf("SubLeagues = %+v", st.SubLeagues)
	}
	if !st.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", st.UpdatedAt, now)
	}
}

// TestStandingFromResponse_Rows verifies row mapping across all six views,
// including the recent-form slice and the promotion/relegation zone
// resolution from the Color index into leagueColorInfos.
func TestStandingFromResponse_Rows(t *testing.T) {
	r := mustParseFixture(t)
	st := standingFromResponse(r, time.Now())

	if len(st.Standings.Total) != 4 {
		t.Fatalf("Total rows = %d, want 4", len(st.Standings.Total))
	}
	cases := []struct {
		name           string
		row            int
		wantTeamID     string
		wantPoints     int
		wantRecentForm []int
		wantColorZone  string
		wantColorHex   string
	}{
		{"rank1 zone 0", 0, "31073", 17, []int{0, 0, 0, 1, 2, 0}, "LIBC CL qualifying", "#a2e76f"},
		{"rank3 zone 1", 1, "4478", 16, []int{1, 0, 0, 1, 0, 0}, "LIBC qualifying", "#FF9966"},
		{"rank7 no zone (-1)", 2, "7641", 11, []int{2, 2, 0, 1, 0, 0}, "", ""},
		{"rank16 relegation zone 4", 3, "44560", 5, []int{1, 2, 2, 0, 2, 2}, "Relegation", "#B1A7A7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := st.Standings.Total[c.row]
			if row.TeamID != c.wantTeamID {
				t.Errorf("TeamID = %q, want %q", row.TeamID, c.wantTeamID)
			}
			if row.Points != c.wantPoints {
				t.Errorf("Points = %d, want %d", row.Points, c.wantPoints)
			}
			if len(row.RecentForm) != 6 {
				t.Fatalf("RecentForm len = %d, want 6", len(row.RecentForm))
			}
			for i, v := range c.wantRecentForm {
				if row.RecentForm[i] != v {
					t.Errorf("RecentForm[%d] = %d, want %d", i, row.RecentForm[i], v)
				}
			}
			if row.ColorZone != c.wantColorZone || row.ColorHex != c.wantColorHex {
				t.Errorf("ColorZone/ColorHex = %q/%q, want %q/%q", row.ColorZone, row.ColorHex, c.wantColorZone, c.wantColorHex)
			}
		})
	}

	// The other five views each carry exactly the one fixture row.
	if got := len(st.Standings.Half); got != 1 || st.Standings.Half[0].TeamID != "4480" {
		t.Errorf("Half view: len=%d teamID=%q", got, st.Standings.Half[0].TeamID)
	}
	if got := len(st.Standings.Home); got != 1 || st.Standings.Home[0].TeamID != "4140" {
		t.Errorf("Home view: len=%d teamID=%q", got, st.Standings.Home[0].TeamID)
	}
	if got := len(st.Standings.Away); got != 1 || st.Standings.Away[0].TeamID != "1995" {
		t.Errorf("Away view: len=%d teamID=%q", got, st.Standings.Away[0].TeamID)
	}
	if got := len(st.Standings.HomeHalf); got != 1 || st.Standings.HomeHalf[0].TeamID != "35501" {
		t.Errorf("HomeHalf view: len=%d teamID=%q", got, st.Standings.HomeHalf[0].TeamID)
	}
	if got := len(st.Standings.AwayHalf); got != 1 || st.Standings.AwayHalf[0].TeamID != "4480" {
		t.Errorf("AwayHalf view: len=%d teamID=%q", got, st.Standings.AwayHalf[0].TeamID)
	}
}

// TestStandingColorZone covers the index resolution directly, including the
// -1 sentinel and an out-of-range index (defensive: upstream should never
// send one, but a corrupt/short leagueColorInfos must not panic).
func TestStandingColorZone(t *testing.T) {
	zones := []thscore.StandingColorInfo{
		{Color: "#111111", LeagueName: "Zone A"},
		{Color: "#222222", LeagueName: "Zone B"},
	}
	cases := []struct {
		name      string
		idx       int
		wantLabel string
		wantHex   string
	}{
		{"no zone sentinel", -1, "", ""},
		{"first zone", 0, "Zone A", "#111111"},
		{"second zone", 1, "Zone B", "#222222"},
		{"out of range", 5, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			label, hex := standingColorZone(c.idx, zones)
			if label != c.wantLabel || hex != c.wantHex {
				t.Errorf("standingColorZone(%d) = %q, %q, want %q, %q", c.idx, label, hex, c.wantLabel, c.wantHex)
			}
		})
	}
}
