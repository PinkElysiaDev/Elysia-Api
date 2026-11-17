package server

import "github.com/elysia-api/backend/protocol"

// The first eligible candidate owns its actual outbound inputs and encoding.
// Alternative candidates retain only semantic preparation until dispatch.
type gatewayRequestEncoding struct {
	value   protocol.Value
	request *protocol.Request
	options protocol.EvaluationContext
}

func (candidate gatewayCandidate) prepareRequestEncoding(fallback *protocol.Request, record *usageRecord) (*gatewayRequestEncoding, error) {
	if candidate.encoding != nil && candidate.encoding.request != nil {
		return candidate.encoding, nil
	}
	prepared := candidate.prepared
	if prepared == nil {
		prepared = fallback
	}
	request := prepared.Clone()
	request.Model = protocol.StringValue(candidate.model.Identifier())
	options, err := (protocol.EvaluationContext{InterleavedThinking: candidate.operation.IsInterleavedThinking, Scope: candidate.scope, Diagnostics: &protocol.DiagnosticSink{}, ClientOutput: request.ClientOutput}).WithOperation(record.RequestID, record.StartedAt.Unix())
	if err != nil {
		return nil, err
	}
	return &gatewayRequestEncoding{request: request, options: options}, nil
}

func (candidate gatewayCandidate) releaseRequestEncoding() {
	if candidate.encoding != nil {
		*candidate.encoding = gatewayRequestEncoding{}
	}
}
