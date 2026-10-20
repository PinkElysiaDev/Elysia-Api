package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

// The application adapter uses verified generation bindings without retries.
// Missing contracts and rate limits provide no evidence to change availability.
func (s *Server) probeModelHealth(ctx context.Context, model storage.Model, timeoutSeconds int) healthProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	candidate, request, err := s.prepareHealthProbe(probeCtx, model)
	if err != nil {
		s.logEvent("warn").Str("model_name", model.Name).Err(err).Msg("health check: model has no usable probe contract")
		return probeUnavailable
	}
	_, err = s.collectProtocolGenerationAttempt(probeCtx, candidate, request, nil, nil)
	var failure *gatewayFailure
	if errors.As(err, &failure) && failure.status == http.StatusTooManyRequests {
		return probeUnavailable
	}
	if err != nil {
		return probeUnhealthy
	}
	return probeHealthy
}

func (s *Server) prepareHealthProbe(ctx context.Context, model storage.Model) (gatewayCandidate, *protocol.Request, error) {
	service, err := s.protocolService()
	if err != nil {
		return gatewayCandidate{}, nil, err
	}
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return gatewayCandidate{}, nil, err
	}
	ref := config.ModelRef{ID: model.ID, Name: model.Name, SourceID: model.SourceID, BaseURL: model.BaseURL, APIKey: model.APIKey}
	view := service.View()
	var candidate gatewayCandidate
	var failure *protocol.ConversionError
	for _, transport := range []protocol.Transport{protocol.HTTPJSON, protocol.SSE, protocol.NDJSON} {
		candidate, failure = makeGatewayCandidate(view, bindings, ref, transport, "generate")
		if failure == nil {
			break
		}
	}
	if failure != nil {
		return candidate, nil, failure
	}
	if candidate.operation.Kind != "generate" {
		return candidate, nil, gatewayIssue(candidate.compiled.Identity(), protocol.UnsupportedCapability, "/operations", "background health probes require synchronous generation")
	}
	budget, err := protocol.EncodeValue(HealthProbeMaxTokens)
	if err != nil {
		return candidate, nil, err
	}
	stream, err := protocol.EncodeValue(candidate.operation.Transport != protocol.HTTPJSON)
	if err != nil {
		return candidate, nil, err
	}
	request := &protocol.Request{
		SchemaVersion: protocol.SemanticSchemaVersion,
		Source:        protocol.Identity{Family: "elysia-health", WireVersion: "1"},
		Model:         protocol.StringValue(model.Name),
		Content:       []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("ping")}}}},
		Parameters:    protocol.Object{"max_output_tokens": budget, "stream": stream},
	}
	if err := protocol.IssuesError(protocol.CheckRoute(request, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport)); err != nil {
		return candidate, nil, err
	}
	body, err := candidate.compiled.EncodeRequest(ctx, request, protocol.EvaluationContext{Scope: candidate.scope})
	if err != nil {
		return candidate, nil, err
	}
	return candidate, request, candidate.compiled.CheckOperationInput(ctx, candidate.operationName, body)
}
