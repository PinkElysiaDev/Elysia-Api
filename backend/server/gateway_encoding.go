package server

import (
	"context"
	"reflect"

	"github.com/elysia-api/backend/protocol"
)

// gatewayRequestEncoding is single-consumer, shared only by copies of the same
// private candidate. A plan keeps at most its first eligible candidate's body.
type gatewayRequestEncoding struct {
	value    protocol.Value
	request  *protocol.Request
	compiled *protocol.Compiled
	limits   protocol.Limits
	options  protocol.EvaluationContext
	issues   []protocol.ConversionIssue
}

func newGatewayRequestEncoding(ctx context.Context, compiled *protocol.Compiled, request *protocol.Request, value protocol.Value, options protocol.EvaluationContext) *gatewayRequestEncoding {
	issues := options.Diagnostics.Issues()
	options.Diagnostics = nil
	return &gatewayRequestEncoding{value: value, request: request, compiled: compiled, limits: compiled.RequestLimits(ctx), options: options, issues: issues}
}

func (candidate gatewayCandidate) takeRequestValue(ctx context.Context, request *protocol.Request, options protocol.EvaluationContext) (protocol.Value, bool) {
	encoding := candidate.encoding
	if encoding == nil || encoding.value.IsZero() {
		return protocol.Value{}, false
	}
	value := encoding.value
	defer candidate.releaseRequestEncoding()
	sink := options.Diagnostics
	options.Diagnostics = nil
	if ctx.Err() != nil || candidate.model.CacheSynthesis || candidate.compiled != encoding.compiled || candidate.compiled.RequestLimits(ctx) != encoding.limits || !reflect.DeepEqual(options, encoding.options) || !reflect.DeepEqual(request, encoding.request) {
		return protocol.Value{}, false
	}
	for _, issue := range encoding.issues {
		sink.Add(issue)
	}
	return value, true
}

func (candidate gatewayCandidate) releaseRequestEncoding() {
	if candidate.encoding != nil {
		candidate.encoding.value = protocol.Value{}
		candidate.encoding.request = nil
		candidate.encoding.options = protocol.EvaluationContext{}
		candidate.encoding.issues = nil
	}
}
