// Command logo-sync is the standalone logo backfill: it mirrors every
// pending league/team logo (stored logo_url differs from the expected
// mirrored URL) from thscore's CDN into R2 and stamps logo_url on the Mongo
// docs. It needs only MONGO_URI and the R2_* vars — no Redis, no thscore API
// key — so it can run anywhere, anytime, and re-running is always safe
// (idempotent; failed downloads simply stay pending for the next run).
//
// Note: it does not evict the API's Redis dictionary cache; served logo URLs
// catch up within DICTIONARY_TTL (default 6h) or on the next dictionary sync.
//
// Usage: go run ./cmd/logo-sync   (reads the same env vars as cmd/api)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/flip1688/livescore/internal/config"
	"github.com/flip1688/livescore/internal/service"
	"github.com/flip1688/livescore/internal/storage"
	"github.com/flip1688/livescore/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.R2AccountID == "" {
		return fmt.Errorf("logo-sync requires the R2_* env vars")
	}

	st, err := store.New(ctx, cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		return err
	}
	defer st.Close(context.Background())

	r2, err := storage.New(ctx, storage.Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		Bucket:          cfg.R2Bucket,
		PublicBaseURL:   cfg.R2PublicBaseURL,
	})
	if err != nil {
		return fmt.Errorf("init r2 storage: %w", err)
	}
	logos := service.NewLogoMirror(r2, cfg.LogoDNSServer, log)

	done, err := service.SyncLogos(ctx, st, logos, log)
	if err != nil {
		return err
	}
	log.Info("logo sync finished", "stamped", done)
	return nil
}
