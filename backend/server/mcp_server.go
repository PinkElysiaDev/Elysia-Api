package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpServerName            = "elysia-api-agent"
	mcpServerVersion         = "1.0.0"
	mcpModernProtocolVersion = "2026-07-28"
	mcpInstructions          = "MCP 只提供 elysia_cli，用于执行 Elysia API 网关运维命令。首次使用先查 elysia help。每次调用无状态；需要复用协议草稿或测试目标时，请把相关命令放在同一次 command 批处理中。"
)

// Hertz owns HTTP validation and SSE delivery; the SDK owns message dispatch,
// discovery and tools. Authentication is applied by the native router.
func (s *Server) handleMCP(ctx context.Context, c *app.RequestContext) {
	if origin := strings.TrimSpace(c.Request.Header.Get("Origin")); origin != "" && !s.mcpOriginAllowed(ctx, c, origin) {
		c.AbortWithStatusJSON(http.StatusForbidden, utils.H{"error": "origin not allowed"})
		return
	}
	// The SDK also supports legacy handshakes with an absent version header.
	// This endpoint deliberately exposes only the explicit 2026-07-28 contract.
	if string(c.Method()) == http.MethodPost && string(c.GetHeader("MCP-Protocol-Version")) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, utils.H{"error": "MCP-Protocol-Version header is required"})
		return
	}
	s.mcpOnce.Do(func() { s.mcpHandler = s.newMCPHandler() })
	c.Header("X-Accel-Buffering", "no")
	s.serveMCPRequest(ctx, c)
}

func (s *Server) newMCPHandler() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name: mcpServerName, Title: "Elysia API Gateway Agent", Version: mcpServerVersion,
	}, &mcp.ServerOptions{
		Instructions:              mcpInstructions,
		SupportedProtocolVersions: []string{mcpModernProtocolVersion},
		Capabilities:              &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		SetCacheable: func(_ context.Context, req mcp.Request, cache *mcp.Cacheable) {
			cache.CacheScope = "private"
			if _, ok := req.(*mcp.ListToolsRequest); ok {
				cache.TTLMs = 300_000
			}
		},
	})
	server.AddTool(mcpCLITool(), s.callMCPCLI)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			parent, ok := ctx.Value(mcpRequestContextKey{}).(context.Context)
			if !ok {
				return next(ctx, method, req)
			}
			ctx, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(parent, cancel)
			defer stop()
			defer cancel()
			if parent.Err() != nil {
				cancel()
			}
			return next(ctx, method, req)
		}
	})
	return server
}

// mcpOriginAllowed 校验浏览器 Origin（同源或本机回环）。非浏览器客户端
// 不带 Origin，天然放行。
func (s *Server) mcpOriginAllowed(ctx context.Context, c *app.RequestContext, origin string) bool {
	if strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://[::1]:") {
		return true
	}
	if publicURL := strings.TrimSpace(s.publicBaseURL(ctx, c)); publicURL != "" && origin == publicURL {
		return true
	}
	if at := strings.Index(origin, "://"); at > 0 {
		if host := origin[at+3:]; host == string(c.Host()) {
			return true
		}
	}
	return false
}

// publicBaseURL 从配置或请求推导对外基础地址（Agent Card / Origin 校验共用）。
func (s *Server) publicBaseURL(ctx context.Context, c *app.RequestContext) string {
	if configured := strings.TrimSpace(s.config.GetAgentRemote().PublicURL); configured != "" {
		return configured
	}
	scheme := "http"
	if string(c.URI().Scheme()) == "https" || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + string(c.Host())
}
