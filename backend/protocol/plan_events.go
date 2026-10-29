package protocol

import (
	"context"
	"fmt"
)

// Event batches keep the original per-member contract boundary. A malformed
// member must report its own decoding error, not an offset in a rebuilt array.
func decodePlannedEvents(ctx context.Context, compiled *Compiled, direction Direction, input Value, options EvaluationContext) ([]Event, error) {
	output, err := evaluateCoreOutput(ctx, compiled, direction, input, options)
	if err != nil {
		return nil, err
	}
	var items []JSONOutput
	switch plannedJSONType(output) {
	case "object":
		items = []JSONOutput{output}
	case "array":
		items, err = coreArrayItems(output)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("expected an array")
	}
	events := make([]Event, len(items))
	reader := coreOutputReader(output, nil)
	for index, item := range items {
		event, err := decodeCoreOutputWithin(item, writeCoreEvent, reader)
		if err != nil {
			return nil, err
		}
		events[index] = *event
	}
	return events, nil
}
