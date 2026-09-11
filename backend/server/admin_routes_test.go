package server

import (
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

// 协议设计器新增路由与既有 admin 路由注册不冲突（冲突会在启动时 panic）。
func TestAdminRoutesRegisterWithProtocolEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{
		config:        &config.Config{},
		engine:        gin.New(),
		openaiAdapter: relay.NewOpenAIAdapter(10 * time.Second),
	}
	s.setupRoutes()
	found := 0
	for _, route := range s.engine.Routes() {
		switch route.Path {
		case "/api/admin/custom-protocols",
			"/api/admin/custom-protocols/schema",
			"/api/admin/custom-protocols/preview",
			"/api/admin/custom-protocols/test",
			"/api/admin/custom-protocols/assist",
			"/api/admin/custom-protocols/:id":
			found++
		}
	}
	if found != 7 {
		t.Fatalf("expected 7 custom-protocol route entries (PUT+DELETE share :id), got %d", found)
	}
}
