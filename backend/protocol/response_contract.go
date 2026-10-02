package protocol

import "fmt"

// CheckResponse validates observable response semantics before client encoding.
// Tool input and usage validation share the same rules as request and event paths.
func CheckResponse(response *Response, target Target, limits Limits) []ConversionIssue {
	check := capabilityCheck{target: target, limits: limits, calls: map[string]struct{}{}, results: map[string]struct{}{}}
	if response == nil || response.SchemaVersion != SemanticSchemaVersion {
		check.add(InvalidInput, "/schemaVersion", "", "missing response or unsupported semantic version")
		return check.issues
	}
	check.content(response.Content, "/content", 1)
	check.usage(response.Usage, "/usage")
	return check.issues
}

func (check *capabilityCheck) usage(usage *Usage, path string) {
	if usage == nil {
		return
	}
	check.require(UsageCapability, path)
	validate := func(counter *Counter, location string) {
		if counter != nil && (counter.Count < 0 || (counter.Origin != ObservedCount && counter.Origin != InferredCount)) {
			check.add(InvalidInput, location, UsageCapability, "usage counters require a nonnegative count and observed/inferred origin")
		}
	}
	for _, entry := range []struct {
		name  string
		count *Counter
	}{
		{"input", usage.Input}, {"output", usage.Output}, {"total", usage.Total}, {"cacheRead", usage.CacheRead}, {"cacheCreation", usage.CacheCreation},
	} {
		validate(entry.count, path+"/"+entry.name)
	}
	for name, counter := range usage.Details {
		validate(&counter, path+"/details/"+escapePointer(name))
	}
}

// CheckEvent checks a single event's shape and declared capabilities. Ordered
// lifecycle validation belongs to StreamState, which is unique to a session.
func CheckEvent(event Event, target Target, limits Limits) []ConversionIssue {
	check := capabilityCheck{target: target, limits: limits, calls: map[string]struct{}{}, results: map[string]struct{}{}}
	if event.SchemaVersion != SemanticSchemaVersion {
		check.add(InvalidInput, "/schemaVersion", "", "unsupported semantic event version")
		return check.issues
	}
	switch event.Type {
	case SessionStarted, SessionConfigured, SessionConfigure, InputCommit, ResponseCreate, ResponseCancel, SessionClose:
		check.require(SessionsCapability, "/type")
	case InputAppend:
		check.require(SessionsCapability, "/type")
		if event.Item == nil {
			check.add(InvalidInput, "/item", "", "input append requires an ordered content item")
		}
	case ToolResultSubmitted:
		check.require(SessionsCapability, "/type")
		if event.Item == nil || event.Item.Kind != ToolResultNode || event.Item.Payload.IsZero() {
			check.add(InvalidInput, "/item", "", "tool result submission requires an associated result payload")
		} else {
			check.requireName(event.Item.CallID, "/item/callId")
		}
	case ResponseStarted, ResponseFinished, OperationCancelled:
	case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished:
		if event.ItemID.IsZero() && event.CallID.IsZero() && event.Index == nil {
			check.add(InvalidAssociation, "/itemId", "", "item events require a stable item, call or index identity")
		}
		if event.Type == ItemDelta && event.Delta.IsZero() {
			check.add(InvalidInput, "/delta", "", "delta event requires a payload")
		}
	case UsageUpdated:
		if event.Usage == nil {
			check.add(InvalidInput, "/usage", UsageCapability, "usage event requires counters")
		}
	case OperationFailed:
		if event.Error.IsZero() || event.Error.IsNull() {
			check.add(InvalidInput, "/error", "", "failure event requires an error")
		}
	case MediaReceived:
		check.require(RealtimeMediaCapability, "/media")
		if event.Media == nil || event.Media.Type == "" || event.Media.Format == "" || event.Media.Reference.ID.IsZero() {
			check.add(InvalidInput, "/media", RealtimeMediaCapability, "media event requires format, type and payload reference")
		}
	case NativeEvent:
		check.require(NativeExtensionsCapability, "/native")
		check.native(event.Native, "/native")
	default:
		check.add(InvalidInput, "/type", "", fmt.Sprintf("unknown event type %q", event.Type))
	}
	check.usage(event.Usage, "/usage")
	if event.Item != nil {
		if event.Item.Kind == ToolCallNode && event.Type != ItemFinished {
			if event.Item.Input == nil || (event.Item.Input.Kind != JSONInput && event.Item.Input.Kind != TextInput) {
				check.add(InvalidInput, "/item/input/kind", "", "streamed tool item requires an input kind")
			} else if event.Item.Input.Kind == JSONInput {
				check.require(FunctionToolsCapability, "/item")
			} else {
				check.require(FreeTextToolsCapability, "/item")
			}
			check.cache(event.Item.Cache, "/item/cache")
			check.resources(event.Item.Resources, "/item/resources")
		} else if event.Item.Kind != ToolResultNode {
			check.content([]Node{*event.Item}, "/item", 1)
		}
	}
	if event.Response != nil {
		check.issues = append(check.issues, CheckResponse(event.Response, target, limits)...)
	}
	if event.Request != nil {
		check.issues = append(check.issues, CheckRequest(event.Request, target, limits)...)
	}
	return check.issues
}
