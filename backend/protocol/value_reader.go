package protocol

import "strings"

const requestViewMinBytes = 64 << 10

// valueReader is scoped to one decode. Small fragments are detached immediately;
// sharing is useful only for payloads that account for a substantial part of the
// root. Public ReadObject/Decode continue to return independent child storage.
type valueReader struct {
	minimumSharedBytes     int
	index                  *valueIndex
	requireCapturedOrigins bool
	canGrowThreshold       bool
	isBorrowed             bool
}

func minimumSharedValueBytes(parentBytes int) int {
	return max(requestViewMinBytes, parentBytes/4)
}

// Constructed raw containers can exceed the original input's largest Value.
// An encoder's private reader retains the largest threshold across bindings;
// borrowed decoder readers receive only a local adjustment.
func (reader *valueReader) forParent(value Value) *valueReader {
	if reader == nil || reader.isBorrowed {
		return reader
	}
	minimum := minimumSharedValueBytes(value.Len())
	if minimum <= reader.minimumSharedBytes {
		return reader
	}
	if reader.canGrowThreshold {
		reader.minimumSharedBytes = minimum
		return reader
	}
	parent := *reader
	parent.minimumSharedBytes = minimum
	return &parent
}

func requestValueReader(root Value) *valueReader {
	reader := &valueReader{minimumSharedBytes: minimumSharedValueBytes(root.Len())}
	if len(root.raw) >= requestViewMinBytes {
		reader.index = &valueIndex{root: root.raw, minimumBytes: reader.minimumSharedBytes}
	}
	return reader
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
	if reader != nil && (reader.isBorrowed || len(raw) >= reader.minimumSharedBytes) {
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
	detachResourceIDs(request.Resources)
	detachCacheValues(request.Cache)
	detachNodeResources(request.Content)
	for index := range request.Tools {
		detachCacheValues(request.Tools[index].Cache)
	}
}

func detachEventResources(event *Event) {
	if event.Request != nil {
		detachRequestResources(event.Request)
	}
	if event.Item != nil {
		detachNodeResource(event.Item)
	}
	if event.Response != nil {
		detachNodeResources(event.Response.Content)
	}
	if event.Media != nil {
		event.Media.Reference.ID.raw = strings.Clone(event.Media.Reference.ID.raw)
	}
}

func detachResourceIDs(resources []Resource) {
	for index := range resources {
		resources[index].ID.raw = strings.Clone(resources[index].ID.raw)
	}
}

func detachCacheValues(intents []CacheIntent) {
	for index := range intents {
		intent := &intents[index]
		intent.Value.raw = strings.Clone(intent.Value.raw)
		intent.TTL.raw = strings.Clone(intent.TTL.raw)
		if intent.Resource != nil {
			intent.Resource.ID.raw = strings.Clone(intent.Resource.ID.raw)
		}
	}
}

func detachNodeResources(nodes []Node) {
	for index := range nodes {
		detachNodeResource(&nodes[index])
	}
}

func detachNodeResource(node *Node) {
	detachResourceIDs(node.Resources)
	detachCacheValues(node.Cache)
	detachNodeResources(node.Children)
	detachNodeResources(node.ReasoningContent)
}
