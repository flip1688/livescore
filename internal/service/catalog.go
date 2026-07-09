// Package service holds the read paths (cache-first) and the sync worker
// that pulls from thscore on the cadences documented in docs/thscore-api.md.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/flip1688/livescore/internal/cache"
	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
	"github.com/flip1688/livescore/internal/thscore"
	"github.com/flip1688/livescore/internal/ws"
)

// ErrBadInput marks errors caused by invalid client input (HTTP 400).
var ErrBadInput = errors.New("bad input")

// ErrNotFound marks a lookup that found nothing (HTTP 404).
var ErrNotFound = errors.New("not found")

// matchDetailTTL is short: events/stats are synced every minute and evicted
// on write, so this only bounds staleness between a sync and its eviction.
const matchDetailTTL = 30 * time.Second

// Catalog serves dictionary and schedule reads: Redis first, MongoDB on miss.
// It never calls thscore — only the sync worker does.
type Catalog struct {
	store *store.Store
	cache *cache.Cache
	log   *slog.Logger

	dictTTL time.Duration
}

func NewCatalog(st *store.Store, c *cache.Cache, log *slog.Logger, dictTTL time.Duration) *Catalog {
	return &Catalog{store: st, cache: c, log: log, dictTTL: dictTTL}
}

func (s *Catalog) Leagues(ctx context.Context) ([]model.League, error) {
	return readThrough(ctx, s, "leagues", s.dictTTL, func() ([]model.League, error) {
		return s.store.ListLeagues(ctx)
	})
}

func (s *Catalog) Teams(ctx context.Context, leagueID string) ([]model.Team, error) {
	key := "teams:" + leagueID
	return readThrough(ctx, s, key, s.dictTTL, func() ([]model.Team, error) {
		return s.store.ListTeams(ctx, leagueID)
	})
}

// Bangkok is the display timezone, matching thscore's own GMT+7 matchTime.
// Thailand has no DST.
var Bangkok = time.FixedZone("ICT", 7*60*60)

// matchDayCutoff: a "match day" runs 04:00 ICT through 04:00 ICT the next
// day, so European matches kicking off at 01:00–03:59 Thai time belong to the
// previous day's programme (e.g. kickoff 2026-07-08 03:00 → matchdate
// 2026-07-07). Business rule — do not change to midnight.
const matchDayCutoff = 4 * time.Hour

// CurrentMatchDate returns today's matchdate ("2006-01-02") under the 04:00
// ICT cutoff.
func CurrentMatchDate(now time.Time) string {
	return now.In(Bangkok).Add(-matchDayCutoff).Format("2006-01-02")
}

// MatchDateFor buckets a kickoff instant into its matchdate — used by the
// sync worker to stamp Match.MatchDate. Same 04:00 cutoff as CurrentMatchDate.
func MatchDateFor(kickoff time.Time) string {
	return CurrentMatchDate(kickoff)
}

// MatchesByDate returns the match list for one matchdate.
// date is "2006-01-02"; empty means the current matchdate.
func (s *Catalog) MatchesByDate(ctx context.Context, date string) ([]model.Match, error) {
	today := CurrentMatchDate(time.Now())
	if date == "" {
		date = today
	}
	if _, err := time.ParseInLocation("2006-01-02", date, Bangkok); err != nil {
		return nil, fmt.Errorf("%w: date must be YYYY-MM-DD", ErrBadInput)
	}

	// Today's list carries live scores (Mongo is refreshed by the snapshot
	// loop every minute), so cache it briefly; other days barely change.
	ttl := 10 * time.Minute
	if date == today {
		ttl = 30 * time.Second
	}

	key := "matches:" + date
	return readThrough(ctx, s, key, ttl, func() ([]model.Match, error) {
		return s.store.ListMatchesByMatchDate(ctx, date)
	})
}

// Snapshot implements ws.SnapshotFunc: the state a client needs right after
// subscribing, before any live deltas arrive (closes the SSR→stream gap).
func (s *Catalog) Snapshot(ctx context.Context, channel string) ([]ws.Message, error) {
	kind, arg, _ := strings.Cut(channel, ":")
	switch kind {
	case "live":
		return s.listSnapshot(ctx, channel, CurrentMatchDate(time.Now()))
	case "matchlist":
		return s.listSnapshot(ctx, channel, arg)
	case "match":
		m, err := s.matchState(ctx, arg)
		if err != nil {
			return nil, nil // unknown match (or DB hiccup): no snapshot, deltas may still follow
		}
		return snapshotMessages(channel, m)
	default:
		return nil, nil
	}
}

// matchState resolves the current state of one match: Redis live state
// (`live:match:<id>`) first, falling back to MongoDB. Returns ErrNotFound
// when neither source has the match.
func (s *Catalog) matchState(ctx context.Context, id string) (model.Match, error) {
	var m model.Match
	if err := s.cache.GetJSON(ctx, liveMatchKey(id), &m); err == nil {
		return m, nil
	} else if !errors.Is(err, cache.ErrMiss) {
		s.log.Warn("match state: live cache read failed", "match_id", id, "err", err)
	}

	m, err := s.store.GetMatch(ctx, id)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return model.Match{}, fmt.Errorf("match %s: %w", id, ErrNotFound)
		}
		return model.Match{}, err
	}
	return m, nil
}

// Match returns one match's current state by id — the REST counterpart of
// the `match:<id>` WS channel's snapshot.
func (s *Catalog) Match(ctx context.Context, id string) (model.Match, error) {
	if id == "" {
		return model.Match{}, fmt.Errorf("%w: match id required", ErrBadInput)
	}
	return s.matchState(ctx, id)
}

// MatchEvents returns the event timeline for one match (empty slice if none
// has been synced yet). Cached briefly; syncEventsStats evicts the key on
// every write so this TTL only bounds staleness between sync and eviction.
func (s *Catalog) MatchEvents(ctx context.Context, id string) ([]thscore.EventItem, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: match id required", ErrBadInput)
	}
	return readThrough(ctx, s, "events:"+id, matchDetailTTL, func() ([]thscore.EventItem, error) {
		return s.store.GetMatchEvents(ctx, id)
	})
}

// MatchStats returns the technical stats for one match (empty slice if none
// has been synced yet). Same cache/eviction story as MatchEvents.
func (s *Catalog) MatchStats(ctx context.Context, id string) ([]thscore.StatItem, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: match id required", ErrBadInput)
	}
	return readThrough(ctx, s, "stats:"+id, matchDetailTTL, func() ([]thscore.StatItem, error) {
		return s.store.GetMatchStats(ctx, id)
	})
}

// standingsTTL: the sync worker refreshes tables every 6h and evicts the
// cache key on every successful upsert, so this only bounds staleness
// between a sync and its eviction.
const standingsTTL = 6 * time.Hour

// Standings returns one league's table (all 6 standing views), Redis first
// falling back to MongoDB. ErrNotFound if the league has never been synced
// (unknown id, or simply no fixtures in the sync window yet).
func (s *Catalog) Standings(ctx context.Context, leagueID string) (model.LeagueStanding, error) {
	if leagueID == "" {
		return model.LeagueStanding{}, fmt.Errorf("%w: league id required", ErrBadInput)
	}
	return readThroughOne(ctx, s, "standings:"+leagueID, standingsTTL, func() (model.LeagueStanding, error) {
		st, err := s.store.GetStanding(ctx, leagueID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return model.LeagueStanding{}, fmt.Errorf("standings %s: %w", leagueID, ErrNotFound)
			}
			return model.LeagueStanding{}, err
		}
		return st, nil
	})
}

// analysisTTL: the sync worker refreshes analysis every 30m (skipping
// matches fetched within the last 24h) and evicts the cache key on every
// upsert, so this only bounds staleness between a sync and its eviction —
// kept well under thscore's own 24h upstream cache.
const analysisTTL = 6 * time.Hour

// MatchAnalysis returns one match's pre-fetched H2H/form/odds analysis blob
// as thscore sent it (see model.MatchAnalysis), Redis first falling back to
// MongoDB. ErrNotFound if the match hasn't been analyzed yet (not scheduled
// within the sync window, or synced but thscore had nothing for it).
func (s *Catalog) MatchAnalysis(ctx context.Context, matchID string) (json.RawMessage, error) {
	if matchID == "" {
		return nil, fmt.Errorf("%w: match id required", ErrBadInput)
	}
	return readThroughOne(ctx, s, "analysis:"+matchID, analysisTTL, func() (json.RawMessage, error) {
		a, err := s.store.GetMatchAnalysis(ctx, matchID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, fmt.Errorf("analysis %s: %w", matchID, ErrNotFound)
			}
			return nil, err
		}
		return a.Analysis, nil
	})
}

func (s *Catalog) listSnapshot(ctx context.Context, channel, date string) ([]ws.Message, error) {
	matches, err := s.MatchesByDate(ctx, date)
	if err != nil {
		return nil, err
	}
	return snapshotMessages(channel, matches)
}

func snapshotMessages(channel string, payload any) ([]ws.Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return []ws.Message{{Channel: channel, Type: "snapshot", Data: data}}, nil
}

// readThrough returns the cached value at key, falling back to load() and
// populating the cache. Cache errors degrade to a DB read, never a failure.
func readThrough[T any](ctx context.Context, s *Catalog, key string, ttl time.Duration, load func() ([]T, error)) ([]T, error) {
	var cached []T
	err := s.cache.GetJSON(ctx, key, &cached)
	if err == nil {
		return cached, nil
	}
	if !errors.Is(err, cache.ErrMiss) {
		s.log.Warn("cache read failed", "key", key, "err", err)
	}

	fresh, err := load()
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", key, err)
	}
	if err := s.cache.SetJSON(ctx, key, fresh, ttl); err != nil {
		s.log.Warn("cache write failed", "key", key, "err", err)
	}
	return fresh, nil
}

// readThroughOne is readThrough's single-document counterpart: same
// cache-then-load-then-populate flow, but for one T instead of a list. load's
// error (including ErrNotFound) always propagates uncached, so a league that
// hasn't synced yet is re-checked on every call rather than pinned as absent.
func readThroughOne[T any](ctx context.Context, s *Catalog, key string, ttl time.Duration, load func() (T, error)) (T, error) {
	var zero T
	var cached T
	err := s.cache.GetJSON(ctx, key, &cached)
	if err == nil {
		return cached, nil
	}
	if !errors.Is(err, cache.ErrMiss) {
		s.log.Warn("cache read failed", "key", key, "err", err)
	}

	fresh, err := load()
	if err != nil {
		return zero, err
	}
	if err := s.cache.SetJSON(ctx, key, fresh, ttl); err != nil {
		s.log.Warn("cache write failed", "key", key, "err", err)
	}
	return fresh, nil
}
