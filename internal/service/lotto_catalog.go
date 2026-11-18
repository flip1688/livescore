package service

import (
	"context"
	"errors"
	"log/slog"
	"regexp"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/flip1688/livescore/internal/model"
	"github.com/flip1688/livescore/internal/store"
)

// maxLottoHistory bounds ?limit= on the history endpoint. Upstream caps its
// own history at 100 per market, so anything above this can only ever be
// served from what we have accumulated ourselves.
const maxLottoHistory = 500

// dayPattern matches a Bangkok calendar day, the key draws are grouped by.
var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// LottoCatalog is the lottery read path: MongoDB only, never upstream.
// Results are not cached in Redis — a draw is written once and then read
// unchanged for months, so Mongo's own working set already serves them from
// memory, and a cache would mostly add a way to serve a stale result in the
// minutes after a draw lands, which is exactly when it matters most.
type LottoCatalog struct {
	store *store.Store
	log   *slog.Logger
}

func NewLottoCatalog(st *store.Store, log *slog.Logger) *LottoCatalog {
	return &LottoCatalog{store: st, log: log}
}

// Markets lists markets for the menus. activeOnly hides the ones upstream
// still lists but no longer feeds.
func (c *LottoCatalog) Markets(ctx context.Context, category string, activeOnly bool) ([]model.LottoMarket, error) {
	if category != "" && !validLottoCategory(category) {
		return nil, ErrBadInput
	}
	markets, err := c.store.ListLottoMarkets(ctx, category, activeOnly)
	if err != nil {
		return nil, err
	}
	if markets == nil {
		markets = []model.LottoMarket{}
	}
	return markets, nil
}

func (c *LottoCatalog) Market(ctx context.Context, slug string) (model.LottoMarket, error) {
	if slug == "" {
		return model.LottoMarket{}, ErrBadInput
	}
	m, err := c.store.GetLottoMarket(ctx, slug)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.LottoMarket{}, ErrNotFound
	}
	return m, err
}

// LatestPerMarket returns the newest visible draw for every market, for a
// category landing page.
func (c *LottoCatalog) LatestPerMarket(ctx context.Context, category string) ([]model.LottoDraw, error) {
	if category != "" && !validLottoCategory(category) {
		return nil, ErrBadInput
	}
	draws, err := c.store.LatestLottoDrawPerMarket(ctx, category)
	if err != nil {
		return nil, err
	}
	model.SortDrawsDesc(draws)
	if draws == nil {
		draws = []model.LottoDraw{}
	}
	return draws, nil
}

// History returns one market's past draws, newest first.
func (c *LottoCatalog) History(ctx context.Context, slug string, limit int) ([]model.LottoDraw, error) {
	if slug == "" || limit <= 0 || limit > maxLottoHistory {
		return nil, ErrBadInput
	}
	draws, err := c.store.ListLottoDraws(ctx, slug, limit, false)
	if err != nil {
		return nil, err
	}
	if draws == nil {
		draws = []model.LottoDraw{}
	}
	return draws, nil
}

// DrawsByDay returns every visible draw on one Bangkok calendar day.
func (c *LottoCatalog) DrawsByDay(ctx context.Context, day, category string) ([]model.LottoDraw, error) {
	if !dayPattern.MatchString(day) {
		return nil, ErrBadInput
	}
	if category != "" && !validLottoCategory(category) {
		return nil, ErrBadInput
	}
	draws, err := c.store.LottoDrawsByDay(ctx, day, category)
	if err != nil {
		return nil, err
	}
	if draws == nil {
		draws = []model.LottoDraw{}
	}
	return draws, nil
}

func validLottoCategory(c string) bool {
	switch c {
	case model.LottoCatGov, model.LottoCatLao, model.LottoCatHanoi, model.LottoCatStock:
		return true
	}
	return false
}
