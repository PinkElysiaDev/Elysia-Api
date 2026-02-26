//go:build !windows

package signal

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/elysia-api/backend/config"
)

var reloadHandler func() error

type ReloadHandler func() error

func SetupSIGHUP(handler func() error) {
	reloadHandler = handler
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGHUP)

	go func() {
		for range sigchan {
			log.Println("Received SIGHUP, reloading config...")
			reloadConfig()
		}
	}()
}

func reloadConfig() {
	if reloadHandler != nil {
		if err := reloadHandler(); err != nil {
			log.Printf("Failed to reload config: %v", err)
		} else {
			log.Println("Config reloaded successfully")
		}
	}
}

func init() {
	SetupSIGHUP(func() error {
		return config.GlobalConfig.Reload()
	})
}
