package lnw

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// SportFootball is upstream's default feed; the sport parameter is omitted
// for it. The other feeds (basketball, tennis, volleyball, …) are read
// through the same endpoint with ?sport=.
const SportFootball = "football"

// MatchesResponse is /api/public/matches.
type MatchesResponse struct {
	OK    bool    `json:"ok"`
	Tier  string  `json:"tier"`
	Sport string  `json:"sport"`
	Count int     `json:"count"`
	Data  []Match `json:"data"`
}

// Match is one fixture row. Note what is missing: no league id, no team
// ids, no date, and no timezone. See ResolveKickoff.
type Match struct {
	ID          string    `json:"id"`
	Time        string    `json:"time"` // "20:00", Bangkok wall clock
	Day         string    `json:"day"`  // "today" | "tomorrow"
	League      string    `json:"league"`
	TeamHome    string    `json:"teamHome"`
	TeamAway    string    `json:"teamAway"`
	HomeLogo    string    `json:"homeLogo"`
	AwayLogo    string    `json:"awayLogo"`
	LeagueLogo  string    `json:"leagueLogo"`
	CountryLogo string    `json:"countryLogo"`
	IsLive      bool      `json:"isLive"`
	Score       string    `json:"score"`       // "2 - 1", or "vs" before kick-off
	MatchStatus string    `json:"matchStatus"` // "30'" | "HT" | "FT" | "รอแข่ง"
	Sport       string    `json:"sport"`
	Channels    []Channel `json:"channels"`

	// Stream URLs are present on live rows only and carry short-lived
	// tokens. They are decoded so a caller can serve one on demand, and are
	// deliberately never persisted — see model.LnwMatch.
	StreamURL string `json:"streamUrl"`
	PlayerURL string `json:"playerUrl"`
	EmbedURL  string `json:"embedUrl"`
}

// Channel is one broadcast of a match.
type Channel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Logo      string `json:"logo"`
	StreamURL string `json:"streamUrl"`
	PlayerURL string `json:"playerUrl"`
	EmbedURL  string `json:"embedUrl"`
}

// ResultsResponse is /api/public/results. Rows are nested under leagues
// rather than flat, and the ids are a different space from MatchesResponse.
type ResultsResponse struct {
	OK          bool           `json:"ok"`
	Tier        string         `json:"tier"`
	Date        string         `json:"date"`
	FetchedAt   int64          `json:"fetchedAt"` // epoch milliseconds
	LeagueCount int            `json:"leagueCount"`
	MatchCount  int            `json:"matchCount"`
	Leagues     []ResultLeague `json:"leagues"`
}

type ResultLeague struct {
	LeagueID   int64         `json:"leagueId"`
	LeagueName string        `json:"leagueName"`
	LeagueLogo string        `json:"leagueLogo"`
	Matches    []ResultMatch `json:"matches"`
}

type ResultMatch struct {
	ID   int64  `json:"id"`
	Name string `json:"name"` // frequently empty
	// StartingAt is "2006-01-02 15:04:05" with no zone marker.
	StartingAt string     `json:"startingAt"`
	State      string     `json:"state"`
	HomeTeam   ResultTeam `json:"homeTeam"`
	AwayTeam   ResultTeam `json:"awayTeam"`
	HomeScore  int        `json:"homeScore"`
	AwayScore  int        `json:"awayScore"`
}

type ResultTeam struct {
	Name string `json:"name"`
	Logo string `json:"logo"`
}

// StandingsResponse is /api/public/standings. IsStale and Refreshing are
// upstream's own flags and are preserved into storage.
type StandingsResponse struct {
	OK         bool       `json:"ok"`
	Tier       string     `json:"tier"`
	IsStale    bool       `json:"isStale"`
	Refreshing bool       `json:"refreshing"`
	FetchedAt  int64      `json:"fetchedAt"`
	Count      int        `json:"count"`
	Data       []Standing `json:"data"`
}

type Standing struct {
	LeagueName      string        `json:"leagueName"`
	LeagueShortName string        `json:"leagueShortName"`
	LeagueURL       string        `json:"leagueUrl"` // "/standings#epl"
	LeagueLogo      string        `json:"leagueLogo"`
	IsPreSeason     bool          `json:"isPreSeason"`
	Rows            []StandingRow `json:"rows"`
}

type StandingRow struct {
	Rank     int    `json:"rank"`
	Team     string `json:"team"`
	TeamEn   string `json:"teamEn"`
	TeamLogo string `json:"teamLogo"`
	MP       int    `json:"mp"`
	W        int    `json:"w"`
	D        int    `json:"d"`
	L        int    `json:"l"`
	GF       int    `json:"gf"`
	GA       int    `json:"ga"`
	GD       int    `json:"gd"`
	Pts      int    `json:"pts"`
}

// AnalyseListResponse is /api/analyse. Unlike the other endpoints it wraps
// its payload twice: rows are at data.matches, not data.
type AnalyseListResponse struct {
	OK   bool `json:"ok"`
	Data struct {
		Date      string    `json:"date"`
		Type      string    `json:"type"`
		Page      int       `json:"page"`
		PageSize  int       `json:"pageSize"`
		Count     int       `json:"count"`
		Total     int       `json:"total"`
		FetchedAt string    `json:"fetchedAt"`
		Matches   []Analyse `json:"matches"`
	} `json:"data"`
}

type Analyse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// MatchDatetime is RFC3339 with a +07:00 offset — the only endpoint on
	// this vendor that states its zone.
	MatchDatetime string       `json:"matchDatetime"`
	MatchStatus   string       `json:"matchStatus"` // "FULL_TIME", …
	HomeScore     int          `json:"homeScore"`
	AwayScore     int          `json:"awayScore"`
	HomeTeam      AnalyseTeam  `json:"homeTeam"`
	AwayTeam      AnalyseTeam  `json:"awayTeam"`
	League        AnalyseLeage `json:"league"`
	HasOdds       bool         `json:"hasOdds"`
	HasLineup     bool         `json:"hasLineup"`
	HasTeamStats  bool         `json:"hasTeamStats"`
	Article       *struct {
		Title string `json:"title"`
	} `json:"article"`
	Views     int    `json:"views"`
	DetailURL string `json:"detailUrl"`
}

type AnalyseTeam struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
	Logo string `json:"logo"`
}

type AnalyseLeage struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Logo string `json:"logo"`
}

// KeyInfo is /api/portal/verify: what the account actually grants, as
// opposed to what the pricing pages claim.
//
// Shape confirmed against a live Pro key on 2026-11-22, and it is not the
// one the vendor's examples show: every field that matters is wrapped in
// data, the expiry fields are epoch milliseconds rather than strings, and
// the per-sport grants are called permissions, not allow.
type KeyInfo struct {
	OK   bool        `json:"ok"`
	Data KeyInfoData `json:"data"`
}

type KeyInfoData struct {
	MaskedKey string `json:"maskedKey"`
	Label     string `json:"label"`
	Owner     string `json:"owner"`
	Tier      string `json:"tier"`
	Status    string `json:"status"`
	Active    bool   `json:"active"`
	IsExpired bool   `json:"isExpired"`
	IsSubKey  bool   `json:"isSubKey"`

	// RateLimit is requests per minute. EnforceRateLimit false means the
	// vendor is not counting yet — which is not a promise, so LNW_RPS stays
	// well under the quoted ceiling either way.
	RateLimit        int  `json:"rateLimit"`
	EnforceRateLimit bool `json:"enforceRateLimit"`

	// AllowedDomains/AllowedIPs empty means the key works from anywhere.
	// Monthly keys cannot set these themselves (CanAccessPanel is false),
	// so locking one down is a request to the vendor.
	AccessPolicy   string   `json:"accessPolicy"`
	AllowedDomains []string `json:"allowedDomains"`
	AllowedIPs     []string `json:"allowedIps"`
	CanAccessPanel bool     `json:"canAccessPanel"`

	CreatedAt     int64 `json:"createdAt"` // epoch milliseconds
	ExpiresAt     int64 `json:"expiresAt"` // epoch milliseconds
	DaysRemaining int   `json:"daysRemaining"`

	Permissions map[string]bool `json:"permissions"`
}

// ExpiresAtTime converts the epoch-millisecond expiry into a time. Zero
// when upstream sent nothing.
func (d KeyInfoData) ExpiresAtTime() time.Time {
	if d.ExpiresAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(d.ExpiresAt).UTC()
}

// Sports lists the sports this key may read, sorted so the verify output is
// stable between runs.
func (d KeyInfoData) Sports() []string {
	out := make([]string, 0, len(d.Permissions))
	for sport, allowed := range d.Permissions {
		if allowed {
			out = append(out, sport)
		}
	}
	sort.Strings(out)
	return out
}

// ParseScore reads upstream's "2 - 1" score string. ok is false before
// kick-off, when the field reads "vs" — which is why callers must not treat
// a zero return as 0-0.
func ParseScore(s string) (home, away int, ok bool) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	a, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return h, a, true
}

// ParseMinute reads the elapsed minute out of a status like "30'".
// It returns 0 for every non-minute status ("HT", "FT", "รอแข่ง").
func ParseMinute(status string) int {
	s := strings.TrimSuffix(strings.TrimSpace(status), "'")
	if s == "" {
		return 0
	}
	// A status such as "45+2" reports stoppage time; the base minute is
	// what the rest of the codebase stores.
	if i := strings.IndexAny(s, "+"); i > 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 200 {
		return 0
	}
	return n
}

// ResolveKickoff turns upstream's relative day plus wall-clock time into an
// absolute instant.
//
// This is the feed's sharpest edge: a row says only "today" or "tomorrow"
// and "20:00", so its meaning depends entirely on the clock at the moment
// it was fetched. now must be the fetch time, loc the Bangkok zone. A row
// resolved hours after it was fetched, or against a different zone, lands
// on the wrong day.
//
// ok is false when the row carries no usable time, which callers should
// treat as a row to skip rather than as midnight.
func ResolveKickoff(day, hhmm string, now time.Time, loc *time.Location) (time.Time, bool) {
	hhmm = strings.TrimSpace(hhmm)
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, false
	}
	base := now.In(loc)
	switch strings.ToLower(strings.TrimSpace(day)) {
	case "today", "":
		// "" is treated as today: upstream has been seen to omit the field
		// rather than to mean something else by it.
	case "tomorrow":
		base = base.AddDate(0, 0, 1)
	default:
		return time.Time{}, false
	}
	return time.Date(base.Year(), base.Month(), base.Day(), t.Hour(), t.Minute(), 0, 0, loc), true
}

// StandingKey extracts the anchor from a leagueUrl ("/standings#epl" ->
// "epl"). It is the only stable league identifier this feed offers; an
// empty return means the table cannot be keyed and must be skipped rather
// than stored under a guessed name.
func StandingKey(leagueURL string) string {
	i := strings.LastIndex(leagueURL, "#")
	if i < 0 || i == len(leagueURL)-1 {
		return ""
	}
	return strings.TrimSpace(leagueURL[i+1:])
}

// ParseResultTime reads /api/public/results' "2006-01-02 15:04:05".
//
// The field carries no zone and it is NOT Bangkok, unlike every other time
// this vendor publishes. Proof from two of its own endpoints describing one
// fixture on 2026-11-22: /api/public/results gives Newcastle–Arsenal as
// "2026-11-21 17:30:00" while /api/analyse gives the same fixture as
// "2026-11-22T00:30:00+07:00" — seven hours apart, i.e. this one is UTC.
//
// loc is accepted and ignored so the caller still reads as though a zone
// were a choice here; it is not.
func ParseResultTime(s string, _ *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
