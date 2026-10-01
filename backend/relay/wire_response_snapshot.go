package relay

import (
	"fmt"
	"sort"

	"github.com/elysia-api/backend/protocol"
)

type wireResponseSnapshot struct {
	format   FormatType
	original protocol.Value
	semantic protocol.Object
	usage    protocol.Value
}

func captureWireResponse(response *MaheshvaraResponse, body []byte, adapter builtinWireAdapter) error {
	original, err := protocol.ParseValue(body)
	if err != nil {
		return err
	}
	semantic, err := snapshotObject(response)
	if err != nil {
		return err
	}
	if response.Error != nil {
		response.nativeSnapshot = &wireResponseSnapshot{format: adapter.format, original: original, semantic: semantic}
		return nil
	}
	normalized, err := adapter.encodeResponse(response)
	if err != nil {
		return err
	}
	value, err := protocol.ParseValue(normalized)
	if err != nil {
		return err
	}
	fields, err := value.ReadObject()
	if err != nil {
		return err
	}
	response.nativeSnapshot = &wireResponseSnapshot{format: adapter.format, original: original, semantic: semantic, usage: fields[wireUsagePath(adapter.format)]}
	return nil
}

func preserveWireResponse(response *MaheshvaraResponse, adapter builtinWireAdapter) ([]byte, error) {
	snapshot := response.nativeSnapshot
	current, err := snapshotObject(response)
	if err != nil {
		return nil, err
	}
	var mutations []protocol.Mutation
	for _, key := range unionObjectKeys(snapshot.semantic, current) {
		if snapshot.semantic[key] == current[key] {
			continue
		}
		switch key {
		case "id", "model":
			wireKey := key
			if adapter.format == FormatGemini {
				if key == "id" {
					wireKey = "responseId"
				} else {
					wireKey = "modelVersion"
				}
			}
			mutations = append(mutations, protocol.Mutation{Op: protocol.SetValue, Path: "/" + wireKey, Value: current[key]})
		case "usage":
			normalized, err := adapter.encodeResponse(response)
			if err != nil {
				return nil, err
			}
			value, err := protocol.ParseValue(normalized)
			if err != nil {
				return nil, err
			}
			fields, err := value.ReadObject()
			if err != nil {
				return nil, err
			}
			usageKey := wireUsagePath(adapter.format)
			original, err := snapshot.original.ReadObject()
			if err != nil {
				return nil, err
			}
			usage, err := overlayKnownUsage(original[usageKey], snapshot.usage, fields[usageKey])
			if err != nil {
				return nil, err
			}
			if !usage.IsZero() {
				mutations = append(mutations, protocol.Mutation{Op: protocol.SetValue, Path: "/" + usageKey, Value: usage})
			}
		default:
			return nil, &protocol.ConversionError{Issues: []protocol.ConversionIssue{{Code: protocol.UnsupportedNative, Severity: protocol.SeverityError,
				Protocol: adapter.Identity(), Direction: protocol.EncodeResponse, Stage: "encode", Path: "/" + key,
				Reason: "legacy response mutation has no native update rule", Suggestion: "Use explicit semantic node edits in the versioned engine."}}}
		}
	}
	if len(mutations) == 0 {
		return snapshot.original.Bytes(), nil
	}
	updated, err := protocol.ApplyMutations(snapshot.original, mutations, protocol.DefaultLimits())
	if err != nil {
		return nil, err
	}
	return updated.Bytes(), nil
}

func unionObjectKeys(left, right protocol.Object) []string {
	keys := make([]string, 0, len(left)+len(right))
	for key := range left {
		keys = append(keys, key)
	}
	for key := range right {
		if _, exists := left[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// overlayKnownUsage changes only counters modified since parsing. Unknown
// provider detail fields and explicit zero values keep their original presence.
func overlayKnownUsage(native, before, after protocol.Value) (protocol.Value, error) {
	if before == after {
		return native, nil
	}
	if after.IsZero() {
		return protocol.Value{}, fmt.Errorf("removing native usage requires an explicit delete operation")
	}
	if !native.IsObject() || !after.IsObject() {
		return after, nil
	}
	var previous protocol.Object
	if before.IsObject() {
		var err error
		previous, err = before.ReadObject()
		if err != nil {
			return protocol.Value{}, err
		}
	}
	updated, err := after.ReadObject()
	if err != nil {
		return protocol.Value{}, err
	}
	original, err := native.ReadObject()
	if err != nil {
		return protocol.Value{}, err
	}
	for _, key := range unionObjectKeys(previous, updated) {
		if previous[key] == updated[key] {
			continue
		}
		if updated[key].IsZero() {
			delete(original, key)
			continue
		}
		merged, err := overlayKnownUsage(original[key], previous[key], updated[key])
		if err != nil {
			return protocol.Value{}, err
		}
		original[key] = merged
	}
	return protocol.EncodeValue(original)
}
