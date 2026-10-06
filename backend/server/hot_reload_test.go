package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
)

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func osWriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// #17：取消检查帮助函数——已断开的请求返回 true 并补全 499 记录字段。
func TestGatewayFailureRecordsClientCancel(t *testing.T) {
	s := &Server{}
	c, rec := chatRequestContext(`{}`)
	ctx, cancel := context.WithCancel(t.Context())
	c.Request = c.Request.WithContext(ctx)
	cancel()
	record := &usageRecord{RequestID: "cancel-probe"}
	s.failGateway(c, record, 502, ctx.Err())
	if record.StatusCode != 499 || record.ErrorKind != ErrorKindClientCanceled || rec.Code != 499 {
		t.Fatal(record, rec.Code)
	}
}

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
