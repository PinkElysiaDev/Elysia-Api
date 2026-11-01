package server

import (
	"context"
	"net/http"

	"github.com/elysia-api/backend/protocol"
)

// Gateway forwarding and Agent collection share the final outgoing boundary.
// Wire edits cannot change the authorized route or silently override caller intent.
func (candidate gatewayCandidate) finalizeRequestWire(ctx context.Context, wire protocol.Value, route protocol.ConversionContext, options protocol.EvaluationContext) (protocol.Value, error) {
	if candidate.conversion.HasPhase(protocol.ConversionWire) {
		var err error
		wire, err = candidate.conversion.ApplyValue(ctx, protocol.ConversionWire, wire, route, options.Diagnostics)
		if err != nil {
			return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, err}
		}
		decoded, err := candidate.compiled.DecodeRequestValue(ctx, wire, options)
		if err == nil {
			err = protocol.IssuesError(protocol.CheckRoute(decoded, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport))
		}
		if err != nil {
			return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, err}
		}
	}
	if err := candidate.compiled.ValidateRequestOutput(ctx, wire, options); err != nil {
		return protocol.Value{}, err
	}
	if err := candidate.compiled.CheckOperationValueInput(ctx, candidate.operationName, wire); err != nil {
		return protocol.Value{}, &gatewayFailure{http.StatusBadRequest, err}
	}
	return wire, nil
}
