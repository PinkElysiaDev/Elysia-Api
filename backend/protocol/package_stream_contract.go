package protocol

import "fmt"

type streamField struct {
	path    string
	pointer []string
}

type compiledPackageStreamContract struct {
	discriminator, responseID, itemID, sequence streamField
	events                                      map[string][]compiledPackageStreamEvent
	hasTools                                    bool
}

type compiledPackageStreamEvent struct {
	action         EventType
	delta, failure streamField
	usage          [3]streamField
	tool           *compiledStreamTool
	isTool         bool
}

type compiledStreamTool struct {
	name, callID streamField
}

func compilePackageStreamContract(spec *ProtocolStreamSpec, path string, limits Limits) (*compiledPackageStreamContract, error) {
	if spec == nil {
		return nil, nil
	}
	contract := &compiledPackageStreamContract{events: make(map[string][]compiledPackageStreamEvent, len(spec.Events))}
	for _, selector := range []struct {
		name, path string
		field      *streamField
	}{{"discriminator", spec.Discriminator, &contract.discriminator}, {"responseId", spec.ResponseID, &contract.responseID}, {"itemId", spec.ItemID, &contract.itemID}, {"sequence", spec.Sequence, &contract.sequence}} {
		if selector.name == "sequence" && selector.path == "" {
			continue
		}
		field, err := compileStreamField(selector.path, path+"/"+selector.name, limits)
		if err != nil {
			return nil, err
		}
		*selector.field = field
	}
	if len(spec.Events) == 0 || len(spec.Events) > limits.StateItems {
		return nil, fmt.Errorf("%s/events: requires a nonempty bounded event table", path)
	}
	actions := map[EventType]bool{}
	toolActions := map[EventType]bool{}
	actionCount := 0
	for _, tag := range sortedKeys(spec.Events) {
		eventPath := path + "/events/" + escapePointer(tag)
		if tag == "" {
			return nil, fmt.Errorf("%s: event tag must not be empty", eventPath)
		}
		entries := spec.Events[tag]
		actionCount += len(entries)
		if len(entries) == 0 || actionCount > limits.StateItems {
			return nil, fmt.Errorf("%s: requires nonempty actions within the stream item limit", eventPath)
		}
		for index, entry := range entries {
			event, err := compileStreamEvent(entry, fmt.Sprintf("%s/%d", eventPath, index), limits)
			if err != nil {
				return nil, err
			}
			contract.events[tag] = append(contract.events[tag], event)
			if event.isTool {
				contract.hasTools, toolActions[event.action] = true, true
			} else {
				actions[event.action] = true
			}
		}
	}
	for _, action := range []EventType{ResponseStarted, ItemStarted, ItemDelta, ItemFinished, UsageUpdated, ResponseFinished} {
		if !actions[action] {
			return nil, fmt.Errorf("%s/events: missing lifecycle action for %s", path, action)
		}
	}
	if contract.hasTools {
		for _, action := range []EventType{ItemStarted, ItemDelta} {
			if !toolActions[action] {
				return nil, fmt.Errorf("%s/events: missing tool lifecycle action for %s", path, action)
			}
		}
	}
	return contract, nil
}

func compileStreamField(pointer, path string, limits Limits) (streamField, error) {
	parts, err := parsePointer(pointer)
	if err != nil {
		return streamField{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(parts) == 0 || len(parts) > limits.Depth {
		return streamField{}, fmt.Errorf("%s: requires a non-root pointer within the depth limit", path)
	}
	return streamField{path: pointer, pointer: parts}, nil
}

func compileStreamEvent(spec ProtocolStreamActionSpec, path string, limits Limits) (compiledPackageStreamEvent, error) {
	event := compiledPackageStreamEvent{}
	switch spec.Action {
	case "response_start":
		event.action = ResponseStarted
	case "item_start":
		event.action = ItemStarted
	case "text_delta":
		event.action = ItemDelta
	case "item_finish":
		event.action = ItemFinished
	case "tool_start":
		event.action, event.isTool = ItemStarted, true
	case "tool_delta":
		event.action, event.isTool = ItemDelta, true
	case "tool_finish":
		event.action, event.isTool = ItemFinished, true
	case "usage":
		event.action = UsageUpdated
	case "response_finish":
		event.action = ResponseFinished
	case "failure":
		event.action = OperationFailed
	case "cancel":
		event.action = OperationCancelled
	default:
		return event, fmt.Errorf("%s/action: unsupported lifecycle action", path)
	}
	if (spec.Action == "text_delta") != (spec.Text != "") || (event.action == OperationFailed) != (spec.Error != "") || (event.action == UsageUpdated) != (spec.Usage != nil) {
		return event, fmt.Errorf("%s: text, error and usage selectors belong only to their respective actions and are required there", path)
	}
	if (spec.Action == "tool_delta") != (spec.Arguments != "") || (spec.Action == "tool_start") != (spec.Tool != nil) {
		return event, fmt.Errorf("%s: tool identity belongs to tool_start and arguments to tool_delta; both are required there", path)
	}
	var err error
	if spec.Text != "" {
		event.delta, err = compileStreamField(spec.Text, path+"/text", limits)
	}
	if spec.Arguments != "" {
		event.delta, err = compileStreamField(spec.Arguments, path+"/arguments", limits)
	}
	if spec.Error != "" {
		event.failure, err = compileStreamField(spec.Error, path+"/error", limits)
	}
	if err != nil {
		return event, err
	}
	if spec.Tool != nil {
		event.tool = &compiledStreamTool{}
		for _, field := range []struct {
			name, pointer string
			target        *streamField
		}{{"name", spec.Tool.Name, &event.tool.name}, {"callId", spec.Tool.CallID, &event.tool.callID}} {
			selected, err := compileStreamField(field.pointer, path+"/tool/"+field.name, limits)
			if err != nil {
				return event, err
			}
			*field.target = selected
		}
	}
	if spec.Usage != nil {
		if spec.Usage.Input == "" && spec.Usage.Output == "" && spec.Usage.Total == "" {
			return event, fmt.Errorf("%s/usage: requires at least one counter selector", path)
		}
		for index, selector := range []struct{ name, pointer string }{{"input", spec.Usage.Input}, {"output", spec.Usage.Output}, {"total", spec.Usage.Total}} {
			if selector.pointer == "" {
				continue
			}
			event.usage[index], err = compileStreamField(selector.pointer, path+"/usage/"+selector.name, limits)
			if err != nil {
				return event, err
			}
		}
	}
	return event, nil
}
