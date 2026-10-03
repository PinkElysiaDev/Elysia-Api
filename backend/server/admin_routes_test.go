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
		config:            &config.Config{},
		engine:            gin.New(),
		protocolTransport: relay.NewProtocolTransport(10 * time.Second),
	}
	s.setupRoutes()
	found := 0
	for _, route := range s.engine.Routes() {
		switch route.Path {
		case "/api/admin/protocols",
			"/api/admin/protocols/schema",
			"/api/admin/protocols/preview",
			"/api/admin/protocols/test",
			"/api/admin/protocols/:id/draft":
			found++
		}
	}
	if found != 6 {
		t.Fatalf("expected 6 custom-protocol route entries (PUT+DELETE share :id), got %d", found)
	}
}
