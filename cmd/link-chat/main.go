// Command link-chat is the terminal chat client.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/app"
)

func main() {
	var (
		instance   = flag.String("as", app.DefaultInstance, "client instance name; each instance keeps its own credentials")
		configPath = flag.String("config", "", "path to the client config file (overrides -as)")
		serverFlag = flag.String("server", "", "server address, overrides the config")
	)
	flag.Parse()

	// Without -config, the instance name decides which credentials file this
	// process uses, so two clients on one machine can be two different accounts
	// (design.md decision 1).
	path := *configPath
	if path == "" {
		var err error
		path, err = app.InstanceConfigPath(*instance)
		if err != nil {
			fmt.Fprintf(os.Stderr, "link_chat: %v\n", err)
			os.Exit(1)
		}
	}

	cfg, err := app.LoadConfig(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "link_chat: %v\n", err)
		os.Exit(1)
	}
	// The instance name is what keys this client's credential, so it must match
	// the file the config came from. Without this, every instance would overwrite
	// the same credential entry.
	cfg.Instance = *instance
	if cfg.Instance == "" {
		cfg.Instance = app.DefaultInstance
	}
	if *serverFlag != "" {
		cfg.Server = *serverFlag
	}

	scr, err := tcell.NewScreen()
	if err != nil {
		fmt.Fprintf(os.Stderr, "link_chat: %v\n", err)
		os.Exit(1)
	}
	if err := scr.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "link_chat: %v\n", err)
		os.Exit(1)
	}
	defer scr.Fini()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := app.New(scr, cfg, path)
	// The instance name is part of the identity: it is what the server shows the
	// person who gets replaced, so they can tell which client took over.
	a.SetDevice(*instance)
	if err := a.Run(ctx); err != nil {
		scr.Fini()
		fmt.Fprintf(os.Stderr, "link_chat: %v\n", err)
		os.Exit(1)
	}
}
