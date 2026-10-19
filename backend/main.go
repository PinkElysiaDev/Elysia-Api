package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/server"
)

func main() {
	if config.GlobalConfig == nil {
		logging.Error().Msg("config not loaded")
		os.Exit(1)
	}

	// Capture termination even while startup is opening stores and preparing
	// protocols. All shutdown sources use the server's RequestShutdown entry point.
	signalContext, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()
	srv := server.New(config.GlobalConfig)
	logging.SetDefault(srv.Logger())
	go func() { <-signalContext.Done(); srv.RequestShutdown() }()
	if os.Getenv("ELYSIA_PARENT_STDIN") == "1" {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			srv.RequestShutdown()
		}()
	}

	logging.Info().Str("version", server.BuildIdentity()).Str("host", config.GlobalConfig.Host).Int("port", config.GlobalConfig.Port).Msg("starting Elysia-API backend")

	if err := srv.ListenAndServe(); err != nil {
		logging.Emergency("server stopped with an error", err)
		os.Exit(1)
	}
}
