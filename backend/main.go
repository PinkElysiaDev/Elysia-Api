package main

import (
	"io"
	"log"
	"os"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/server"
)

func main() {
	if config.GlobalConfig == nil {
		log.Fatal("Config not loaded")
	}

	srv := server.New(config.GlobalConfig)
	if os.Getenv("ELYSIA_PARENT_STDIN") == "1" {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			srv.RequestShutdown()
		}()
	}

	log.Printf("Starting Elysia-API backend on %s:%d",
		config.GlobalConfig.Server.Host,
		config.GlobalConfig.Server.Port)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
