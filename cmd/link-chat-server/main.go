// Command link-chat-server runs the chat server.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"golang.org/x/term"

	"link-chat/pkg/dirlock"
	"link-chat/pkg/server"
	"link-chat/pkg/server/live"
	"link-chat/pkg/storage"
	"link-chat/pkg/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		configPath = flag.String("config", "server.json", "path to the server config file")
		dataDir    = flag.String("data", "", "override the data directory from the config")
		genConfig  = flag.Bool("init", true, "write a default config file when none exists")
		showVer    = flag.Bool("version", false, "print the version and exit")
		plain      = flag.Bool("plain", false, "write ordinary logs instead of the live terminal dashboard")
		mkAdmin    = flag.String("create-admin", "", "create or promote this account to super administrator, then exit (server must be stopped)")
		resetPw    = flag.String("reset-password", "", "set a new password for this account, then exit (server must be stopped)")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("link-chat-server " + version.String())
		return 0
	}

	cfg, err := server.LoadConfig(*configPath, *genConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if err := cfg.EnsureDataDir(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// One process at a time owns the data directory: the server, or one of the
	// commands below (spec server-runtime / 数据目录独占).
	lock, err := dirlock.Acquire(cfg.DataDir)
	if err != nil {
		if errors.Is(err, dirlock.ErrLocked) {
			fmt.Fprintf(os.Stderr, "link-chat-server: %s is in use by another link-chat-server; stop it first\n", absOrSame(cfg.DataDir))
		} else {
			fmt.Fprintln(os.Stderr, "link-chat-server:", err)
		}
		return 1
	}
	defer lock.Release()

	if *mkAdmin != "" || *resetPw != "" {
		return runAdminCommand(cfg.DataDir, *mkAdmin, *resetPw)
	}

	// The admin overview shows the last lines of the same log, formatted the
	// same way but without colour.
	ring := server.NewLogRing()
	level := parseLevel(cfg.LogLevel)
	// The terminal is written through a queue: a console that stops reading
	// (a Windows QuickEdit selection pauses stderr) must not stall requests
	// that log, which once left sign-ins hanging with no reply.
	stderr := server.NewAsyncWriter(os.Stderr)
	defer stderr.Close(2 * time.Second)
	plainLogger := slog.New(server.NewTeeHandler(
		server.NewLogHandler(stderr, level, server.ColorWanted(os.Stderr)),
		server.NewLogHandler(ring, level, false),
	))
	slog.SetDefault(plainLogger)

	st, err := storage.Open(cfg.DataDir)
	if err != nil {
		plainLogger.Error("open storage", "err", err)
		return 1
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var screen tcell.Screen
	var logs *live.LogSink
	logger := plainLogger
	if useLiveTerminal(*plain, term.IsTerminal(int(os.Stdout.Fd()))) {
		screen, err = live.Open()
		if err != nil {
			plainLogger.Warn("cannot open live terminal", "err", err)
		} else {
			logs = live.NewLogSink()
			logger = slog.New(server.NewTeeHandler(
				server.NewLogHandler(logs, level, false),
				server.NewLogHandler(ring, level, false),
			))
			slog.SetDefault(logger)
		}
	}
	srv := server.New(cfg, st, logger)
	srv.SetLogRing(ring)
	logger.Info("starting link_chat server",
		"version", version.String(),
		"min_client_version", minClientLabel(cfg),
		"admin", adminLabel(cfg),
		"data_dir", absOrSame(cfg.DataDir),
		"heartbeat", cfg.HeartbeatSeconds,
		"max_message_bytes", cfg.MaxMessageBytes,
	)

	if screen == nil {
		err = srv.ListenAndServe(ctx)
	} else {
		dashboardCtx, closeDashboard := context.WithCancel(context.Background())
		finished := make(chan error, 1)
		go func() {
			finished <- srv.ListenAndServe(ctx)
			closeDashboard()
		}()
		live.Run(dashboardCtx, screen, srv, logs, stop)
		err = <-finished
		slog.SetDefault(plainLogger)
	}
	if err != nil {
		plainLogger.Error("server stopped", "err", err)
		return 1
	}
	plainLogger.Info("bye")
	return 0
}

func useLiveTerminal(plain, stdout bool) bool { return !plain && stdout }

// runAdminCommand performs -create-admin or -reset-password and returns the
// exit status.
func runAdminCommand(dataDir, mkAdmin, resetPw string) int {
	st, err := storage.Open(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "link-chat-server:", err)
		return 1
	}
	defer st.Close()
	if mkAdmin != "" {
		res, err := server.CreateAdmin(st, mkAdmin, readPassword)
		if err != nil {
			fmt.Fprintln(os.Stderr, "link-chat-server:", err)
			return 1
		}
		if res.Created {
			fmt.Printf("created super administrator %s\n", res.Username)
		} else {
			fmt.Printf("%s is now the super administrator (password unchanged)\n", res.Username)
		}
		return 0
	}
	if err := server.ResetPasswordCLI(st, resetPw, readPassword); err != nil {
		fmt.Fprintln(os.Stderr, "link-chat-server:", err)
		return 1
	}
	fmt.Printf("password for %s changed; all of its sessions were signed out\n", resetPw)
	return 0
}

// stdin is shared so successive prompts read successive lines when input is
// piped rather than typed.
var stdin = bufio.NewReader(os.Stdin)

// readPassword prompts on stderr and reads without echo when stdin is a
// terminal, or one line otherwise, so the command can also be scripted.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// minClientLabel is what the startup log shows for the client floor.
func minClientLabel(cfg server.Config) string {
	if !cfg.HasMinClient {
		return "none"
	}
	return cfg.MinClient.String()
}

// adminLabel is what the startup log shows for the console switch.
func adminLabel(cfg server.Config) string {
	if cfg.AdminEnabled {
		return "/admin/"
	}
	return "off"
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
