package builtin

import (
	"errors"

	p "github.com/elysia-api/backend/protocol"
)

// The shared envelope also serves source decoding. Final wire failures belong
// to the target mapping, including frames synthesized or changed by after.
func validateResponsesMetadataOutput(frame p.Value) error {
	err := p.ValidateResponsesMetadataFrame(frame)
	var failure *p.ConversionError
	if errors.As(err, &failure) {
		for i := range failure.Issues {
			issue := &failure.Issues[i]
			issue.Direction, issue.Stage = p.EncodeEvent, "wire.output"
			issue.Suggestion = "Correct the metadata envelope produced by the target mapping."
		}
	}
	return err
}

func (stream *streamModule) decodeResponsesMetadata(frame p.Value, fields p.Object) ([]p.Event, error) {
	if err := p.ValidateResponsesMetadataFrame(frame); err != nil {
		return nil, err
	}
	if !stream.isStarted || stream.isFinished || stream.hasFailed {
		return nil, unsupported("/type", "response metadata requires an active response")
	}
	if stream.id.IsZero() || stream.id.IsNull() || stream.id.IsString("") {
		return nil, unsupported("/response_id", "response metadata cannot establish a missing response identity")
	}
	if err := stream.sourceIdentity.bind(0, "/response_id", fields["response_id"], stream.limits.BufferBytes); err != nil {
		return nil, err
	}
	if err := stream.observeResponsesSequence(fields["sequence_number"]); err != nil {
		return nil, err
	}
	// Native frame ownership keeps this auxiliary payload out of ordinary
	// response metadata, tool results and continuation history.
	return []p.Event{{Type: p.NativeEvent, ResponseID: fields["response_id"], Sequence: fields["sequence_number"]}}, nil
}
