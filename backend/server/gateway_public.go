package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func (s *Server) serveVersionedPublicIngress(ctx context.Context, c *app.RequestContext) {
	selection, ok := s.requireIngress(c)
	if !ok {
		return
	}
	view, ingress := selection.view, selection.compiled
	if s.serveTokenCounting(ctx, c, selection) {
		return
	}
	if ingress.Identity().DefinitionID == protocol.PresetResponsesID {
		configuration := s.config.GetResponsesConfig()
		if configuration.Enabled != nil && !*configuration.Enabled {
			s.writeIngressError(ctx, c, &builtin.GatewayError{Class: builtin.ErrorClassModelNotFound, Status: http.StatusNotFound, Message: "Responses API is disabled"})
			return
		}
	}
	defer s.releaseGatewayPreparation(c)
	body, ok := s.readGatewayRequest(ctx, c, ingress)
	if !ok {
		return
	}
	path := string(c.Path())
	if ingress.CoreVersion() == protocol.ProtocolCoreVersion {
		s.serveProtocolRequest(ctx, c, view, ingress, path, body)
		return
	}
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
