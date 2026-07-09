package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/flip1688/livescore/internal/cache"
	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
	"github.com/flip1688/livescore/internal/thscore"
)

// Publisher is the realtime fan-out seam (in-process WS hub today; swap for
// Redis Pub/Sub if the API ever scales past one instance).
type Publisher interface {
	Publish(channel, msgType string, data any)
}

// NoopPublisher is used when realtime push is disabled.
type NoopPublisher struct{}

func (NoopPublisher) Publish(string, string, any) {}

// liveMatchKey is the Redis key holding the latest model.Match JSON for one
// in-play match — the WS snapshot source and the base state deltas merge onto.
func liveMatchKey(matchID string) string { return "live:match:" + matchID }

// liveStateTTL must outlive the snapshot refresh interval so state survives
// between hydrations but doesn't linger after the match day ends.
const liveStateTTL = 15 * time.Minute

// Syncer owns every thscore call in the system. Cadences follow
// docs/widgets-repo-analysis.md; the client's per-endpoint rate limiters are
// the hard backstop.
type Syncer struct {
	ts    *thscore.Client
	store *store.Store
	cache *cache.Cache
	pub   Publisher
	logos *LogoMirror
	log   *slog.Logger
}

// logos == nil disables logo mirroring: syncDictionaries falls back to
// passing thscore's own logo URLs through untouched (dev mode, no R2
// credentials configured).
func NewSyncer(ts *thscore.Client, st *store.Store, c *cache.Cache, pub Publisher, logos *LogoMirror, log *slog.Logger) *Syncer {
	if pub == nil {
		pub = NoopPublisher{}
	}
	return &Syncer{ts: ts, store: st, cache: c, pub: pub, logos: logos, log: log}
}

// Run starts all sync loops and blocks until ctx is canceled.
//
// live-changes runs every 15s: the delta feed covers the last 20s, so any
// interval under 20s is gap-free; the 1-minute snapshot heals anything missed.
func (s *Syncer) Run(ctx context.Context) {
	go s.loop(ctx, "dictionary", 24*time.Hour, s.syncDictionariesAndLogos)
	go s.loop(ctx, "schedule", 1*time.Hour, s.syncSchedule)
	go s.loop(ctx, "schedule-ahead", 24*time.Hour, s.syncScheduleAhead)
	go s.loop(ctx, "schedule-modify", 30*time.Minute, s.syncScheduleModifications)
	go s.loop(ctx, "live-snapshot", 1*time.Minute, s.syncLiveSnapshot)
	go s.loop(ctx, "live-changes", 15*time.Second, s.syncLiveChanges)
	go s.loop(ctx, "events-stats", 1*time.Minute, s.syncEventsStats)
	go s.loop(ctx, "standings", 6*time.Hour, s.syncStandings)
	go s.loop(ctx, "analysis", 30*time.Minute, s.syncAnalysis)
	<-ctx.Done()
}

// RunOnce runs a single named sync job to completion and returns its error —
// used by `cmd/api -once <job>` for manual backfills (e.g. the first
// dictionary load) without starting the server or the other loops.
func (s *Syncer) RunOnce(ctx context.Context, job string) error {
	jobs := map[string]func(context.Context) error{
		"dictionary":      s.syncDictionaries,
		"logos":           s.syncLogos,
		"schedule":        s.syncSchedule,
		"schedule-ahead":  s.syncScheduleAhead,
		"schedule-modify": s.syncScheduleModifications,
		"live-snapshot":   s.syncLiveSnapshot,
		"live-changes":    s.syncLiveChanges,
		"events-stats":    s.syncEventsStats,
		"standings":       s.syncStandings,
		"analysis":        s.syncAnalysis,
	}
	fn, ok := jobs[job]
	if !ok {
		return fmt.Errorf("unknown sync job %q", job)
	}
	return fn(ctx)
}

// loop runs fn immediately, then on every tick, until ctx is canceled.
func (s *Syncer) loop(ctx context.Context, name string, every time.Duration, fn func(context.Context) error) {
	run := func() {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("sync failed", "job", name, "err", err)
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

// syncDictionaries refreshes leagues/teams/countries into MongoDB and
// invalidates their cache keys. Runs daily; thscore recommends 1 call/day.
// Logo mirroring happens afterwards in syncLogos so a slow or failing logo
// backfill never delays or fails the dictionary data itself.
func (s *Syncer) syncDictionaries(ctx context.Context) error {
	now := time.Now().UTC()

	profiles, err := s.ts.FetchLeagues(ctx, 0)
	if err != nil {
		return err
	}
	leagues := make([]model.League, 0, len(profiles))
	for _, p := range profiles {
		leagues = append(leagues, leagueFromProfile(p, now))
	}
	if err := s.store.UpsertLeagues(ctx, leagues); err != nil {
		return err
	}

	teamProfiles, err := s.ts.FetchTeams(ctx, 0, 0)
	if err != nil {
		return err
	}
	teams := make([]model.Team, 0, len(teamProfiles))
	for _, p := range teamProfiles {
		teams = append(teams, teamFromProfile(p, now))
	}
	if err := s.store.UpsertTeams(ctx, teams); err != nil {
		return err
	}

	// country.aspx is not included in our current thscore plan (API code 2,
	// confirmed 2026-07-08) — don't fail leagues/teams over it.
	var countries []model.Country
	if payload, err := s.ts.FetchCountries(ctx); err != nil {
		s.log.Warn("skip country sync", "err", err)
	} else {
		countries = make([]model.Country, 0, len(payload))
		for _, c := range payload {
			countries = append(countries, countryFromPayload(c, now))
		}
		if err := s.store.UpsertCountries(ctx, countries); err != nil {
			return err
		}
	}

	if err := s.cache.Delete(ctx, "leagues"); err != nil {
		s.log.Warn("evict dictionary cache", "err", err)
	}
	s.log.Info("dictionaries synced", "leagues", len(leagues), "teams", len(teams), "countries", len(countries))
	return nil
}

// syncDictionariesAndLogos is the scheduled daily job: dictionary first, then
// the logo backfill on the freshly committed docs. They stay separate jobs in
// RunOnce so ops can load the dictionary quickly here and run the (long) logo
// backfill elsewhere via cmd/logo-sync.
func (s *Syncer) syncDictionariesAndLogos(ctx context.Context) error {
	if err := s.syncDictionaries(ctx); err != nil {
		return err
	}
	return s.syncLogos(ctx)
}

// syncLogos runs SyncLogos and evicts the dictionary cache when anything
// changed. Runs after every dictionary sync and manually via `-once logos`;
// cmd/logo-sync calls SyncLogos directly (no Redis there).
func (s *Syncer) syncLogos(ctx context.Context) error {
	if s.logos == nil {
		s.log.Warn("logo sync skipped — R2 not configured")
		return nil
	}
	done, err := SyncLogos(ctx, s.store, s.logos, s.log)
	if err != nil {
		return err
	}
	if done > 0 {
		if err := s.cache.Delete(ctx, "leagues"); err != nil {
			s.log.Warn("evict dictionary cache", "err", err)
		}
	}
	return nil
}

// SyncLogos mirrors league/team logos into R2 and stamps logo_url on the
// dictionary docs, returning how many docs were stamped. Pending docs are
// detected by comparing the stored logo_url against the deterministic
// ExpectedURL, so the job is idempotent and self-heals earlier failures,
// changed sources, and public-base-URL migrations. It only needs Mongo and
// R2 — no Redis, no thscore — so cmd/logo-sync can run it standalone.
func SyncLogos(ctx context.Context, st *store.Store, logos *LogoMirror, log *slog.Logger) (int, error) {
	leagues, err := st.ListLeagues(ctx)
	if err != nil {
		return 0, err
	}
	pendingLeagues := map[string]string{}
	for _, l := range leagues {
		if l.LogoSourceURL != "" && l.LogoURL != logos.ExpectedURL("leagues", l.ID, l.LogoSourceURL) {
			pendingLeagues[l.ID] = l.LogoSourceURL
		}
	}
	doneLeagues, err := mirrorAndStamp(ctx, logos, "leagues", pendingLeagues, st.SetLeagueLogoURLs)
	if err != nil {
		return 0, err
	}

	teams, err := st.ListTeams(ctx, "")
	if err != nil {
		return doneLeagues, err
	}
	pendingTeams := map[string]string{}
	for _, t := range teams {
		if t.LogoSourceURL != "" && t.LogoURL != logos.ExpectedURL("teams", t.ID, t.LogoSourceURL) {
			pendingTeams[t.ID] = t.LogoSourceURL
		}
	}
	doneTeams, err := mirrorAndStamp(ctx, logos, "teams", pendingTeams, st.SetTeamLogoURLs)
	if err != nil {
		return doneLeagues, err
	}

	log.Info("logos synced",
		"league_pending", len(pendingLeagues), "league_done", doneLeagues,
		"team_pending", len(pendingTeams), "team_done", doneTeams)
	return doneLeagues + doneTeams, nil
}

// mirrorAndStamp mirrors the pending id→source map and persists the mirrored
// URLs via save. Per-item failures are already logged by MirrorAll and simply
// stay pending for the next run.
func mirrorAndStamp(ctx context.Context, logos *LogoMirror, kind string, pending map[string]string, save func(context.Context, map[string]string) error) (int, error) {
	if len(pending) == 0 {
		return 0, nil
	}
	urls := make(map[string]string, len(pending))
	for id, u := range logos.MirrorAll(ctx, kind, pending) {
		if u != "" {
			urls[id] = u
		}
	}
	if len(urls) == 0 {
		return 0, ctx.Err()
	}
	if err := save(ctx, urls); err != nil {
		return 0, err
	}
	return len(urls), nil
}

// scheduleAheadDays is how far forward the daily schedule-ahead loop keeps
// the fixture list populated (the "โปรแกรมล่วงหน้า" pages).
const scheduleAheadDays = 7

// syncSchedule refreshes near-term fixtures hourly. One of our matchdates
// (04:00→04:00 ICT) spans two thscore calendar dates (thscore's date param is
// GMT+7, midnight-based), so we always fetch today and tomorrow.
func (s *Syncer) syncSchedule(ctx context.Context) error {
	return s.syncScheduleDays(ctx, 0, 1)
}

// syncScheduleAhead refreshes the forward window (day +2 .. +scheduleAheadDays)
// once a day — every date in the window is re-fetched daily, so fixture data
// is never older than 24h. The client's 60s-per-call schedule limiter spaces
// the calls out automatically.
func (s *Syncer) syncScheduleAhead(ctx context.Context) error {
	return s.syncScheduleDays(ctx, 2, scheduleAheadDays)
}

// syncScheduleDays fetches and upserts fixtures for thscore dates fromDay
// through toDay (offsets from today, GMT+7).
func (s *Syncer) syncScheduleDays(ctx context.Context, fromDay, toDay int) error {
	now := time.Now()
	bkkNow := now.In(Bangkok)
	for day := fromDay; day <= toDay; day++ {
		d := bkkNow.AddDate(0, 0, day).Format("2006-01-02")
		rows, err := s.ts.FetchScheduleByDate(ctx, d)
		if err != nil {
			return err
		}
		if err := s.upsertScheduleRows(ctx, rows, now.UTC()); err != nil {
			return err
		}
		s.log.Info("schedule synced", "thscore_date", d, "matches", len(rows))
	}
	return nil
}

// upsertScheduleRows maps payload rows to matches, upserts them, and evicts
// the cache of every matchdate the rows touched.
func (s *Syncer) upsertScheduleRows(ctx context.Context, rows []thscore.LivescoreMatch, now time.Time) error {
	matches := make([]model.Match, 0, len(rows))
	dates := map[string]struct{}{}
	for _, row := range rows {
		m, err := matchFromLivescore(row, now)
		if err != nil {
			s.log.Warn("skip match with bad kickoff", "match_id", row.MatchID, "match_time", row.MatchTime, "err", err)
			continue
		}
		matches = append(matches, m)
		dates[m.MatchDate] = struct{}{}
	}
	if err := s.store.UpsertMatches(ctx, matches); err != nil {
		return err
	}
	s.evictMatchdates(ctx, dates)
	return nil
}

// syncScheduleModifications applies deletions/reschedules from the past 12h:
// deletes drop the match doc; modifies re-fetch the match (the feed only says
// *when* it changed, not the new kick-off).
func (s *Syncer) syncScheduleModifications(ctx context.Context) error {
	mods, err := s.ts.FetchScheduleModifications(ctx)
	if err != nil {
		return err
	}

	var refetch []string
	dates := map[string]struct{}{}
	for _, mod := range mods {
		id := string(mod.MatchID)
		switch mod.Type {
		case "delete":
			if old, err := s.store.GetMatch(ctx, id); err == nil {
				dates[old.MatchDate] = struct{}{}
			}
			if err := s.store.DeleteMatch(ctx, id); err != nil {
				return err
			}
		case "modify":
			if old, err := s.store.GetMatch(ctx, id); err == nil {
				dates[old.MatchDate] = struct{}{} // old bucket may change
			}
			refetch = append(refetch, id)
		}
	}

	// Re-fetch rescheduled matches in batches of 50 (upstream cap).
	for start := 0; start < len(refetch); start += 50 {
		batch := refetch[start:min(start+50, len(refetch))]
		rows, err := s.ts.FetchScheduleByMatchIDs(ctx, batch)
		if err != nil {
			return err
		}
		if err := s.upsertScheduleRows(ctx, rows, time.Now().UTC()); err != nil {
			return err
		}
	}

	s.evictMatchdates(ctx, dates)
	if len(mods) > 0 {
		s.log.Info("schedule modifications applied", "total", len(mods), "refetched", len(refetch))
	}
	return nil
}

// syncLiveSnapshot re-hydrates the full live state every minute: per-match
// Redis keys (WS snapshot source + delta base) and a Mongo upsert so the
// daily match list carries current scores. It also heals anything the
// 15-second delta poll missed — including halfStartTime, which the delta feed
// only sends on change but the minute computation always needs.
func (s *Syncer) syncLiveSnapshot(ctx context.Context) error {
	rows, err := s.ts.FetchLivescores(ctx)
	if err != nil {
		return err
	}
	now := time.Now()

	matches := make([]model.Match, 0, len(rows))
	for _, row := range rows {
		m, err := matchFromLivescore(row, now.UTC())
		if err != nil {
			s.log.Warn("skip live match with bad kickoff", "match_id", row.MatchID, "err", err)
			continue
		}
		if minute := model.ComputeLiveMinute(m.StatusCode, m.HalfStartAt, now); minute > 0 {
			m.Minute = minute
		}
		matches = append(matches, m)
		if err := s.cache.SetJSON(ctx, liveMatchKey(m.ID), m, liveStateTTL); err != nil {
			s.log.Warn("write live state", "match_id", m.ID, "err", err)
		}
	}
	if err := s.store.UpsertMatches(ctx, matches); err != nil {
		return err
	}
	s.log.Info("live snapshot hydrated", "matches", len(matches))
	return nil
}

// syncLiveChanges merges the 20-second delta feed onto the Redis live state
// and pushes the updated matches out over the realtime channels.
func (s *Syncer) syncLiveChanges(ctx context.Context) error {
	changes, err := s.ts.FetchLiveChanges(ctx)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	now := time.Now()

	updated := make([]model.Match, 0, len(changes))
	for _, ch := range changes {
		id := strconv.Itoa(ch.MatchID)

		// The delta has no league/team fields — base it on the snapshot
		// state, falling back to Mongo when Redis is cold.
		var prev model.Match
		if err := s.cache.GetJSON(ctx, liveMatchKey(id), &prev); err != nil {
			if !errors.Is(err, cache.ErrMiss) {
				s.log.Warn("read live state", "match_id", id, "err", err)
			}
			if prev, err = s.store.GetMatch(ctx, id); err != nil {
				if !errors.Is(err, mongo.ErrNoDocuments) {
					return err
				}
				// Unknown match (not in schedule yet) — the next snapshot
				// hydration will pick it up with full context.
				continue
			}
		}

		m := applyChange(prev, ch, now)
		if err := s.cache.SetJSON(ctx, liveMatchKey(id), m, liveStateTTL); err != nil {
			s.log.Warn("write live state", "match_id", id, "err", err)
		}
		updated = append(updated, m)

		s.pub.Publish("live", "live", m)
		s.pub.Publish("match:"+id, "live", m)
		s.pub.Publish("matchlist:"+m.MatchDate, "live", m)
	}

	if err := s.store.UpsertMatches(ctx, updated); err != nil {
		return err
	}
	s.log.Info("live changes applied", "matches", len(updated))
	return nil
}

// syncEventsStats pulls the event timeline and technical stats deltas and
// pushes them to the per-match channels.
func (s *Syncer) syncEventsStats(ctx context.Context) error {
	events, err := s.ts.FetchRecentEvents(ctx)
	if err != nil {
		return err
	}
	for _, em := range events {
		id := strconv.Itoa(em.MatchID)
		if err := s.store.ReplaceMatchEvents(ctx, id, em.Events); err != nil {
			return err
		}
		if err := s.cache.Delete(ctx, "events:"+id); err != nil {
			s.log.Warn("evict events cache", "match_id", id, "err", err)
		}
		s.pub.Publish("match:"+id, "events", em)
	}

	stats, err := s.ts.FetchLiveStats(ctx, "")
	if err != nil {
		return err
	}
	for _, sm := range stats {
		id := strconv.Itoa(sm.MatchID)
		if err := s.store.ReplaceMatchStats(ctx, id, sm.Stats); err != nil {
			return err
		}
		if err := s.cache.Delete(ctx, "stats:"+id); err != nil {
			s.log.Warn("evict stats cache", "match_id", id, "err", err)
		}
		s.pub.Publish("match:"+id, "stats", sm)
	}

	if len(events) > 0 || len(stats) > 0 {
		s.log.Info("events/stats synced", "event_matches", len(events), "stat_matches", len(stats))
	}
	return nil
}

// standingsSyncDates returns yesterday/today/tomorrow's matchdates (04:00 ICT
// cutoff) — the window syncStandings scopes its league selection to, since a
// matchdate can carry late-kickoff fixtures that belong to the neighboring
// calendar day.
func standingsSyncDates() []string {
	today, err := time.ParseInLocation("2006-01-02", CurrentMatchDate(time.Now()), Bangkok)
	if err != nil { // unreachable: CurrentMatchDate always emits 2006-01-02
		today = time.Now().In(Bangkok)
	}
	return []string{
		today.AddDate(0, 0, -1).Format("2006-01-02"),
		today.Format("2006-01-02"),
		today.AddDate(0, 0, 1).Format("2006-01-02"),
	}
}

// syncStandings refreshes the league table for every league with a fixture
// yesterday/today/tomorrow. Runs every 6h; thscore recommends 1 call/day per
// league (hard limit 5s/call), so this cadence stays well inside both.
// Per-league failures are logged and skipped so one bad league never blocks
// the rest of the run.
func (s *Syncer) syncStandings(ctx context.Context) error {
	leagueIDs, err := s.store.DistinctLeagueIDsForMatchDates(ctx, standingsSyncDates())
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	synced, noData, failed := 0, 0, 0
	for _, leagueID := range leagueIDs {
		if leagueID == "" {
			continue
		}
		if s.noDataMarked(ctx, "standings:nodata:"+leagueID) {
			noData++
			continue
		}
		resp, err := s.ts.FetchLeagueStanding(ctx, leagueID)
		if err != nil {
			var apiErr *thscore.APIError
			if errors.As(err, &apiErr) && apiErr.Code == thscore.CodeNoData {
				// Knockout-only competitions and friendlies have no table —
				// expected, not a failure. Remember it so recurring runs skip
				// the call instead of burning a rate-limiter slot re-asking.
				noData++
				s.markNoData(ctx, "standings:nodata:"+leagueID, standingsNoDataTTL)
				continue
			}
			s.log.Warn("sync standing failed", "league_id", leagueID, "err", err)
			failed++
			continue
		}
		st := standingFromResponse(resp, now)
		if st.ID == "" {
			st.ID = leagueID // keep our key stable if upstream ever omits leagueInfo.leagueId
		}
		if err := s.store.UpsertStanding(ctx, st); err != nil {
			s.log.Warn("upsert standing failed", "league_id", leagueID, "err", err)
			continue
		}
		if err := s.cache.Delete(ctx, "standings:"+leagueID); err != nil {
			s.log.Warn("evict standings cache", "league_id", leagueID, "err", err)
		}
		synced++
	}
	s.log.Info("standings synced", "leagues", len(leagueIDs), "synced", synced, "no_data", noData, "failed", failed)
	return nil
}

// No-data markers: thscore answers code 1 ("No Data.") for ids that simply
// have nothing — remember that in Redis so recurring sync runs skip the call.
// Markers are cache-only: a Redis flush just means re-checking once.
//
// Standings: a competition without a table won't grow one mid-season → 24h.
// Analysis: thscore may generate analysis closer to kickoff and the whole
// prefetch window is only 24h → retry sooner (6h).
const (
	standingsNoDataTTL = 24 * time.Hour
	analysisNoDataTTL  = 6 * time.Hour
)

func (s *Syncer) noDataMarked(ctx context.Context, key string) bool {
	var marked bool
	err := s.cache.GetJSON(ctx, key, &marked)
	return err == nil && marked
}

func (s *Syncer) markNoData(ctx context.Context, key string, ttl time.Duration) {
	if err := s.cache.SetJSON(ctx, key, true, ttl); err != nil {
		s.log.Warn("mark no-data", "key", key, "err", err)
	}
}

// analysisLookahead is how far ahead of now the analysis sync job looks for
// upcoming scheduled matches to pre-fetch H2H/form/odds data for. A user
// request must never trigger a thscore call (see docs/architecture.md), so
// analysis has to land in Mongo well before kickoff.
const analysisLookahead = 24 * time.Hour

// analysisRefetchWindow mirrors thscore's own analysis.aspx cache (24h
// upstream, per docs/thscore-api.md docs_id=109) — refetching a match sooner
// than this just burns quota for a payload that hasn't changed.
const analysisRefetchWindow = 24 * time.Hour

// analysisSyncDates returns the matchdates ("2006-01-02", 04:00 ICT cutoff)
// spanned by [now, now+analysisLookahead) — at most two consecutive
// matchdate buckets, since the lookahead window and the bucket size are both
// 24h. Deduplicated so a single-bucket window (e.g. lookahead landing before
// the next 04:00 cutoff) yields one date, not a repeated one.
func analysisSyncDates(now time.Time) []string {
	from := CurrentMatchDate(now)
	to := CurrentMatchDate(now.Add(analysisLookahead))
	if from == to {
		return []string{from}
	}
	return []string{from, to}
}

// analysisSkip reports whether a match's analysis was fetched recently
// enough (within analysisRefetchWindow) to skip refetching it now. Pulled
// out as pure logic so it's unit-testable without a store/clock fake.
func analysisSkip(now, fetchedAt time.Time) bool {
	return !fetchedAt.IsZero() && now.Sub(fetchedAt) < analysisRefetchWindow
}

// syncAnalysis pre-fetches H2H/form/odds/goal-distribution analysis for
// matches kicking off in the next 24h, skipping any match whose analysis was
// already fetched within thscore's own 24h upstream cache window. Runs every
// 30 minutes; per-match failures are logged and skipped so one bad match
// never blocks the rest of the run.
func (s *Syncer) syncAnalysis(ctx context.Context) error {
	now := time.Now()
	candidates, err := s.store.ListMatchesForAnalysis(ctx, analysisSyncDates(now), now.UTC(), now.Add(analysisLookahead).UTC())
	if err != nil {
		return err
	}

	ids := make([]string, len(candidates))
	for i, m := range candidates {
		ids[i] = m.ID
	}
	fetchedAt, err := s.store.MatchAnalysisFetchedAtForIDs(ctx, ids)
	if err != nil {
		return err
	}

	skipped, fetched, noData := 0, 0, 0
	for _, m := range candidates {
		if analysisSkip(now, fetchedAt[m.ID]) {
			skipped++
			continue
		}
		if s.noDataMarked(ctx, "analysis:nodata:"+m.ID) {
			noData++
			continue
		}
		raw, err := s.ts.FetchAnalysis(ctx, m.ID)
		if err != nil {
			var apiErr *thscore.APIError
			if errors.As(err, &apiErr) && apiErr.Code == thscore.CodeNoData {
				// No analysis for this match (yet) — expected for obscure
				// fixtures; retried after analysisNoDataTTL, not every run.
				noData++
				s.markNoData(ctx, "analysis:nodata:"+m.ID, analysisNoDataTTL)
				continue
			}
			s.log.Warn("fetch analysis failed", "match_id", m.ID, "err", err)
			continue
		}
		a := model.MatchAnalysis{MatchID: m.ID, Analysis: raw, FetchedAt: now.UTC()}
		if err := s.store.UpsertMatchAnalysis(ctx, a); err != nil {
			s.log.Warn("upsert analysis failed", "match_id", m.ID, "err", err)
			continue
		}
		if err := s.cache.Delete(ctx, "analysis:"+m.ID); err != nil {
			s.log.Warn("evict analysis cache", "match_id", m.ID, "err", err)
		}
		fetched++
	}
	s.log.Info("analysis synced", "selected", len(candidates), "skipped", skipped, "fetched", fetched, "no_data", noData)
	return nil
}

// resolveLogoURLs mirrors srcByID into R2 when logo mirroring is enabled;
// otherwise it passes thscore's own URLs through untouched (dev mode).
// evictMatchdates drops the cached daily lists for the given matchdates.
func (s *Syncer) evictMatchdates(ctx context.Context, dates map[string]struct{}) {
	if len(dates) == 0 {
		return
	}
	keys := make([]string, 0, len(dates))
	for d := range dates {
		keys = append(keys, "matches:"+d)
	}
	if err := s.cache.Delete(ctx, keys...); err != nil {
		s.log.Warn("evict matchdate cache", "keys", keys, "err", err)
	}
}
