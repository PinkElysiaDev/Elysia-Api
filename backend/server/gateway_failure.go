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

// failureDiagnostics keeps secondary failures inspectable without including an
// unrecognized provider body or decoder internals in the public error message.
type failureDiagnostics struct{ primary, secondary error }

func (failure *failureDiagnostics) Error() string { return failure.primary.Error() }
func (failure *failureDiagnostics) Unwrap() []error {
	return []error{failure.primary, failure.secondary}
}

func attachFailureDiagnostics(primary, secondary error) error {
	if secondary == nil {
		return primary
	}
	return &failureDiagnostics{primary, secondary}
}

// decodeUpstreamHTTPFailure gives authored mappings first refusal. An HTTP
// rejection remains a failure even if its body is outside the declared wire.
func decodeUpstreamHTTPFailure(ctx context.Context, status int, body []byte, compiled *protocol.Compiled, options protocol.EvaluationContext) (*protocol.Response, error) {
	response, err := compiled.DecodeHTTPFailure(ctx, status, body, options)
	if err == nil {
		return response, nil
	}
	return fallbackHTTPFailure(status, body), err
}

func fallbackHTTPFailure(status int, body []byte) *protocol.Response {
	message := fmt.Sprintf("上游请求失败（HTTP %d）", status)
	if status == 524 {
		wire, parseErr := protocol.ParseValue(body)
		fields, objectErr := wire.ReadObject()
		var hasCloudflareError bool
		var bodyStatus int
		if parseErr == nil && objectErr == nil && fields["cloudflare_error"].Decode(&hasCloudflareError) == nil && hasCloudflareError && fields["status"].Decode(&bodyStatus) == nil && bodyStatus == status && fields["error_name"].IsString("origin_response_timeout") {
			message = "上游源站响应超时（HTTP 524）"
		}
	}
	payload, _ := protocol.EncodeValue(protocol.Object{"category": protocol.StringValue(string(builtin.ClassFromStatus(status))), "message": protocol.StringValue(message)})
	return &protocol.Response{SchemaVersion: 1, Error: payload}
}

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
	response, secondary := decodeUpstreamHTTPFailure(ctx, status, body, candidate.compiled, options)
	updateRecordProtocolUsage(record, response.Usage)
	failure := projectGatewayFailure(ctx, status, response, ingress, candidate, options)
	failure.cause = attachFailureDiagnostics(failure.cause, secondary)
	return &upstreamHTTPFailure{failure}
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
