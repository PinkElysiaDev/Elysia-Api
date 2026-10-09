package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// upstreamHTTPFailure distinguishes an observed HTTP rejection from a 502
// generated while translating a successful HTTP response. Only the former may retry.
type upstreamHTTPFailure struct{ *gatewayFailure }

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
	return &upstreamHTTPFailure{encodeGatewayFailure(ctx, status, response, ingress, options)}
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
