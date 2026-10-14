package server

import (
	"errors"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
)

type transportFailure struct {
	Kind  string                `json:"kind"`
	Phase relay.StreamIdlePhase `json:"phase"`
}

// recordFailureEvidence keeps transport and conversion evidence independent of
// the primary provider failure. The caller retains HTTP and cancellation policy.
func recordFailureEvidence(record *usageRecord, err error) {
	var idle *relay.StreamIdleError
	if errors.As(err, &idle) {
		record.TransportFailure = &transportFailure{Kind: ErrorKindUpstreamIdleTimeout, Phase: idle.Phase}
		record.ErrorKind = ErrorKindUpstreamIdleTimeout
	}
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		record.appendConversionIssues(conversion.Issues)
		if len(conversion.Issues) > 0 && record.TransportFailure == nil {
			record.ErrorKind = string(conversion.Issues[0].Code)
		}
	}
	if class := generationFailureClass(err); class != "" {
		record.ErrorKind = string(class)
	} else if record.UpstreamHTTPStatus >= 400 {
		record.ErrorKind = ErrorKindUpstream
	}
}
