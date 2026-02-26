//go:build windows

package signal

import (
	"log"
	"os"
	"time"

	"github.com/elysia-api/backend/config"
)

var reloadHandler func() error

// SetupSIGHUP 在 Windows 上启动文件监控
func SetupSIGHUP(handler func() error) {
	reloadHandler = handler
	log.Println("Running on Windows: using file watcher for config reload")
}

// StartFileWatcher 启动文件监控
func StartFileWatcher(filePath string, interval int) {
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		var lastModTime time.Time

		for range ticker.C {
			info, err := os.Stat(filePath)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastModTime) && !lastModTime.IsZero() {
				log.Println("Config file changed, reloading...")
				if reloadHandler != nil {
					if err := reloadHandler(); err != nil {
						log.Printf("Failed to reload config: %v", err)
					} else {
						log.Println("Config reloaded successfully")
					}
				}
			}
			lastModTime = info.ModTime()
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
	// 默认 5 秒检查一次配置文件变化
	StartFileWatcher("config.json", 5)
}
