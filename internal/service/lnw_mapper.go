package service

import (
	"strconv"
	"strings"
	"time"

	"github.com/flip1688/livescore/internal/lnw"
	"github.com/flip1688/livescore/internal/model"
)

// Mapping from the lnwthailive feeds into model.Lnw* documents.
//
// Each feed has its own status vocabulary and none of them is thscore's, so
// every raw label is stored next to the normalized one. When upstream
// invents a value we have not seen, the raw field is what lets us notice.

// matchStatusFrom normalizes the fixture feed's status field, which mixes a
// Thai word, a period abbreviation and an elapsed minute in one string.
func matchStatusFrom(raw string, isLive bool) model.MatchStatus {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToUpper(trimmed) {
	case "FT", "AET", "PEN":
		return model.MatchFinished
	case "HT":
		return model.MatchHalftime
	case "POSTP", "POSTPONED":
		return model.MatchPostponed
	case "CANC", "CANCELLED", "CANCELED":
		return model.MatchCanceled
	}
	// The basketball feed speaks a different vocabulary through the same
	// field: quarters rather than minutes, and a Thai word for full time.
	// Without these, 42 of 121 basketball fixtures on 2026-11-23 — every
	// finished one — fell through to "scheduled" and the page would have
	// shown games that ended hours ago as not yet started.
	if trimmed == "จบเกม" {
		return model.MatchFinished
	}
	if isQuarterLabel(trimmed) {
		return model.MatchLive
	}
	if strings.Contains(raw, "'") || isLive {
		return model.MatchLive
	}
	// "รอแข่ง" and anything else unrecognised: upstream has not started the
	// match, so scheduled is the safe reading.
	return model.MatchScheduled
}

// isQuarterLabel reports whether a status is a basketball period: Q1..Q4
// as upstream writes them, and OT / OT2 for overtime.
func isQuarterLabel(s string) bool {
	u := strings.ToUpper(s)
	switch u {
	case "Q1", "Q2", "Q3", "Q4", "OT", "OT2", "OT3":
		return true
	}
	return false
}

// resultStateFrom normalizes the results feed's state codes. These are
// Sportmonks-style and share nothing with the fixture feed's labels.
func resultStateFrom(state string) model.MatchStatus {
	s := strings.ToUpper(strings.TrimSpace(state))
	switch s {
	case "NS", "TBA":
		return model.MatchScheduled
	case "HT":
		return model.MatchHalftime
	case "FT", "FT_PEN", "AET", "AWARDED":
		return model.MatchFinished
	case "POSTP", "POSTPONED":
		return model.MatchPostponed
	case "CANCL", "CANCELLED", "ABANDONED", "WO":
		return model.MatchCanceled
	}
	if strings.HasPrefix(s, "INPLAY") || strings.HasPrefix(s, "LIVE") {
		return model.MatchLive
	}
	return model.MatchScheduled
}

// analyseStatusFrom normalizes the analysis feed's status, a third
// vocabulary again ("FULL_TIME", "NOT_STARTED", …).
func analyseStatusFrom(status string) model.MatchStatus {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FULL_TIME", "FINISHED", "ENDED":
		return model.MatchFinished
	case "HALF_TIME":
		return model.MatchHalftime
	case "IN_PLAY", "LIVE", "FIRST_HALF", "SECOND_HALF":
		return model.MatchLive
	case "POSTPONED":
		return model.MatchPostponed
	case "CANCELLED", "CANCELED":
		return model.MatchCanceled
	}
	return model.MatchScheduled
}

// mapLnwMatch converts one fixture row. fetchedAt must be the clock the row
// was actually fetched with: the row carries no date of its own, so a wrong
// clock puts the match on the wrong day. ok is false for a row whose time
// cannot be resolved — skipped rather than stored at a guessed instant.
func mapLnwMatch(m lnw.Match, fetchedAt time.Time) (model.LnwMatch, bool) {
	kickoff, ok := lnw.ResolveKickoff(m.Day, m.Time, fetchedAt, Bangkok)
	if !ok || m.ID == "" {
		return model.LnwMatch{}, false
	}
	home, away, hasScore := lnw.ParseScore(m.Score)

	sport := m.Sport
	if sport == "" {
		sport = model.LnwSportFootball
	}

	out := model.LnwMatch{
		ID:            m.ID,
		Sport:         sport,
		LeagueName:    m.League,
		HomeName:      m.TeamHome,
		AwayName:      m.TeamAway,
		HomeLogoURL:   m.HomeLogo,
		AwayLogoURL:   m.AwayLogo,
		LeagueLogoURL: m.LeagueLogo,
		CountryLogo:   m.CountryLogo,
		KickoffAt:     kickoff.UTC(),
		FetchedAt:     fetchedAt.UTC(),
		RawDay:        m.Day,
		RawTime:       m.Time,
		MatchDate:     MatchDateFor(kickoff),
		Status:        matchStatusFrom(m.MatchStatus, m.IsLive),
		RawStatus:     m.MatchStatus,
		Minute:        lnw.ParseMinute(m.MatchStatus),
		IsLive:        m.IsLive,
		HomeScore:     home,
		AwayScore:     away,
		HasScore:      hasScore,
		UpdatedAt:     time.Now().UTC(),
	}
	// Channel names and ids only. The stream/player/embed URLs on each
	// channel carry short-lived tokens that upstream says must not be
	// cached past 15 seconds, so they are dropped here rather than written
	// to Mongo and served stale.
	for _, c := range m.Channels {
		out.Channels = append(out.Channels, model.LnwChannel{ID: c.ID, Name: c.Name, LogoURL: c.Logo})
	}
	return out, true
}

// mapLnwResult converts one result row. The id is prefixed because this
// feed's integer ids collide with nothing and belong to nothing else —
// without a prefix an "19893334" here is indistinguishable from an id on
// the analysis feed.
func mapLnwResult(league lnw.ResultLeague, m lnw.ResultMatch) (model.LnwResult, bool) {
	if m.ID == 0 {
		return model.LnwResult{}, false
	}
	kickoff, ok := lnw.ParseResultTime(m.StartingAt, Bangkok)
	if !ok {
		return model.LnwResult{}, false
	}
	return model.LnwResult{
		ID:            "sm_" + strconv.FormatInt(m.ID, 10),
		UpstreamID:    m.ID,
		LeagueID:      league.LeagueID,
		LeagueName:    league.LeagueName,
		LeagueLogoURL: league.LeagueLogo,
		HomeName:      m.HomeTeam.Name,
		AwayName:      m.AwayTeam.Name,
		HomeLogoURL:   m.HomeTeam.Logo,
		AwayLogoURL:   m.AwayTeam.Logo,
		KickoffAt:     kickoff.UTC(),
		MatchDate:     MatchDateFor(kickoff),
		Status:        resultStateFrom(m.State),
		RawState:      m.State,
		HomeScore:     m.HomeScore,
		AwayScore:     m.AwayScore,
		UpdatedAt:     time.Now().UTC(),
	}, true
}

// mapLnwStanding converts one league table. ok is false when upstream gives
// no anchor to key the table by — it is dropped rather than stored under a
// name that may change.
func mapLnwStanding(s lnw.Standing, upstreamFetched time.Time, stale bool) (model.LnwStanding, bool) {
	key := lnw.StandingKey(s.LeagueURL)
	if key == "" {
		return model.LnwStanding{}, false
	}
	out := model.LnwStanding{
		ID:               key,
		LeagueName:       s.LeagueName,
		LeagueShort:      s.LeagueShortName,
		LeagueLogoURL:    s.LeagueLogo,
		IsPreSeason:      s.IsPreSeason,
		IsStale:          stale,
		UpstreamFetchdAt: upstreamFetched,
		UpdatedAt:        time.Now().UTC(),
	}
	for _, r := range s.Rows {
		out.Rows = append(out.Rows, model.LnwStandingRow{
			Rank: r.Rank, Team: r.Team, TeamEn: r.TeamEn, TeamLogo: r.TeamLogo,
			Played: r.MP, Won: r.W, Drawn: r.D, Lost: r.L,
			GoalsFor: r.GF, GoalsAg: r.GA, GoalDiff: r.GD, Points: r.Pts,
		})
	}
	return out, true
}

// mapLnwAnalysis converts one analysis index row. This feed is the only one
// that states its timezone, so its timestamp is parsed as sent.
func mapLnwAnalysis(a lnw.Analyse) (model.LnwAnalysis, bool) {
	if a.ID == 0 {
		return model.LnwAnalysis{}, false
	}
	kickoff, err := time.Parse(time.RFC3339, a.MatchDatetime)
	if err != nil {
		return model.LnwAnalysis{}, false
	}
	return model.LnwAnalysis{
		ID:          "an_" + strconv.FormatInt(a.ID, 10),
		UpstreamID:  a.ID,
		Name:        a.Name,
		HomeName:    a.HomeTeam.Name,
		AwayName:    a.AwayTeam.Name,
		LeagueName:  a.League.Name,
		HomeTeamID:  a.HomeTeam.ID,
		AwayTeamID:  a.AwayTeam.ID,
		LeagueID:    a.League.ID,
		KickoffAt:   kickoff.UTC(),
		MatchDate:   MatchDateFor(kickoff),
		Status:      analyseStatusFrom(a.MatchStatus),
		RawStatus:   a.MatchStatus,
		HomeScore:   a.HomeScore,
		AwayScore:   a.AwayScore,
		HasOdds:     a.HasOdds,
		HasLineup:   a.HasLineup,
		HasTeamStat: a.HasTeamStats,
		HasArticle:  a.Article != nil,
		UpdatedAt:   time.Now().UTC(),
	}, true
}
