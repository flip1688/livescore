// Package model holds the core domain types shared across the service.
//
// Field names follow thscore's data feed vocabulary where known; fields will
// be extended once the exact thscore API payloads are confirmed.
package model

import "time"

// League is dictionary data — synced from thscore into MongoDB and served
// from cache, never fetched from thscore on the request path.
type League struct {
	ID      string `bson:"_id" json:"id"` // thscore league id
	Name    string `bson:"name" json:"name"`
	NameTH  string `bson:"name_th,omitempty" json:"name_th,omitempty"`
	Country string `bson:"country,omitempty" json:"country,omitempty"`
	Season  string `bson:"season,omitempty" json:"season,omitempty"`
	// LogoURL is our mirrored (R2) URL, stamped by the logo sync job — the
	// dictionary sync never writes it. Empty until the logo is mirrored.
	LogoURL string `bson:"logo_url,omitempty" json:"logo_url,omitempty"`
	// LogoSourceURL is thscore's own logo URL, kept for the logo sync job to
	// mirror from; never exposed over the API (hotlinking is disallowed).
	LogoSourceURL string    `bson:"logo_source_url,omitempty" json:"-"`
	UpdatedAt     time.Time `bson:"updated_at" json:"updated_at"`
}

// Team is dictionary data, same lifecycle as League.
type Team struct {
	ID       string `bson:"_id" json:"id"` // thscore team id
	Name     string `bson:"name" json:"name"`
	NameTH   string `bson:"name_th,omitempty" json:"name_th,omitempty"`
	LeagueID string `bson:"league_id,omitempty" json:"league_id,omitempty"`
	// LogoURL / LogoSourceURL: see League.
	LogoURL       string    `bson:"logo_url,omitempty" json:"logo_url,omitempty"`
	LogoSourceURL string    `bson:"logo_source_url,omitempty" json:"-"`
	UpdatedAt     time.Time `bson:"updated_at" json:"updated_at"`
}

// MatchStatus is the normalized match state.
type MatchStatus string

const (
	MatchScheduled MatchStatus = "scheduled"
	MatchLive      MatchStatus = "live"
	MatchHalftime  MatchStatus = "halftime"
	MatchFinished  MatchStatus = "finished"
	MatchPostponed MatchStatus = "postponed"
	MatchCanceled  MatchStatus = "canceled"
)

// Match is time-window data: upcoming/recent matches live in MongoDB,
// in-play state is refreshed into Redis by the sync worker.
// StatusCode keeps thscore's raw code; Status is the normalized state.
type Match struct {
	ID       string `bson:"_id" json:"id"` // thscore match id
	LeagueID string `bson:"league_id" json:"league_id"`
	// League display fields denormalized from thscore's schedule payload so
	// the daily match list can be grouped per league without a join.
	LeagueName      string `bson:"league_name,omitempty" json:"league_name,omitempty"`
	LeagueShortName string `bson:"league_short_name,omitempty" json:"league_short_name,omitempty"`
	LeagueColor     string `bson:"league_color,omitempty" json:"league_color,omitempty"`

	HomeTeamID string    `bson:"home_team_id" json:"home_team_id"`
	AwayTeamID string    `bson:"away_team_id" json:"away_team_id"`
	HomeName   string    `bson:"home_name,omitempty" json:"home_name,omitempty"`
	AwayName   string    `bson:"away_name,omitempty" json:"away_name,omitempty"`
	KickoffAt  time.Time `bson:"kickoff_at" json:"kickoff_at"` // matchTime, GMT+7 upstream — stored UTC
	// MatchDate is the display day ("2006-01-02") under the 04:00 ICT cutoff,
	// computed from KickoffAt at sync time. Daily-list queries key on this,
	// never on a KickoffAt range.
	MatchDate  string      `bson:"match_date" json:"match_date"`
	Status     MatchStatus `bson:"status" json:"status"`
	StatusCode int         `bson:"status_code" json:"status_code"`
	Minute     int         `bson:"minute,omitempty" json:"minute,omitempty"`
	// HalfStartAt is the kick-off of the current half (startTime/halfStartTime
	// upstream) — the live minute is computed from it, not streamed.
	HalfStartAt time.Time `bson:"half_start_at,omitempty" json:"half_start_at,omitempty"`
	InjuryTime  int       `bson:"injury_time,omitempty" json:"injury_time,omitempty"`

	HomeScore     int `bson:"home_score" json:"home_score"`
	AwayScore     int `bson:"away_score" json:"away_score"`
	HomeHalfScore int `bson:"home_half_score,omitempty" json:"home_half_score,omitempty"`
	AwayHalfScore int `bson:"away_half_score,omitempty" json:"away_half_score,omitempty"`
	HomeRed       int `bson:"home_red,omitempty" json:"home_red,omitempty"`
	AwayRed       int `bson:"away_red,omitempty" json:"away_red,omitempty"`
	HomeYellow    int `bson:"home_yellow,omitempty" json:"home_yellow,omitempty"`
	AwayYellow    int `bson:"away_yellow,omitempty" json:"away_yellow,omitempty"`
	HomeCorner    int `bson:"home_corner,omitempty" json:"home_corner,omitempty"`
	AwayCorner    int `bson:"away_corner,omitempty" json:"away_corner,omitempty"`

	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// MatchEvent is a single in-match event (goal, card, substitution, …).
// TypeCode uses thscore event codes (see internal/thscore/status.go).
type MatchEvent struct {
	EventID   string    `bson:"_id" json:"event_id"`
	MatchID   string    `bson:"match_id" json:"match_id"`
	Minute    string    `bson:"minute" json:"minute"`
	TypeCode  int       `bson:"type_code" json:"type_code"`
	HomeEvent bool      `bson:"home_event" json:"home_event"`
	PlayerID  string    `bson:"player_id,omitempty" json:"player_id,omitempty"`
	Player    string    `bson:"player,omitempty" json:"player,omitempty"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// Country is dictionary data (thscore provides Thai names).
type Country struct {
	ID        string    `bson:"_id" json:"id"`
	Name      string    `bson:"name" json:"name"`
	NameTH    string    `bson:"name_th,omitempty" json:"name_th,omitempty"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
