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
	configPath := flag.String("config", server.DefaultPanelConfigPath(), "path to panel.yaml")
	flag.Parse()
	cfg, err := server.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load config", "error", err, "path", *configPath)
		os.Exit(1)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)
	// /install uses this hook after saving a different database backend. The
	// process re-executes itself so the store, migrations and every component
	// holding it are rebuilt from the new panel.yaml.
	cfg.Restart = func() {
		if err := restartProcess(); err != nil {
			logger.Error("restart after database change", "error", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.DBDriver == server.SetupDriver {
		app := server.NewSetup(cfg, logger)
		defer app.Close()
		logger.Info("setup mode: no database selected", "config", cfg.ConfigPath, "path", "/install")
		serveHTTP(ctx, cfg, app.Handler(), logger, stop)
		return
	}

	st, err := store.OpenConfig(store.Config{
		Driver:          cfg.DBDriver,
		DSN:             cfg.DBDSN,
		Path:            cfg.DBPath,
		MaxOpenConns:    cfg.DBMaxOpen,
		MaxIdleConns:    cfg.DBMaxIdle,
		ConnMaxLifetime: cfg.DBConnMaxLife,
	})
	if err != nil {
		logger.Error("open database", "error", err, "driver", cfg.DBDriver)
		os.Exit(1)
	}
	defer st.Close()
	logger.Info("database ready", "driver", st.Dialect().Name(), "config", cfg.ConfigPath)

	app, err := server.New(cfg, st, logger)
	if err != nil {
		logger.Error("initialize server", "error", err)
		os.Exit(1)
	}
	defer app.Close()
	go app.Hub().Monitor(ctx)
	go scheduler(ctx, app, logger)
	serveHTTP(ctx, cfg, app.Handler(), logger, stop)
}

// serveHTTP runs the listener and waits for SIGINT/SIGTERM or a listener
// failure. Both the setup-only app and the full panel share this path.
func serveHTTP(ctx context.Context, cfg server.Config, handler http.Handler, logger *slog.Logger, stop context.CancelFunc) {
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
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
		n, err := app.ReconcileAccess(ctx)
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
