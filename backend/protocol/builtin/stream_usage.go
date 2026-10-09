package builtin

import p "github.com/elysia-api/backend/protocol"

// decodeUsageUpdate retains the provider's components before normalization.
// A cache/reasoning detail can arrive after its subtotal, so adding already
// normalized snapshots would double count or miss the late component.
func (stream *streamModule) decodeUsageUpdate(value p.Value) (*p.Usage, error) {
	update, err := stream.module.decodeUsage(value)
	if err != nil || update == nil {
		return update, err
	}
	fields, err := value.ReadObject()
	if err != nil {
		return nil, err
	}
	merged := copyFields(stream.usageFields)
	for key, value := range fields {
		if previous := merged[key]; previous.IsObject() && value.IsObject() {
			before, _ := previous.ReadObject()
			after, _ := value.ReadObject()
			for name, count := range after {
				before[name] = count
			}
			value = object(before)
		}
		merged[key] = value
	}
	snapshot := object(merged)
	size := len(snapshot.Bytes())
	buffered := stream.buffered + size - stream.usageBytes
	if buffered > stream.limits.BufferBytes {
		return nil, unsupported("/usage", "usage state exceeds the stream buffer limit")
	}
	normalized, err := stream.module.decodeUsage(snapshot)
	if err != nil {
		return nil, err
	}
	switch stream.name {
	case Anthropic:
		if !fields["cache_creation"].IsZero() && update.CacheCreation == nil {
			update.CacheCreation = normalized.CacheCreation
		}
		if update.Input != nil || update.CacheRead != nil || update.CacheCreation != nil {
			update.Input = normalized.Input
			if count, exists := normalized.Details["uncached_input_tokens"]; exists {
				update.Details["uncached_input_tokens"] = count
			}
		}
	case Gemini:
		if update.Output != nil || !fields["thoughtsTokenCount"].IsZero() || !fields["promptTokenCount"].IsZero() || !fields["totalTokenCount"].IsZero() {
			update.Output = normalized.Output
		}
	}
	stream.usageFields, stream.usageBytes, stream.buffered = merged, size, buffered
	return p.MergeUsage(nil, update), nil
}
