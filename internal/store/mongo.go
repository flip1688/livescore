// Package store persists dictionary and schedule data in MongoDB Atlas.
package store

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/flip1688/livescore/internal/model"
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
