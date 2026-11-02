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
	if candidate.compiled.CoreVersion() != protocol.ProtocolCoreVersion {
		if err := observeLegacyHostedTools(record, candidate.compiled, body); err != nil {
			return err
		}
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
	route := candidate.conversionContext(plan.ingress, true).WithOperation(options)
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
	wireValue, err := plan.ingress.EncodeResponseValue(ctx, semantic, options)
	if err != nil {
		return err
	}
	beforeWire := wireValue
	if candidate.conversion.HasPhase(protocol.ConversionWire) {
		wireValue, err = candidate.conversion.ApplyValue(ctx, protocol.ConversionWire, wireValue, route, options.Diagnostics)
		if err != nil {
			return err
		}
		if plan.ingress.CoreVersion() != protocol.ProtocolCoreVersion {
			if _, err = plan.ingress.DecodeResponseValue(ctx, wireValue, options); err != nil {
				return err
			}
		}
	}
	if candidate.continuation != nil && candidate.continuation.isPackage {
		options = options.WithContinuation(candidate.continuation.binding)
		wireValue, err = plan.ingress.EncodeContinuationResponse(ctx, wireValue, options)
		if err != nil {
			return err
		}
	} else {
		if candidate.continuation != nil {
			body, err = builtin.AttachContinuationCarriers(wireValue.Bytes(), plan.ingress.Identity().Family, candidate.continuation.tokens)
			if err != nil {
				return err
			}
			wireValue, err = protocol.ParseValue(body)
			if err != nil {
				return err
			}
		}
	}
	err = plan.ingress.ValidateResponseOutput(ctx, beforeWire, wireValue, options)
	if err != nil {
		return route.AttributeResponseModelFailure(err, semantic, wireValue)
	}
	if wireValue.Len() > plan.ingress.ResourceLimits().BufferBytes {
		return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "continuation", Path: "/response", Reason: "response including continuation carriers exceeds target buffer limit"}})
	}
	record.FirstByteMs = time.Since(record.StartedAt).Milliseconds()
	if candidate.continuation != nil && candidate.continuation.isPackage && candidate.continuation.binding.HasPending() {
		binding := candidate.continuation.binding
		state := ensureHTTPDeliveryState(c)
		onHTTPComplete(c, func() {
			if state.result.Outcome == deliveryCompleted {
				// Validation has confirmed every planned carrier. Failure here is
				// an internal ownership violation, not a recoverable I/O outcome.
				if err := binding.CommitDelivery(); err != nil {
					panic(err)
				}
			}
		})
	}
	c.Data(http.StatusOK, "application/json", wireValue.Bytes())
	return nil
}
