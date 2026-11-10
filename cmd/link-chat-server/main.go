// Command link-chat-server runs the chat server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"link-chat/pkg/server"
	"link-chat/pkg/storage"
)

func main() {
	var (
		configPath = flag.String("config", "server.json", "path to the server config file")
		dataDir    = flag.String("data", "", "override the data directory from the config")
		genConfig  = flag.Bool("init", true, "write a default config file when none exists")
	)
	flag.Parse()

	cfg, err := server.LoadConfig(*configPath, *genConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if err := cfg.EnsureDataDir(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	st, err := storage.OpenJSON(cfg.DataDir)
	if err != nil {
		logger.Error("open storage", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, st, logger)
	logger.Info("starting link_chat server",
		"data_dir", absOrSame(cfg.DataDir),
		"heartbeat", cfg.HeartbeatSeconds,
		"max_message_bytes", cfg.MaxMessageBytes,
	)

	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
	logger.Info("bye")
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func absOrSame(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
