package protocol

import "strings"

const requestViewMinBytes = 64 << 10

// valueReader is scoped to one decode. Small fragments are detached immediately;
// sharing is useful only for payloads that account for a substantial part of the
// root. Public ReadObject/Decode continue to return independent child storage.
type valueReader struct{ minimumSharedBytes int }

func requestValueReader(root Value) *valueReader {
	return &valueReader{minimumSharedBytes: max(requestViewMinBytes, len(root.raw)/4)}
}

// WithValueReader scopes large immutable views to a trusted adapter invocation.
// Containers are always caller-owned and small values are detached. Do not keep
// this context or large views beyond the invocation unless they intentionally
// own the payload; detach any values saved as long-lived resource identifiers.
// The public Value.ReadObject and Value.Decode ownership contracts are unchanged.
func (options EvaluationContext) WithValueReader(root Value) EvaluationContext {
	options.valueReader = requestValueReader(root)
	return options
}

func (reader *valueReader) fragment(raw string) string {
	if reader != nil && len(raw) >= reader.minimumSharedBytes {
		return raw
	}
	return strings.Clone(raw)
}

// ReadObject lets trusted adapters borrow large immutable request fragments.
// Outside a request decode it retains Value.ReadObject's ownership behavior.
func (options EvaluationContext) ReadObject(value Value) (Object, error) {
	return value.readObjectWithin(options.valueReader)
}

// ReadArray preserves the builtin adapter's absent/null distinction while
// allowing the same request-owned views as ReadObject.
func (options EvaluationContext) ReadArray(value Value) ([]Value, error) {
	return value.readArrayWithin(options.valueReader)
}

// Resource IDs can survive in continuation storage. Even a large signature
// must own its bytes instead of pinning the original request behind a slice.
func detachRequestResources(request *Request) {
	detach := func(resources []Resource) {
		for index := range resources {
			resources[index].ID.raw = strings.Clone(resources[index].ID.raw)
		}
	}
	cache := func(intents []CacheIntent) {
		for index := range intents {
			intent := &intents[index]
			intent.Value.raw = strings.Clone(intent.Value.raw)
			intent.TTL.raw = strings.Clone(intent.TTL.raw)
			if intent.Resource != nil {
				intent.Resource.ID.raw = strings.Clone(intent.Resource.ID.raw)
			}
		}
	}
	var nodes func([]Node)
	nodes = func(items []Node) {
		for index := range items {
			detach(items[index].Resources)
			cache(items[index].Cache)
			nodes(items[index].Children)
			nodes(items[index].ReasoningContent)
		}
	}
	detach(request.Resources)
	cache(request.Cache)
	nodes(request.Content)
	for index := range request.Tools {
		cache(request.Tools[index].Cache)
	}
}
