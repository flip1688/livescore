// livescore API server: serves football data from Redis/MongoDB and runs the
// thscore sync worker in-process. See docs/thscore-api.md for upstream limits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flip1688/livescore/internal/cache"
	"github.com/flip1688/livescore/internal/config"
	"github.com/flip1688/livescore/internal/handler"
	"github.com/flip1688/livescore/internal/service"
	"github.com/flip1688/livescore/internal/storage"
	"github.com/flip1688/livescore/internal/store"
	"github.com/flip1688/livescore/internal/thscore"
	"github.com/flip1688/livescore/internal/ws"
)

func main() {
	once := flag.String("once", "", "run a single sync job (dictionary|logos|schedule|schedule-ahead|schedule-modify|live-snapshot|live-changes|events-stats|standings|analysis) and exit")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log, *once); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, once string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	st, err := store.New(ctx, cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		return err
	}
	defer st.Close(context.Background())
	if err := st.EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("ensure indexes: %w", err)
	}

	c, err := cache.New(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer c.Close()

	catalog := service.NewCatalog(st, c, log, cfg.DictionaryTTL)

	hub := ws.New(log, catalog.Snapshot)
	go hub.Run(ctx)

	var logos *service.LogoMirror
	if cfg.R2AccountID != "" {
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
		logos = service.NewLogoMirror(r2, cfg.LogoDNSServer, log)
		log.Info("logo mirroring enabled", "bucket", cfg.R2Bucket)
	} else {
		log.Warn("R2 not configured — logo mirroring disabled, storing thscore's source URLs")
	}

	if once != "" {
		if cfg.ThscoreAPIKey == "" {
			return fmt.Errorf("-once requires THSCORE_API_KEY")
		}
		ts := thscore.New(cfg.ThscoreBaseURL, cfg.ThscoreAPIKey)
		syncer := service.NewSyncer(ts, st, c, nil, logos, log)
		log.Info("running one-shot sync", "job", once)
		return syncer.RunOnce(ctx, once)
	}

	if cfg.ThscoreAPIKey != "" {
		ts := thscore.New(cfg.ThscoreBaseURL, cfg.ThscoreAPIKey)
		syncer := service.NewSyncer(ts, st, c, hub, logos, log)
		go syncer.Run(ctx)
		log.Info("sync worker started", "upstream", cfg.ThscoreBaseURL)
	} else {
		log.Warn("THSCORE_API_KEY not set — sync worker disabled, serving stored data only")
	}

	root := http.NewServeMux()
	root.Handle("GET /ws", hub.Handler(cfg.WSAllowedOrigins))
	root.Handle("/", handler.New(catalog, log, cfg.CORSAllowedOrigins).Routes())

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
