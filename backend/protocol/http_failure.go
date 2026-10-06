package protocol

import (
	"context"
	"fmt"
)

// DecodeHTTPFailure applies the same response decoder to a failed HTTP request.
// Authors can branch on context.httpStatus; a success-shaped error body is an
// upstream contract violation, never a successful generation.
func (compiled *Compiled) DecodeHTTPFailure(ctx context.Context, status int, body []byte, options EvaluationContext) (*Response, error) {
	if status >= 200 && status < 300 {
		return nil, fmt.Errorf("HTTP failure decoding requires a non-success status")
	}
	values := Object{}
	for name, value := range options.Values {
		values[name] = value
	}
	statusValue, err := EncodeValue(status)
	if err != nil {
		return nil, err
	}
	values["httpStatus"] = statusValue
	options.Values = values
	response, err := compiled.DecodeResponse(ctx, body, options)
	if err != nil {
		return nil, err
	}
	if CheckGenerationOutcome(response) == nil {
		return nil, compiled.runtimeError(DecodeResponse, streamIssue(UpstreamContractViolation, "/error", "non-success HTTP body did not decode to a declared generation failure"))
	}
	return response, nil
}
