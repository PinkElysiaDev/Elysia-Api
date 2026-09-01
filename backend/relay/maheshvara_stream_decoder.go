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
	format       FormatType
	responseID   string
	model        string
	terminal     bool
	sawWireEvent bool
	sawOutput    bool
	// sawFinishReason：上游是否发过真实 finish_reason（chat）/message_delta
	//（Claude）/finishReason（Gemini）/终态 status（Responses）。空但合法的
	// 完成（content_filter 拒答）据此与残缺流区分。
	sawFinishReason bool
	openAITools     map[int]*maheshvaraStreamToolState
	openAIToolOrder []int
	finishedChoices map[int]bool
	seenChoices     map[int]bool
	// chat 线的终态快照语义（PC7f）：部分上游在终态 chunk 里回传完整 message，
	// 只允许补发缺失后缀，不得把已流式输出过的内容再发一遍。
	openAIChoiceText     map[int]string
	openAIChoiceReasoned map[int]bool
	// openAIPartText 按 (choice, contentIndex) 分桶累计快照文本：终态 message
	// 的多 text part 各自独立差分，后续 part 不因前一个 part 的累计而误判
	// 前缀不匹配被丢弃。
	openAIPartText map[string]string
	// nextSyntheticCallID：无 id 工具调用的合成 id 单调计数器（跨 chunk 不撞）。
	nextSyntheticCallID int
	anthropicBlocks     map[int]*maheshvaraAnthropicBlock
}

func NewMaheshvaraStreamDecoder(format FormatType) *MaheshvaraStreamDecoder {
	return &MaheshvaraStreamDecoder{
		format:               normalizeMaheshvaraStreamFormat(format),
		openAITools:          make(map[int]*maheshvaraStreamToolState),
		finishedChoices:      make(map[int]bool),
		seenChoices:          make(map[int]bool),
		openAIChoiceText:     make(map[int]string),
		openAIChoiceReasoned: make(map[int]bool),
		openAIPartText:       make(map[string]string),
		anthropicBlocks:      make(map[int]*maheshvaraAnthropicBlock),
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

// SawFinishReason 报告上游是否发过真实终态原因（区别于合成 [DONE] 终态）。
func (decoder *MaheshvaraStreamDecoder) SawFinishReason() bool {
	return decoder != nil && decoder.sawFinishReason
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
		// 已见过 choice 却从未收到任何 finish_reason：[DONE] 不是终态替身，
		// 这样的流是残缺的（对齐 PC7/DC7b：不允许把没写完的答卷当完整交付）。
		if len(decoder.seenChoices) > 0 && !allMaheshvaraChoicesFinished(decoder.seenChoices, decoder.finishedChoices) {
			event := decoder.baseEvent(MaheshvaraEventResponseFailed, map[string]any{})
			event.Error = &MaheshvaraError{Type: "upstream_stream_error", Message: "upstream stream ended without a finish_reason"}
			return []MaheshvaraStreamEvent{event}, nil
		}
		return []MaheshvaraStreamEvent{{Type: MaheshvaraEventResponseCompleted, ResponseID: decoder.responseID, Model: decoder.model}}, nil
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
	}
	return FormatOpenAIChat
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
	if usage := maheshvaraUsageFromRawMap(mapValue(raw["usage"])); usage != nil {
		event := decoder.baseEvent(MaheshvaraEventUsageDelta, raw)
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
		snapshot := false
		if delta == nil {
			// 终态 chunk 用完整 message 回传时是快照而非增量：已流式输出过的
			// 内容只能补缺失后缀，不能整段重发（PC7f）。
			delta = mapValue(choice["message"])
			snapshot = delta != nil
		}
		if role := stringValue(delta["role"]); role != "" {
			event := decoder.baseEvent(MaheshvaraEventResponseInProgress, raw)
			event.Role = role
			event.ChoiceIndex = choiceIndex
			event.CreatedAt = createdAt
			events = append(events, event)
		}
		events = append(events, decoder.openAIContentEvents(delta["content"], choiceIndex, raw, snapshot)...)

		reasoning := firstNonEmptyString(stringValue(delta["reasoning_content"]), stringValue(delta["reasoning"]), stringValue(delta["thinking"]))
		if reasoning != "" && !(snapshot && decoder.openAIChoiceReasoned[choiceIndex]) {
			decoder.openAIChoiceReasoned[choiceIndex] = true
			event := decoder.baseEvent(MaheshvaraEventReasoningDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.ReasoningDelta = reasoning
			events = append(events, event)
		}
		if refusal := stringValue(delta["refusal"]); refusal != "" && !(snapshot && decoder.openAIChoiceText[choiceIndex] != "") {
			event := decoder.baseEvent(MaheshvaraEventRefusalDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.RefusalDelta = refusal
			events = append(events, event)
		}
		if details, ok := delta["reasoning_details"].([]any); ok {
			for _, detailValue := range details {
				detail := mapValue(detailValue)
				if text := firstNonEmptyString(stringValue(detail["text"]), stringValue(detail["content"])); text != "" {
					event := decoder.baseEvent(MaheshvaraEventReasoningDelta, raw)
					event.ChoiceIndex = choiceIndex
					event.ReasoningDelta = text
					events = append(events, event)
				}
				if signature := firstNonEmptyString(stringValue(detail["signature"]), stringValue(detail["data"])); signature != "" {
					event := decoder.baseEvent(MaheshvaraEventReasoningSignatureDelta, raw)
					event.ChoiceIndex = choiceIndex
					event.ReasoningSignatureDelta = signature
					event.ReasoningSignatureProvider = openAIReasoningSignatureProvider(detail)
					events = append(events, event)
				}
			}
		}
		if signature := stringValue(delta["reasoning_signature"]); signature != "" {
			event := decoder.baseEvent(MaheshvaraEventReasoningSignatureDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.ReasoningSignatureDelta = signature
			event.ReasoningSignatureProvider = stringValue(delta["reasoning_signature_provider"])
			events = append(events, event)
		}
		if audio := mapValue(delta["audio"]); audio != nil {
			part := MaheshvaraContentPart{Type: MaheshvaraContentAudio, AudioBase64: firstNonEmptyString(stringValue(audio["data"]), stringValue(audio["audio_data"])), AudioURL: firstNonEmptyString(stringValue(audio["url"]), stringValue(audio["audio_url"])), Text: stringValue(audio["transcript"]), MediaType: firstNonEmptyString(stringValue(audio["format"]), stringValue(audio["mime_type"])), Raw: audio}
			event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
			event.ChoiceIndex = choiceIndex
			event.ContentPart = &part
			events = append(events, event)
		}
		if toolCalls, ok := delta["tool_calls"].([]any); ok {
			events = append(events, decoder.decodeOpenAIToolCalls(toolCalls, choiceIndex, raw, snapshot)...)
		}

		finishReason := stringValue(choice["finish_reason"])
		if finishReason != "" {
			decoder.sawFinishReason = true
			// finish_reason:"error" 是终态失败，不得伪装成正常 stop（DC5b）。
			if strings.EqualFold(finishReason, "error") {
				decoder.finishedChoices[choiceIndex] = true
				event := decoder.baseEvent(MaheshvaraEventResponseFailed, raw)
				message := firstNonEmptyString(stringValue(mapValue(raw["error"])["message"]), stringValue(delta["content"]))
				if message == "" {
					message = "upstream reported finish_reason=error"
				}
				event.Error = &MaheshvaraError{Type: "upstream_stream_error", Message: message}
				terminalEvents = append(terminalEvents, event)
				continue
			}
			decoder.finishedChoices[choiceIndex] = true
			for _, toolIndex := range decoder.openAIToolOrder {
				state := decoder.openAITools[toolIndex]
				if state.arguments.Len() == 0 {
					continue
				}
				event := decoder.baseEvent(MaheshvaraEventFunctionCallArgumentsDone, raw)
				event.ChoiceIndex = choiceIndex
				event.ToolCallIndex = toolIndex
				event.ToolCallID = state.id
				event.ToolName = state.name
				event.ToolArgumentsDone = state.arguments.String()
				terminalEvents = append(terminalEvents, event)
			}
			event := decoder.baseEvent(MaheshvaraEventResponseCompleted, raw)
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

func (decoder *MaheshvaraStreamDecoder) decodeOpenAIToolCalls(toolCalls []any, choiceIndex int, raw map[string]any, snapshot bool) []MaheshvaraStreamEvent {
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
			event := decoder.baseEvent(MaheshvaraEventReasoningSignatureDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.ToolCallIndex = toolIndex
			event.ReasoningSignatureDelta = signature
			event.ReasoningSignatureProvider = MaheshvaraSignatureProviderGemini
			events = append(events, event)
		}
		state.id = firstNonEmptyString(stringValue(tool["id"]), state.id)
		state.name = firstNonEmptyString(stringValue(function["name"]), state.name)
		if !state.added && (state.id != "" || state.name != "") {
			state.added = true
			event := decoder.baseEvent(MaheshvaraEventFunctionCallAdded, raw)
			event.ChoiceIndex = choiceIndex
			event.ToolCallIndex = toolIndex
			event.ToolCallID = state.id
			event.ToolName = state.name
			events = append(events, event)
		}
		if arguments := stringValue(function["arguments"]); arguments != "" {
			if snapshot {
				// 快照是完整参数值：与已累计内容一致时不重发，否则作为完整值
				// 走 done 事件（渲染层对 done 做前缀差分，只补后缀）。
				if state.arguments.String() == arguments {
					continue
				}
				rewritten := &maheshvaraStreamToolState{id: state.id, name: state.name, added: true}
				rewritten.arguments.WriteString(arguments)
				decoder.openAITools[toolIndex] = rewritten
				event := decoder.baseEvent(MaheshvaraEventFunctionCallArgumentsDone, raw)
				event.ChoiceIndex = choiceIndex
				event.ToolCallIndex = toolIndex
				event.ToolCallID = state.id
				event.ToolName = state.name
				event.ToolArgumentsDone = arguments
				events = append(events, event)
				continue
			}
			state.arguments.WriteString(arguments)
			event := decoder.baseEvent(MaheshvaraEventFunctionCallArgumentsDelta, raw)
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
		return MaheshvaraSignatureProviderGemini
	case strings.Contains(typeName, "anthropic"), strings.Contains(typeName, "claude"):
		return MaheshvaraSignatureProviderAnthropic
	case strings.Contains(typeName, "openai"):
		return MaheshvaraSignatureProviderOpenAI
	default:
		return ""
	}
}

func (decoder *MaheshvaraStreamDecoder) openAIContentEvents(value any, choiceIndex int, raw map[string]any, snapshot bool) []MaheshvaraStreamEvent {
	if text, ok := value.(string); ok {
		if text == "" {
			return nil
		}
		if snapshot {
			return decoder.openAISnapshotTextSuffix(text, choiceIndex, 0, raw)
		}
		decoder.openAIPartText[fmt.Sprintf("%d:%d", choiceIndex, 0)] += text
		event := decoder.baseEvent(MaheshvaraEventTextDelta, raw)
		event.ChoiceIndex = choiceIndex
		event.Delta = text
		return []MaheshvaraStreamEvent{event}
	}
	parts := interfaceToContentParts(value)
	events := make([]MaheshvaraStreamEvent, 0, len(parts))
	for index := range parts {
		part := parts[index]
		event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
		event.ChoiceIndex = choiceIndex
		event.ContentIndex = index
		switch part.Type {
		case MaheshvaraContentText:
			event.Type = MaheshvaraEventTextDelta
			event.Delta = part.Text
		case MaheshvaraContentReasoning:
			event.Type = MaheshvaraEventReasoningDelta
			event.ReasoningDelta = firstNonEmptyString(part.ReasoningText, part.Text)
		case MaheshvaraContentRefusal:
			event.Type = MaheshvaraEventRefusalDelta
			event.RefusalDelta = part.Text
		default:
			event.ContentPart = &part
		}
		if event.Type == MaheshvaraEventTextDelta && snapshot {
			suffixEvents := decoder.openAISnapshotTextSuffix(event.Delta, choiceIndex, index, raw)
			events = append(events, suffixEvents...)
			continue
		}
		if maheshvaraStreamEventHasOutput(event) {
			if event.Type == MaheshvaraEventTextDelta {
				decoder.openAIPartText[fmt.Sprintf("%d:%d", choiceIndex, index)] += event.Delta
			}
			if event.Type == MaheshvaraEventReasoningDelta {
				decoder.openAIChoiceReasoned[choiceIndex] = true
			}
			events = append(events, event)
		}
	}
	return events
}

// openAISnapshotTextSuffix 应用终态快照的后缀语义（PC7f）：完整文本与已流式
// 输出的前缀一致时只补缺失后缀；完全相同或分歧（非前缀）时不再重发，避免
// 下游收到重复/冲突内容。
func (decoder *MaheshvaraStreamDecoder) openAISnapshotTextSuffix(text string, choiceIndex, contentIndex int, raw map[string]any) []MaheshvaraStreamEvent {
	streamed := decoder.openAIPartText[fmt.Sprintf("%d:%d", choiceIndex, contentIndex)]
	if text == streamed || streamed == "" {
		if streamed == "" {
			decoder.openAIPartText[fmt.Sprintf("%d:%d", choiceIndex, contentIndex)] = text
			event := decoder.baseEvent(MaheshvaraEventTextDelta, raw)
			event.ChoiceIndex = choiceIndex
			event.Delta = text
			return []MaheshvaraStreamEvent{event}
		}
		return nil
	}
	if strings.HasPrefix(text, streamed) {
		suffix := text[len(streamed):]
		decoder.openAIPartText[fmt.Sprintf("%d:%d", choiceIndex, contentIndex)] = text
		event := decoder.baseEvent(MaheshvaraEventTextDelta, raw)
		event.ChoiceIndex = choiceIndex
		event.Delta = suffix
		return []MaheshvaraStreamEvent{event}
	}
	return nil
}

func allMaheshvaraChoicesFinished(seen, finished map[int]bool) bool {
	for index := range seen {
		if !finished[index] {
			return false
		}
	}
	return true
}
