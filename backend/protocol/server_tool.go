package protocol

import "strings"

func (check *capabilityCheck) serverTool(node Node, path string) {
	if check.target.coreSchemaVersion() != ProtocolCoreVersion {
		check.add(InvalidInput, path+"/kind", ServerToolsCapability, "server execution requires core schemaVersion 2")
		return
	}
	check.require(ServerToolsCapability, path)
	check.requireName(node.Name, path+"/name")
	for _, field := range []struct {
		name  string
		value Value
	}{{"id", node.ID}, {"status", node.Status}} {
		if !field.value.IsZero() {
			check.requireName(field.value, path+"/"+field.name)
		}
	}
	if !node.CallID.IsZero() {
		check.add(InvalidInput, path+"/callId", ServerToolsCapability, "upstream execution does not request a client tool result")
	}
	if node.Input == nil {
		return
	}
	switch node.Input.Kind {
	case JSONInput:
	case TextInput:
		if !node.Input.Value.IsZero() && !node.Input.Value.IsStringValue() {
			check.add(InvalidInput, path+"/input/value", ServerToolsCapability, "observed text input must be a string when present")
		}
	default:
		check.add(InvalidInput, path+"/input/kind", ServerToolsCapability, "observed input requires a JSON or text kind")
	}
}

func (check *capabilityCheck) serverToolHeader(node Node, path string) {
	if node.Status.IsZero() || !node.Payload.IsZero() || node.Input != nil || len(node.Children) != 0 ||
		len(node.Resources) != 0 || node.Native != nil || len(node.Attributes) != 0 || len(node.Cache) != 0 ||
		len(node.Metadata) != 0 || !node.Role.IsZero() || !node.CallID.IsZero() || node.ReasoningForm != "" || node.ReasoningContent != nil {
		check.add(InvalidInput, path, ServerToolsCapability, "server progress requires a status and only identity and source fields")
		return
	}
	check.serverTool(node, path)
}

func (replay *EventReplay) consumeServerTool(key string, item *replayItem, event Event) error {
	if !event.CallID.IsZero() {
		return streamIssue(InvalidAssociation, "/callId", "upstream execution cannot acquire a client call identity")
	}
	isHeader := event.Type == ItemDelta && event.Item != nil
	for _, metadata := range event.Metadata {
		if metadata.Location != "response" && metadata.Location != "usage" {
			return streamIssue(UnsupportedCapability, metadata.Path, "server item metadata belongs in its declared execution snapshot")
		}
	}
	if event.Item != nil {
		if !isHeader || !event.Item.ID.IsZero() {
			if item.wireID.IsZero() {
				item.wireID = Value{raw: strings.Clone(event.Item.ID.raw)}
			} else if !equalValues(item.wireID, event.Item.ID) {
				return streamIssue(InvalidAssociation, "/item/id", "server execution identity changed")
			}
		}
		name, err := readString(event.Item.Name)
		if err != nil {
			return err
		}
		if item.name == "" {
			item.name = strings.Clone(name)
		} else if item.name != name {
			return streamIssue(InvalidAssociation, "/item/name", "server execution name changed")
		}
		if isHeader {
			return nil // Progress does not replace input or complete the item.
		}
		if event.Item.Input != nil {
			if item.input != "" && item.input != event.Item.Input.Kind {
				return streamIssue(InvalidAssociation, "/item/input/kind", "observed server input kind changed")
			}
			item.input = event.Item.Input.Kind
		} else if item.input != "" {
			return streamIssue(InvalidAssociation, "/item/input", "server snapshot removed its observed input")
		}
	}
	value := event.Delta
	isSnapshot := event.Type != ItemDelta
	if isSnapshot {
		value = Value{}
		if event.Item != nil && event.Item.Input != nil && item.input == TextInput {
			value = event.Item.Input.Value
		}
		if event.Item != nil && item.input == TextInput && value.IsZero() && replay.state.texts[key] != nil {
			return streamIssue(InvalidAssociation, "/item/input/value", "server snapshot removed already observed text input")
		}
	} else if item.input != TextInput {
		return streamIssue(UnsupportedCapability, "/delta", "server input deltas require a declared text input")
	}
	if !value.IsZero() {
		text, err := readString(value)
		if err != nil {
			return err
		}
		if _, err := replay.state.TrackText(key, text, isSnapshot); err != nil {
			return err
		}
	}
	return nil
}
