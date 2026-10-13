package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// upstreamHTTPFailure distinguishes an observed HTTP rejection from a 502
// generated while translating a successful HTTP response. Only the former may retry.
type upstreamHTTPFailure struct{ *gatewayFailure }

func generationFailureClass(err error) builtin.ErrorClass {
	var failure *protocol.GenerationFailure
	if !errors.As(err, &failure) {
		return ""
	}
	fields, parseErr := failure.Payload.ReadObject()
	if parseErr != nil {
		return ""
	}
	var class builtin.ErrorClass
	if fields["category"].Decode(&class) != nil {
		return ""
	}
	switch class {
	case builtin.ErrorClassInvalidRequest, builtin.ErrorClassAuthentication, builtin.ErrorClassPermission, builtin.ErrorClassModelNotFound, builtin.ErrorClassRateLimit, builtin.ErrorClassOverloaded, builtin.ErrorClassUpstream, builtin.ErrorClassServer:
		return class
	default:
		return ""
	}
}

func (failure *upstreamHTTPFailure) Unwrap() error { return failure.gatewayFailure }

func canRetryGeneration(err error) bool {
	var failure *upstreamHTTPFailure
	return errors.As(err, &failure) && isRetryableGenerationStatus(failure.status)
}

// mapGatewayHTTPFailure preserves the actual HTTP status and runs the declared
// response directions. An invalid failure mapping retains its diagnostics.
func mapGatewayHTTPFailure(ctx context.Context, record *usageRecord, status int, body []byte, ingress *protocol.Compiled, candidate gatewayCandidate, options protocol.EvaluationContext) error {
	response, err := candidate.compiled.DecodeHTTPFailure(ctx, status, body, options)
	if err != nil {
		return &upstreamHTTPFailure{&gatewayFailure{status, err}}
	}
	updateRecordProtocolUsage(record, response.Usage)
	return &upstreamHTTPFailure{projectGatewayFailure(ctx, status, response, ingress, candidate, options)}
}

func projectGatewayFailure(ctx context.Context, status int, response *protocol.Response, ingress *protocol.Compiled, candidate gatewayCandidate, options protocol.EvaluationContext) *gatewayFailure {
	original := protocol.CheckGenerationOutcome(response)
	projected, err := candidate.conversion.Response(ctx, response, candidate.conversionContext(ingress, true), options.Diagnostics)
	if err != nil {
		return &gatewayFailure{status, errors.Join(original, err)}
	}
	failure := encodeGatewayFailure(ctx, status, projected, ingress, options)
	var upstream *upstreamFailure
	if errors.As(failure.cause, &upstream) {
		upstream.cause = original
	} else {
		failure.cause = errors.Join(original, failure.cause)
	}
	return failure
}

func encodeGatewayFailure(ctx context.Context, status int, response *protocol.Response, ingress *protocol.Compiled, options protocol.EvaluationContext) *gatewayFailure {
	statusValue, err := protocol.EncodeValue(status)
	if err != nil {
		return &gatewayFailure{status, err}
	}
	options.Values = protocol.Object{"httpStatus": statusValue}
	encoded, err := ingress.EncodeResponse(ctx, response, options)
	if err != nil {
		// Preserve the readable provider cause without serializing its whole
		// error object (which may contain opaque details or private data).
		// Keep the conversion error in the chain for diagnostics/retry logic.
		if response != nil {
			fields, _ := response.Error.ReadObject()
			var message string
			if fields["message"].Decode(&message) == nil && message != "" {
				err = fmt.Errorf("upstream failure: %q; error conversion failed: %w", message, err)
			}
		}
		return &gatewayFailure{status, err}
	}
	return &gatewayFailure{status, &upstreamFailure{cause: protocol.CheckGenerationOutcome(response), body: encoded}}
}
