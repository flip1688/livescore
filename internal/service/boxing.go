package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
)

// BoxingCatalog is the read side of the boxing page.
//
// What it can serve is bounded by what the vendor actually carries, and
// that is less than the page was designed around. The fighting feed is a
// broadcast listing: a programme name, a start time and, once the
// programme is on air, a stream. There are no fighters, no corners, no
// weights and no bout order anywhere in it, so none of those are invented
// here — the API returns what exists and the page says what is missing.
type BoxingCatalog struct {
	store *store.Store
	log   *slog.Logger
}

func NewBoxingCatalog(st *store.Store, log *slog.Logger) *BoxingCatalog {
	return &BoxingCatalog{store: st, log: log}
}

// BoxingBroadcast is one programme on one day.
type BoxingBroadcast struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Discipline is our classification of the programme's name, since
	// upstream does not say which combat sport a broadcast is. Rows it
	// cannot be read from are "unknown" rather than guessed into one.
	Discipline string    `json:"discipline"`
	StartAt    time.Time `json:"start_at"`
	MatchDate  string    `json:"match_date"`
	// Status is the normalized state; RawStatus is upstream's own label,
	// which mixes a minute, a period and a Thai word in one field.
	Status    model.MatchStatus `json:"status"`
	RawStatus string            `json:"raw_status,omitempty"`
	IsLive    bool              `json:"is_live"`
	// HasStream is true once upstream lists a channel. The URL itself is
	// never stored or served: it carries a token derived from our API key.
	HasStream bool `json:"has_stream"`
	Channels  int  `json:"channels"`
}

// BoxingProgram is one promotion in the vendor's programme directory.
type BoxingProgram struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Logo   string `json:"logo_url,omitempty"`
	Splash string `json:"splash_url,omitempty"`
	// Cards is upstream's own count of fights on the programme. It has been
	// zero on every programme since the feed was first read, which is why
	// the page cannot show a fight card yet.
	Cards int `json:"cards"`
}

// disciplineOf classifies a broadcast by its title.
//
// The vendor sends every combat broadcast under one sport, "fighting", so
// the page's มวยไทย / คิกบ็อกซิ่ง / มวยสากล / MMA filter has nothing to
// filter on unless the title is read. Titles observed so far are promotion
// names ("Muay Thai Alliance 2026 Ep.44", "Rajadamnern Knockout"), which
// carry the discipline often enough to be worth matching and not often
// enough to be trusted silently: anything unmatched stays "unknown" and the
// page shows it under every filter rather than hiding it.
func disciplineOf(title string) string {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "mma") || strings.Contains(t, "cage"):
		return "mma"
	case strings.Contains(t, "kickbox") || strings.Contains(t, "คิกบ็อก"):
		return "kickboxing"
	case strings.Contains(t, "muay") || strings.Contains(t, "มวยไทย") ||
		strings.Contains(t, "rajadamnern") || strings.Contains(t, "lumpinee") ||
		strings.Contains(t, "one championship"):
		return "muay-thai"
	case strings.Contains(t, "boxing") || strings.Contains(t, "มวยสากล"):
		return "boxing"
	default:
		return "unknown"
	}
}

// Schedule returns one day's broadcasts. day is "YYYY-MM-DD" under the
// 04:00 ICT cutoff; empty means the current matchday.
func (c *BoxingCatalog) Schedule(ctx context.Context, day string) ([]BoxingBroadcast, error) {
	if day == "" {
		day = CurrentMatchDate(time.Now())
	}
	rows, err := c.store.LnwMatchesBySportDate(ctx, model.LnwSportFighting, day)
	if err != nil {
		return nil, err
	}
	out := make([]BoxingBroadcast, 0, len(rows))
	for _, m := range rows {
		title := m.HomeName
		if title == "" {
			// Upstream puts the programme name in both the league and the
			// home-team field and leaves the away side empty.
			title = m.LeagueName
		}
		out = append(out, BoxingBroadcast{
			ID:         m.ID,
			Title:      title,
			Discipline: disciplineOf(title + " " + m.LeagueName),
			StartAt:    m.KickoffAt,
			MatchDate:  m.MatchDate,
			Status:     m.Status,
			RawStatus:  m.RawStatus,
			IsLive:     m.IsLive,
			HasStream:  len(m.Channels) > 0,
			Channels:   len(m.Channels),
		})
	}
	return out, nil
}

// Programs returns the promotion directory.
func (c *BoxingCatalog) Programs(ctx context.Context) ([]BoxingProgram, error) {
	rows, err := c.store.ListLnwBoxingPrograms(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BoxingProgram, 0, len(rows))
	for _, p := range rows {
		out = append(out, BoxingProgram{
			ID: p.ID, Name: p.Name, Logo: p.Logo, Splash: p.Splash, Cards: p.TotalEvents,
		})
	}
	return out, nil
}
