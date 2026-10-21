package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/protocol"
)

var publicProtocolIDs = map[builtin.FormatType]string{
	builtin.FormatOpenAIChat: protocol.PresetChatCompletionsID,
	builtin.FormatResponses:  protocol.PresetResponsesID,
	builtin.FormatClaude:     protocol.PresetAnthropicID,
	builtin.FormatGemini:     protocol.PresetGeminiID,
}

func (s *Server) serveVersionedPublicIngress(ctx context.Context, c *app.RequestContext) {
	if !s.requireProtocolRuntime(ctx, c) {
		return
	}
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	format := inputFormatFromPath(string(c.Path()))
	if format == builtin.FormatOpenAI {
		format = builtin.FormatOpenAIChat
	}
	view := service.View()
	ingress, exists := view.Pin(publicProtocolIDs[format])
	if !exists {
		respondFail(ctx, c, http.StatusServiceUnavailable, "inactive_protocol", "public endpoint requires its verified active protocol revision")
		return
	}

	if ingress.Identity().Family != string(format) {
		respondProtocolError(ctx, c, gatewayIssue(ingress.Identity(), protocol.InvalidDefinition, "/family", "public endpoint requires its documented wire family"))
		return
	}
	if format == builtin.FormatResponses {
		configuration := s.config.GetResponsesConfig()
		if configuration.Enabled != nil && !*configuration.Enabled {
			respondFail(ctx, c, http.StatusNotFound, "unsupported_endpoint", "Responses API is disabled")
			return
		}
	}
	defer s.releaseGatewayPreparation(c)
	body, ok := s.readGatewayRequest(ctx, c, ingress)
	if !ok {
		return
	}
	path := string(c.Path())
	// Existing base URLs can include /v1 or /v1beta; operation paths remain
	// relative to that configured base. Try only these documented API prefixes.
	for _, candidate := range []string{path, strings.TrimPrefix(path, "/v1"), strings.TrimPrefix(path, "/v1beta")} {
		for _, operation := range ingress.OperationRoutes() {
			if _, matches := protocol.MatchOperationPath(operation.Path, candidate); matches {
				s.serveProtocolRequest(ctx, c, view, ingress, candidate, body)
				return
			}
		}
	}
	s.serveProtocolRequest(ctx, c, view, ingress, path, body)
}
