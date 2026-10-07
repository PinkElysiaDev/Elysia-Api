package server

import (
	"net/http"
	"strings"

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

var publicProtocolIDs = map[builtin.FormatType]string{
	builtin.FormatOpenAIChat: protocol.PresetChatCompletionsID,
	builtin.FormatResponses:  protocol.PresetResponsesID,
	builtin.FormatClaude:     protocol.PresetAnthropicID,
	builtin.FormatGemini:     protocol.PresetGeminiID,
}

func (s *Server) serveVersionedPublicIngress(c *gin.Context) {
	if !s.requireProtocolRuntime(c) {
		return
	}
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	format := inputFormatFromPath(c.Request.URL.Path)
	if format == builtin.FormatOpenAI {
		format = builtin.FormatOpenAIChat
	}
	view := service.View()
	ingress, exists := view.Pin(publicProtocolIDs[format])
	if !exists {
		respondFail(c, http.StatusServiceUnavailable, "inactive_protocol", "public endpoint requires its verified active protocol revision")
		return
	}

	if ingress.Identity().Family != string(format) {
		respondProtocolError(c, gatewayIssue(ingress.Identity(), protocol.InvalidDefinition, "/family", "public endpoint requires its documented wire family"))
		return
	}
	if format == builtin.FormatResponses {
		configuration := s.config.GetResponsesConfig()
		if configuration.Enabled != nil && !*configuration.Enabled {
			respondFail(c, http.StatusNotFound, "unsupported_endpoint", "Responses API is disabled")
			return
		}
	}
	body, err := protocol.ReadBoundedBody(c.Request.Body, ingress.ResourceLimits().BufferBytes)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	path := c.Request.URL.Path
	// Existing base URLs can include /v1 or /v1beta; operation paths remain
	// relative to that configured base. Try only these documented API prefixes.
	for _, candidate := range []string{path, strings.TrimPrefix(path, "/v1"), strings.TrimPrefix(path, "/v1beta")} {
		for _, operation := range ingress.Operations() {
			if _, matches := protocol.MatchOperationPath(operation.Path, candidate); matches {
				s.serveProtocolRequest(c, view, ingress, candidate, body)
				return
			}
		}
	}
	s.serveProtocolRequest(c, view, ingress, path, body)
}
