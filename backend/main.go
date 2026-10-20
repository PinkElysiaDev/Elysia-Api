package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/server"
)

func main() {
	if err := run(); err != nil {
		logging.Emergency("backend stopped with an error", err)
		os.Exit(1)
	}
}

// run owns process-level initialization. Importing config or server never parses
// command-line flags, creates configuration files, or terminates the process.
func run() error {
	configPath := flag.String("config", "config.json", "Path to config file")
	flag.Parse()
	if *configPath == "" {
		*configPath = "config.json"
	}
	cfg, created, err := config.EnsureConfig(*configPath)
	if err != nil {
		return err
	}
	logging.Default().Configure(cfg.GetLoggingOptions())
	if created {
		logging.Info().Str("path", *configPath).Msg("created default config with a generated panelAccessToken")
		logging.Info().Msgf("Generated panelAccessToken: %s", cfg.GetPanelAccessToken())
		logging.Info().Msgf("Please review %s and rotate the token before exposing the service.", *configPath)
	}
	logging.Info().Str("component", "config").Msg("config loaded successfully")

	// Capture termination even while startup is opening stores and preparing
	// protocols. All shutdown sources use the server's RequestShutdown entry point.
	signalContext, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()
	srv := server.New(cfg)
	logging.SetDefault(srv.Logger())
	go func() { <-signalContext.Done(); srv.RequestShutdown() }()
	if os.Getenv("ELYSIA_PARENT_STDIN") == "1" {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			srv.RequestShutdown()
		}()
	}

	logging.Info().Str("version", server.BuildIdentity()).Str("host", cfg.Host).Int("port", cfg.Port).Msg("starting Elysia-API backend")

	return srv.ListenAndServe()
}
