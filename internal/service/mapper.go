package service

import (
	"strconv"
	"time"

	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/thscore"
)

// matchFromLivescore maps a thscore livescores/schedule payload row into our
// domain Match. MatchDate is bucketed from the kickoff under the 04:00 ICT
// cutoff — never from the date the row was fetched.
func matchFromLivescore(m thscore.LivescoreMatch, now time.Time) (model.Match, error) {
	kickoff, err := thscore.ParseMatchTime(m.MatchTime)
	if err != nil {
		return model.Match{}, err
	}

	var halfStart time.Time
	if m.HalfStartTime != "" {
		if hs, err := thscore.ParseMatchTime(m.HalfStartTime); err == nil {
			halfStart = hs
		}
	}

	return model.Match{
		ID:              strconv.Itoa(m.MatchID),
		LeagueID:        strconv.Itoa(m.LeagueID),
		LeagueName:      m.LeagueName,
		LeagueShortName: m.LeagueShortName,
		LeagueColor:     m.LeagueColor,
		HomeTeamID:      string(m.HomeID),
		AwayTeamID:      string(m.AwayID),
		HomeName:        m.HomeName,
		AwayName:        m.AwayName,
		KickoffAt:       kickoff,
		MatchDate:       MatchDateFor(kickoff),
		Status:          thscore.MapStatus(m.Status),
		StatusCode:      m.Status,
		Minute:          m.Minute,
		HalfStartAt:     halfStart,
		InjuryTime:      m.InjuryTime,
		HomeScore:       m.HomeScore,
		AwayScore:       m.AwayScore,
		HomeHalfScore:   m.HomeHalfScore,
		AwayHalfScore:   m.AwayHalfScore,
		HomeRed:         m.HomeRed,
		AwayRed:         m.AwayRed,
		HomeYellow:      m.HomeYellow,
		AwayYellow:      m.AwayYellow,
		HomeCorner:      m.HomeCorner,
		AwayCorner:      m.AwayCorner,
		UpdatedAt:       now,
	}, nil
}

// applyChange merges a livescores/changes delta onto the previous state of a
// match. The delta carries no league/team fields, so prev must come from the
// live snapshot (Redis) or the schedule (Mongo).
func applyChange(prev model.Match, ch thscore.LivescoreChange, now time.Time) model.Match {
	m := prev
	m.Status = thscore.MapStatus(ch.Status)
	m.StatusCode = ch.Status
	m.HomeScore = ch.HomeScore
	m.AwayScore = ch.AwayScore
	m.HomeHalfScore = ch.HomeHalfScore
	m.AwayHalfScore = ch.AwayHalfScore
	m.HomeRed = ch.HomeRed
	m.AwayRed = ch.AwayRed
	m.HomeYellow = ch.HomeYellow
	m.AwayYellow = ch.AwayYellow
	m.HomeCorner = ch.HomeCorner
	m.AwayCorner = ch.AwayCorner
	m.InjuryTime = ch.InjuryTime
	if hs, ok := thscore.ParseTimeAny(ch.StartTime); ok {
		m.HalfStartAt = hs
	}
	// The feed only carries a usable minute at the moment something changes;
	// derive the ticking minute from the half start when we can.
	if minute := model.ComputeLiveMinute(ch.Status, m.HalfStartAt, now); minute > 0 {
		m.Minute = minute
	} else if ch.Minute > 0 {
		m.Minute = ch.Minute
	}
	m.UpdatedAt = now
	return m
}

// leagueFromProfile maps a thscore league profile into our domain League.
// Only the source logo URL is stored here; LogoURL (our mirrored R2 URL) is
// stamped separately by the logo sync job.
func leagueFromProfile(l thscore.LeagueProfile, now time.Time) model.League {
	return model.League{
		ID:            strconv.Itoa(l.LeagueID),
		Name:          l.Name,
		Country:       l.Country,
		Season:        l.CurrentSeason,
		LogoSourceURL: l.Logo,
		UpdatedAt:     now,
	}
}

// teamFromProfile maps a thscore team profile into our domain Team. See
// leagueFromProfile for the logo field split.
func teamFromProfile(t thscore.TeamProfile, now time.Time) model.Team {
	return model.Team{
		ID:            strconv.Itoa(t.TeamID),
		Name:          t.Name,
		LeagueID:      strconv.Itoa(t.LeagueID),
		LogoSourceURL: t.Logo,
		UpdatedAt:     now,
	}
}

func countryFromPayload(c thscore.Country, now time.Time) model.Country {
	return model.Country{
		ID:        strconv.Itoa(c.CountryID),
		Name:      c.Country,
		NameTH:    c.CountryTh,
		UpdatedAt: now,
	}
}
