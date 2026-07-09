package thscore

import "encoding/json"

// FlexString decodes a JSON value that may arrive as either a string or a
// number, normalizing to its string form. thscore is inconsistent per
// endpoint: homeId/awayId are strings on schedule/basic.aspx but numbers on
// livescores.aspx (confirmed against live payloads, 2026-07-08).
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = FlexString(n.String())
	return nil
}

// LivescoreMatch represents a single match from /football_th/livescores.aspx,
// /football_th/schedule/basic.aspx and /football_th/schedule.aspx. The three
// endpoints share this shape; fields not present on a given endpoint are left
// zero-valued.
//
// HomeID/AwayID arrive as strings on schedule/basic.aspx but as numbers on
// livescores.aspx — see FlexString.
type LivescoreMatch struct {
	MatchID         int             `json:"matchId"`
	LeagueType      int             `json:"leagueType"` // 1:League 2:Cup
	LeagueID        int             `json:"leagueId"`
	LeagueName      string          `json:"leagueName"`
	LeagueShortName string          `json:"leagueShortName"`
	LeagueColor     string          `json:"leagueColor"`
	SubLeagueID     string          `json:"subLeagueId"`
	SubLeagueName   string          `json:"subLeagueName"`
	MatchTime       string          `json:"matchTime"`     // GMT+7, dd-MM-yyyy HH:mm:ss — see ParseMatchTime
	HalfStartTime   string          `json:"halfStartTime"` // GMT+7, dd-MM-yyyy HH:mm:ss
	KickOff         int             `json:"kickOff"`       // 1:Home kicks off first 2:Away
	Status          int             `json:"status"`        // see status.go Status* constants
	HomeID          FlexString      `json:"homeId"`
	HomeName        string          `json:"homeName"`
	AwayID          FlexString      `json:"awayId"`
	AwayName        string          `json:"awayName"`
	HomeScore       int             `json:"homeScore"`
	AwayScore       int             `json:"awayScore"`
	HomeHalfScore   int             `json:"homeHalfScore"`
	AwayHalfScore   int             `json:"awayHalfScore"`
	HomeRed         int             `json:"homeRed"`
	AwayRed         int             `json:"awayRed"`
	HomeYellow      int             `json:"homeYellow"`
	AwayYellow      int             `json:"awayYellow"`
	HomeCorner      int             `json:"homeCorner"`
	AwayCorner      int             `json:"awayCorner"`
	HomeRank        string          `json:"homeRank"`
	AwayRank        string          `json:"awayRank"`
	Season          string          `json:"season"`
	StageID         string          `json:"stageId"`
	Round           string          `json:"round"`
	Group           string          `json:"group"`
	Location        string          `json:"location"`
	Weather         string          `json:"weather"`
	Temperature     string          `json:"temperature"`
	Explain         string          `json:"explain"`
	ExtraExplain    json.RawMessage `json:"extraExplain"` // extra-time/penalty/two-legs detail; shape not needed yet
	Minute          int             `json:"minute"`
	HasLineup       bool            `json:"hasLineup"`
	Neutral         bool            `json:"neutral"`
	Var             string          `json:"var"`
	InjuryTime      int             `json:"injuryTime"`
}

// LivescoreChange represents a single match update from
// /football_th/livescores/changes.aspx. Only fields that changed since the
// last poll are populated by the API.
//
// MatchTime/StartTime are documented as Unix timestamps (number or string),
// but live payloads (2026-07-08) send "dd-MM-yyyy HH:mm:ss" GMT+7 strings —
// parse with ParseTimeAny, which accepts all three encodings.
type LivescoreChange struct {
	MatchID       int             `json:"matchId"`
	MatchTime     any             `json:"matchTime"` // GMT+7; unix number/string or dd-MM-yyyy HH:mm:ss
	StartTime     any             `json:"startTime"` // kick-off of the current half, same encodings
	Status        int             `json:"status"`
	HomeScore     int             `json:"homeScore"`
	AwayScore     int             `json:"awayScore"`
	HomeHalfScore int             `json:"homeHalfScore"`
	AwayHalfScore int             `json:"awayHalfScore"`
	HomeRed       int             `json:"homeRed"`
	AwayRed       int             `json:"awayRed"`
	HomeYellow    int             `json:"homeYellow"`
	AwayYellow    int             `json:"awayYellow"`
	HomeCorner    int             `json:"homeCorner"`
	AwayCorner    int             `json:"awayCorner"`
	HasLineup     bool            `json:"hasLineup"`
	ExtraExplain  json.RawMessage `json:"extraExplain"`
	Minute        int             `json:"minute"`
	Var           string          `json:"var"`
	InjuryTime    int             `json:"injuryTime"`
	Winner        int             `json:"winner"` // 1:Home 2:Away
}

// EventItem is a single live event (goal/card/substitution/...) within a
// match from /football_th/events.aspx.
type EventItem struct {
	EventID        int    `json:"eventId"`
	Minute         string `json:"minute"` // "45"/"90" style; may carry stoppage suffix
	Type           int    `json:"type"`   // see status.go Event* constants
	HomeEvent      bool   `json:"homeEvent"`
	PlayerID       any    `json:"playerId"`
	PlayerName     string `json:"playerName"`
	AssistPlayerID any    `json:"assistPlayerId"`
	Overtime       string `json:"overtime"`
	OprTime        string `json:"oprTime"` // GMT+7, dd-MM-yyyy HH:mm:ss
}

// EventsMatch groups events for a single match, as returned by
// /football_th/events.aspx.
type EventsMatch struct {
	MatchID int         `json:"matchId"`
	Events  []EventItem `json:"events"`
}

// StatItem is one technical stat entry (possession/shots/...) from
// /football_th/stats.aspx.
type StatItem struct {
	Type int    `json:"type"`
	Home string `json:"home"`
	Away string `json:"away"`
}

// StatsMatch groups technical stats for a single match, as returned by
// /football_th/stats.aspx. OprTime is match-level, not per stat item
// (confirmed against live payloads, 2026-07-08).
type StatsMatch struct {
	MatchID int        `json:"matchId"`
	Stats   []StatItem `json:"stats"`
	OprTime string     `json:"oprTime"` // GMT+7, dd-MM-yyyy HH:mm:ss
}

// LeagueProfile is the full league/cup profile from /football_th/league.aspx.
type LeagueProfile struct {
	LeagueID      int    `json:"leagueId"`
	Type          int    `json:"type"` // 1:League 2:Cup
	Color         string `json:"color"`
	Logo          string `json:"logo"` // download and host ourselves; hotlinking is disallowed
	Name          string `json:"name"`
	ShortName     string `json:"shortName"`
	SubLeagueName string `json:"subLeagueName"`
	TotalRound    int    `json:"totalRound"`
	CurrentRound  int    `json:"currentRound"`
	CurrentSeason string `json:"currentSeason"`
	CountryID     int    `json:"countryId"`
	Country       string `json:"country"`
	CountryLogo   string `json:"countryLogo"`
	AreaID        int    `json:"areaId"` // 0:International 1:Europe 2:America 3:Asia 4:Oceania 5:Africa
}

// TeamProfile is a team profile from /football_th/team.aspx.
type TeamProfile struct {
	TeamID       int    `json:"teamId"`
	LeagueID     int    `json:"leagueId"`
	Name         string `json:"name"`
	Logo         string `json:"logo"` // download and host ourselves; hotlinking is disallowed
	FoundingDate string `json:"foundingDate"`
	Address      string `json:"address"`
	Area         string `json:"area"`
	Venue        string `json:"venue"`
	Capacity     int    `json:"capacity"`
	Coach        string `json:"coach"`
	Website      string `json:"website"`
	IsNational   bool   `json:"isNational"`
}

// Country is a single entry from /football_th/country.aspx.
type Country struct {
	CountryID int    `json:"countryId"`
	Country   string `json:"country"`
	CountryTh string `json:"countryTh"`
}

// ScheduleModification records a deletion or reschedule from
// /football_th/schedule/modify.aspx, covering the past 12h.
// matchTime arrives as a GMT+7 datetime string OR a unix number depending on
// the row (same inconsistency as changes.aspx, observed live 2026-07-10) —
// FlexString absorbs both; parse with ParseTimeAny if ever needed.
type ScheduleModification struct {
	MatchID    FlexString `json:"matchId"`
	Type       string     `json:"type"`       // "modify" or "delete"
	MatchTime  FlexString `json:"matchTime"`  // original kick-off, GMT+7
	ModifyTime FlexString `json:"modifyTime"` // when the change occurred, GMT+7 (the new kick-off must be re-fetched)
}

// --- Standings ---
//
// Shapes below are confirmed against a production thscore client
// (ChangPuakk/widgets, internal/services/thscore.go + pkg/models/thscore.go)
// and cross-checked with cmd/thscore-smoke against a live payload
// (2026-07-09). Notably /football_th/standing/league.aspx returns the
// standing object directly — no {"code","message","data"} envelope, unlike
// every other typed endpoint — see Client.FetchLeagueStanding.

// StandingSubLeagueInfo describes one stage/division a league's standing can
// be split into (e.g. group stage vs. playoff group).
type StandingSubLeagueInfo struct {
	SubLeagueID      FlexString `json:"subLeagueId"`
	Name             string     `json:"name"`
	TotalRound       int        `json:"totalRound"`
	CurrentRound     int        `json:"currentRound"`
	HasScore         bool       `json:"hasScore"`
	HasTwoLegs       bool       `json:"hasTwoLegs"`
	CurrentSubLeague bool       `json:"currentSubLeague"`
}

// StandingLeagueInfo is the league header of a standing response.
// TotalRound/CurrentRound are NOT populated at this nesting level in
// practice (confirmed against a live payload, 2026-07-09) — use the current
// entry (CurrentSubLeague == true) in StandingResponse.SubLeagueInfos
// instead, which is where round numbers actually live.
type StandingLeagueInfo struct {
	LeagueID      FlexString `json:"leagueId"`
	Name          string     `json:"name"`
	CurrentSeason string     `json:"currentSeason"`
	Color         string     `json:"color"`
	ShortName     string     `json:"shortName"`
}

// StandingTeamInfo is a team roster entry within a standing response — just
// identity, not a table row (see StandingRow for the per-view rankings).
type StandingTeamInfo struct {
	TeamID FlexString `json:"teamId"`
	Name   string     `json:"name"`
	Area   int        `json:"area"` // 0:no partition 1:East 2:West
}

// StandingRow is one team's row in a single standing view (total/half/home/
// away/homeHalf/awayHalf). Color is an index into the response's
// LeagueColorInfos (-1 = no promotion/relegation zone); Recent*Result fields
// are the team's last six results, most recent first (0:win 1:draw 2:lose
// 3:no match played in that slot yet).
type StandingRow struct {
	Rank               int        `json:"rank"`
	TeamID             FlexString `json:"teamId"`
	WinRate            float64    `json:"winRate"`
	DrawRate           float64    `json:"drawRate"`
	LoseRate           float64    `json:"loseRate"`
	WinAverage         float64    `json:"winAverage"`
	LoseAverage        float64    `json:"loseAverage"`
	Deduction          int        `json:"deduction"`
	DeductionExplain   string     `json:"deductionExplain"`
	RecentFirstResult  int        `json:"recentFirstResult"`
	RecentSecondResult int        `json:"recentSecondResult"`
	RecentThirdResult  int        `json:"recentThirdResult"`
	RecentFourthResult int        `json:"recentFourthResult"`
	RecentFifthResult  int        `json:"recentFifthResult"`
	RecentSixthResult  int        `json:"recentSixthResult"`
	Color              int        `json:"color"`
	Red                int        `json:"red"`
	TotalCount         int        `json:"totalCount"`
	WinCount           int        `json:"winCount"`
	DrawCount          int        `json:"drawCount"`
	LoseCount          int        `json:"loseCount"`
	GetScore           int        `json:"getScore"`
	LoseScore          int        `json:"loseScore"`
	GoalDifference     int        `json:"goalDifference"`
	TotalAddScore      int        `json:"totalAddScore"`
	Integral           int        `json:"integral"` // points
}

// StandingColorInfo is one promotion/relegation zone entry. StandingRow.Color
// indexes into the response's LeagueColorInfos slice. LeagueName here is
// upstream's (slightly misleading) field name for the zone's display label
// (e.g. "Champions League", "Relegation") — it is not the league's own name.
type StandingColorInfo struct {
	Color      string `json:"color"`
	LeagueName string `json:"leagueName"`
}

// StandingResponse is the full payload from /football_th/standing/league.aspx.
// Field placement here (SubLeagueInfos top-level, not nested in LeagueInfo;
// TeamInfos keyed "teamInfo" singular; no top-level totalRound/currentRound)
// is confirmed against a live payload (cmd/thscore-smoke, 2026-07-09) and
// differs from the endpoint's docs summary — trust this over docs.
type StandingResponse struct {
	LeagueInfo        StandingLeagueInfo      `json:"leagueInfo"`
	SubLeagueInfos    []StandingSubLeagueInfo `json:"subLeagueInfos"`
	TeamInfos         []StandingTeamInfo      `json:"teamInfo"` // note: singular upstream key
	TotalStandings    []StandingRow           `json:"totalStandings"`
	HalfStandings     []StandingRow           `json:"halfStandings"`
	HomeStandings     []StandingRow           `json:"homeStandings"`
	AwayStandings     []StandingRow           `json:"awayStandings"`
	HomeHalfStandings []StandingRow           `json:"homeHalfStandings"`
	AwayHalfStandings []StandingRow           `json:"awayHalfStandings"`
	LeagueColorInfos  []StandingColorInfo     `json:"leagueColorInfos"`
	Conference        bool                    `json:"conference"`
}
