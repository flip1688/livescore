package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/flip1688/livescore/internal/lnw"
	"github.com/flip1688/livescore/internal/model"
)

// The combat feeds. These are the part of this vendor that thscore has no
// answer to at all, so they are stored even though the football comparison
// is what the purchase decision nominally turns on.

// SyncBoxing stores the Thai boxing programme list.
//
// Upstream has never yet served a card: all 26 programmes read totalEvents
// 0 on 2026-11-22, and the endpoint ignores every parameter that might ask
// for another day. So the job's real work is to keep watching — when a
// card does appear it is logged at WARN and the raw array is stored
// verbatim, which is what an event struct will eventually be written from.
func (s *LnwSyncer) SyncBoxing(ctx context.Context) error {
	fetchedAt := time.Now()
	resp, err := s.client.FetchBoxing(ctx)
	if err != nil {
		return err
	}

	scraped, _ := lnw.ParseCombatTime(resp.Data.ScrapedAt)
	docs := make([]model.LnwBoxingProgram, 0, len(resp.Data.Programs))
	var withCard int
	for _, p := range resp.Data.Programs {
		if p.ID == "" {
			continue
		}
		doc := model.LnwBoxingProgram{
			ID:          p.ID,
			Name:        p.Name,
			Logo:        p.Logo,
			Splash:      p.Splash,
			TotalEvents: p.TotalEvents,
			ScrapedAt:   scraped.UTC(),
			FetchedAt:   fetchedAt.UTC(),
			UpdatedAt:   fetchedAt.UTC(),
		}
		if p.HasEvents() {
			withCard++
			doc.RawEvents = string(p.Events)
			// Loud on purpose: this is the first evidence of a shape we
			// have had to leave untyped, and it is only visible while the
			// card is live.
			s.log.Warn("lnw boxing card seen — capture it, the event shape is still unknown",
				"program_id", p.ID, "program", p.Name,
				"total_events", p.TotalEvents, "bytes", len(p.Events))
		}
		docs = append(docs, doc)
	}

	if err := s.store.UpsertLnwBoxingPrograms(ctx, docs); err != nil {
		return err
	}
	s.log.Info("lnw boxing synced",
		"programs", len(docs), "with_card", withCard, "tier", resp.Tier)
	return nil
}

// SyncCombat stores one combat feed's bouts.
func (s *LnwSyncer) SyncCombat(ctx context.Context, sport string) error {
	fetchedAt := time.Now()
	resp, err := s.client.FetchCombat(ctx, sport)
	if err != nil {
		return err
	}

	docs := make([]model.LnwCombatBout, 0, len(resp.Data))
	var skipped, unknownResult, liveDisagree int
	for _, b := range resp.Data {
		doc, ok := mapLnwCombatBout(b, sport, fetchedAt)
		if !ok {
			skipped++
			continue
		}
		if !doc.ResultKnown {
			unknownResult++
		}
		if doc.IsLive && doc.Status == "ended" {
			liveDisagree++
		}
		docs = append(docs, doc)
	}

	if err := s.store.UpsertLnwCombatBouts(ctx, docs); err != nil {
		return err
	}
	s.log.Info("lnw combat synced",
		"sport", sport, "stored", len(docs), "skipped", skipped,
		"unknown_result", unknownResult, "live_contradicts_status", liveDisagree,
		"stadiums", len(resp.Stadiums))
	return nil
}

// mapLnwCombatBout converts one bout. ok is false for a row with no id or
// no readable start time: unlike the football fixtures there is no clock to
// fall back on, and a bout stored at the zero instant would sort into every
// query for every day.
func mapLnwCombatBout(b lnw.CombatBout, sport string, fetchedAt time.Time) (model.LnwCombatBout, bool) {
	if b.ID == 0 {
		return model.LnwCombatBout{}, false
	}
	start, ok := lnw.ParseCombatTime(b.DateTime)
	if !ok {
		return model.LnwCombatBout{}, false
	}

	result, known := lnw.CombatResult(b.Result)

	return model.LnwCombatBout{
		ID:    fmt.Sprintf("%s:%d", sport, b.ID),
		Sport: sport,
		// Type is upstream's sub-sport, which is not the same thing as the
		// feed it arrived on: the wuachon feed carries cock fights too.
		Type:         b.Type,
		TypeLabel:    b.TypeLabel,
		RedName:      strings.TrimSpace(b.RedCorner.Name),
		BlueName:     strings.TrimSpace(b.BlueCorner.Name),
		Stadium:      strings.TrimSpace(b.Stadium),
		StadiumID:    b.StadiumID,
		BoutNo:       lnw.BoutNumber(b.RoundName),
		RawRoundName: b.RoundName,
		StartAt:      start.UTC(),
		MatchDate:    MatchDateFor(start),
		Status:       b.Status,
		StatusLabel:  b.StatusLabel,
		IsLive:       b.IsLive,
		Result:       result,
		ResultKnown:  known,
		RawResult:    b.Result,
		ResultLabel:  b.ResultLabel,
		Stakes:       b.Stakes,
		Stats: model.LnwCombatStats{
			RedWinPercent:  b.Stats.RedWinPercent,
			BlueWinPercent: b.Stats.BlueWinPercent,
			RedFights:      b.Stats.RedFights,
			BlueFights:     b.Stats.BlueFights,
			RedWins:        b.Stats.RedWins,
			BlueWins:       b.Stats.BlueWins,
			RedWeight:      b.Stats.RedWeight,
			BlueWeight:     b.Stats.BlueWeight,
			WeightUnit:     b.Stats.WeightUnit,
			RedForm:        b.Stats.RedForm,
			BlueForm:       b.Stats.BlueForm,
			Opinion:        b.Stats.Opinion,
		},
		// Only the fact of a stream is kept. The URL embeds an stk= token
		// that base64-decodes to the account's own API key, so storing one
		// would write the key into the database.
		HasStream: b.HasStream(),
		FetchedAt: fetchedAt.UTC(),
		UpdatedAt: fetchedAt.UTC(),
	}, true
}
