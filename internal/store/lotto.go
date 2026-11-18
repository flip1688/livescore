package store

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/flip1688/livescore/internal/model"
)

// Lotto collections. Draws are deliberately NOT given a TTL index: unlike
// match data, a past draw stays useful forever (the archive pages are built
// on it) and upstream only serves ~100 draws back, so anything we drop is
// gone for good.
const (
	collLottoMarkets = "lotto_markets"
	collLottoDraws   = "lotto_draws"
)

// EnsureLottoIndexes creates the indexes the lotto read paths depend on.
//
// There is deliberately no unique index on (slug, draw_day): upstream
// publishes the same draw twice under different drawIds, so a unique key
// would turn a normal upstream quirk into a failed write. Deduplication is
// the `superseded` flag instead — see model.MarkSuperseded.
func (s *Store) EnsureLottoIndexes(ctx context.Context) error {
	_, err := s.db.Collection(collLottoDraws).Indexes().CreateMany(ctx, []mongo.IndexModel{
		// history: one market, newest first, visible rows only
		{Keys: bson.D{
			{Key: "slug", Value: 1},
			{Key: "superseded", Value: 1},
			{Key: "complete", Value: 1},
			{Key: "draw_at", Value: -1},
		}},
		// category landing pages
		{Keys: bson.D{{Key: "category", Value: 1}, {Key: "draw_at", Value: -1}}},
		// "what changed recently", used by the reconcile sweep
		{Keys: bson.D{{Key: "published_at", Value: -1}}},
		// archive filter by calendar day
		{Keys: bson.D{{Key: "draw_day", Value: -1}}},
	})
	if err != nil {
		return err
	}
	_, err = s.db.Collection(collLottoMarkets).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "category", Value: 1}, {Key: "active", Value: 1}}},
	})
	return err
}

// UpsertLottoMarkets $sets market fields by slug, leaving fields owned by
// the results sync (latest_draw_at, active) intact — the market list sync
// and the results sync write the same documents on different cadences.
func (s *Store) UpsertLottoMarkets(ctx context.Context, markets []model.LottoMarket) error {
	if len(markets) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(markets))
	for _, m := range markets {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": m.Slug}).
			SetUpdate(bson.M{"$set": bson.M{
				"name":          m.Name,
				"short_name":    m.ShortName,
				"category":      m.Category,
				"tier":          m.Tier,
				"country":       m.Country,
				"draw_schedule": m.DrawSchedule,
				"updated_at":    m.UpdatedAt,
			}}).
			SetUpsert(true))
	}
	_, err := s.db.Collection(collLottoMarkets).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

// SetLottoMarketFreshness stamps the newest draw we hold and the derived
// active flag. Separate from UpsertLottoMarkets because only the results
// sync knows these.
func (s *Store) SetLottoMarketFreshness(ctx context.Context, slug string, latest time.Time, active bool) error {
	_, err := s.db.Collection(collLottoMarkets).UpdateOne(ctx,
		bson.M{"_id": slug},
		bson.M{"$set": bson.M{"latest_draw_at": latest, "active": active}},
	)
	return err
}

// UpsertLottoDraws replaces draws by id. Upstream revises published draws
// (that is what result.updated means), so a replace — not an insert-only —
// is the correct write.
func (s *Store) UpsertLottoDraws(ctx context.Context, draws []model.LottoDraw) error {
	return upsertByID(ctx, s.db.Collection(collLottoDraws), draws, func(d model.LottoDraw) string { return d.ID })
}

// SetLottoSuperseded flips the flag for a set of ids in one write.
func (s *Store) SetLottoSuperseded(ctx context.Context, ids []string, superseded bool) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Collection(collLottoDraws).UpdateMany(ctx,
		bson.M{"_id": bson.M{"$in": ids}},
		bson.M{"$set": bson.M{"superseded": superseded}},
	)
	return err
}

// ListLottoMarkets returns markets, optionally filtered to one category and
// to active ones. Sorted by category then name so the UI can render without
// re-sorting.
func (s *Store) ListLottoMarkets(ctx context.Context, category string, activeOnly bool) ([]model.LottoMarket, error) {
	filter := bson.M{}
	if category != "" {
		filter["category"] = category
	}
	if activeOnly {
		filter["active"] = true
	}
	sort := bson.D{{Key: "category", Value: 1}, {Key: "name", Value: 1}}
	return findAll[model.LottoMarket](ctx, s.db.Collection(collLottoMarkets), filter, sort)
}

// GetLottoMarket returns one market by slug.
func (s *Store) GetLottoMarket(ctx context.Context, slug string) (model.LottoMarket, error) {
	var m model.LottoMarket
	err := s.db.Collection(collLottoMarkets).FindOne(ctx, bson.M{"_id": slug}).Decode(&m)
	return m, err
}

// ListLottoDraws returns a market's draws, newest first, hiding superseded
// and (unless includeIncomplete) incomplete ones — see model.LottoDraw.
func (s *Store) ListLottoDraws(ctx context.Context, slug string, limit int, includeIncomplete bool) ([]model.LottoDraw, error) {
	filter := bson.M{"slug": slug, "superseded": false}
	if !includeIncomplete {
		filter["complete"] = true
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "draw_at", Value: -1}}).
		SetLimit(int64(limit))
	cur, err := s.db.Collection(collLottoDraws).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []model.LottoDraw
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LatestLottoDrawPerMarket returns the newest visible draw for each market
// in slugs, for the category landing pages. One aggregation beats N finds
// when a category holds 78 markets.
func (s *Store) LatestLottoDrawPerMarket(ctx context.Context, category string) ([]model.LottoDraw, error) {
	match := bson.M{"superseded": false, "complete": true}
	if category != "" {
		match["category"] = category
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$sort", Value: bson.D{{Key: "draw_at", Value: -1}}}},
		{{Key: "$group", Value: bson.M{"_id": "$slug", "doc": bson.M{"$first": "$$ROOT"}}}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$doc"}}},
		{{Key: "$sort", Value: bson.D{{Key: "slug", Value: 1}}}},
	}
	cur, err := s.db.Collection(collLottoDraws).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []model.LottoDraw
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LottoDrawsByDay returns every visible draw on one Bangkok calendar day.
func (s *Store) LottoDrawsByDay(ctx context.Context, day, category string) ([]model.LottoDraw, error) {
	filter := bson.M{"draw_day": day, "superseded": false, "complete": true}
	if category != "" {
		filter["category"] = category
	}
	sort := bson.D{{Key: "draw_at", Value: -1}}
	return findAll[model.LottoDraw](ctx, s.db.Collection(collLottoDraws), filter, sort)
}

// LottoDrawIDsForMarket returns the ids we already hold for a market, so the
// reconcile sweep can tell new draws from revisions without refetching.
func (s *Store) LottoDrawIDsForMarket(ctx context.Context, slug string) (map[string]time.Time, error) {
	opts := options.Find().SetProjection(bson.M{"published_at": 1})
	cur, err := s.db.Collection(collLottoDraws).Find(ctx, bson.M{"slug": slug}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := map[string]time.Time{}
	for cur.Next(ctx) {
		var row struct {
			ID          string    `bson:"_id"`
			PublishedAt time.Time `bson:"published_at"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		out[row.ID] = row.PublishedAt
	}
	return out, cur.Err()
}
