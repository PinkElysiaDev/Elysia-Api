package protocol

import "context"

// verifyArrayPrefix compares retained elements without joining previous fragments.
// The caller supplies the complete snapshot length and its immutable element reader.
func verifyArrayPrefix(ctx context.Context, expected []Value, length, offset int, current func(int) (Value, error)) (int, error) {
	if len(expected) > length-offset {
		return offset, streamIssue(UpstreamContractViolation, "/buffer", "array snapshot removed retained items")
	}
	for _, previous := range expected {
		if err := ctx.Err(); err != nil {
			return offset, err
		}
		value, err := current(offset)
		if err != nil {
			return offset, err
		}
		if !equalValues(previous, value) {
			return offset, streamIssue(UpstreamContractViolation, "/buffer", "array snapshot changed the retained prefix")
		}
		offset++
	}
	return offset, nil
}
