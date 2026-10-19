package server

import (
	"context"
	"reflect"

	"github.com/elysia-api/backend/protocol"
)

// gatewayRequestEncoding is single-consumer, shared only by copies of the same
// private candidate. A plan keeps at most its first eligible candidate's body.
type gatewayRequestEncoding struct {
	body        []byte
	request     *protocol.Request
	identity    protocol.Identity
	scope       protocol.Scope
	limits      protocol.Limits
	interleaved bool
	issues      []protocol.ConversionIssue
}

func (candidate gatewayCandidate) takeRequestEncoding(ctx context.Context, request *protocol.Request, options protocol.EvaluationContext) ([]byte, bool) {
	encoding := candidate.encoding
	if encoding == nil || encoding.body == nil {
		return nil, false
	}
	body := encoding.body
	defer candidate.releaseRequestEncoding()
	if ctx.Err() != nil || candidate.model.CacheSynthesis || candidate.compiled.Identity() != encoding.identity || options.Scope != encoding.scope || options.InterleavedThinking != encoding.interleaved || candidate.compiled.RequestLimits(ctx) != encoding.limits || !reflect.DeepEqual(request, encoding.request) {
		return nil, false
	}
	for _, issue := range encoding.issues {
		options.Diagnostics.Add(issue)
	}
	return body, true
}

func (candidate gatewayCandidate) releaseRequestEncoding() {
	if candidate.encoding != nil {
		candidate.encoding.body = nil
		candidate.encoding.request = nil
		candidate.encoding.issues = nil
	}
}
