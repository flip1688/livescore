package store

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/flip1688/livescore/internal/model"
)

// lnwthailive collections.
//
// Every name is prefixed lnw_ and no function in this file touches the
// thscore-backed collections (matches, teams, leagues, standings,
// match_analysis). That separation is the point: the two feeds are being
// compared, and a comparison in which one vendor can overwrite the other
// measures nothing. It also means the whole evaluation is undone by
// dropping these five collections.
const (
	collLnwMatches  = "lnw_matches"
	collLnwResults  = "lnw_results"
	collLnwStandng  = "lnw_standings"
	collLnwAnalysis = "lnw_analysis"
	collLnwLinks    = "lnw_match_links"
	collLnwCoverage = "lnw_coverage"
	collLnwBoxing   = "lnw_boxing_programs"
	collLnwCombat   = "lnw_combat_bouts"
)

// lnwRetentionDays bounds the evaluation data. The feed carries only today
// and tomorrow, so this is about how much comparison history we keep, not
// about how far back the vendor can serve: 90 days is several times the
// week or two the decision needs, and still bounded.
const lnwRetentionDays = 90

// EnsureLnwIndexes creates the indexes the evaluation queries need.
//
// Matches and results get a TTL on updated_at — unlike lottery draws,
// nothing here is irreplaceable (results can be re-fetched by date) and
// unbounded growth on an evaluation dataset is not worth carrying.
// Coverage rows are tiny and are kept.
func (s *Store) EnsureLnwIndexes(ctx context.Context) error {
	if _, err := s.db.Collection(collLnwMatches).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_date", Value: 1}, {Key: "kickoff_at", Value: 1}}},
		{Keys: bson.D{{Key: "sport", Value: 1}, {Key: "match_date", Value: 1}}},
		{Keys: bson.D{{Key: "is_live", Value: 1}}},
	}); err != nil {
		return err
	}
	if err := ensureTTLIndex(ctx, s.db.Collection(collLnwMatches), "updated_at", lnwRetentionDays); err != nil {
		return err
	}

	if _, err := s.db.Collection(collLnwResults).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_date", Value: 1}, {Key: "kickoff_at", Value: 1}}},
		{Keys: bson.D{{Key: "league_id", Value: 1}, {Key: "match_date", Value: -1}}},
	}); err != nil {
		return err
	}
	if err := ensureTTLIndex(ctx, s.db.Collection(collLnwResults), "updated_at", lnwRetentionDays); err != nil {
		return err
	}

	if _, err := s.db.Collection(collLnwAnalysis).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_date", Value: 1}}},
	}); err != nil {
		return err
	}
	if err := ensureTTLIndex(ctx, s.db.Collection(collLnwAnalysis), "updated_at", lnwRetentionDays); err != nil {
		return err
	}

	// Boxing programmes are a small, slow-changing list and are kept
	// without a TTL: the point of storing them is to notice the day a card
	// finally appears, which needs the previous state to compare against.
	if _, err := s.db.Collection(collLnwBoxing).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "total_events", Value: -1}},
	}); err != nil {
		return err
	}

	if _, err := s.db.Collection(collLnwCombat).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "sport", Value: 1}, {Key: "match_date", Value: -1}}},
		{Keys: bson.D{{Key: "match_date", Value: -1}, {Key: "start_at", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}); err != nil {
		return err
	}
	if err := ensureTTLIndex(ctx, s.db.Collection(collLnwCombat), "updated_at", lnwRetentionDays); err != nil {
		return err
	}

	_, err := s.db.Collection(collLnwLinks).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_date", Value: 1}}},
		{Keys: bson.D{{Key: "method", Value: 1}}},
	})
	return err
}

// UpsertLnwMatches replaces fixture rows by upstream id.
//
// A full replace rather than a $set: this feed re-sends the whole row every
// poll, and a field that disappears upstream (a match losing its channel
// list when it ends, say) must disappear here too.
func (s *Store) UpsertLnwMatches(ctx context.Context, matches []model.LnwMatch) error {
	return upsertByID(ctx, s.db.Collection(collLnwMatches), matches, func(m model.LnwMatch) string { return m.ID })
}

// UpsertLnwResults replaces result rows by prefixed upstream id.
func (s *Store) UpsertLnwResults(ctx context.Context, results []model.LnwResult) error {
	return upsertByID(ctx, s.db.Collection(collLnwResults), results, func(r model.LnwResult) string { return r.ID })
}

// UpsertLnwStandings replaces league tables by their leagueUrl anchor.
func (s *Store) UpsertLnwStandings(ctx context.Context, tables []model.LnwStanding) error {
	return upsertByID(ctx, s.db.Collection(collLnwStandng), tables, func(t model.LnwStanding) string { return t.ID })
}

// UpsertLnwAnalysis replaces analysis index rows by prefixed upstream id.
func (s *Store) UpsertLnwAnalysis(ctx context.Context, rows []model.LnwAnalysis) error {
	return upsertByID(ctx, s.db.Collection(collLnwAnalysis), rows, func(a model.LnwAnalysis) string { return a.ID })
}

// UpsertLnwLinks $sets crosswalk rows, so a link found by one job does not
// blank the ids another job filled in.
func (s *Store) UpsertLnwLinks(ctx context.Context, links []model.LnwMatchLink) error {
	if len(links) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(links))
	for _, l := range links {
		set := bson.M{
			"match_date": l.MatchDate,
			"method":     l.Method,
			"confidence": l.Confidence,
			"updated_at": l.UpdatedAt,
		}
		// Only non-empty ids are written: each feed is linked by its own
		// job, on its own cadence.
		for k, v := range map[string]string{
			"match_id":    l.MatchID,
			"result_id":   l.ResultID,
			"analysis_id": l.AnalysisID,
			"thscore_id":  l.ThscoreID,
		} {
			if v != "" {
				set[k] = v
			}
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": l.ID}).
			SetUpdate(bson.M{"$set": set}).
			SetUpsert(true))
	}
	_, err := s.db.Collection(collLnwLinks).BulkWrite(ctx, models)
	return err
}

// SaveLnwCoverage stores one day's vendor comparison, replacing the
// previous computation for that day.
func (s *Store) SaveLnwCoverage(ctx context.Context, c model.LnwCoverage) error {
	c.ComputedAt = time.Now().UTC()
	_, err := s.db.Collection(collLnwCoverage).ReplaceOne(ctx,
		bson.M{"_id": c.Day}, c, options.Replace().SetUpsert(true))
	return err
}

// LnwMatchesByDate returns the vendor's fixtures for one matchdate.
func (s *Store) LnwMatchesByDate(ctx context.Context, day string) ([]model.LnwMatch, error) {
	return findAll[model.LnwMatch](ctx, s.db.Collection(collLnwMatches),
		bson.M{"match_date": day}, bson.D{{Key: "kickoff_at", Value: 1}})
}

// LnwResultsByDate returns the vendor's results for one matchdate.
func (s *Store) LnwResultsByDate(ctx context.Context, day string) ([]model.LnwResult, error) {
	return findAll[model.LnwResult](ctx, s.db.Collection(collLnwResults),
		bson.M{"match_date": day}, bson.D{{Key: "kickoff_at", Value: 1}})
}

// LnwAnalysisByDate returns the vendor's analysis index for one matchdate.
func (s *Store) LnwAnalysisByDate(ctx context.Context, day string) ([]model.LnwAnalysis, error) {
	return findAll[model.LnwAnalysis](ctx, s.db.Collection(collLnwAnalysis),
		bson.M{"match_date": day}, bson.D{{Key: "kickoff_at", Value: 1}})
}

// LnwStandings returns every league table the vendor publishes.
func (s *Store) LnwStandings(ctx context.Context) ([]model.LnwStanding, error) {
	return findAll[model.LnwStanding](ctx, s.db.Collection(collLnwStandng),
		bson.M{}, bson.D{{Key: "league_name", Value: 1}})
}

// LnwLinksByDate returns the crosswalk rows for one matchdate.
func (s *Store) LnwLinksByDate(ctx context.Context, day string) ([]model.LnwMatchLink, error) {
	return findAll[model.LnwMatchLink](ctx, s.db.Collection(collLnwLinks),
		bson.M{"match_date": day}, bson.D{{Key: "_id", Value: 1}})
}

// LnwCoverageSince returns the comparison rows from day onward, oldest
// first — the table the vendor decision is read off.
func (s *Store) LnwCoverageSince(ctx context.Context, day string) ([]model.LnwCoverage, error) {
	return findAll[model.LnwCoverage](ctx, s.db.Collection(collLnwCoverage),
		bson.M{"_id": bson.M{"$gte": day}}, bson.D{{Key: "_id", Value: 1}})
}

// CountMatchesByDate counts thscore fixtures on a matchdate, for the
// coverage comparison. It reads the thscore collection but never writes it.
func (s *Store) CountMatchesByDate(ctx context.Context, day string) (int, int, error) {
	coll := s.db.Collection("matches")
	n, err := coll.CountDocuments(ctx, bson.M{"match_date": day})
	if err != nil {
		return 0, 0, err
	}
	var leagues []string
	if err := coll.Distinct(ctx, "league_id", bson.M{"match_date": day}).Decode(&leagues); err != nil {
		return int(n), 0, err
	}
	return int(n), len(leagues), nil
}

// UpsertLnwBoxingPrograms replaces programmes by upstream id.
func (s *Store) UpsertLnwBoxingPrograms(ctx context.Context, programs []model.LnwBoxingProgram) error {
	return upsertByID(ctx, s.db.Collection(collLnwBoxing), programs,
		func(p model.LnwBoxingProgram) string { return p.ID })
}

// UpsertLnwCombatBouts replaces bouts by sport-prefixed upstream id.
func (s *Store) UpsertLnwCombatBouts(ctx context.Context, bouts []model.LnwCombatBout) error {
	return upsertByID(ctx, s.db.Collection(collLnwCombat), bouts,
		func(b model.LnwCombatBout) string { return b.ID })
}

// LnwMatchesBySportDate returns one display day's rows for a single sport,
// sorted by kick-off. The fighting feed is read through this: it shares the
// fixture collection with football because upstream serves both through
// /api/public/matches, and only the sport field separates them.
func (s *Store) LnwMatchesBySportDate(ctx context.Context, sport, matchDate string) ([]model.LnwMatch, error) {
	filter := bson.M{"sport": sport, "match_date": matchDate}
	return findAll[model.LnwMatch](ctx, s.db.Collection(collLnwMatches), filter,
		bson.D{{Key: "kickoff_at", Value: 1}})
}

// ListLnwBoxingPrograms returns the promotion list, most-populated first so
// a programme that finally carries a card sorts to the top.
func (s *Store) ListLnwBoxingPrograms(ctx context.Context) ([]model.LnwBoxingProgram, error) {
	return findAll[model.LnwBoxingProgram](ctx, s.db.Collection(collLnwBoxing), bson.M{},
		bson.D{{Key: "total_events", Value: -1}, {Key: "name", Value: 1}})
}
