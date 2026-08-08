package relay

import (
	"fmt"
	"strings"
)

type CustomProtocolStreamDecoder struct {
	config            CustomProtocolConfig
	mode              string
	doneValues        map[string]struct{}
	events            map[string]struct{}
	previousText      map[string]string
	previousReasoning map[string]string
	previousArguments map[string]string
	toolAdded         map[string]bool
	terminal          bool
	sawOutput         bool
}

func NewCustomProtocolStreamDecoder(config CustomProtocolConfig) (*CustomProtocolStreamDecoder, error) {
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	decoder := &CustomProtocolStreamDecoder{
		config:            config,
		mode:              "delta",
		doneValues:        map[string]struct{}{"[DONE]": {}},
		events:            make(map[string]struct{}),
		previousText:      make(map[string]string),
		previousReasoning: make(map[string]string),
		previousArguments: make(map[string]string),
		toolAdded:         make(map[string]bool),
	}
	if stream := config.Response.Stream; stream != nil {
		if mode := strings.ToLower(strings.TrimSpace(stream.Mode)); mode != "" {
			decoder.mode = mode
		}
		for _, value := range stream.DoneValues {
			value = strings.TrimSpace(value)
			if value != "" {
				decoder.doneValues[value] = struct{}{}
			}
		}
		for _, eventName := range stream.Events {
			eventName = strings.TrimSpace(eventName)
			if eventName != "" {
				decoder.events[eventName] = struct{}{}
			}
		}
	}
	return decoder, nil
}

func (decoder *CustomProtocolStreamDecoder) TerminalReceived() bool {
	return decoder != nil && decoder.terminal
}

func (decoder *CustomProtocolStreamDecoder) SawOutput() bool {
	return decoder != nil && decoder.sawOutput
}

func (decoder *CustomProtocolStreamDecoder) Decode(wireEvent SSEEvent) ([]MaheshvaraStreamEvent, bool, error) {
	if decoder == nil {
		return nil, false, fmt.Errorf("nil custom protocol stream decoder")
	}
	data := strings.TrimSpace(wireEvent.Data)
	if data == "" {
		return nil, false, nil
	}
	if _, done := decoder.doneValues[data]; done {
		decoder.terminal = true
		return []MaheshvaraStreamEvent{{Type: CanonicalEventResponseCompleted}}, true, nil
	}
	if len(decoder.events) > 0 {
		eventName := strings.TrimSpace(wireEvent.Event)
		if eventName == "" {
			if raw, err := decodeSSEEventJSON(data); err == nil {
				eventName = firstNonEmptyString(stringValue(raw["type"]), stringValue(raw["event"]))
			}
		}
		if _, allowed := decoder.events[eventName]; !allowed {
			return nil, false, nil
		}
	}
	response, err := customProtocolStreamEventToCanonicalValidated([]byte(data), decoder.config)
	if err != nil {
		return nil, false, err
	}
	events := decoder.responseEvents(response)
	for _, event := range events {
		if maheshvaraStreamEventHasOutput(event) {
			decoder.sawOutput = true
		}
		if event.Type == CanonicalEventResponseCompleted || event.Type == CanonicalEventResponseFailed {
			decoder.terminal = true
		}
	}
	return events, decoder.terminal, nil
}

func (decoder *CustomProtocolStreamDecoder) responseEvents(response *CanonicalResponse) []MaheshvaraStreamEvent {
	if response == nil {
		return nil
	}
	var events []MaheshvaraStreamEvent
	if response.Error != nil {
		events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventResponseFailed, ResponseID: response.ID, Model: response.Model, Error: response.Error})
		return events
	}
	for outputIndex, item := range response.Output {
		switch item.Type {
		case CanonicalOutputFunctionCall:
			key := firstNonEmptyString(item.CallID, item.Name, fmt.Sprintf("tool_%d", outputIndex))
			if !decoder.toolAdded[key] {
				decoder.toolAdded[key] = true
				events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventFunctionCallAdded, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ToolCallIndex: outputIndex, ToolCallID: item.CallID, ToolName: item.Name})
			}
			arguments := string(item.Arguments)
			if arguments != "" {
				delta := decoder.streamDelta(decoder.previousArguments, key, arguments)
				if delta != "" {
					events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventFunctionCallArgumentsDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ToolCallIndex: outputIndex, ToolCallID: item.CallID, ToolName: item.Name, ToolArgumentsDelta: delta})
				}
			}
		case CanonicalOutputReasoning:
			text := canonicalReasoningText(item)
			delta := decoder.streamDelta(decoder.previousReasoning, fmt.Sprintf("reasoning_%d", outputIndex), text)
			if delta != "" {
				events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventReasoningDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ItemID: item.ID, ReasoningDelta: delta})
			}
		default:
			for contentIndex, part := range item.Content {
				key := fmt.Sprintf("%d:%d", outputIndex, contentIndex)
				switch part.Type {
				case CanonicalContentText:
					delta := decoder.streamDelta(decoder.previousText, key, part.Text)
					if delta != "" {
						events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventTextDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, Delta: delta})
					}
				case CanonicalContentReasoning:
					delta := decoder.streamDelta(decoder.previousReasoning, key, firstNonEmptyString(part.ReasoningText, part.Text))
					if delta != "" {
						events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventReasoningDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, ReasoningDelta: delta})
					}
				case CanonicalContentRefusal:
					delta := decoder.streamDelta(decoder.previousText, "refusal:"+key, part.Text)
					if delta != "" {
						events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventRefusalDelta, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, RefusalDelta: delta})
					}
				default:
					partCopy := part
					events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventContentPartAdded, ResponseID: response.ID, Model: response.Model, OutputIndex: outputIndex, ContentIndex: contentIndex, ItemID: item.ID, ContentPart: &partCopy})
				}
			}
		}
	}
	if response.Usage != nil {
		events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventUsageDelta, ResponseID: response.ID, Model: response.Model, Usage: response.Usage})
	}
	if response.StopReason != "" || response.Status == "completed" {
		events = append(events, MaheshvaraStreamEvent{Type: CanonicalEventResponseCompleted, ResponseID: response.ID, Model: response.Model, FinishReason: response.StopReason, Response: response})
	}
	return events
}

func (decoder *CustomProtocolStreamDecoder) streamDelta(previous map[string]string, key, current string) string {
	if decoder.mode != "cumulative" {
		return current
	}
	before := previous[key]
	previous[key] = current
	switch {
	case current == before:
		return ""
	case strings.HasPrefix(current, before):
		return strings.TrimPrefix(current, before)
	default:
		return current
	}
}
