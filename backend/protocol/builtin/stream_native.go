package builtin

import (
	"context"

	p "github.com/elysia-api/backend/protocol"
)

// ObserveNativeFrame keeps only the sequence watermark. It does not replay
// content into the renderer, buffer payloads or alter unknown native fields.
func (stream *streamModule) ObserveNativeFrame(ctx context.Context, frame p.Value, _ p.EvaluationContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if stream.name != Responses {
		return nil
	}
	fields, err := frame.ReadObject()
	if err != nil {
		return err
	}
	return stream.observeResponsesSequence(fields["sequence_number"])
}

func (stream *streamModule) observeResponsesSequence(value p.Value) error {
	if value.IsZero() {
		return nil
	}
	var sequence int64
	if err := value.Decode(&sequence); err != nil || value.IsNull() || sequence < 0 || (stream.hasSequence && sequence <= stream.sequence) {
		return unsupported("/sequence_number", "Responses event sequence must increase; repeated or reordered frames cannot be replayed")
	}
	stream.sequence, stream.hasSequence = sequence, true
	return nil
}
