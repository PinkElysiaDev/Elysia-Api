package signal

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	lastHeartbeat   time.Time
	heartbeatMu     sync.RWMutex
	heartbeatTimeout = 300 * time.Second // 默认 300 秒
	shutdownTimer    *time.Timer
)

// HeartbeatStatus 心跳状态
type HeartbeatStatus struct {
	Timestamp int64  `json:"timestamp"`
	Status    string `json:"status"`
	Uptime    int64  `json:"uptime"`
}

// StartHeartbeatMonitor 启动心跳监控
func StartHeartbeatMonitor(timeout time.Duration) {
	heartbeatTimeout = timeout
	lastHeartbeat = time.Now()

	// 启动监控 goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			heartbeatMu.RLock()
			lastSeen := lastHeartbeat
			heartbeatMu.RUnlock()

			if time.Since(lastSeen) > heartbeatTimeout {
				log.Println("No heartbeat received, shutting down...")
				os.Exit(0)
			}
		}
	}()

	log.Printf("Heartbeat monitor started (timeout: %v)", heartbeatTimeout)
}

// HandleHeartbeat 处理心跳请求
func HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	heartbeatMu.Lock()
	lastHeartbeat = time.Now()
	heartbeatMu.Unlock()

	// 重置关闭定时器
	if shutdownTimer != nil {
		shutdownTimer.Stop()
	}
	shutdownTimer = time.AfterFunc(heartbeatTimeout, func() {
		log.Println("Heartbeat timeout, shutting down...")
		os.Exit(0)
	})

	// 返回状态
	status := HeartbeatStatus{
		Timestamp: time.Now().Unix(),
		Status:    "ok",
		Uptime:    time.Since(lastHeartbeat).Milliseconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// GetHeartbeatStatus 获取心跳状态（用于日志）
func GetHeartbeatStatus() map[string]interface{} {
	heartbeatMu.RLock()
	defer heartbeatMu.RUnlock()

	return map[string]interface{}{
		"last_heartbeat": lastHeartbeat.Format(time.RFC3339),
		"seconds_since":  int(time.Since(lastHeartbeat).Seconds()),
	}
}
