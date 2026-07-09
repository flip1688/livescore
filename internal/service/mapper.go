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

// standingFromResponse maps a thscore standings payload into our domain
// LeagueStanding. TotalRound/CurrentRound are not populated on LeagueInfo
// itself (confirmed against a live payload, 2026-07-09) — they live on
// whichever SubLeagueInfos entry has CurrentSubLeague == true, so that's
// where we source the league-level round numbers from.
func standingFromResponse(r thscore.StandingResponse, now time.Time) model.LeagueStanding {
	subLeagues := make([]model.StandingSubLeague, 0, len(r.SubLeagueInfos))
	var totalRound, currentRound int
	for _, sl := range r.SubLeagueInfos {
		subLeagues = append(subLeagues, model.StandingSubLeague{
			ID:               string(sl.SubLeagueID),
			Name:             sl.Name,
			TotalRound:       sl.TotalRound,
			CurrentRound:     sl.CurrentRound,
			HasScore:         sl.HasScore,
			HasTwoLegs:       sl.HasTwoLegs,
			CurrentSubLeague: sl.CurrentSubLeague,
		})
		if sl.CurrentSubLeague {
			totalRound = sl.TotalRound
			currentRound = sl.CurrentRound
		}
	}

	return model.LeagueStanding{
		ID:            string(r.LeagueInfo.LeagueID),
		Name:          r.LeagueInfo.Name,
		Format:        "league",
		CurrentSeason: r.LeagueInfo.CurrentSeason,
		Color:         r.LeagueInfo.Color,
		ShortName:     r.LeagueInfo.ShortName,
		TotalRound:    totalRound,
		CurrentRound:  currentRound,
		SubLeagues:    subLeagues,
		Standings: model.StandingViews{
			Total:    standingRows(r.TotalStandings, r.LeagueColorInfos),
			Half:     standingRows(r.HalfStandings, r.LeagueColorInfos),
			Home:     standingRows(r.HomeStandings, r.LeagueColorInfos),
			Away:     standingRows(r.AwayStandings, r.LeagueColorInfos),
			HomeHalf: standingRows(r.HomeHalfStandings, r.LeagueColorInfos),
			AwayHalf: standingRows(r.AwayHalfStandings, r.LeagueColorInfos),
		},
		UpdatedAt: now,
	}
}

// cupStandingFromResponse maps a thscore standing/cup.aspx payload (group-
// stage cup competitions, e.g. the World Cup) into our domain LeagueStanding.
// leagueID comes from the caller (the id syncStandings is iterating), not the
// payload's own leagueId field, mirroring standingFromResponse's fallback for
// when upstream omits it — cup.aspx has been observed to echo it back
// (league 75, 2026-07-10), but the caller-supplied id keeps behavior
// consistent between both mappers regardless.
func cupStandingFromResponse(leagueID string, r thscore.CupStandingResponse, now time.Time) model.LeagueStanding {
	id := leagueID
	if id == "" {
		id = string(r.LeagueID)
	}
	rounds := make([]model.CupRound, 0, len(r.RoundScoreItems))
	for _, round := range r.RoundScoreItems {
		groups := make([]model.CupGroup, 0, len(round.GroupScoreItems))
		for _, g := range round.GroupScoreItems {
			rows := make([]model.CupRow, 0, len(g.ScoreItems))
			for _, item := range g.ScoreItems {
				rows = append(rows, model.CupRow{
					Rank:           item.Rank,
					TeamID:         string(item.TeamID),
					TeamName:       item.TeamName,
					ColorHex:       item.Color,
					Played:         item.TotalCount,
					Win:            item.WinCount,
					Draw:           item.DrawCount,
					Lose:           item.LoseCount,
					GoalsFor:       item.GetScore,
					GoalsAgainst:   item.LoseScore,
					GoalDifference: item.GoalDifference,
					Points:         item.Integral,
				})
			}
			groups = append(groups, model.CupGroup{GroupName: g.GroupName, Rows: rows})
		}
		rounds = append(rounds, model.CupRound{RoundName: round.RoundName, Groups: groups})
	}

	return model.LeagueStanding{
		ID:            id,
		Format:        "cup",
		CurrentSeason: r.Season,
		CupRounds:     rounds,
		UpdatedAt:     now,
	}
}

// standingRows maps one standing view's rows, resolving each row's
// promotion/relegation zone from its Color index into zones.
func standingRows(rows []thscore.StandingRow, zones []thscore.StandingColorInfo) []model.StandingRow {
	out := make([]model.StandingRow, 0, len(rows))
	for _, row := range rows {
		zone, hex := standingColorZone(row.Color, zones)
		out = append(out, model.StandingRow{
			Rank:             row.Rank,
			TeamID:           string(row.TeamID),
			WinRate:          row.WinRate,
			DrawRate:         row.DrawRate,
			LoseRate:         row.LoseRate,
			WinAverage:       row.WinAverage,
			LoseAverage:      row.LoseAverage,
			Deduction:        row.Deduction,
			DeductionExplain: row.DeductionExplain,
			RecentForm: []int{
				row.RecentFirstResult, row.RecentSecondResult, row.RecentThirdResult,
				row.RecentFourthResult, row.RecentFifthResult, row.RecentSixthResult,
			},
			ColorZone:      zone,
			ColorHex:       hex,
			TotalCount:     row.TotalCount,
			WinCount:       row.WinCount,
			DrawCount:      row.DrawCount,
			LoseCount:      row.LoseCount,
			GetScore:       row.GetScore,
			LoseScore:      row.LoseScore,
			GoalDifference: row.GoalDifference,
			Points:         row.Integral,
		})
	}
	return out
}

// standingColorZone resolves a row's Color index into its zone label + hex
// color. -1 (or an out-of-range index) means no promotion/relegation zone.
func standingColorZone(idx int, zones []thscore.StandingColorInfo) (label, hex string) {
	if idx < 0 || idx >= len(zones) {
		return "", ""
	}
	return zones[idx].LeagueName, zones[idx].Color
}
