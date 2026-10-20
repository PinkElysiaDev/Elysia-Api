package server

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c.Request)
		accessToken, ok := s.findAccessToken(token)
		message := ""
		if !ok {
			message = "Incorrect API key provided"
		} else if accessTokenHasScope(accessToken, storage.TokenScopeAgent) {
			message = "This API key is reserved for AI assistant remote control"
		}
		if message != "" {
			c.Abort()
			// Inference authentication errors use the caller's wire protocol.
			writeProtocolError(c, inputFormatFromPath(c.Request.URL.Path), &builtin.GatewayError{
				Class: builtin.ErrorClassAuthentication, Message: message,
			})
			return
		}
		c.Set("elysiaKeyName", accessToken.Name)
		c.Set("elysiaKeyHash", shortTokenHash(token))
		c.Set("elysiaAllowedGroups", accessToken.AllowedGroups)
		c.Next()
	}
}

func (s *Server) dashboardAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.config.IsValidPanelAccessToken(extractAccessToken(c.Request)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "panel access token is not configured or invalid",
			})
			return
		}
		c.Next()
	}
}

// All remote Agent surfaces share scope checks and WWW-Authenticate responses.
func (s *Server) agentRemoteAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		accessToken, ok := s.findAccessToken(extractAccessToken(c.Request))
		if !ok {
			c.Header("WWW-Authenticate", `Bearer realm="elysia-agent"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing API key"})
			return
		}
		if !accessTokenHasScope(accessToken, storage.TokenScopeAgent) {
			c.Header("WWW-Authenticate", `Bearer realm="elysia-agent", scope="agent"`)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "this API key does not carry the agent scope"})
			return
		}
		c.Set("elysiaKeyName", accessToken.Name)
		c.Next()
	}
}

func (s *Server) agentRemoteGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.config.GetAgentRemote().AgentRemoteEnabled() {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "agent remote surface is disabled"})
			return
		}
		c.Next()
	}
}

func accessTokenHasScope(token config.AccessToken, scope string) bool {
	return slices.Contains(token.Scopes, scope)
}

func extractAccessToken(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	for _, header := range []string{"x-api-key", "x-goog-api-key"} {
		if key := strings.TrimSpace(r.Header.Get(header)); key != "" {
			return key
		}
	}
	if key := strings.TrimSpace(r.URL.Query().Get("key")); key != "" {
		return key
	}
	// The WebUI writes encodeURIComponent values; Request.Cookie does not decode.
	if cookie, err := r.Cookie("panel_access_token"); err == nil {
		if decoded, err := url.QueryUnescape(cookie.Value); err == nil {
			return strings.TrimSpace(decoded)
		}
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func (s *Server) loopbackOnly(handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isLoopbackRequest(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "reload endpoint is only available from loopback",
			})
			return
		}
		handler(c)
	}
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) tokenAllowsGroup(c *gin.Context, groupName string) bool {
	allowed := c.GetStringSlice("elysiaAllowedGroups")
	return len(allowed) == 0 || slices.Contains(allowed, groupName)
}
