package main

import (
	"log"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/server"
)

func main() {
	if config.GlobalConfig == nil {
		log.Fatal("Config not loaded")
	}

	srv := server.New(config.GlobalConfig)

	log.Printf("Starting Elysia-API backend on %s:%d",
		config.GlobalConfig.Server.Host,
		config.GlobalConfig.Server.Port)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
