package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/flip1688/livescore/internal/lotto"
	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
)

// LottoSyncer pulls lottery results from lotto2news into MongoDB.
//
// Two paths feed the same collections:
//
//   - webhook (Starter plan and up): upstream POSTs result.published /
//     result.updated the moment a draw lands. This is the low-latency path.
//   - a reconcile sweep: re-reads each active market's history on a slow
//     cadence. This is not redundancy for its own sake — upstream revises
//     already-published draws, and a webhook delivery that fails is not
//     retried, so without the sweep a missed POST is a permanently wrong
//     page.
//
// Both paths converge on applyDraws, so a draw arriving twice is harmless.
type LottoSyncer struct {
	client *lotto.Client
	store  *store.Store
	log    *slog.Logger

	// staleAfter decides LottoMarket.Active: a market with no draw newer
	// than this is hidden from the API. Upstream lists 14 markets that have
	// never produced a draw and one (หุ้นไทยเที่ยง) dead since June 2026,
	// with no flag of its own to go by.
	staleAfter time.Duration

	// historyLimit is how many draws the sweep re-reads per market.
	historyLimit int
}

func NewLottoSyncer(c *lotto.Client, st *store.Store, log *slog.Logger, staleAfter time.Duration, historyLimit int) *LottoSyncer {
	if staleAfter <= 0 {
		staleAfter = 30 * 24 * time.Hour
	}
	if historyLimit <= 0 || historyLimit > lotto.MaxHistoryLimit {
		historyLimit = lotto.MaxHistoryLimit
	}
	return &LottoSyncer{client: c, store: st, log: log, staleAfter: staleAfter, historyLimit: historyLimit}
}

// Run starts the lotto loops and blocks until ctx is canceled.
//
// Cadences are deliberately slow. The webhook carries the live path, so the
// sweep only has to catch missed deliveries and upstream revisions; at 162
// markets a full sweep costs 162 calls, and the Starter plan allows 5,000
// per day. Six-hourly leaves the budget ~85% unused for everything else.
func (s *LottoSyncer) Run(ctx context.Context) {
	// SyncResults iterates the markets already in the store, so on a cold
	// start it has nothing to iterate until SyncMarkets has written them.
	// Starting both loops together left the first results pass reporting
	// "markets: 0" and the first draws six hours late; seed the list first.
	if err := s.SyncMarkets(ctx); err != nil && ctx.Err() == nil {
		s.log.Error("lotto sync failed", "job", "lotto-markets", "err", err)
	}
	go s.loopAfter(ctx, "lotto-markets", 24*time.Hour, s.SyncMarkets)
	go s.loop(ctx, "lotto-results", 6*time.Hour, s.SyncResults)
	<-ctx.Done()
}

// loopAfter is loop without the leading immediate run — for a job that has
// already been run once by the caller.
func (s *LottoSyncer) loopAfter(ctx context.Context, name string, every time.Duration, fn func(context.Context) error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("lotto sync failed", "job", name, "err", err)
			}
		}
	}
}

func (s *LottoSyncer) loop(ctx context.Context, name string, every time.Duration, fn func(context.Context) error) {
	run := func() {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("lotto sync failed", "job", name, "err", err)
		}
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// SyncMarkets refreshes the market list. It writes descriptive fields only —
// active/latest_draw_at belong to SyncResults.
func (s *LottoSyncer) SyncMarkets(ctx context.Context) error {
	markets, err := s.client.FetchMarkets(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	docs := make([]model.LottoMarket, 0, len(markets))
	for _, m := range markets {
		if !supportedCategory(m.Category) {
			// Upstream has only ever returned the four known categories; a
			// new one means the UI has no page for it, so skip rather than
			// store something nothing can render.
			s.log.Warn("lotto: unknown category, skipping market", "slug", m.Slug, "category", m.Category)
			continue
		}
		docs = append(docs, m.ToModel(now))
	}
	if err := s.store.UpsertLottoMarkets(ctx, docs); err != nil {
		return err
	}
	s.log.Info("lotto markets synced", "count", len(docs), "skipped", len(markets)-len(docs))
	return nil
}

// SyncResults re-reads every market's recent history and reconciles it.
func (s *LottoSyncer) SyncResults(ctx context.Context) error {
	markets, err := s.store.ListLottoMarkets(ctx, "", false)
	if err != nil {
		return err
	}
	var firstErr error
	stored, failed := 0, 0
	for _, m := range markets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := s.syncMarketResults(ctx, m.Slug)
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			// One bad market must not abort the sweep: a single dead slug
			// would otherwise stop every market after it, alphabetically.
			s.log.Warn("lotto: market sync failed", "slug", m.Slug, "err", err)
			continue
		}
		stored += n
	}
	s.log.Info("lotto results synced", "markets", len(markets), "draws", stored, "failed", failed)
	return firstErr
}

func (s *LottoSyncer) syncMarketResults(ctx context.Context, slug string) (int, error) {
	hist, err := s.client.FetchHistory(ctx, slug, s.historyLimit)
	if err != nil {
		return 0, err
	}
	return s.applyDraws(ctx, hist.Market, hist.Results)
}

// applyDraws normalizes, deduplicates and stores a batch of draws for one
// market, then restamps the market's freshness. Shared by the sweep and the
// webhook so both arrive at the same state.
func (s *LottoSyncer) applyDraws(ctx context.Context, m lotto.Market, raw []lotto.Draw) (int, error) {
	if m.Slug == "" {
		return 0, errors.New("lotto: draw batch without a market slug")
	}
	now := time.Now().UTC()
	if len(raw) == 0 {
		// Upstream lists 14 markets it has never fed a single draw. Stamping
		// them inactive here rather than returning early is what keeps every
		// market carrying an explicit active flag — otherwise those 14 have
		// no flag at all, and "inactive" and "never seen" become
		// indistinguishable in the data.
		return 0, s.store.SetLottoMarketFreshness(ctx, m.Slug, time.Time{}, false)
	}
	draws := make([]model.LottoDraw, 0, len(raw))
	incomplete := 0
	for _, d := range raw {
		doc := d.ToModel(m, now)
		if !doc.Complete {
			incomplete++
		}
		draws = append(draws, doc)
	}

	// Decide superseded over the whole batch rather than per draw: the
	// duplicate and its replacement only ever appear together.
	model.MarkSuperseded(draws)

	if err := s.store.UpsertLottoDraws(ctx, draws); err != nil {
		return 0, err
	}
	if incomplete > 0 {
		s.log.Warn("lotto: draws missing prize rows upstream",
			"slug", m.Slug, "incomplete", incomplete, "of", len(draws))
	}

	latest := time.Time{}
	for _, d := range draws {
		if d.DrawAt.After(latest) {
			latest = d.DrawAt
		}
	}
	active := !latest.IsZero() && latest.After(now.Add(-s.staleAfter))
	if err := s.store.SetLottoMarketFreshness(ctx, m.Slug, latest, active); err != nil {
		return len(draws), err
	}
	return len(draws), nil
}

// ApplyWebhook stores the draw carried by a result.published /
// result.updated event.
//
// The event carries only the one latest draw, so superseded can be decided
// for the market's day only after a re-read; the sweep settles it. Storing
// immediately is still right: a visitor refreshing the page the minute a
// result drops matters more than a duplicate that resolves within hours.
func (s *LottoSyncer) ApplyWebhook(ctx context.Context, ev lotto.WebhookEvent) error {
	if ev.Data.Latest == nil {
		return errors.New("lotto: webhook event without a draw")
	}
	if !supportedCategory(ev.Data.Category) {
		s.log.Warn("lotto: webhook for unknown category", "slug", ev.Data.Slug, "category", ev.Data.Category)
		return nil
	}
	_, err := s.applyDraws(ctx, ev.Data, []lotto.Draw{*ev.Data.Latest})
	return err
}

func supportedCategory(c string) bool {
	switch c {
	case model.LottoCatGov, model.LottoCatLao, model.LottoCatHanoi, model.LottoCatStock:
		return true
	}
	return false
}
