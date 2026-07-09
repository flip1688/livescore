package model

import "time"

// StandingRow is one team's row within a single standing view (total, half,
// home, away, home-half, away-half). ColorZone/ColorHex are already resolved
// from thscore's LeagueColorInfos index (see internal/service.standingRows)
// so the API exposes a ready-to-render label/color per row.
type StandingRow struct {
	Rank             int     `bson:"rank" json:"rank"`
	TeamID           string  `bson:"team_id" json:"team_id"`
	WinRate          float64 `bson:"win_rate" json:"win_rate"`
	DrawRate         float64 `bson:"draw_rate" json:"draw_rate"`
	LoseRate         float64 `bson:"lose_rate" json:"lose_rate"`
	WinAverage       float64 `bson:"win_average" json:"win_average"`
	LoseAverage      float64 `bson:"lose_average" json:"lose_average"`
	Deduction        int     `bson:"deduction,omitempty" json:"deduction,omitempty"`
	DeductionExplain string  `bson:"deduction_explain,omitempty" json:"deduction_explain,omitempty"`
	// RecentForm is the team's last six results, most recent first
	// (0:win 1:draw 2:lose 3:no match played in that slot yet).
	RecentForm     []int  `bson:"recent_form,omitempty" json:"recent_form,omitempty"`
	ColorZone      string `bson:"color_zone,omitempty" json:"color_zone,omitempty"` // promotion/relegation label, "" if none
	ColorHex       string `bson:"color_hex,omitempty" json:"color_hex,omitempty"`   // zone color, "" if none
	TotalCount     int    `bson:"total_count" json:"total_count"`
	WinCount       int    `bson:"win_count" json:"win_count"`
	DrawCount      int    `bson:"draw_count" json:"draw_count"`
	LoseCount      int    `bson:"lose_count" json:"lose_count"`
	GetScore       int    `bson:"get_score" json:"get_score"`
	LoseScore      int    `bson:"lose_score" json:"lose_score"`
	GoalDifference int    `bson:"goal_difference" json:"goal_difference"`
	Points         int    `bson:"points" json:"points"` // thscore's "integral"
}

// StandingViews groups the six standing table perspectives thscore returns
// for one league.
type StandingViews struct {
	Total    []StandingRow `bson:"total,omitempty" json:"total,omitempty"`
	Half     []StandingRow `bson:"half,omitempty" json:"half,omitempty"`
	Home     []StandingRow `bson:"home,omitempty" json:"home,omitempty"`
	Away     []StandingRow `bson:"away,omitempty" json:"away,omitempty"`
	HomeHalf []StandingRow `bson:"home_half,omitempty" json:"home_half,omitempty"`
	AwayHalf []StandingRow `bson:"away_half,omitempty" json:"away_half,omitempty"`
}

// StandingSubLeague describes one stage/division a league's table is split
// into (e.g. group stage vs. playoff group).
type StandingSubLeague struct {
	ID               string `bson:"id" json:"id"`
	Name             string `bson:"name" json:"name"`
	TotalRound       int    `bson:"total_round,omitempty" json:"total_round,omitempty"`
	CurrentRound     int    `bson:"current_round,omitempty" json:"current_round,omitempty"`
	HasScore         bool   `bson:"has_score" json:"has_score"`
	HasTwoLegs       bool   `bson:"has_two_legs" json:"has_two_legs"`
	CurrentSubLeague bool   `bson:"current_sub_league" json:"current_sub_league"`
}

// LeagueStanding is the league table synced from thscore's
// standing/league.aspx, refreshed every 6h by the sync worker (scoped to
// leagues with a fixture around today — see Syncer.syncStandings). _id is
// the thscore league id.
type LeagueStanding struct {
	ID            string              `bson:"_id" json:"id"`
	Name          string              `bson:"name" json:"name"`
	CurrentSeason string              `bson:"current_season,omitempty" json:"current_season,omitempty"`
	Color         string              `bson:"color,omitempty" json:"color,omitempty"`
	ShortName     string              `bson:"short_name,omitempty" json:"short_name,omitempty"`
	TotalRound    int                 `bson:"total_round,omitempty" json:"total_round,omitempty"`
	CurrentRound  int                 `bson:"current_round,omitempty" json:"current_round,omitempty"`
	SubLeagues    []StandingSubLeague `bson:"sub_leagues,omitempty" json:"sub_leagues,omitempty"`
	Standings     StandingViews       `bson:"standings" json:"standings"`
	UpdatedAt     time.Time           `bson:"updated_at" json:"updated_at"`
}
