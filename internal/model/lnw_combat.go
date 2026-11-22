package model

import "time"

// The combat sports this vendor carries and thscore does not: Thai boxing
// programmes, the round-by-round muay feed, and bull/cock fighting.
//
// These are stored in their own collections for the same reason the
// football ones are: so the evaluation can be undone by dropping lnw_*
// and so nothing here can touch a thscore-backed document.

// LnwBoxingProgram is one boxing promotion ("ศึก").
//
// The card itself is not modelled. Upstream has only ever served
// totalEvents 0, so the shape of an event is unknown, and a struct written
// against a guess would quietly drop fields of the first real card. RawEvents
// keeps the bytes instead.
type LnwBoxingProgram struct {
	ID     string `bson:"_id" json:"id"` // upstream programme id, e.g. "17343"
	Name   string `bson:"name" json:"name"`
	Logo   string `bson:"logo_url,omitempty" json:"logo_url,omitempty"`
	Splash string `bson:"splash_url,omitempty" json:"splash_url,omitempty"`

	TotalEvents int `bson:"total_events" json:"total_events"`
	// RawEvents is upstream's events array verbatim, stored only when it is
	// non-empty. It is the evidence a card ever existed.
	RawEvents string `bson:"raw_events,omitempty" json:"raw_events,omitempty"`

	ScrapedAt time.Time `bson:"scraped_at,omitempty" json:"scraped_at,omitempty"`
	FetchedAt time.Time `bson:"fetched_at" json:"fetched_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// LnwCombatBout is one fight from the pakyok or wuachon feed.
type LnwCombatBout struct {
	// ID is the sport and upstream's id, because the two feeds number their
	// bouts independently and would otherwise collide.
	ID    string `bson:"_id" json:"id"` // "wuachon:68539"
	Sport string `bson:"sport" json:"sport"`
	// Type is upstream's own sub-sport: the wuachon feed serves both
	// "wuachon" (bulls) and "cock" (cockerels) under one endpoint.
	Type      string `bson:"type,omitempty" json:"type,omitempty"`
	TypeLabel string `bson:"type_label,omitempty" json:"type_label,omitempty"`

	RedName  string `bson:"red_name,omitempty" json:"red_name,omitempty"`
	BlueName string `bson:"blue_name,omitempty" json:"blue_name,omitempty"`

	Stadium   string `bson:"stadium,omitempty" json:"stadium,omitempty"`
	StadiumID int64  `bson:"stadium_id,omitempty" json:"stadium_id,omitempty"`
	// BoutNo is the position on the card, read out of upstream's roundName
	// ("… คู่ที่ 33"). Zero means the string did not carry one.
	BoutNo       int    `bson:"bout_no,omitempty" json:"bout_no,omitempty"`
	RawRoundName string `bson:"raw_round_name,omitempty" json:"raw_round_name,omitempty"`

	// StartAt comes from a field that states its +07:00 offset, so unlike
	// the football result feed it needs no reinterpretation.
	StartAt   time.Time `bson:"start_at" json:"start_at"`
	MatchDate string    `bson:"match_date" json:"match_date"`

	Status      string `bson:"status" json:"status"` // upstream: "open" | "ended"
	StatusLabel string `bson:"status_label,omitempty" json:"status_label,omitempty"`
	// IsLive is stored even though it contradicts Status on real rows
	// (bouts marked ended while still flagged live). Status is the field to
	// believe; keeping both makes the disagreement measurable rather than
	// invisible.
	IsLive bool `bson:"is_live" json:"is_live"`

	// Result is normalized to red|blue|tie|"" and ResultKnown is false when
	// upstream sent something outside that set — it has been seen to leak
	// an unmapped "green". RawResult keeps whatever arrived.
	Result      string `bson:"result,omitempty" json:"result,omitempty"`
	ResultKnown bool   `bson:"result_known" json:"result_known"`
	RawResult   string `bson:"raw_result,omitempty" json:"raw_result,omitempty"`
	ResultLabel string `bson:"result_label,omitempty" json:"result_label,omitempty"`

	Stakes int64 `bson:"stakes,omitempty" json:"stakes,omitempty"`

	Stats LnwCombatStats `bson:"stats,omitempty" json:"stats,omitempty"`

	// No stream URL is stored, and none may be. The wuachon stream link
	// carries an stk= token whose base64 decodes to the account's own API
	// key, so persisting one would put the key in the database and in any
	// response built from it.
	HasStream bool `bson:"has_stream" json:"has_stream"`

	FetchedAt time.Time `bson:"fetched_at" json:"fetched_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// LnwCombatStats is the form guide shown beside a bout.
type LnwCombatStats struct {
	RedWinPercent  float64  `bson:"red_win_percent,omitempty" json:"red_win_percent,omitempty"`
	BlueWinPercent float64  `bson:"blue_win_percent,omitempty" json:"blue_win_percent,omitempty"`
	RedFights      int      `bson:"red_fights,omitempty" json:"red_fights,omitempty"`
	BlueFights     int      `bson:"blue_fights,omitempty" json:"blue_fights,omitempty"`
	RedWins        int      `bson:"red_wins,omitempty" json:"red_wins,omitempty"`
	BlueWins       int      `bson:"blue_wins,omitempty" json:"blue_wins,omitempty"`
	RedWeight      float64  `bson:"red_weight,omitempty" json:"red_weight,omitempty"`
	BlueWeight     float64  `bson:"blue_weight,omitempty" json:"blue_weight,omitempty"`
	WeightUnit     string   `bson:"weight_unit,omitempty" json:"weight_unit,omitempty"`
	RedForm        []string `bson:"red_form,omitempty" json:"red_form,omitempty"`
	BlueForm       []string `bson:"blue_form,omitempty" json:"blue_form,omitempty"`
	Opinion        string   `bson:"opinion,omitempty" json:"opinion,omitempty"`
}
