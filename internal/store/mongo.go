// Package store persists dictionary and schedule data in MongoDB Atlas.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/thscore"
)

type Store struct {
	client *mongo.Client
	db     *mongo.Database
}

func New(ctx context.Context, uri, dbName string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		return nil, fmt.Errorf("mongo: ping: %w", err)
	}
	return &Store{client: client, db: client.Database(dbName)}, nil
}

func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

// EnsureIndexes creates the indexes the query paths depend on.
func (s *Store) EnsureIndexes(ctx context.Context) error {
	_, err := s.db.Collection("matches").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_date", Value: 1}, {Key: "kickoff_at", Value: 1}}},
		{Keys: bson.D{{Key: "league_id", Value: 1}, {Key: "kickoff_at", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	})
	if err != nil {
		return err
	}
	_, err = s.db.Collection("teams").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "league_id", Value: 1}},
	})
	return err
}

// upsertByID replaces each doc by _id, inserting when absent.
func upsertByID[T any](ctx context.Context, coll *mongo.Collection, docs []T, id func(T) string) error {
	if len(docs) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(docs))
	for _, d := range docs {
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.M{"_id": id(d)}).
			SetReplacement(d).
			SetUpsert(true))
	}
	_, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

// upsertSet $sets each doc's marshaled fields by _id, inserting when absent.
// Unlike upsertByID it does not replace the whole document, so fields owned
// by other jobs — leagues/teams' logo_url, stamped by the logo sync — survive
// a dictionary re-sync. Zero-valued omitempty fields are simply left as-is.
func upsertSet[T any](ctx context.Context, coll *mongo.Collection, docs []T, id func(T) string) error {
	if len(docs) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(docs))
	for _, d := range docs {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": id(d)}).
			SetUpdate(bson.M{"$set": d}).
			SetUpsert(true))
	}
	_, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

func (s *Store) UpsertLeagues(ctx context.Context, leagues []model.League) error {
	return upsertSet(ctx, s.db.Collection("leagues"), leagues, func(l model.League) string { return l.ID })
}

func (s *Store) UpsertTeams(ctx context.Context, teams []model.Team) error {
	return upsertSet(ctx, s.db.Collection("teams"), teams, func(t model.Team) string { return t.ID })
}

// SetLeagueLogoURLs stamps mirrored logo URLs onto league docs by id.
func (s *Store) SetLeagueLogoURLs(ctx context.Context, urlByID map[string]string) error {
	return setLogoURLs(ctx, s.db.Collection("leagues"), urlByID)
}

// SetTeamLogoURLs stamps mirrored logo URLs onto team docs by id.
func (s *Store) SetTeamLogoURLs(ctx context.Context, urlByID map[string]string) error {
	return setLogoURLs(ctx, s.db.Collection("teams"), urlByID)
}

func setLogoURLs(ctx context.Context, coll *mongo.Collection, urlByID map[string]string) error {
	if len(urlByID) == 0 {
		return nil
	}
	now := time.Now().UTC()
	models := make([]mongo.WriteModel, 0, len(urlByID))
	for id, u := range urlByID {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": id}).
			SetUpdate(bson.M{"$set": bson.M{"logo_url": u, "updated_at": now}}))
	}
	_, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

func (s *Store) UpsertCountries(ctx context.Context, countries []model.Country) error {
	return upsertByID(ctx, s.db.Collection("countries"), countries, func(c model.Country) string { return c.ID })
}

func (s *Store) UpsertMatches(ctx context.Context, matches []model.Match) error {
	return upsertByID(ctx, s.db.Collection("matches"), matches, func(m model.Match) string { return m.ID })
}

func (s *Store) DeleteMatch(ctx context.Context, matchID string) error {
	_, err := s.db.Collection("matches").DeleteOne(ctx, bson.M{"_id": matchID})
	return err
}

// GetMatch returns one match by thscore match id, or mongo.ErrNoDocuments.
func (s *Store) GetMatch(ctx context.Context, matchID string) (model.Match, error) {
	var m model.Match
	err := s.db.Collection("matches").FindOne(ctx, bson.M{"_id": matchID}).Decode(&m)
	return m, err
}

// ReplaceMatchEvents stores the full event timeline for a match (one doc per
// match; the feed always sends the complete list).
func (s *Store) ReplaceMatchEvents(ctx context.Context, matchID string, events any) error {
	return s.replaceByID(ctx, "match_events", matchID, bson.M{"events": events})
}

// ReplaceMatchStats stores the technical stats for a match.
func (s *Store) ReplaceMatchStats(ctx context.Context, matchID string, stats any) error {
	return s.replaceByID(ctx, "match_stats", matchID, bson.M{"stats": stats})
}

func (s *Store) replaceByID(ctx context.Context, coll, id string, doc bson.M) error {
	doc["_id"] = id
	doc["updated_at"] = time.Now().UTC()
	_, err := s.db.Collection(coll).ReplaceOne(ctx, bson.M{"_id": id}, doc, options.Replace().SetUpsert(true))
	return err
}

// eventsDoc/statsDoc mirror the shape ReplaceMatchEvents/ReplaceMatchStats
// write ({_id, events|stats, updated_at}).
type eventsDoc struct {
	Events []thscore.EventItem `bson:"events"`
}

type statsDoc struct {
	Stats []thscore.StatItem `bson:"stats"`
}

// GetMatchEvents returns the event timeline stored for a match. A missing
// doc (no events synced yet) is not an error — it returns an empty slice.
func (s *Store) GetMatchEvents(ctx context.Context, matchID string) ([]thscore.EventItem, error) {
	var doc eventsDoc
	err := s.db.Collection("match_events").FindOne(ctx, bson.M{"_id": matchID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return []thscore.EventItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	if doc.Events == nil {
		doc.Events = []thscore.EventItem{}
	}
	return doc.Events, nil
}

// GetMatchStats returns the technical stats stored for a match. A missing
// doc (no stats synced yet) is not an error — it returns an empty slice.
func (s *Store) GetMatchStats(ctx context.Context, matchID string) ([]thscore.StatItem, error) {
	var doc statsDoc
	err := s.db.Collection("match_stats").FindOne(ctx, bson.M{"_id": matchID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return []thscore.StatItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	if doc.Stats == nil {
		doc.Stats = []thscore.StatItem{}
	}
	return doc.Stats, nil
}

// UpsertStanding replaces one league's standing table by league id.
func (s *Store) UpsertStanding(ctx context.Context, st model.LeagueStanding) error {
	_, err := s.db.Collection("standings").ReplaceOne(ctx, bson.M{"_id": st.ID}, st, options.Replace().SetUpsert(true))
	return err
}

// GetStanding returns one league's standing table, or mongo.ErrNoDocuments.
func (s *Store) GetStanding(ctx context.Context, leagueID string) (model.LeagueStanding, error) {
	var st model.LeagueStanding
	err := s.db.Collection("standings").FindOne(ctx, bson.M{"_id": leagueID}).Decode(&st)
	return st, err
}

// UpsertMatchAnalysis replaces one match's stored analysis payload,
// stamping fetched_at (when thscore was actually called) and updated_at.
func (s *Store) UpsertMatchAnalysis(ctx context.Context, a model.MatchAnalysis) error {
	a.UpdatedAt = time.Now().UTC()
	_, err := s.db.Collection("match_analysis").ReplaceOne(ctx, bson.M{"_id": a.MatchID}, a, options.Replace().SetUpsert(true))
	return err
}

// GetMatchAnalysis returns one match's stored analysis payload, or
// mongo.ErrNoDocuments if it hasn't been synced yet.
func (s *Store) GetMatchAnalysis(ctx context.Context, matchID string) (model.MatchAnalysis, error) {
	var a model.MatchAnalysis
	err := s.db.Collection("match_analysis").FindOne(ctx, bson.M{"_id": matchID}).Decode(&a)
	return a, err
}

// matchAnalysisFetchedAtDoc is the projection used by
// MatchAnalysisFetchedAtSince — decoding only _id/fetched_at avoids pulling
// the (potentially large) analysis blob just to check staleness.
type matchAnalysisFetchedAtDoc struct {
	MatchID   string    `bson:"_id"`
	FetchedAt time.Time `bson:"fetched_at"`
}

// MatchAnalysisFetchedAtForIDs returns fetched_at timestamps keyed by match
// id for the given ids, projecting out the analysis blob itself — the sync
// job's cheap skip-logic check (don't refetch inside thscore's 24h upstream
// cache window) without loading every stored payload.
func (s *Store) MatchAnalysisFetchedAtForIDs(ctx context.Context, matchIDs []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(matchIDs) == 0 {
		return out, nil
	}
	cur, err := s.db.Collection("match_analysis").Find(ctx,
		bson.M{"_id": bson.M{"$in": matchIDs}},
		options.Find().SetProjection(bson.M{"_id": 1, "fetched_at": 1}))
	if err != nil {
		return nil, err
	}
	var docs []matchAnalysisFetchedAtDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	for _, d := range docs {
		out[d.MatchID] = d.FetchedAt
	}
	return out, nil
}

// ListMatchesForAnalysis returns scheduled matches whose kickoff falls within
// [from, to) among the given matchdates — the query the `analysis` sync job
// uses to pick candidates, scoped by match_date first so it hits the
// existing {match_date:1, kickoff_at:1} index before filtering by kickoff.
func (s *Store) ListMatchesForAnalysis(ctx context.Context, matchDates []string, from, to time.Time) ([]model.Match, error) {
	filter := bson.M{
		"match_date": bson.M{"$in": matchDates},
		"status":     model.MatchScheduled,
		"kickoff_at": bson.M{"$gte": from, "$lt": to},
	}
	return findAll[model.Match](ctx, s.db.Collection("matches"), filter, bson.D{{Key: "kickoff_at", Value: 1}})
}

// DistinctLeagueIDsForMatchDates returns the distinct league_id values among
// matches whose match_date is one of the given dates — used to scope the
// standings sync to leagues that actually have fixtures around today rather
// than refetching every league thscore knows about.
func (s *Store) DistinctLeagueIDsForMatchDates(ctx context.Context, dates []string) ([]string, error) {
	var ids []string
	err := s.db.Collection("matches").Distinct(ctx, "league_id", bson.M{"match_date": bson.M{"$in": dates}}).Decode(&ids)
	return ids, err
}

func (s *Store) ListLeagues(ctx context.Context) ([]model.League, error) {
	return findAll[model.League](ctx, s.db.Collection("leagues"), bson.M{}, bson.D{{Key: "name", Value: 1}})
}

func (s *Store) ListTeams(ctx context.Context, leagueID string) ([]model.Team, error) {
	filter := bson.M{}
	if leagueID != "" {
		filter["league_id"] = leagueID
	}
	return findAll[model.Team](ctx, s.db.Collection("teams"), filter, bson.D{{Key: "name", Value: 1}})
}

// TeamLogoURLsForIDs fetches the mirrored logo_url for a set of team ids in
// one projected query (id + logo_url only) — used to stamp Match.HomeLogoURL/
// AwayLogoURL at sync time without loading full Team docs. Teams with no
// mirrored logo yet are simply absent from the returned map.
func (s *Store) TeamLogoURLsForIDs(ctx context.Context, teamIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(teamIDs))
	if len(teamIDs) == 0 {
		return out, nil
	}
	cur, err := s.db.Collection("teams").Find(ctx,
		bson.M{"_id": bson.M{"$in": teamIDs}},
		options.Find().SetProjection(bson.M{"logo_url": 1}),
	)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID      string `bson:"_id"`
		LogoURL string `bson:"logo_url"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.LogoURL != "" {
			out[r.ID] = r.LogoURL
		}
	}
	return out, nil
}

// ListMatchesByMatchDate returns one display day's matches, keyed on the
// precomputed match_date field (04:00 ICT cutoff), sorted by kickoff.
func (s *Store) ListMatchesByMatchDate(ctx context.Context, matchDate string) ([]model.Match, error) {
	filter := bson.M{"match_date": matchDate}
	return findAll[model.Match](ctx, s.db.Collection("matches"), filter, bson.D{{Key: "kickoff_at", Value: 1}})
}

func findAll[T any](ctx context.Context, coll *mongo.Collection, filter any, sort any) ([]T, error) {
	cur, err := coll.Find(ctx, filter, options.Find().SetSort(sort))
	if err != nil {
		return nil, err
	}
	var out []T
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
