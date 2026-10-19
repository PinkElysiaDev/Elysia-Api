package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpServerName            = "elysia-api-agent"
	mcpServerVersion         = "1.0.0"
	mcpModernProtocolVersion = "2026-07-28"
	mcpInstructions          = "MCP 只提供 elysia_cli，用于执行 Elysia API 网关运维命令。首次使用先查 elysia help。每次调用无状态；需要复用协议草稿或测试目标时，请把相关命令放在同一次 command 批处理中。"
)

// The SDK owns JSON-RPC, discovery, version/header validation, tool dispatch,
// progress framing and request-scoped session cleanup. Keep only gateway policy
// (authentication is applied by the router) outside the transport.
func (s *Server) handleMCP(c *gin.Context) {
	if origin := strings.TrimSpace(c.Request.Header.Get("Origin")); origin != "" && !s.mcpOriginAllowed(c, origin) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}
	// The SDK also supports legacy handshakes with an absent version header.
	// This endpoint deliberately exposes only the explicit 2026-07-28 contract.
	if c.Request.Method == http.MethodPost && c.GetHeader("MCP-Protocol-Version") == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "MCP-Protocol-Version header is required"})
		return
	}
	s.mcpOnce.Do(func() { s.mcpHandler = s.newMCPHandler() })
	c.Header("X-Accel-Buffering", "no")
	s.mcpHandler.ServeHTTP(c.Writer, c.Request)
}

func (s *Server) newMCPHandler() http.Handler {
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
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, MaxRequestBodyBytes: agentMaxBodyBytes,
		PropagateRequestCancellation: true,
	})
}

// mcpOriginAllowed 校验浏览器 Origin（同源或本机回环）。非浏览器客户端
// 不带 Origin，天然放行。
func (s *Server) mcpOriginAllowed(c *gin.Context, origin string) bool {
	if strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://[::1]:") {
		return true
	}
	if publicURL := strings.TrimSpace(s.publicBaseURL(c)); publicURL != "" && origin == publicURL {
		return true
	}
	if at := strings.Index(origin, "://"); at > 0 {
		if host := origin[at+3:]; host == c.Request.Host {
			return true
		}
	}
	return false
}

// publicBaseURL 从配置或请求推导对外基础地址（Agent Card / Origin 校验共用）。
func (s *Server) publicBaseURL(c *gin.Context) string {
	if configured := strings.TrimSpace(s.config.GetAgentRemote().PublicURL); configured != "" {
		return configured
	}
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}
