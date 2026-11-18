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
	"link-chat/pkg/version"
)

func main() {
	var (
		configPath = flag.String("config", "server.json", "path to the server config file")
		dataDir    = flag.String("data", "", "override the data directory from the config")
		genConfig  = flag.Bool("init", true, "write a default config file when none exists")
		showVer    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("link-chat-server " + version.String())
		return
	}

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

	logger := slog.New(server.NewLogHandler(os.Stderr, parseLevel(cfg.LogLevel), server.ColorWanted(os.Stderr)))
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
		"version", version.String(),
		"min_client_version", minClientLabel(cfg),
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

// minClientLabel is what the startup log shows for the client floor.
func minClientLabel(cfg server.Config) string {
	if !cfg.HasMinClient {
		return "none"
	}
	return cfg.MinClient.String()
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
