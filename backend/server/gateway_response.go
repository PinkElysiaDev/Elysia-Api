package server

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// Response delivery uses the same narrow handoff as streaming; request history
// and retry candidates belong exclusively to preparation and dispatch.
func (s *Server) forwardGatewayResponse(ctx context.Context, c *app.RequestContext, record *usageRecord, plan *gatewayResponsePlan, candidate gatewayCandidate, response *http.Response, responseRoute protocol.ConversionContext, options protocol.EvaluationContext) error {
	body, err := protocol.ReadBoundedBody(response.Body, candidate.compiled.ResourceLimits().BufferBytes)
	if err != nil {
		return protocol.AnnotateResourceError(err, candidate.compiled.Identity(), protocol.DecodeResponse, "read.response")
	}
	record.ProviderResponse = record.sanitizeBody(body)
	semantic, err := candidate.compiled.DecodeResponse(ctx, body, options)
	if err != nil {
		return err
	}
	if err := observeHostedTools(record, candidate.compiled, body); err != nil {
		return err
	}
	// DecodeResponse has already validated usage arithmetic and capabilities.
	updateRecordProtocolUsage(record, semantic.Usage)
	if err := protocol.CheckGenerationOutcome(semantic); err != nil {
		return projectGatewayFailure(ctx, http.StatusBadGateway, semantic, plan.ingress, candidate, options)
	}
	if err := protocol.IssuesError(protocol.CheckModelResponse(semantic, candidate.compiled, candidate.binding, candidate.scope)); err != nil {
		return err
	}
	if err := s.captureContinuationResponse(ctx, candidate.continuation, semantic); err != nil {
		return err
	}
	// Recovery evidence is created while capturing this response. Only the
	// outbound model is a dispatch snapshot; refresh mutable recovery context now.
	route := candidate.conversionContext(plan.ingress, true)
	route.ResponseModel = responseRoute.ResponseModel
	route, originErr := route.WithObservedResponseModel(body)
	if originErr != nil {
		return originErr
	}
	if candidate.conversion != nil {
		semantic, err = candidate.conversion.Response(ctx, semantic, route, options.Diagnostics)
		if err != nil {
			return err
		}
	}
	applyClientModelAlias(semantic, plan.clientModel)
	body, err = plan.ingress.EncodeResponse(ctx, semantic, options)
	if err != nil {
		return err
	}
	if candidate.conversion.HasPhase(protocol.ConversionWire) {
		body, err = candidate.conversion.Wire(ctx, body, candidate.conversionContext(plan.ingress, true), options.Diagnostics)
		if err != nil {
			return err
		}
		// The target decoder applies its declared structure and capabilities.
		if _, err = plan.ingress.DecodeResponse(ctx, body, options); err != nil {
			return err
		}
	}
	if candidate.continuation != nil {
		body, err = builtin.AttachContinuationCarriers(body, plan.ingress.Identity().Family, candidate.continuation.tokens)
		if err != nil {
			return err
		}
	}
	wireValue, err := protocol.ParseValue(body)
	if err != nil {
		return err
	}
	if err := plan.ingress.ValidateWireOutput(protocol.EncodeResponse, wireValue); err != nil {
		return route.AttributeResponseModelFailure(err, semantic, wireValue)
	}
	if len(body) > plan.ingress.ResourceLimits().BufferBytes {
		return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "continuation", Path: "/response", Reason: "response including continuation carriers exceeds target buffer limit"}})
	}
	record.FirstByteMs = time.Since(record.StartedAt).Milliseconds()
	c.Data(http.StatusOK, "application/json", body)
	return nil
}
