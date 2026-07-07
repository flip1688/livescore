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

	"github.com/flip1688/livescore/internal/cache"
	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
	"github.com/flip1688/livescore/internal/ws"
)

// ErrBadInput marks errors caused by invalid client input (HTTP 400).
var ErrBadInput = errors.New("bad input")

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
		var m model.Match
		if err := s.cache.GetJSON(ctx, liveMatchKey(arg), &m); err != nil {
			if !errors.Is(err, cache.ErrMiss) {
				s.log.Warn("snapshot: live state read failed", "channel", channel, "err", err)
			}
			var dbErr error
			if m, dbErr = s.store.GetMatch(ctx, arg); dbErr != nil {
				return nil, nil // unknown match: no snapshot, deltas may still follow
			}
		}
		return snapshotMessages(channel, m)
	default:
		return nil, nil
	}
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
