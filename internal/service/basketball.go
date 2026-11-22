package service

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
)

// BasketballCatalog is the read side of the basketball page.
//
// Unlike the boxing feed, this one is a real fixture list: teams, logos,
// a live score and the quarter it is in. What upstream does NOT carry is
// anything beyond that — ?sport=basketball is ignored by both the results
// and the standings endpoints, which answer with football whatever is
// asked, so there is no league table and no team statistics to serve. The
// page says so rather than showing an empty table.
type BasketballCatalog struct {
	store *store.Store
	log   *slog.Logger
}

func NewBasketballCatalog(st *store.Store, log *slog.Logger) *BasketballCatalog {
	return &BasketballCatalog{store: st, log: log}
}

// BasketballGame is one fixture.
type BasketballGame struct {
	ID         string `json:"id"`
	LeagueName string `json:"league_name,omitempty"`
	LeagueLogo string `json:"league_logo_url,omitempty"`

	HomeName string `json:"home_name"`
	AwayName string `json:"away_name"`
	HomeLogo string `json:"home_logo_url,omitempty"`
	AwayLogo string `json:"away_logo_url,omitempty"`

	TipoffAt  time.Time `json:"tipoff_at"`
	MatchDate string    `json:"match_date"`

	Status model.MatchStatus `json:"status"`
	// Period is upstream's own label — "Q1".."Q4", "OT", "HT", "จบเกม",
	// "รอแข่ง". It is the only place the quarter appears, so it is passed
	// through rather than folded into Status.
	Period string `json:"period,omitempty"`
	IsLive bool   `json:"is_live"`

	HomeScore int  `json:"home_score"`
	AwayScore int  `json:"away_score"`
	HasScore  bool `json:"has_score"`

	HasStream bool `json:"has_stream"`
}

// BasketballDay is one display day, already grouped the way the page reads
// it: live first, then upcoming, then finished.
type BasketballDay struct {
	Date    string           `json:"date"`
	Games   []BasketballGame `json:"games"`
	Leagues []string         `json:"leagues"`
	Live    int              `json:"live"`
}

// statusRank orders the list: a game in progress is the reason to open the
// page, a finished one is the least urgent.
func statusRank(s model.MatchStatus) int {
	switch s {
	case model.MatchLive, model.MatchHalftime:
		return 0
	case model.MatchScheduled:
		return 1
	case model.MatchFinished:
		return 2
	default:
		return 3
	}
}

// Games returns one display day. day is "YYYY-MM-DD" under the 04:00 ICT
// cutoff; empty means the current matchday.
func (c *BasketballCatalog) Games(ctx context.Context, day string) (BasketballDay, error) {
	if day == "" {
		day = CurrentMatchDate(time.Now())
	}
	rows, err := c.store.LnwMatchesBySportDate(ctx, model.LnwSportBasketball, day)
	if err != nil {
		return BasketballDay{}, err
	}

	out := BasketballDay{Date: day, Games: make([]BasketballGame, 0, len(rows))}
	seen := map[string]struct{}{}
	for _, m := range rows {
		if m.LeagueName != "" {
			if _, ok := seen[m.LeagueName]; !ok {
				seen[m.LeagueName] = struct{}{}
				out.Leagues = append(out.Leagues, m.LeagueName)
			}
		}
		if m.IsLive || m.Status == model.MatchLive || m.Status == model.MatchHalftime {
			out.Live++
		}
		out.Games = append(out.Games, BasketballGame{
			ID:         m.ID,
			LeagueName: m.LeagueName,
			LeagueLogo: m.LeagueLogoURL,
			HomeName:   m.HomeName,
			AwayName:   m.AwayName,
			HomeLogo:   m.HomeLogoURL,
			AwayLogo:   m.AwayLogoURL,
			TipoffAt:   m.KickoffAt,
			MatchDate:  m.MatchDate,
			Status:     m.Status,
			Period:     m.RawStatus,
			IsLive:     m.IsLive,
			HomeScore:  m.HomeScore,
			AwayScore:  m.AwayScore,
			HasScore:   m.HasScore,
			HasStream:  len(m.Channels) > 0,
		})
	}

	sort.Strings(out.Leagues)
	sort.SliceStable(out.Games, func(i, j int) bool {
		a, b := out.Games[i], out.Games[j]
		if ra, rb := statusRank(a.Status), statusRank(b.Status); ra != rb {
			return ra < rb
		}
		return a.TipoffAt.Before(b.TipoffAt)
	})
	return out, nil
}
