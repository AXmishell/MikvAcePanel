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
	"strings"
	"syscall"
	"time"

	"github.com/axmishell/mikvacepanel/internal/server"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/version"
)

func main() {
	cfg := server.LoadConfig()
	addr := flag.String("addr", cfg.Addr, "HTTP listen address")
	dataDir := flag.String("data", cfg.DataDir, "data directory")
	logLevel := flag.String("log-level", cfg.LogLevel, "log level")
	flag.Parse()
	originalDataDir := cfg.DataDir
	cfg.Addr = *addr
	cfg.DataDir = *dataDir
	if cfg.DBPath == "" || *dataDir != originalDataDir {
		cfg.DBPath = *dataDir + "/panel.db"
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(*logLevel)}))
	slog.SetDefault(logger)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	app, err := server.New(cfg, st, logger)
	if err != nil {
		logger.Error("initialize server", "error", err)
		os.Exit(1)
	}
	defer app.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.Hub().Monitor(ctx)
	go scheduler(ctx, app, logger)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		logger.Info("MikvAce Panel listening", "addr", cfg.Addr, "version", version.String(),
			"data", cfg.DataDir)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown", "error", err)
	}
}

func scheduler(ctx context.Context, app *server.Server, logger *slog.Logger) {
	st := app.Store()
	// Run once at startup, then every five minutes for expiry checks and once
	// per hour for log/metric retention.
	expire := func() {
		n, err := st.ExpireUsers(ctx)
		if err != nil {
			logger.Warn("expire users", "error", err)
			return
		}
		if n > 0 {
			st.AddEvent(ctx, "user.expired", 0, "info", "Expired accounts disabled automatically")
			logger.Info("expired users disabled", "count", n)
		}
	}
	prune := func() {
		settings, _ := st.GetSettings(ctx)
		days := parseInt(settings["log_retention_days"], 14)
		if days < 1 {
			days = 14
		}
		if err := st.Prune(ctx, days, 60); err != nil {
			logger.Warn("prune", "error", err)
		}
	}
	expire()
	prune()
	app.MaybeAutoBackup(ctx)
	expireTicker := time.NewTicker(5 * time.Minute)
	pruneTicker := time.NewTicker(time.Hour)
	defer expireTicker.Stop()
	defer pruneTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-expireTicker.C:
			expire()
		case <-pruneTicker.C:
			prune()
			app.MaybeAutoBackup(ctx)
		}
	}
}

func parseLevel(v string) slog.Level {
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func parseInt(v string, def int) int {
	var n int
	if _, err := fmt.Sscan(strings.TrimSpace(v), &n); err == nil {
		return n
	}
	return def
}
