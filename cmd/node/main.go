package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/axmishell/mikvacepanel/internal/agent"
	"github.com/axmishell/mikvacepanel/internal/version"
)

func main() {
	cfg, err := agent.ParseConfig(os.Args[1:], version.String())
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)

	a, err := agent.New(cfg, logger)
	if err != nil {
		logger.Error("start agent", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("MikvAce node agent starting", "version", version.String(),
		"panel", cfg.PanelURL, "runtime", cfg.Runtime, "data", cfg.DataDir)
	if err := a.Run(ctx); err != nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
	logger.Info("agent stopped")
}

func logLevel(v string) slog.Level {
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
