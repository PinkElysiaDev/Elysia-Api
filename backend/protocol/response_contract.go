package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// CheckResponse validates observable response semantics before client encoding.
// Tool input and usage validation share the same rules as request and event paths.
func CheckResponse(response *Response, target Target, limits Limits) []ConversionIssue {
	check := capabilityCheck{target: target, limits: limits, calls: map[string]struct{}{}, results: map[string]struct{}{}}
	if response == nil || response.SchemaVersion != ProtocolCoreVersion {
		check.add(InvalidInput, "/schemaVersion", "", "missing response or unsupported semantic version")
		return check.issues
	}
	check.metadataSnapshot(response.Metadata, "/metadata")
	check.content(response.Content, "/content", 1, false)
	check.usage(response.Usage, "/usage")
	return check.issues
}

func (check *capabilityCheck) usage(usage *Usage, path string) {
	if usage == nil {
		return
	}
	check.require(UsageCapability, path)
	if err := ValidateUsageArithmetic(usage); err != nil {
		check.add(InvalidInput, path, UsageCapability, err.Error())
	}
	if usage.Input != nil && usage.Output != nil && usage.Output.Count >= 0 && usage.Input.Count > math.MaxInt64-usage.Output.Count {
		check.add(LimitExceeded, path, UsageCapability, "combined token count exceeds the supported integer range")
	}
	validate := func(counter *Counter, location string) {
		if counter != nil && (counter.Count < 0 || (counter.Origin != ObservedCount && counter.Origin != InferredCount && counter.Origin != PlaceholderCount)) {
			check.add(InvalidInput, location, UsageCapability, "usage counters require a nonnegative count and observed/inferred/placeholder origin")
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
	if event.SchemaVersion != ProtocolCoreVersion {
		check.add(InvalidInput, "/schemaVersion", "", "unsupported semantic event version")
		return check.issues
	}
	if err := restoreProtectedEvent(&event, event.auxiliary, event.omittedAuxiliary, event.controlReceipt, event.omittedControl, false); err != nil {
		check.add(UnsupportedNative, "/native", "", err.Error())
		return check.issues
	}
	if event.omittedAuxiliary != nil && !isOmittedAuxiliaryEvent(event, target) {
		check.add(UnsupportedNative, "/native", "", "auxiliary omission belongs to a different target or scope")
		return check.issues
	}
	if event.omittedControl != nil && !isOmittedControlEvent(event, target) {
		check.add(UnsupportedNative, "/control", "", "control omission belongs to a different target, mapping or scope")
		return check.issues
	}
	if event.Control != nil && event.Type != ControlUpdated {
		check.add(InvalidInput, "/control", "", "control state requires its isolated event type")
		return check.issues
	}
	if event.Unmapped == nil && len(event.UnmappedPaths) != 0 {
		check.add(InvalidInput, "/unmappedPaths", "", "source field paths require unmapped wire evidence")
	}
	if event.Unmapped != nil {
		for _, path := range check.unmappedEventPaths(event) {
			check.require(NativeExtensionsCapability, path)
			check.native(event.Unmapped, path)
		}
	}
	if !event.ParentID.IsZero() {
		check.requireName(event.ParentID, "/parentId")
		switch event.Type {
		case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished:
		default:
			check.add(InvalidAssociation, "/parentId", "", "only item lifecycle events can reference a parent")
		}
	}
	if event.Collection != "" {
		if event.Collection != "summary" && event.Collection != "reasoning_content" {
			check.add(InvalidAssociation, "/collection", "", "unknown reasoning child collection")
		}
		if event.ParentID.IsZero() {
			check.add(InvalidAssociation, "/collection", "", "reasoning collection requires a parent")
		}
		if event.Item != nil && event.Item.Kind != TextNode {
			check.add(InvalidAssociation, "/item/kind", "", "reasoning collection children must be text")
		}
	}
	switch event.Type {
	case ControlUpdated:
		if event.Control == nil || event.controlReceipt == nil {
			check.add(UnsupportedNative, "/control", "", "control state requires a validated source wire receipt")
		} else {
			check.require(ControlStateCapability, "/control")
			if !CanPreserveNative(event.Control.Source, target) {
				check.add(UnsupportedNative, "/control", "", "control state requires an explicit target disposition")
			}
			if !CheckScope(event.Control.Source.Scope, target.Scope) {
				check.add(ResourceScopeMismatch, "/control", "", "control state scope differs from target")
			}
			if event.Native != nil && (!event.controlReceipt.canReplayNative || event.controlReceipt.encoderHash != target.eventEncoderHash || event.controlReceipt.decoderHash != target.eventDecoderHash) {
				check.add(UnsupportedNative, "/native", "", "control native replay requires source preservation and equivalent event mappings")
			}
		}
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
		if event.Type == ItemDelta {
			if event.Item != nil && event.Item.Kind == ServerToolNode {
				if !event.Delta.IsZero() {
					check.add(InvalidInput, "/delta", ServerToolsCapability, "server progress cannot also contain input text")
				}
			} else if event.Delta.IsZero() {
				check.add(InvalidInput, "/delta", "", "delta event requires a payload")
			}
		}
	case UsageUpdated:
		if event.Usage == nil {
			check.add(InvalidInput, "/usage", UsageCapability, "usage event requires counters")
		}
	case MetadataUpdated:
		// Projection may remove all recognized metadata. This remains a no-op,
		// not an observation of zero tokens and not a native extension escape.
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
	for index, metadata := range event.Metadata {
		check.metadataValue(metadata, fmt.Sprintf("/metadata/%d", index))
		if metadata.Update == "" {
			continue
		}
		path := fmt.Sprintf("/metadata/%d/update", index)
		if metadata.Update != "append" || event.Type != ItemDelta || metadata.Location == "response" || metadata.Location == "usage" || strings.HasPrefix(metadata.Location, "event:") {
			check.add(InvalidInput, path, "", "append metadata requires an item delta and item-owned array")
		} else if !metadata.Value.IsArray() {
			check.add(InvalidInput, fmt.Sprintf("/metadata/%d/value", index), "", "append metadata requires an array")
		}
	}
	check.usage(event.Usage, "/usage")
	if event.Item != nil {
		if event.Item.Kind == ServerToolNode && event.Type == ItemDelta {
			check.serverToolHeader(*event.Item, "/item")
		} else if event.Item.Kind == ToolCallNode && event.Type != ItemFinished {
			check.metadataSnapshot(event.Item.Metadata, "/item/metadata")
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
			check.content([]Node{*event.Item}, "/item", 1, false)
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

func (check *capabilityCheck) unmappedEventPaths(event Event) []string {
	if len(event.UnmappedPaths) == 0 {
		return []string{"/unmapped"}
	}
	if len(event.UnmappedPaths) > check.limits.Nodes {
		check.add(LimitExceeded, "/unmappedPaths", "", "source field path count exceeds the node limit")
		return nil
	}
	paths := append([]string(nil), event.UnmappedPaths...)
	for index, path := range paths {
		if _, err := parsePointer(path); err != nil || path == "" {
			check.add(InvalidInput, fmt.Sprintf("/unmappedPaths/%d", index), "", "source field path must be a nonempty JSON pointer")
			return nil
		}
	}
	sort.Strings(paths)
	return paths
}
