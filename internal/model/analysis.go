package model

import (
	"encoding/json"
	"time"
)

// MatchAnalysis stores thscore's pre-match analysis payload (H2H, last-20
// form for both teams, next fixtures, odds stats, goal-distribution buckets,
// HT/FT combos) for one match. The exact field schema is not documented (see
// docs/thscore-api.md docs_id=109), so we don't attempt to type the body:
// Analysis is kept as json.RawMessage end to end — stored in Mongo as raw
// bytes (BSON binary, byte-for-byte, confirmed via bson round-trip) and
// re-emitted as-is over the API. Never round-trip this through
// map[string]any: JSON numbers would get re-encoded through float64 and lose
// precision on the odds/percentage fields.
type MatchAnalysis struct {
	MatchID   string          `bson:"_id" json:"match_id"`
	Analysis  json.RawMessage `bson:"analysis" json:"analysis"`
	FetchedAt time.Time       `bson:"fetched_at" json:"fetched_at"`
	UpdatedAt time.Time       `bson:"updated_at" json:"updated_at"`
}
