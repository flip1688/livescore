package model

import "time"

// Types in this file store the lnwthailive feed. They are deliberately kept
// apart from Match/League/Team/LeagueStanding: that vendor is under
// evaluation against thscore, and the comparison is only honest if neither
// feed can overwrite the other. Every collection is prefixed lnw_ and
// nothing here is read by the football read path.
//
// Upstream ids are not unique across endpoints — see the package doc of
// internal/lnw — so every _id in here is prefixed by its source feed.

// LnwSport identifies which feed a row came from. Upstream's football feed
// is the default and omits the parameter.
const (
	LnwSportFootball = "football"
	// LnwSportFighting is every combat broadcast. Upstream answers to both
	// ?sport=boxing and ?sport=fighting with the same rows and stamps them
	// all "fighting", so one constant covers muay thai, kickboxing, boxing
	// and MMA alike — the discipline, where it can be told at all, has to
	// be read out of the programme title.
	LnwSportFighting = "fighting"
	// LnwSportBasketball is upstream's basketball feed: the same row shape
	// as football, with teams, logos and a live score, but a status field
	// that counts quarters instead of minutes.
	LnwSportBasketball = "basketball"
)

// LnwMatch is one row of /api/public/matches: today's and tomorrow's
// fixtures, with live score and the channels carrying the match.
//
// Upstream gives league and team as display strings only — there are no
// league or team ids on this feed at all — so there is nothing to join to
// and the names are stored as they arrive.
type LnwMatch struct {
	ID    string `bson:"_id" json:"id"` // upstream id, e.g. "rb_4344928"
	Sport string `bson:"sport" json:"sport"`

	LeagueName string `bson:"league_name,omitempty" json:"league_name,omitempty"`
	HomeName   string `bson:"home_name,omitempty" json:"home_name,omitempty"`
	AwayName   string `bson:"away_name,omitempty" json:"away_name,omitempty"`

	// Logo URLs are upstream's own CDN and are stored unmirrored for now.
	// If this vendor is adopted they must be mirrored to R2 like thscore's,
	// both to survive the CDN and to stop every visitor hitting it.
	HomeLogoURL   string `bson:"home_logo_url,omitempty" json:"home_logo_url,omitempty"`
	AwayLogoURL   string `bson:"away_logo_url,omitempty" json:"away_logo_url,omitempty"`
	LeagueLogoURL string `bson:"league_logo_url,omitempty" json:"league_logo_url,omitempty"`
	CountryLogo   string `bson:"country_logo_url,omitempty" json:"country_logo_url,omitempty"`

	// KickoffAt is reconstructed at ingest time: upstream sends only
	// day:"today"|"tomorrow" plus a "HH:MM" Bangkok wall-clock time, so the
	// absolute instant depends on when the row was fetched. FetchedAt
	// records the clock that resolved it.
	KickoffAt time.Time `bson:"kickoff_at" json:"kickoff_at"`
	FetchedAt time.Time `bson:"fetched_at" json:"fetched_at"`
	// RawDay and RawTime keep what upstream actually said, so a wrong
	// resolution can be spotted and recomputed instead of silently believed.
	RawDay  string `bson:"raw_day,omitempty" json:"raw_day,omitempty"`
	RawTime string `bson:"raw_time,omitempty" json:"raw_time,omitempty"`
	// MatchDate is the display day under the same 04:00 ICT cutoff the
	// thscore path uses, so the two feeds can be counted against each other
	// day by day.
	MatchDate string `bson:"match_date" json:"match_date"`

	Status MatchStatus `bson:"status" json:"status"`
	// RawStatus is upstream's own label ("30'", "HT", "FT", "รอแข่ง").
	// It mixes a minute, a period and a Thai word in one field.
	RawStatus string `bson:"raw_status,omitempty" json:"raw_status,omitempty"`
	Minute    int    `bson:"minute,omitempty" json:"minute,omitempty"`
	IsLive    bool   `bson:"is_live" json:"is_live"`

	// HomeScore/AwayScore are parsed from upstream's "2 - 1" string, which
	// reads "vs" before kick-off. HasScore separates a real 0-0 from the
	// absence of a score.
	HomeScore int  `bson:"home_score" json:"home_score"`
	AwayScore int  `bson:"away_score" json:"away_score"`
	HasScore  bool `bson:"has_score" json:"has_score"`

	// Channels is the broadcast list. Stream URLs are NOT stored: they
	// carry tokens upstream itself says must not be cached beyond 15s.
	Channels []LnwChannel `bson:"channels,omitempty" json:"channels,omitempty"`

	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// LnwChannel is one broadcast channel for a match, without its stream URL.
type LnwChannel struct {
	ID      string `bson:"id" json:"id"`
	Name    string `bson:"name" json:"name"`
	LogoURL string `bson:"logo_url,omitempty" json:"logo_url,omitempty"`
}

// LnwResult is one finished or in-play match from /api/public/results.
//
// This feed is Sportmonks-shaped and entirely separate from LnwMatch: int
// ids, English team names, its own status vocabulary. It is the only way to
// read past days from this vendor.
type LnwResult struct {
	ID         string `bson:"_id" json:"id"` // "sm_19893334"
	UpstreamID int64  `bson:"upstream_id" json:"upstream_id"`

	LeagueID      int64  `bson:"league_id,omitempty" json:"league_id,omitempty"`
	LeagueName    string `bson:"league_name,omitempty" json:"league_name,omitempty"`
	LeagueLogoURL string `bson:"league_logo_url,omitempty" json:"league_logo_url,omitempty"`

	// Team names on this feed arrive in English ("Real Unión Club") while
	// the matches and standings feeds are Thai. Anything rendering both
	// must reconcile that.
	HomeName    string `bson:"home_name,omitempty" json:"home_name,omitempty"`
	AwayName    string `bson:"away_name,omitempty" json:"away_name,omitempty"`
	HomeLogoURL string `bson:"home_logo_url,omitempty" json:"home_logo_url,omitempty"`
	AwayLogoURL string `bson:"away_logo_url,omitempty" json:"away_logo_url,omitempty"`

	KickoffAt time.Time   `bson:"kickoff_at" json:"kickoff_at"`
	MatchDate string      `bson:"match_date" json:"match_date"`
	Status    MatchStatus `bson:"status" json:"status"`
	// RawState is upstream's own code: NS, INPLAY_1ST_HALF, HT, FT,
	// FT_PEN, AET and others not yet observed.
	RawState string `bson:"raw_state,omitempty" json:"raw_state,omitempty"`

	HomeScore int `bson:"home_score" json:"home_score"`
	AwayScore int `bson:"away_score" json:"away_score"`

	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// LnwStanding is one league table from /api/public/standings.
//
// Upstream publishes no league id on this feed. The anchor in leagueUrl
// ("/standings#epl") is the only stable identifier it offers, so that is
// the _id — if upstream ever renames an anchor the table arrives as a new
// document rather than corrupting the old one.
type LnwStanding struct {
	ID            string `bson:"_id" json:"id"` // anchor, e.g. "epl"
	LeagueName    string `bson:"league_name" json:"league_name"`
	LeagueShort   string `bson:"league_short_name,omitempty" json:"league_short_name,omitempty"`
	LeagueLogoURL string `bson:"league_logo_url,omitempty" json:"league_logo_url,omitempty"`
	IsPreSeason   bool   `bson:"is_pre_season,omitempty" json:"is_pre_season,omitempty"`

	// IsStale and FetchedAt are upstream's own freshness flags, kept rather
	// than dropped: upstream serves a stale table while it refreshes, and a
	// table that is quietly old is worse than one known to be old.
	IsStale          bool      `bson:"is_stale,omitempty" json:"is_stale,omitempty"`
	UpstreamFetchdAt time.Time `bson:"upstream_fetched_at,omitempty" json:"upstream_fetched_at,omitempty"`

	Rows      []LnwStandingRow `bson:"rows" json:"rows"`
	UpdatedAt time.Time        `bson:"updated_at" json:"updated_at"`
}

// LnwStandingRow is one team's line in a table. There is no team id here
// either — only the Thai and English names.
type LnwStandingRow struct {
	Rank     int    `bson:"rank" json:"rank"`
	Team     string `bson:"team" json:"team"`
	TeamEn   string `bson:"team_en,omitempty" json:"team_en,omitempty"`
	TeamLogo string `bson:"team_logo_url,omitempty" json:"team_logo_url,omitempty"`
	Played   int    `bson:"mp" json:"mp"`
	Won      int    `bson:"w" json:"w"`
	Drawn    int    `bson:"d" json:"d"`
	Lost     int    `bson:"l" json:"l"`
	GoalsFor int    `bson:"gf" json:"gf"`
	GoalsAg  int    `bson:"ga" json:"ga"`
	GoalDiff int    `bson:"gd" json:"gd"`
	Points   int    `bson:"pts" json:"pts"`
}

// LnwAnalysis is one row of the pre-match analysis index. The detail call
// is 14 upstream requests behind one endpoint, so it is fetched lazily and
// stored separately; the Has* flags say whether it is worth fetching.
type LnwAnalysis struct {
	ID         string `bson:"_id" json:"id"` // "an_342830"
	UpstreamID int64  `bson:"upstream_id" json:"upstream_id"`

	Name       string `bson:"name,omitempty" json:"name,omitempty"`
	HomeName   string `bson:"home_name,omitempty" json:"home_name,omitempty"`
	AwayName   string `bson:"away_name,omitempty" json:"away_name,omitempty"`
	LeagueName string `bson:"league_name,omitempty" json:"league_name,omitempty"`
	// These ids belong to the analysis feed's own id space and match
	// nothing on the matches or results feeds.
	HomeTeamID int64 `bson:"home_team_id,omitempty" json:"home_team_id,omitempty"`
	AwayTeamID int64 `bson:"away_team_id,omitempty" json:"away_team_id,omitempty"`
	LeagueID   int64 `bson:"league_id,omitempty" json:"league_id,omitempty"`

	KickoffAt time.Time   `bson:"kickoff_at" json:"kickoff_at"`
	MatchDate string      `bson:"match_date" json:"match_date"`
	Status    MatchStatus `bson:"status" json:"status"`
	RawStatus string      `bson:"raw_status,omitempty" json:"raw_status,omitempty"`
	HomeScore int         `bson:"home_score" json:"home_score"`
	AwayScore int         `bson:"away_score" json:"away_score"`

	HasOdds     bool `bson:"has_odds,omitempty" json:"has_odds,omitempty"`
	HasLineup   bool `bson:"has_lineup,omitempty" json:"has_lineup,omitempty"`
	HasTeamStat bool `bson:"has_team_stats,omitempty" json:"has_team_stats,omitempty"`
	HasArticle  bool `bson:"has_article,omitempty" json:"has_article,omitempty"`

	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// LnwMatchLink is the crosswalk between the vendor's four unrelated id
// spaces, and between them and thscore.
//
// Nothing upstream provides this. Each link is a guess made from team names
// and kick-off time, so Method and Confidence travel with it and callers
// are expected to check them. A link is never promoted into the feed
// documents themselves — an evaluation that quietly merges guessed data
// cannot answer the question it was built to answer.
type LnwMatchLink struct {
	ID        string `bson:"_id" json:"id"` // date + normalized team pair
	MatchDate string `bson:"match_date" json:"match_date"`

	MatchID    string `bson:"match_id,omitempty" json:"match_id,omitempty"`       // lnw_matches
	ResultID   string `bson:"result_id,omitempty" json:"result_id,omitempty"`     // lnw_results
	AnalysisID string `bson:"analysis_id,omitempty" json:"analysis_id,omitempty"` // lnw_analysis
	ThscoreID  string `bson:"thscore_id,omitempty" json:"thscore_id,omitempty"`   // matches

	Method     string    `bson:"method" json:"method"` // see LnwLink* below
	Confidence float64   `bson:"confidence" json:"confidence"`
	UpdatedAt  time.Time `bson:"updated_at" json:"updated_at"`
}

// How a link was established, weakest last.
const (
	LnwLinkExactName  = "exact_name"  // normalized names equal, kick-off within tolerance
	LnwLinkAliasName  = "alias_name"  // matched through a known alias
	LnwLinkFuzzyName  = "fuzzy_name"  // similarity above threshold
	LnwLinkTimeWindow = "time_window" // only one candidate in the window
	LnwLinkTimeScore  = "time_score"  // kick-off window plus an identical score
)

// LnwCoverage is one day's comparison between this vendor and thscore.
// This is the output the vendor decision is actually made from, so it
// records what each feed carried rather than a verdict.
type LnwCoverage struct {
	Day string `bson:"_id" json:"day"` // "YYYY-MM-DD"

	LnwMatches    int `bson:"lnw_matches" json:"lnw_matches"`
	LnwLeagues    int `bson:"lnw_leagues" json:"lnw_leagues"`
	LnwResults    int `bson:"lnw_results" json:"lnw_results"`
	LnwAnalysis   int `bson:"lnw_analysis" json:"lnw_analysis"`
	LnwWithStream int `bson:"lnw_with_stream" json:"lnw_with_stream"`

	ThscoreMatches int `bson:"thscore_matches" json:"thscore_matches"`
	ThscoreLeagues int `bson:"thscore_leagues" json:"thscore_leagues"`

	// Linked counts matches present in both feeds; OnlyLnw and OnlyThscore
	// are what each vendor would have cost us had we used only the other.
	Linked        int `bson:"linked" json:"linked"`
	OnlyLnw       int `bson:"only_lnw" json:"only_lnw"`
	OnlyThscore   int `bson:"only_thscore" json:"only_thscore"`
	LinkedByName  int `bson:"linked_by_name" json:"linked_by_name"`
	LinkedFuzzy   int `bson:"linked_fuzzy" json:"linked_fuzzy"`
	LinkedByScore int `bson:"linked_by_score" json:"linked_by_score"`

	// Why the remaining fixtures could not be linked. These are a property
	// of the vendor rather than of the matcher: measured on 2026-11-22,
	// 231 of 312 lnw fixtures named their league differently from thscore
	// and most named at least one team in Latin script while thscore names
	// it in Thai, so for those rows no name comparison exists to make.
	// A low Linked count must be read with these three beside it.
	UnlinkedScript      int `bson:"unlinked_script" json:"unlinked_script"`
	UnlinkedAmbiguous   int `bson:"unlinked_ambiguous" json:"unlinked_ambiguous"`
	UnlinkedNoCandidate int `bson:"unlinked_no_candidate" json:"unlinked_no_candidate"`

	ComputedAt time.Time `bson:"computed_at" json:"computed_at"`
}
