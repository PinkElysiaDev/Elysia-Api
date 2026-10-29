package protocol

import "fmt"

type streamField struct {
	path    string
	pointer []string
}

type compiledPackageStreamContract struct {
	discriminator, responseID, itemID, sequence streamField
	events                                      map[string]compiledPackageStreamEvent
}

type compiledPackageStreamEvent struct {
	action        EventType
	text, failure streamField
	usage         [3]streamField
}

func compilePackageStreamContract(spec *ProtocolStreamSpec, limits Limits) (*compiledPackageStreamContract, error) {
	if spec == nil {
		return nil, nil
	}
	contract := &compiledPackageStreamContract{events: make(map[string]compiledPackageStreamEvent, len(spec.Events))}
	for _, selector := range []struct {
		name, path string
		field      *streamField
	}{{"discriminator", spec.Discriminator, &contract.discriminator}, {"responseId", spec.ResponseID, &contract.responseID}, {"itemId", spec.ItemID, &contract.itemID}, {"sequence", spec.Sequence, &contract.sequence}} {
		if selector.name == "sequence" && selector.path == "" {
			continue
		}
		field, err := compileStreamField(selector.path, "/spec/stream/"+selector.name, limits)
		if err != nil {
			return nil, err
		}
		*selector.field = field
	}
	if len(spec.Events) == 0 || len(spec.Events) > limits.StateItems {
		return nil, fmt.Errorf("/spec/stream/events: requires a nonempty bounded event table")
	}
	actions := map[EventType]bool{}
	for _, tag := range sortedKeys(spec.Events) {
		path := "/spec/stream/events/" + escapePointer(tag)
		if tag == "" {
			return nil, fmt.Errorf("%s: event tag must not be empty", path)
		}
		event, err := compileStreamEvent(spec.Events[tag], path, limits)
		if err != nil {
			return nil, err
		}
		contract.events[tag], actions[event.action] = event, true
	}
	for _, action := range []EventType{ResponseStarted, ItemStarted, ItemDelta, ItemFinished, UsageUpdated, ResponseFinished} {
		if !actions[action] {
			return nil, fmt.Errorf("/spec/stream/events: missing lifecycle action for %s", action)
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

func compileStreamEvent(spec ProtocolStreamEventSpec, path string, limits Limits) (compiledPackageStreamEvent, error) {
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
	if (event.action == ItemDelta) != (spec.Text != "") || (event.action == OperationFailed) != (spec.Error != "") || (event.action == UsageUpdated) != (spec.Usage != nil) {
		return event, fmt.Errorf("%s: text, error and usage selectors belong only to their respective actions and are required there", path)
	}
	var err error
	if spec.Text != "" {
		event.text, err = compileStreamField(spec.Text, path+"/text", limits)
	}
	if spec.Error != "" {
		event.failure, err = compileStreamField(spec.Error, path+"/error", limits)
	}
	if err != nil {
		return event, err
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
