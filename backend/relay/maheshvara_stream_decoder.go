package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type maheshvaraStreamToolState struct {
	id        string
	name      string
	arguments strings.Builder
	added     bool
}

type maheshvaraAnthropicBlock struct {
	typeName  string
	id        string
	name      string
	arguments strings.Builder
}

// MaheshvaraStreamDecoder is stateful because tool calls and content blocks
// are commonly split across multiple upstream events.
type MaheshvaraStreamDecoder struct {
	format          FormatType
	responseID      string
	model           string
	terminal        bool
	sawWireEvent    bool
	sawOutput       bool
	openAITools     map[int]*maheshvaraStreamToolState
	openAIToolOrder []int
	finishedChoices map[int]bool
	seenChoices     map[int]bool
	anthropicBlocks map[int]*maheshvaraAnthropicBlock
}

func NewMaheshvaraStreamDecoder(format FormatType) *MaheshvaraStreamDecoder {
	return &MaheshvaraStreamDecoder{
		format:          normalizeMaheshvaraStreamFormat(format),
		openAITools:     make(map[int]*maheshvaraStreamToolState),
		finishedChoices: make(map[int]bool),
		seenChoices:     make(map[int]bool),
		anthropicBlocks: make(map[int]*maheshvaraAnthropicBlock),
	}
}

func (decoder *MaheshvaraStreamDecoder) TerminalReceived() bool {
	return decoder != nil && decoder.terminal
}

func (decoder *MaheshvaraStreamDecoder) SawWireEvent() bool {
	return decoder != nil && decoder.sawWireEvent
}

func (decoder *MaheshvaraStreamDecoder) SawOutput() bool {
	return decoder != nil && decoder.sawOutput
}

func (decoder *MaheshvaraStreamDecoder) Decode(event SSEEvent) ([]MaheshvaraStreamEvent, error) {
	if decoder == nil {
		return nil, fmt.Errorf("nil Maheshvara stream decoder")
	}
	data := strings.TrimSpace(event.Data)
	if data == "" {
		return nil, nil
	}
	decoder.sawWireEvent = true
	if data == "[DONE]" {
		if decoder.terminal {
			return nil, nil
		}
		decoder.terminal = true
		return []MaheshvaraStreamEvent{{Type: CanonicalEventResponseCompleted, ResponseID: decoder.responseID, Model: decoder.model}}, nil
	}
	raw, err := decodeSSEEventJSON(data)
	if err != nil {
		return nil, err
	}
	if stringValue(raw["type"]) == "" && strings.TrimSpace(event.Event) != "" {
		raw["type"] = strings.TrimSpace(event.Event)
	}

	var events []MaheshvaraStreamEvent
	switch decoder.format {
	case FormatClaude:
		events, err = decoder.decodeAnthropic(raw)
	case FormatGemini:
		events, err = decoder.decodeGemini(raw)
	case FormatResponses:
		events, err = decoder.decodeResponses(raw)
	default:
		events, err = decoder.decodeOpenAIChat(raw)
	}
	if err != nil {
		return nil, err
	}
	for index := range events {
		if events[index].ResponseID == "" {
			events[index].ResponseID = decoder.responseID
		}
		if events[index].Model == "" {
			events[index].Model = decoder.model
		}
		if maheshvaraStreamEventHasOutput(events[index]) {
			decoder.sawOutput = true
		}
	}
	return events, nil
}

func normalizeMaheshvaraStreamFormat(format FormatType) FormatType {
	switch format {
	case FormatClaude, FormatGemini, FormatResponses:
		return format
	case FormatOpenAI, FormatOpenAIChat, FormatDeepSeek:
		return FormatOpenAIChat
	default:
		return FormatOpenAIChat
	}
}

func decodeSSEEventJSON(data string) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(data))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode SSE JSON event: %w", err)
	}
	return raw, nil
}

func maheshvaraStreamEventHasOutput(event MaheshvaraStreamEvent) bool {
	return event.Delta != "" || event.ReasoningDelta != "" || event.RefusalDelta != "" ||
		event.ToolName != "" || event.ToolArgumentsDelta != "" || event.ToolArgumentsDone != "" ||
		event.ContentPart != nil || event.OutputItem != nil || (event.Response != nil && len(event.Response.Output) > 0)
}

func (decoder *MaheshvaraStreamDecoder) baseEvent(typeName string, raw map[string]any) MaheshvaraStreamEvent {
	return MaheshvaraStreamEvent{Type: typeName, ResponseID: decoder.responseID, Model: decoder.model, Raw: raw}
}

func (decoder *MaheshvaraStreamDecoder) decodeOpenAIChat(raw map[string]any) ([]MaheshvaraStreamEvent, error) {
	decoder.responseID = firstNonEmptyString(stringValue(raw["id"]), decoder.responseID)
	decoder.model = firstNonEmptyString(stringValue(raw["model"]), decoder.model)
	createdAt := int64Value(raw["created"])
	var events []MaheshvaraStreamEvent
	if usage := canonicalUsageFromRawMap(mapValue(raw["usage"])); usage != nil {
		event := decoder.baseEvent(CanonicalEventUsageDelta, raw)
		event.Usage = usage
		event.CreatedAt = createdAt
		events = append(events, event)
	}

	choices, _ := raw["choices"].([]any)
	var terminalEvents []MaheshvaraStreamEvent
	for _, choiceValue := range choices {
		choice := mapValue(choiceValue)
		if choice == nil {
			continue
		}
		choiceIndex := intValue(choice["index"])
		decoder.seenChoices[choiceIndex] = true
		delta := mapValue(choice["delta"])
		if delta == nil {
			delta = mapValue(choice["message"])
		}
		if role := stringValue(delta["role"]); role != "" {
			event := decoder.baseEvent(CanonicalEventResponseInProgress, raw)
			event.Role = role
			event.ChoiceIndex = choiceIndex
			event.CreatedAt = createdAt
			events = append(events, event)
		}
		events = append(events, decoder.openAIContentEvents(delta["content"], choiceIndex, raw)...)

		reasoning := firstNonEmptyString(stringValue(delta["reasoning_content"]), stringValue(delta["reasoning"]), stringValue(delta["thinking"]))
		if reasoning != "" {
			event := decoder.baseEvent(CanonicalEventReasoningDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.ReasoningDelta = reasoning
			events = append(events, event)
		}
		if refusal := stringValue(delta["refusal"]); refusal != "" {
			event := decoder.baseEvent(CanonicalEventRefusalDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.RefusalDelta = refusal
			events = append(events, event)
		}
		if details, ok := delta["reasoning_details"].([]any); ok {
			for _, detailValue := range details {
				detail := mapValue(detailValue)
				if text := firstNonEmptyString(stringValue(detail["text"]), stringValue(detail["content"])); text != "" {
					event := decoder.baseEvent(CanonicalEventReasoningDelta, raw)
					event.ChoiceIndex = choiceIndex
					event.ReasoningDelta = text
					events = append(events, event)
				}
				if signature := firstNonEmptyString(stringValue(detail["signature"]), stringValue(detail["data"])); signature != "" {
					event := decoder.baseEvent(CanonicalEventReasoningSignatureDelta, raw)
					event.ChoiceIndex = choiceIndex
					event.ReasoningSignatureDelta = signature
					event.ReasoningSignatureProvider = openAIReasoningSignatureProvider(detail)
					events = append(events, event)
				}
			}
		}
		if signature := stringValue(delta["reasoning_signature"]); signature != "" {
			event := decoder.baseEvent(CanonicalEventReasoningSignatureDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.ReasoningSignatureDelta = signature
			event.ReasoningSignatureProvider = stringValue(delta["reasoning_signature_provider"])
			events = append(events, event)
		}
		if audio := mapValue(delta["audio"]); audio != nil {
			part := CanonicalContentPart{Type: CanonicalContentAudio, AudioBase64: firstNonEmptyString(stringValue(audio["data"]), stringValue(audio["audio_data"])), AudioURL: firstNonEmptyString(stringValue(audio["url"]), stringValue(audio["audio_url"])), Text: stringValue(audio["transcript"]), MediaType: firstNonEmptyString(stringValue(audio["format"]), stringValue(audio["mime_type"])), Raw: audio}
			event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
			event.ChoiceIndex = choiceIndex
			event.ContentPart = &part
			events = append(events, event)
		}
		if toolCalls, ok := delta["tool_calls"].([]any); ok {
			events = append(events, decoder.decodeOpenAIToolCalls(toolCalls, choiceIndex, raw)...)
		}

		finishReason := stringValue(choice["finish_reason"])
		if finishReason != "" {
			decoder.finishedChoices[choiceIndex] = true
			for _, toolIndex := range decoder.openAIToolOrder {
				state := decoder.openAITools[toolIndex]
				if state.arguments.Len() == 0 {
					continue
				}
				event := decoder.baseEvent(CanonicalEventFunctionCallArgumentsDone, raw)
				event.ChoiceIndex = choiceIndex
				event.ToolCallIndex = toolIndex
				event.ToolCallID = state.id
				event.ToolName = state.name
				event.ToolArgumentsDone = state.arguments.String()
				terminalEvents = append(terminalEvents, event)
			}
			event := decoder.baseEvent(CanonicalEventResponseCompleted, raw)
			event.ChoiceIndex = choiceIndex
			event.FinishReason = finishReason
			event.CreatedAt = createdAt
			terminalEvents = append(terminalEvents, event)
		}
	}
	if len(decoder.seenChoices) > 0 && allMaheshvaraChoicesFinished(decoder.seenChoices, decoder.finishedChoices) {
		decoder.terminal = true
	}
	return append(events, terminalEvents...), nil
}

func (decoder *MaheshvaraStreamDecoder) decodeOpenAIToolCalls(toolCalls []any, choiceIndex int, raw map[string]any) []MaheshvaraStreamEvent {
	var events []MaheshvaraStreamEvent
	for _, toolValue := range toolCalls {
		tool := mapValue(toolValue)
		if tool == nil {
			continue
		}
		toolIndex := intValue(tool["index"])
		state := decoder.openAITools[toolIndex]
		if state == nil {
			state = &maheshvaraStreamToolState{}
			decoder.openAITools[toolIndex] = state
			decoder.openAIToolOrder = append(decoder.openAIToolOrder, toolIndex)
		}
		function := mapValue(tool["function"])
		if signature := openAIGoogleThoughtSignature(tool); signature != "" {
			event := decoder.baseEvent(CanonicalEventReasoningSignatureDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.ToolCallIndex = toolIndex
			event.ReasoningSignatureDelta = signature
			event.ReasoningSignatureProvider = CanonicalSignatureProviderGemini
			events = append(events, event)
		}
		state.id = firstNonEmptyString(stringValue(tool["id"]), state.id)
		state.name = firstNonEmptyString(stringValue(function["name"]), state.name)
		if !state.added && (state.id != "" || state.name != "") {
			state.added = true
			event := decoder.baseEvent(CanonicalEventFunctionCallAdded, raw)
			event.ChoiceIndex = choiceIndex
			event.ToolCallIndex = toolIndex
			event.ToolCallID = state.id
			event.ToolName = state.name
			events = append(events, event)
		}
		if arguments := stringValue(function["arguments"]); arguments != "" {
			state.arguments.WriteString(arguments)
			event := decoder.baseEvent(CanonicalEventFunctionCallArgumentsDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.ToolCallIndex = toolIndex
			event.ToolCallID = state.id
			event.ToolName = state.name
			event.ToolArgumentsDelta = arguments
			events = append(events, event)
		}
	}
	return events
}

func openAIReasoningSignatureProvider(detail map[string]any) string {
	provider := strings.ToLower(strings.TrimSpace(stringValue(detail["provider"])))
	if provider != "" {
		return provider
	}
	typeName := strings.ToLower(firstNonEmptyString(stringValue(detail["type"]), stringValue(detail["format"])))
	switch {
	case strings.Contains(typeName, "google"), strings.Contains(typeName, "gemini"):
		return CanonicalSignatureProviderGemini
	case strings.Contains(typeName, "anthropic"), strings.Contains(typeName, "claude"):
		return CanonicalSignatureProviderAnthropic
	case strings.Contains(typeName, "openai"):
		return CanonicalSignatureProviderOpenAI
	default:
		return ""
	}
}

func (decoder *MaheshvaraStreamDecoder) openAIContentEvents(value any, choiceIndex int, raw map[string]any) []MaheshvaraStreamEvent {
	if text, ok := value.(string); ok {
		if text == "" {
			return nil
		}
		event := decoder.baseEvent(CanonicalEventTextDelta, raw)
		event.ChoiceIndex = choiceIndex
		event.Delta = text
		return []MaheshvaraStreamEvent{event}
	}
	parts := interfaceToContentParts(value)
	events := make([]MaheshvaraStreamEvent, 0, len(parts))
	for index := range parts {
		part := parts[index]
		event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
		event.ChoiceIndex = choiceIndex
		event.ContentIndex = index
		switch part.Type {
		case CanonicalContentText:
			event.Type = CanonicalEventTextDelta
			event.Delta = part.Text
		case CanonicalContentReasoning:
			event.Type = CanonicalEventReasoningDelta
			event.ReasoningDelta = firstNonEmptyString(part.ReasoningText, part.Text)
		case CanonicalContentRefusal:
			event.Type = CanonicalEventRefusalDelta
			event.RefusalDelta = part.Text
		default:
			event.ContentPart = &part
		}
		if maheshvaraStreamEventHasOutput(event) {
			events = append(events, event)
		}
	}
	return events
}

func allMaheshvaraChoicesFinished(seen, finished map[int]bool) bool {
	for index := range seen {
		if !finished[index] {
			return false
		}
	}
	return true
}
