package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/arena/avaluna/internal/app"
	"github.com/arena/avaluna/internal/config"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/cron"
	"github.com/arena/avaluna/internal/database"
	"github.com/arena/avaluna/internal/queue"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	log := newLogger(cfg)

	created, err := cfg.EnsurePersistentSecrets()
	if err != nil {
		return fmt.Errorf("resolve secrets: %w", err)
	}
	if missing := cfg.MissingProductionSecrets(); len(missing) > 0 {
		if cfg.IsProduction() {
			log.Error("set these explicitly in production; the generated values are stored on disk",
				"missing", missing, "file", cfg.SecretsPath())
		} else if created {
			log.Warn("generated secrets and saved them for future boots",
				"missing", missing, "file", cfg.SecretsPath())
		}
	}

	for _, dir := range []string{cfg.BackupDir, cfg.UploadDir, filepath.Join("data", "invoices")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if cfg.AutoMigrate {
		if err := db.Migrate(ctx); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		log.Info("database migrated", "path", cfg.DatabasePath)
	}
	if cfg.AutoSeed && !db.Seeded(ctx) {
		start := time.Now()
		if err := database.Seed(ctx, db, cfg); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		log.Info("demo data seeded", "took", time.Since(start).Truncate(time.Millisecond).String())
	}

	c := core.New(cfg, db, log)
	if err := c.ReloadSettings(ctx); err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	var q *queue.Queue
	if cfg.EnableQueue {
		q = queue.New(c)
		c.Queue = q
	}

	var sched *cron.Scheduler
	if cfg.EnableCron {
		sched = cron.New(c)
		if err := cron.RegisterAll(sched); err != nil {
			return fmt.Errorf("register cron jobs: %w", err)
		}
		c.Cron = sched
	}

	a, err := app.New(c)
	if err != nil {
		return fmt.Errorf("build app: %w", err)
	}

	if q != nil {
		q.Start()
	}
	if sched != nil {
		sched.Start()
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	log.Info("server starting",
		"addr", addr, "env", cfg.AppEnv, "api", cfg.APIPrefix,
		"routes", len(a.Router().Routes()), "locales", cfg.SupportedLocales,
		"cron", cfg.EnableCron, "queue", cfg.EnableQueue)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.GracePeriod)
	defer shutdownCancel()

	if sched != nil {
		sched.Stop()
	}
	if q != nil {
		q.Stop()
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("server stopped cleanly")
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	var h slog.Handler
	opts := &slog.HandlerOptions{Level: level}
	if cfg.AppEnv == "production" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
