package server

import (
	"net/http"
	"strings"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

var publicProtocolIDs = map[relay.FormatType]string{
	relay.FormatOpenAIChat: "chat-completions-api",
	relay.FormatResponses:  "responses-api",
	relay.FormatClaude:     "anthropic-api",
	relay.FormatGemini:     "gemini-api",
}

// serveVersionedPublicIngress is the staged cutover point. C15 installs the
// verified public revisions; C16 removes callers' legacy branch entirely.
func (s *Server) serveVersionedPublicIngress(c *gin.Context) bool {
	if s.store == nil {
		return false
	}
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(c, err)
		return true
	}
	format := inputFormatFromPath(c.Request.URL.Path)
	if format == relay.FormatOpenAI {
		format = relay.FormatOpenAIChat
	}
	view := service.View()
	ingress, exists := view.Pin(publicProtocolIDs[format])
	if !exists {
		return false
	}
	if ingress.Identity().Family != string(format) {
		respondProtocolError(c, gatewayIssue(ingress.Identity(), protocol.InvalidDefinition, "/family", "public endpoint requires its documented wire family"))
		return true
	}
	if format == relay.FormatResponses {
		configuration := s.config.GetResponsesConfig()
		if configuration.Enabled != nil && !*configuration.Enabled {
			respondFail(c, http.StatusNotFound, "unsupported_endpoint", "Responses API is disabled")
			return true
		}
	}
	body, err := protocol.ReadBoundedBody(c.Request.Body, protocol.DefaultLimits().BufferBytes)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_input", err.Error())
		return true
	}
	path := c.Request.URL.Path
	// Existing base URLs can include /v1 or /v1beta; operation paths remain
	// relative to that configured base. Try only these documented API prefixes.
	for _, candidate := range []string{path, strings.TrimPrefix(path, "/v1"), strings.TrimPrefix(path, "/v1beta")} {
		for _, operation := range ingress.Operations() {
			if _, matches := protocol.MatchOperationPath(operation.Path, candidate); matches {
				s.serveProtocolRequest(c, view, ingress, candidate, body)
				return true
			}
		}
	}
	s.serveProtocolRequest(c, view, ingress, path, body)
	return true
}
