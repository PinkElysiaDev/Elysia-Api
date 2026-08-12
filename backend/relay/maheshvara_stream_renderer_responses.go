package relay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type maheshvaraResponsesMessageState struct {
	id             string
	outputIndex    int
	textIndex      int
	refusalIndex   int
	textStarted    bool
	refusalStarted bool
	textDone       bool
	refusalDone    bool
	text           strings.Builder
	refusal        strings.Builder
	extraParts     map[int]any
	done           bool
}

type maheshvaraResponsesReasoningState struct {
	id          string
	outputIndex int
	started     bool
	text        strings.Builder
	done        bool
}

type maheshvaraResponsesToolState struct {
	id          string
	callID      string
	name        string
	outputIndex int
	arguments   strings.Builder
	added       bool
	done        bool
}

type maheshvaraResponsesRenderState struct {
	started    bool
	completed  bool
	sequence   int64
	responseID string
	model      string
	createdAt  int64
	nextOutput int
	messages   map[int]*maheshvaraResponsesMessageState
	reasoning  map[int]*maheshvaraResponsesReasoningState
	tools      map[string]*maheshvaraResponsesToolState
	toolOrder  []string
}

func newMaheshvaraResponsesRenderState(responseID, model string, createdAt int64) *maheshvaraResponsesRenderState {
	return &maheshvaraResponsesRenderState{
		responseID: responseID,
		model:      model,
		createdAt:  createdAt,
		messages:   make(map[int]*maheshvaraResponsesMessageState),
		reasoning:  make(map[int]*maheshvaraResponsesReasoningState),
		tools:      make(map[string]*maheshvaraResponsesToolState),
	}
}

func (renderer *MaheshvaraStreamRenderer) writeResponses(event *MaheshvaraStreamEvent) error {
	if event == nil {
		return nil
	}
	if err := renderer.ensureResponsesHeader(); err != nil {
		return err
	}
	switch event.Type {
	case CanonicalEventResponseCreated, CanonicalEventResponseInProgress, CanonicalEventUsageDelta:
		return nil
	case CanonicalEventTextDelta:
		return renderer.writeResponsesText(event.ChoiceIndex, event.Delta)
	case CanonicalEventTextDone:
		return renderer.finishResponsesText(event.ChoiceIndex, event.TextDone)
	case CanonicalEventRefusalDelta:
		return renderer.writeResponsesRefusal(event.ChoiceIndex, event.RefusalDelta)
	case CanonicalEventRefusalDone:
		return renderer.finishResponsesRefusal(event.ChoiceIndex, event.RefusalDone)
	case CanonicalEventReasoningDelta, CanonicalEventReasoningSummaryDelta:
		return renderer.writeResponsesReasoning(event.ChoiceIndex, event.ReasoningDelta)
	case CanonicalEventReasoningDone, CanonicalEventReasoningSummaryDone:
		return renderer.finishResponsesReasoning(event.ChoiceIndex, event.ReasoningDone)
	case CanonicalEventContentPartAdded:
		return renderer.writeResponsesContentPart(event)
	case CanonicalEventFunctionCallAdded, CanonicalEventFunctionCallArgumentsDelta, CanonicalEventFunctionCallArgumentsDone:
		return renderer.writeResponsesTool(event)
	case CanonicalEventOutputItemAdded:
		return renderer.writeResponsesOutputItem(event)
	case CanonicalEventResponseCompleted:
		return renderer.completeResponses()
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) ensureResponsesHeader() error {
	state := renderer.responses
	state.responseID = renderer.responseID
	state.model = renderer.model
	state.createdAt = renderer.createdAt
	if state.started {
		return nil
	}
	state.started = true
	base := map[string]any{"id": state.responseID, "object": "response", "created_at": state.createdAt, "status": "in_progress", "model": state.model, "output": []any{}}
	if err := renderer.writeResponsesEvent(CanonicalEventResponseCreated, map[string]any{"type": CanonicalEventResponseCreated, "response": base}); err != nil {
		return err
	}
	return renderer.writeResponsesEvent(CanonicalEventResponseInProgress, map[string]any{"type": CanonicalEventResponseInProgress, "response": base})
}

func (renderer *MaheshvaraStreamRenderer) ensureResponsesMessage(choiceIndex int) (*maheshvaraResponsesMessageState, error) {
	state := renderer.responses.messages[choiceIndex]
	if state != nil {
		return state, nil
	}
	state = &maheshvaraResponsesMessageState{id: newCanonicalResponseID("msg"), outputIndex: renderer.responses.nextOutput, textIndex: -1, refusalIndex: -1, extraParts: make(map[int]any)}
	renderer.responses.nextOutput++
	renderer.responses.messages[choiceIndex] = state
	item := map[string]any{"id": state.id, "type": CanonicalOutputMessage, "status": "in_progress", "role": "assistant", "content": []any{}}
	if err := renderer.writeResponsesEvent(CanonicalEventOutputItemAdded, map[string]any{"type": CanonicalEventOutputItemAdded, "output_index": state.outputIndex, "item": item}); err != nil {
		return nil, err
	}
	return state, nil
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesText(choiceIndex int, text string) error {
	if text == "" {
		return nil
	}
	state, err := renderer.ensureResponsesMessage(choiceIndex)
	if err != nil {
		return err
	}
	if !state.textStarted {
		state.textStarted = true
		state.textIndex = nextResponsesContentIndex(state)
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		if err := renderer.writeResponsesEvent(CanonicalEventContentPartAdded, map[string]any{"type": CanonicalEventContentPartAdded, "item_id": state.id, "output_index": state.outputIndex, "content_index": state.textIndex, "part": part}); err != nil {
			return err
		}
	}
	state.text.WriteString(text)
	return renderer.writeResponsesEvent(CanonicalEventTextDelta, map[string]any{"type": CanonicalEventTextDelta, "item_id": state.id, "output_index": state.outputIndex, "content_index": state.textIndex, "delta": text})
}

func (renderer *MaheshvaraStreamRenderer) finishResponsesText(choiceIndex int, text string) error {
	state := renderer.responses.messages[choiceIndex]
	if state == nil || !state.textStarted || state.textDone {
		return nil
	}
	if text != "" && state.text.Len() == 0 {
		state.text.WriteString(text)
	}
	state.textDone = true
	return renderer.writeResponsesEvent(CanonicalEventTextDone, map[string]any{"type": CanonicalEventTextDone, "item_id": state.id, "output_index": state.outputIndex, "content_index": state.textIndex, "text": state.text.String()})
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesRefusal(choiceIndex int, text string) error {
	if text == "" {
		return nil
	}
	state, err := renderer.ensureResponsesMessage(choiceIndex)
	if err != nil {
		return err
	}
	if !state.refusalStarted {
		state.refusalStarted = true
		state.refusalIndex = nextResponsesContentIndex(state)
		part := map[string]any{"type": "refusal", "refusal": ""}
		if err := renderer.writeResponsesEvent(CanonicalEventContentPartAdded, map[string]any{"type": CanonicalEventContentPartAdded, "item_id": state.id, "output_index": state.outputIndex, "content_index": state.refusalIndex, "part": part}); err != nil {
			return err
		}
	}
	state.refusal.WriteString(text)
	return renderer.writeResponsesEvent(CanonicalEventRefusalDelta, map[string]any{"type": CanonicalEventRefusalDelta, "item_id": state.id, "output_index": state.outputIndex, "content_index": state.refusalIndex, "delta": text})
}

func (renderer *MaheshvaraStreamRenderer) finishResponsesRefusal(choiceIndex int, text string) error {
	state := renderer.responses.messages[choiceIndex]
	if state == nil || !state.refusalStarted || state.refusalDone {
		return nil
	}
	if text != "" && state.refusal.Len() == 0 {
		state.refusal.WriteString(text)
	}
	state.refusalDone = true
	return renderer.writeResponsesEvent(CanonicalEventRefusalDone, map[string]any{"type": CanonicalEventRefusalDone, "item_id": state.id, "output_index": state.outputIndex, "content_index": state.refusalIndex, "refusal": state.refusal.String()})
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesReasoning(choiceIndex int, text string) error {
	if text == "" {
		return nil
	}
	state := renderer.responses.reasoning[choiceIndex]
	if state == nil {
		state = &maheshvaraResponsesReasoningState{id: newCanonicalResponseID("rs"), outputIndex: renderer.responses.nextOutput}
		renderer.responses.nextOutput++
		renderer.responses.reasoning[choiceIndex] = state
	}
	if !state.started {
		state.started = true
		item := map[string]any{"id": state.id, "type": CanonicalOutputReasoning, "status": "in_progress", "summary": []any{}}
		if err := renderer.writeResponsesEvent(CanonicalEventOutputItemAdded, map[string]any{"type": CanonicalEventOutputItemAdded, "output_index": state.outputIndex, "item": item}); err != nil {
			return err
		}
		if err := renderer.writeResponsesEvent("response.reasoning_summary_part.added", map[string]any{"type": "response.reasoning_summary_part.added", "item_id": state.id, "output_index": state.outputIndex, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}}); err != nil {
			return err
		}
	}
	state.text.WriteString(text)
	return renderer.writeResponsesEvent(CanonicalEventReasoningSummaryDelta, map[string]any{"type": CanonicalEventReasoningSummaryDelta, "item_id": state.id, "output_index": state.outputIndex, "summary_index": 0, "delta": text})
}

func (renderer *MaheshvaraStreamRenderer) finishResponsesReasoning(choiceIndex int, text string) error {
	state := renderer.responses.reasoning[choiceIndex]
	if state == nil || !state.started || state.done {
		return nil
	}
	if text != "" && state.text.Len() == 0 {
		state.text.WriteString(text)
	}
	state.done = true
	return renderer.writeResponsesEvent(CanonicalEventReasoningSummaryDone, map[string]any{"type": CanonicalEventReasoningSummaryDone, "item_id": state.id, "output_index": state.outputIndex, "summary_index": 0, "text": state.text.String()})
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesContentPart(event *MaheshvaraStreamEvent) error {
	if event == nil || event.ContentPart == nil {
		return nil
	}
	part, ok := canonicalPartToResponsesOutputContent(*event.ContentPart)
	if !ok {
		if raw, rawOK := event.ContentPart.Raw.(map[string]any); rawOK && len(raw) > 0 {
			partMap := raw
			return renderer.addResponsesExtraPart(event.ChoiceIndex, partMap)
		}
		return nil
	}
	encoded, err := json.Marshal(part)
	if err != nil {
		return err
	}
	var partMap map[string]any
	if err := json.Unmarshal(encoded, &partMap); err != nil {
		return err
	}
	return renderer.addResponsesExtraPart(event.ChoiceIndex, partMap)
}

func (renderer *MaheshvaraStreamRenderer) addResponsesExtraPart(choiceIndex int, part any) error {
	state, err := renderer.ensureResponsesMessage(choiceIndex)
	if err != nil {
		return err
	}
	contentIndex := nextResponsesContentIndex(state)
	state.extraParts[contentIndex] = part
	payload := map[string]any{"type": CanonicalEventContentPartAdded, "item_id": state.id, "output_index": state.outputIndex, "content_index": contentIndex, "part": part}
	if err := renderer.writeResponsesEvent(CanonicalEventContentPartAdded, payload); err != nil {
		return err
	}
	payload["type"] = CanonicalEventContentPartDone
	return renderer.writeResponsesEvent(CanonicalEventContentPartDone, payload)
}

func nextResponsesContentIndex(state *maheshvaraResponsesMessageState) int {
	for index := 0; ; index++ {
		if state.textStarted && state.textIndex == index {
			continue
		}
		if state.refusalStarted && state.refusalIndex == index {
			continue
		}
		if _, exists := state.extraParts[index]; exists {
			continue
		}
		return index
	}
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesTool(event *MaheshvaraStreamEvent) error {
	key := firstNonEmptyString(event.ToolCallID, fmt.Sprintf("tool_%d", event.ToolCallIndex))
	state := renderer.responses.tools[key]
	if state == nil {
		state = &maheshvaraResponsesToolState{id: newCanonicalResponseID("fc"), callID: event.ToolCallID, name: event.ToolName, outputIndex: renderer.responses.nextOutput}
		renderer.responses.nextOutput++
		renderer.responses.tools[key] = state
		renderer.responses.toolOrder = append(renderer.responses.toolOrder, key)
	}
	state.callID = firstNonEmptyString(event.ToolCallID, state.callID, state.id)
	if state.callID == "" {
		// function_call 事件的 call_id 缺失时合成稳定 ID，避免下游
		// 回传 function_call_output 时对不上调用。
		state.callID = ensureToolCallID("", event.ToolCallIndex, 0)
	}
	state.name = firstNonEmptyString(event.ToolName, state.name)
	if !state.added {
		state.added = true
		item := map[string]any{"id": state.id, "type": CanonicalOutputFunctionCall, "status": "in_progress", "call_id": state.callID, "name": state.name, "arguments": ""}
		if err := renderer.writeResponsesEvent(CanonicalEventOutputItemAdded, map[string]any{"type": CanonicalEventOutputItemAdded, "output_index": state.outputIndex, "item": item}); err != nil {
			return err
		}
	}
	argumentDelta := event.ToolArgumentsDelta
	if event.ToolArgumentsDone != "" {
		complete := event.ToolArgumentsDone
		current := state.arguments.String()
		switch {
		case complete == current:
			argumentDelta = ""
		case strings.HasPrefix(complete, current):
			argumentDelta = strings.TrimPrefix(complete, current)
		default:
			state.arguments.Reset()
			argumentDelta = complete
		}
	}
	if argumentDelta != "" {
		state.arguments.WriteString(argumentDelta)
		if err := renderer.writeResponsesEvent(CanonicalEventFunctionCallArgumentsDelta, map[string]any{"type": CanonicalEventFunctionCallArgumentsDelta, "item_id": state.id, "output_index": state.outputIndex, "delta": argumentDelta}); err != nil {
			return err
		}
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesOutputItem(event *MaheshvaraStreamEvent) error {
	item := event.OutputItem
	if item == nil {
		return nil
	}
	switch item.Type {
	case CanonicalOutputFunctionCall:
		added := &MaheshvaraStreamEvent{Type: CanonicalEventFunctionCallAdded, ToolCallIndex: event.OutputIndex, ToolCallID: item.CallID, ToolName: item.Name}
		if err := renderer.writeResponsesTool(added); err != nil {
			return err
		}
		if len(item.Arguments) > 0 {
			added.Type = CanonicalEventFunctionCallArgumentsDone
			added.ToolArgumentsDone = string(item.Arguments)
			return renderer.writeResponsesTool(added)
		}
	case CanonicalOutputReasoning:
		return renderer.writeResponsesReasoning(event.ChoiceIndex, canonicalReasoningText(*item))
	default:
		for index := range item.Content {
			part := item.Content[index]
			switch part.Type {
			case CanonicalContentText:
				if err := renderer.writeResponsesText(event.ChoiceIndex, part.Text); err != nil {
					return err
				}
			case CanonicalContentReasoning:
				if err := renderer.writeResponsesReasoning(event.ChoiceIndex, firstNonEmptyString(part.ReasoningText, part.Text)); err != nil {
					return err
				}
			case CanonicalContentRefusal:
				if err := renderer.writeResponsesRefusal(event.ChoiceIndex, part.Text); err != nil {
					return err
				}
			default:
				if err := renderer.writeResponsesContentPart(&MaheshvaraStreamEvent{ChoiceIndex: event.ChoiceIndex, ContentPart: &part}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type maheshvaraResponsesRenderedOutput struct {
	index int
	item  map[string]any
}

func (renderer *MaheshvaraStreamRenderer) completeResponses() error {
	state := renderer.responses
	if state.completed {
		return nil
	}
	if err := renderer.ensureResponsesHeader(); err != nil {
		return err
	}
	var outputs []maheshvaraResponsesRenderedOutput
	for choiceIndex, message := range state.messages {
		if message == nil || message.done {
			continue
		}
		if message.textStarted && !message.textDone {
			if err := renderer.finishResponsesText(choiceIndex, ""); err != nil {
				return err
			}
		}
		if message.refusalStarted && !message.refusalDone {
			if err := renderer.finishResponsesRefusal(choiceIndex, ""); err != nil {
				return err
			}
		}
		content := make([]any, responsesMessageContentCount(message))
		if message.textStarted {
			part := map[string]any{"type": "output_text", "text": message.text.String(), "annotations": []any{}}
			content[message.textIndex] = part
			if err := renderer.writeResponsesEvent(CanonicalEventContentPartDone, map[string]any{"type": CanonicalEventContentPartDone, "item_id": message.id, "output_index": message.outputIndex, "content_index": message.textIndex, "part": part}); err != nil {
				return err
			}
		}
		if message.refusalStarted {
			part := map[string]any{"type": "refusal", "refusal": message.refusal.String()}
			content[message.refusalIndex] = part
			if err := renderer.writeResponsesEvent(CanonicalEventContentPartDone, map[string]any{"type": CanonicalEventContentPartDone, "item_id": message.id, "output_index": message.outputIndex, "content_index": message.refusalIndex, "part": part}); err != nil {
				return err
			}
		}
		for index, part := range message.extraParts {
			content[index] = part
		}
		content = compactResponsesContent(content)
		item := map[string]any{"id": message.id, "type": CanonicalOutputMessage, "status": "completed", "role": "assistant", "content": content}
		if err := renderer.writeResponsesEvent(CanonicalEventOutputItemDone, map[string]any{"type": CanonicalEventOutputItemDone, "output_index": message.outputIndex, "item": item}); err != nil {
			return err
		}
		message.done = true
		outputs = append(outputs, maheshvaraResponsesRenderedOutput{index: message.outputIndex, item: item})
	}
	for choiceIndex, reasoning := range state.reasoning {
		if reasoning == nil || !reasoning.started || reasoning.done {
			continue
		}
		if err := renderer.finishResponsesReasoning(choiceIndex, ""); err != nil {
			return err
		}
	}
	for _, reasoning := range state.reasoning {
		if reasoning == nil || !reasoning.started {
			continue
		}
		part := map[string]any{"type": "summary_text", "text": reasoning.text.String()}
		if err := renderer.writeResponsesEvent("response.reasoning_summary_part.done", map[string]any{"type": "response.reasoning_summary_part.done", "item_id": reasoning.id, "output_index": reasoning.outputIndex, "summary_index": 0, "part": part}); err != nil {
			return err
		}
		item := map[string]any{"id": reasoning.id, "type": CanonicalOutputReasoning, "status": "completed", "summary": []any{part}}
		if err := renderer.writeResponsesEvent(CanonicalEventOutputItemDone, map[string]any{"type": CanonicalEventOutputItemDone, "output_index": reasoning.outputIndex, "item": item}); err != nil {
			return err
		}
		outputs = append(outputs, maheshvaraResponsesRenderedOutput{index: reasoning.outputIndex, item: item})
	}
	for _, key := range state.toolOrder {
		tool := state.tools[key]
		if tool == nil || tool.done {
			continue
		}
		if !tool.added {
			if err := renderer.writeResponsesTool(&MaheshvaraStreamEvent{Type: CanonicalEventFunctionCallAdded, ToolCallID: tool.callID, ToolName: tool.name, ToolCallIndex: tool.outputIndex}); err != nil {
				return err
			}
		}
		arguments := tool.arguments.String()
		if arguments == "" {
			arguments = "{}"
		}
		if err := renderer.writeResponsesEvent(CanonicalEventFunctionCallArgumentsDone, map[string]any{"type": CanonicalEventFunctionCallArgumentsDone, "item_id": tool.id, "output_index": tool.outputIndex, "arguments": arguments}); err != nil {
			return err
		}
		item := map[string]any{"id": tool.id, "type": CanonicalOutputFunctionCall, "status": "completed", "call_id": tool.callID, "name": tool.name, "arguments": arguments}
		if err := renderer.writeResponsesEvent(CanonicalEventOutputItemDone, map[string]any{"type": CanonicalEventOutputItemDone, "output_index": tool.outputIndex, "item": item}); err != nil {
			return err
		}
		tool.done = true
		outputs = append(outputs, maheshvaraResponsesRenderedOutput{index: tool.outputIndex, item: item})
	}
	sort.Slice(outputs, func(left, right int) bool { return outputs[left].index < outputs[right].index })
	outputItems := make([]any, 0, len(outputs))
	for _, output := range outputs {
		outputItems = append(outputItems, output.item)
	}
	usage := renderer.usage
	if usage == nil {
		usage = &CanonicalUsage{}
	}
	completed := map[string]any{"id": renderer.responseID, "object": "response", "created_at": renderer.createdAt, "status": "completed", "model": renderer.model, "output": outputItems, "usage": responsesUsageFromCanonical(usage)}
	state.completed = true
	return renderer.writeResponsesEvent(CanonicalEventResponseCompleted, map[string]any{"type": CanonicalEventResponseCompleted, "response": completed})
}

func responsesMessageContentCount(state *maheshvaraResponsesMessageState) int {
	maxIndex := -1
	if state.textStarted && state.textIndex > maxIndex {
		maxIndex = state.textIndex
	}
	if state.refusalStarted && state.refusalIndex > maxIndex {
		maxIndex = state.refusalIndex
	}
	for index := range state.extraParts {
		if index > maxIndex {
			maxIndex = index
		}
	}
	return maxIndex + 1
}

func compactResponsesContent(content []any) []any {
	result := make([]any, 0, len(content))
	for _, part := range content {
		if part != nil {
			result = append(result, part)
		}
	}
	return result
}

func (renderer *MaheshvaraStreamRenderer) finishResponses() error {
	if renderer.responses.completed {
		return nil
	}
	return renderer.completeResponses()
}

func (renderer *MaheshvaraStreamRenderer) abortResponses(streamErr error) error {
	if err := renderer.ensureResponsesHeader(); err != nil {
		return err
	}
	errorPayload := map[string]any{"type": "error", "error": map[string]any{"type": "upstream_stream_error", "message": streamErr.Error()}}
	if err := renderer.writeResponsesEvent("error", errorPayload); err != nil {
		return err
	}
	failed := map[string]any{"id": renderer.responseID, "object": "response", "created_at": renderer.createdAt, "status": "failed", "model": renderer.model, "output": []any{}, "error": map[string]any{"type": "upstream_stream_error", "message": streamErr.Error()}}
	renderer.responses.completed = true
	return renderer.writeResponsesEvent(CanonicalEventResponseFailed, map[string]any{"type": CanonicalEventResponseFailed, "response": failed})
}

func (renderer *MaheshvaraStreamRenderer) writeResponsesEvent(eventType string, payload map[string]any) error {
	renderer.responses.sequence++
	payload["sequence_number"] = renderer.responses.sequence
	if payload["type"] == nil {
		payload["type"] = eventType
	}
	return renderer.writeSSEEvent(eventType, payload)
}
