package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/gin-gonic/gin"
)

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func osWriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// #17：取消检查帮助函数——已断开的请求返回 true 并补全 499 记录字段。
func TestAbortRetryOnClientCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{}
	start := time.Now().Add(-50 * time.Millisecond)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := contextWithCancel()
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	cancel() // 模拟客户端断开

	record := &usageRecord{RequestID: "cancel-probe"}
	if !s.abortRetryOnClientCancel(c, record, start) {
		t.Fatal("canceled context must return true")
	}
	if record.StatusCode != 499 || record.Error == "" || record.EndedAt.IsZero() || record.DurationMs <= 0 {
		t.Fatalf("record must be completed for 499 cancel: %+v", record)
	}

	// 未断开：返回 false 且不动记录。
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	record2 := &usageRecord{RequestID: "alive-probe"}
	if s.abortRetryOnClientCancel(c2, record2, start) {
		t.Fatal("live context must return false")
	}
	if record2.StatusCode != 0 || record2.Error != "" {
		t.Fatalf("live context must not touch the record: %+v", record2)
	}
}

// #16：健康检查周期热更新——Reload 修改 intervalSeconds 后 probeInterval 即时反映。
func TestHealthCheckerIntervalHotReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile := func(content string) {
		if err := osWriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(`{"healthCheck":{"enabled":true,"intervalSeconds":300}}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	h := newHealthChecker(&Server{config: cfg})
	if got := h.probeInterval(); got != 300*time.Second {
		t.Fatalf("initial interval = %s, want 300s", got)
	}

	writeFile(`{"healthCheck":{"enabled":true,"intervalSeconds":60}}`)
	if err := cfg.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := h.probeInterval(); got != 60*time.Second {
		t.Fatalf("reloaded interval = %s, want 60s (hot reload)", got)
	}
}
