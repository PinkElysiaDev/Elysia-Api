package agent

import (
	"fmt"
	"math"

	"github.com/elysia-api/backend/protocol"
)

// addUsage sums independent model calls. A missing component in any call makes
// that component unknown for the turn; partial counts must not look complete.
func addUsage(total, next *protocol.Usage) (*protocol.Usage, error) {
	if total == nil || next == nil {
		return nil, nil
	}
	result := &protocol.Usage{}
	for _, field := range []struct {
		target      **protocol.Counter
		left, right *protocol.Counter
	}{
		{&result.Input, total.Input, next.Input}, {&result.Output, total.Output, next.Output},
		{&result.Total, total.Total, next.Total}, {&result.CacheRead, total.CacheRead, next.CacheRead},
		{&result.CacheCreation, total.CacheCreation, next.CacheCreation},
	} {
		counter, err := addCounter(field.left, field.right)
		if err != nil {
			return nil, err
		}
		*field.target = counter
	}
	for name, left := range total.Details {
		if right, exists := next.Details[name]; exists {
			counter, err := addCounter(&left, &right)
			if err != nil {
				return nil, err
			}
			if result.Details == nil {
				result.Details = map[string]protocol.Counter{}
			}
			result.Details[name] = *counter
		}
	}
	return result, nil
}

func addCounter(left, right *protocol.Counter) (*protocol.Counter, error) {
	if left == nil || right == nil {
		return nil, nil
	}
	if left.Count > math.MaxInt64-right.Count {
		return nil, fmt.Errorf("Agent turn usage exceeds supported integer range")
	}
	origin := protocol.ObservedCount
	if left.Origin == protocol.InferredCount || right.Origin == protocol.InferredCount {
		origin = protocol.InferredCount
	}
	return &protocol.Counter{Count: left.Count + right.Count, Origin: origin}, nil
}
