package server

import (
	"context"
	"reflect"

	"github.com/elysia-api/backend/protocol"
)

// gatewayRequestEncoding is single-consumer, shared only by copies of the same
// private candidate. A plan keeps at most its first eligible candidate's body.
type gatewayRequestEncoding struct {
	value       protocol.Value
	request     *protocol.Request
	identity    protocol.Identity
	scope       protocol.Scope
	limits      protocol.Limits
	interleaved bool
	issues      []protocol.ConversionIssue
}

func (candidate gatewayCandidate) takeRequestValue(ctx context.Context, request *protocol.Request, options protocol.EvaluationContext) (protocol.Value, bool) {
	encoding := candidate.encoding
	if encoding == nil || encoding.value.IsZero() {
		return protocol.Value{}, false
	}
	value := encoding.value
	defer candidate.releaseRequestEncoding()
	if ctx.Err() != nil || candidate.model.CacheSynthesis || candidate.compiled.Identity() != encoding.identity || options.Scope != encoding.scope || options.InterleavedThinking != encoding.interleaved || candidate.compiled.RequestLimits(ctx) != encoding.limits || !reflect.DeepEqual(request, encoding.request) {
		return protocol.Value{}, false
	}
	for _, issue := range encoding.issues {
		options.Diagnostics.Add(issue)
	}
	return value, true
}

func (candidate gatewayCandidate) releaseRequestEncoding() {
	if candidate.encoding != nil {
		candidate.encoding.value = protocol.Value{}
		candidate.encoding.request = nil
		candidate.encoding.issues = nil
	}
}
