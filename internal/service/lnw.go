package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/flip1688/livescore/internal/lnw"
	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
)

// LnwSyncer pulls the lnwthailive feeds into their own lnw_* collections.
//
// This subsystem exists to answer one question: does this vendor fit the
// site better than thscore? It therefore runs alongside the thscore sync
// and never writes to its collections, so either feed can be switched off
// without touching the other and the comparison stays honest.
//
// It is off unless LNW_API_KEY is set — the same switch the lotto
// subsystem uses.
type LnwSyncer struct {
	client *lnw.Client
	store  *store.Store
	log    *slog.Logger

	// sports is which feeds to pull from /api/public/matches. Football is
	// the comparison that matters; the others are the vendor's differentiator
	// and are only worth polling once we know a key is allowed to read them.
	sports []string
}

func NewLnwSyncer(c *lnw.Client, st *store.Store, log *slog.Logger, sports []string) *LnwSyncer {
	if len(sports) == 0 {
		sports = []string{model.LnwSportFootball}
	}
	return &LnwSyncer{client: c, store: st, log: log, sports: sports}
}

// Run starts the evaluation loops and blocks until ctx is canceled.
//
// Cadences are set against the smallest rate limit the vendor quotes
// (10/min on the demo tier) rather than the largest, because its own pages
// disagree: matches 60s + results 120s + standings 10m + analysis 15m is
// about 2 calls a minute for one sport. Once /api/portal/verify reports the
// real limit for a purchased key these can come down — the fixture feed is
// the one worth spending budget on, since it carries the live score.
func (s *LnwSyncer) Run(ctx context.Context) {
	// Log what the key actually grants before spending any budget on data.
	// The vendor's plan pages contradict each other, so this is the only
	// trustworthy statement of tier, expiry and rate limit.
	if info, err := s.client.VerifyKey(ctx); err != nil {
		s.log.Error("lnw key verify failed", "err", err)
	} else {
		s.log.Info("lnw key verified",
			"tier", info.Tier, "status", info.Status,
			"expires_at", info.ExpiresAt, "days_left", info.DaysLeft,
			"rate_limit", info.RateLimit)
	}

	go s.loop(ctx, "lnw-matches", time.Minute, s.SyncMatches)
	go s.loop(ctx, "lnw-results", 2*time.Minute, s.SyncResults)
	go s.loop(ctx, "lnw-standings", 10*time.Minute, s.SyncStandings)
	go s.loop(ctx, "lnw-analysis", 15*time.Minute, s.SyncAnalysis)
	// Coverage reads only what the loops above have already stored, so it
	// costs no upstream calls and can run often enough to be useful.
	go s.loop(ctx, "lnw-coverage", time.Hour, s.SyncCoverage)
	<-ctx.Done()
}

func (s *LnwSyncer) loop(ctx context.Context, name string, every time.Duration, fn func(context.Context) error) {
	run := func() {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("lnw sync failed", "job", name, "err", err)
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

// RunOnce runs a single job by name, for `cmd/api -once`.
func (s *LnwSyncer) RunOnce(ctx context.Context, job string) error {
	switch job {
	case "lnw-matches":
		return s.SyncMatches(ctx)
	case "lnw-results":
		return s.SyncResults(ctx)
	case "lnw-standings":
		return s.SyncStandings(ctx)
	case "lnw-analysis":
		return s.SyncAnalysis(ctx)
	case "lnw-coverage":
		return s.SyncCoverage(ctx)
	case "lnw-verify":
		info, err := s.client.VerifyKey(ctx)
		if err != nil {
			return err
		}
		s.log.Info("lnw key verified",
			"tier", info.Tier, "status", info.Status,
			"expires_at", info.ExpiresAt, "days_left", info.DaysLeft,
			"rate_limit", info.RateLimit, "allow", info.Allow)
		return nil
	}
	return nil
}

// SyncMatches refreshes today's and tomorrow's fixtures for every
// configured sport.
//
// The fetch clock is captured once and passed down, because the rows carry
// no date of their own: resolving them against time.Now() further down the
// call stack would let a slow response move a match to the wrong day.
func (s *LnwSyncer) SyncMatches(ctx context.Context) error {
	for _, sport := range s.sports {
		fetchedAt := time.Now()
		resp, err := s.client.FetchMatches(ctx, sport)
		if err != nil {
			return err
		}
		docs := make([]model.LnwMatch, 0, len(resp.Data))
		var skipped int
		for _, m := range resp.Data {
			doc, ok := mapLnwMatch(m, fetchedAt)
			if !ok {
				skipped++
				continue
			}
			docs = append(docs, doc)
		}
		if err := s.store.UpsertLnwMatches(ctx, docs); err != nil {
			return err
		}
		s.log.Info("lnw matches synced",
			"sport", sport, "stored", len(docs), "skipped", skipped, "tier", resp.Tier)
	}
	return nil
}

// SyncResults refreshes the result feed for today and yesterday.
//
// Yesterday is included because our display day runs 04:00→04:00 ICT: a
// match kicking off at 01:00 Thai time belongs to the previous day's
// programme, and upstream files it under the calendar date instead.
func (s *LnwSyncer) SyncResults(ctx context.Context) error {
	now := time.Now().In(Bangkok)
	days := []string{
		now.Format("2006-01-02"),
		now.AddDate(0, 0, -1).Format("2006-01-02"),
	}
	for _, day := range days {
		resp, err := s.client.FetchResults(ctx, day)
		if err != nil {
			return err
		}
		var docs []model.LnwResult
		var skipped int
		for _, league := range resp.Leagues {
			for _, m := range league.Matches {
				doc, ok := mapLnwResult(league, m)
				if !ok {
					skipped++
					continue
				}
				docs = append(docs, doc)
			}
		}
		if err := s.store.UpsertLnwResults(ctx, docs); err != nil {
			return err
		}
		s.log.Info("lnw results synced",
			"day", day, "stored", len(docs), "skipped", skipped,
			"leagues", resp.LeagueCount)
	}
	return nil
}

// SyncStandings refreshes every league table.
//
// Upstream's own isStale/refreshing flags are carried into storage rather
// than dropped: it serves a stale table while it rebuilds, and a table that
// is quietly old is worse than one known to be old.
func (s *LnwSyncer) SyncStandings(ctx context.Context) error {
	resp, err := s.client.FetchStandings(ctx)
	if err != nil {
		return err
	}
	upstreamFetched := time.UnixMilli(resp.FetchedAt).UTC()
	docs := make([]model.LnwStanding, 0, len(resp.Data))
	var skipped int
	for _, t := range resp.Data {
		doc, ok := mapLnwStanding(t, upstreamFetched, resp.IsStale)
		if !ok {
			skipped++
			continue
		}
		docs = append(docs, doc)
	}
	if err := s.store.UpsertLnwStandings(ctx, docs); err != nil {
		return err
	}
	s.log.Info("lnw standings synced",
		"stored", len(docs), "skipped", skipped, "stale", resp.IsStale)
	return nil
}

// SyncAnalysis refreshes the pre-match analysis index for today and
// tomorrow. Only the index: the per-match detail call fans out to 14
// upstream requests and is fetched on demand instead.
func (s *LnwSyncer) SyncAnalysis(ctx context.Context) error {
	for _, typ := range []string{"today", "tomorrow"} {
		resp, err := s.client.FetchAnalyseList(ctx, typ, 1, 50)
		if err != nil {
			return err
		}
		docs := make([]model.LnwAnalysis, 0, len(resp.Data.Matches))
		var skipped int
		for _, a := range resp.Data.Matches {
			doc, ok := mapLnwAnalysis(a)
			if !ok {
				skipped++
				continue
			}
			docs = append(docs, doc)
		}
		if err := s.store.UpsertLnwAnalysis(ctx, docs); err != nil {
			return err
		}
		s.log.Info("lnw analysis synced",
			"type", typ, "stored", len(docs), "skipped", skipped,
			"total", resp.Data.Total)
	}
	return nil
}
