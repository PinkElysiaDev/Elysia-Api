package server

import (
	"context"
	"net/http"

	"github.com/elysia-api/backend/protocol"
)

// Gateway forwarding and Agent collection share the final outgoing boundary.
// Wire edits cannot change the authorized route or silently override caller intent.
func (candidate gatewayCandidate) finalizeRequestWire(ctx context.Context, wire protocol.Value, route protocol.ConversionContext, options protocol.EvaluationContext) (protocol.Value, error) {
	requestProtocol := candidate.compiled
	isTokenCount := candidate.operation.Kind == "count_tokens"
	hasWireRules := candidate.conversion.HasPhase(protocol.ConversionWire)
	if isTokenCount {
		requestProtocol, _ = candidate.compiled.TokenCountRequestProtocol(candidate.operationName)
	}
	if hasWireRules {
		var err error
		wire, err = candidate.conversion.ApplyValue(ctx, protocol.ConversionWire, wire, route, options.Diagnostics)
		if err != nil {
			return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, err}
		}
	}
	if hasWireRules || isTokenCount {
		decoded, err := requestProtocol.DecodeRequestValue(ctx, wire, options)
		if err != nil {
			return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, err}
		}
		if isTokenCount && !decoded.Model.IsString(candidate.model.Identifier()) {
			return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, gatewayIssue(candidate.compiled.Identity(), protocol.ResourceScopeMismatch, "/model", "counting wire changed the authorized model")}
		}
		if isTokenCount {
			err = protocol.IssuesError(protocol.CheckTokenCountRequest(decoded, candidate.compiled, candidate.operationName, candidate.binding, candidate.scope))
		} else {
			err = protocol.IssuesError(protocol.CheckRoute(decoded, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport))
		}
		if err != nil {
			return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, err}
		}
	}
	if err := requestProtocol.ValidateRequestOutput(ctx, wire, options); err != nil {
		return protocol.Value{}, err
	}
	if err := requestProtocol.CheckOperationValueInput(ctx, candidate.operationName, wire); err != nil {
		return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, err}
	}
	return wire, nil
}
