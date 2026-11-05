package server

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// Public aliases and the generic gateway share the pinned models operation.
// The host owns catalog membership; external fields belong to its Mapping.
func (s *Server) serveModelCatalog(ctx context.Context, c *app.RequestContext, compiled *protocol.Compiled, path string) bool {
	for name, operation := range compiled.OperationRoutes() {
		if operation.Kind != "models" || operation.Method != string(c.Method()) {
			continue
		}
		if _, matches := protocol.MatchOperationPath(operation.Path, path); !matches {
			continue
		}
		if !compiled.CanEncodeModelPage(name) {
			s.writeIngressError(ctx, c, &builtin.GatewayError{Class: builtin.ErrorClassInvalidRequest, Status: http.StatusNotFound, Message: "protocol has no declared client catalog operation"})
			return true
		}
		groups := s.getGroups()
		page := protocol.ModelPage{Models: make([]protocol.DiscoveredModel, 0, len(groups))}
		for _, group := range groups {
			if group.Enabled {
				page.Models = append(page.Models, protocol.DiscoveredModel{ID: group.Name, Name: group.Name, MaxTokens: group.MaxTokens})
			}
		}
		output, err := compiled.EncodeModelPage(ctx, name, page)
		if err != nil {
			s.writeIngressError(ctx, c, &builtin.GatewayError{Class: builtin.ErrorClassServer, Message: "client model catalog could not be encoded"})
			s.logEvent("error").Err(err).Str("protocol", compiled.Identity().DefinitionID).Msg("client model catalog encoding failed")
			return true
		}
		c.Data(http.StatusOK, "application/json", output.Bytes())
		return true
	}
	return false
}

// This version selection is temporary until the production defaults retire the
// legacy handlers. A selected package never falls back after an encoding error.
func (s *Server) servePackageModelCatalog(ctx context.Context, c *app.RequestContext) bool {
	selection, ok := s.requireIngress(c)
	if !ok {
		return true
	}
	if selection.compiled.CoreVersion() != protocol.ProtocolCoreVersion {
		return false
	}
	if !s.serveModelCatalog(ctx, c, selection.compiled, selection.path) {
		s.writeIngressError(ctx, c, &builtin.GatewayError{Class: builtin.ErrorClassInvalidRequest, Status: http.StatusNotFound, Message: "protocol has no matching client catalog operation"})
	}
	return true
}
