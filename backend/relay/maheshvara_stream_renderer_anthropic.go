package relay

import (
	"fmt"
	"strings"
)

type maheshvaraClaudeToolRenderState struct {
	id        string
	name      string
	arguments strings.Builder
	emitted   bool
	index     int
}

type maheshvaraClaudeRenderState struct {
	started     bool
	stopSent    bool
	active      bool
	activeKey   string
	activeType  string
	activeIndex int
	nextIndex   int
	tools       map[int]*maheshvaraClaudeToolRenderState
	toolOrder   []int
}

func newMaheshvaraClaudeRenderState() *maheshvaraClaudeRenderState {
	return &maheshvaraClaudeRenderState{tools: make(map[int]*maheshvaraClaudeToolRenderState)}
}

func (renderer *MaheshvaraStreamRenderer) writeClaude(event *MaheshvaraStreamEvent) error {
	if event == nil {
		return nil
	}
	switch event.Type {
	case CanonicalEventResponseCreated, CanonicalEventResponseInProgress:
		return renderer.startClaude()
	case CanonicalEventUsageDelta:
		return nil
	case CanonicalEventTextDelta:
		if event.Delta == "" {
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("text", event), "text", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "text_delta", "text": event.Delta}})
	case CanonicalEventReasoningDelta, CanonicalEventReasoningSummaryDelta:
		if event.ReasoningDelta == "" {
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("thinking", event), "thinking", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "thinking_delta", "thinking": event.ReasoningDelta}})
	case CanonicalEventReasoningSignatureDelta:
		if canonicalSignatureForProvider(event.ReasoningSignatureDelta, event.ReasoningSignatureProvider, CanonicalSignatureProviderAnthropic) == "" {
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("thinking", event), "thinking", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "signature_delta", "signature": event.ReasoningSignatureDelta}})
	case CanonicalEventRefusalDelta:
		if event.RefusalDelta == "" {
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("refusal", event), "text", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "text_delta", "text": event.RefusalDelta}})
	case CanonicalEventContentPartAdded:
		return renderer.writeClaudeContentPart(event)
	case CanonicalEventFunctionCallAdded, CanonicalEventFunctionCallArgumentsDelta, CanonicalEventFunctionCallArgumentsDone:
		return renderer.writeClaudeToolEvent(event)
	case CanonicalEventOutputItemDone, CanonicalEventContentPartDone:
		return renderer.closeClaudeBlock()
	case CanonicalEventResponseCompleted:
		return renderer.completeClaude(event.FinishReason, event.StopSequence)
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) startClaude() error {
	if renderer.claude.started {
		return nil
	}
	renderer.claude.started = true
	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if renderer.usage != nil {
		usage["input_tokens"] = renderer.usage.InputTokens
		usage["output_tokens"] = renderer.usage.OutputTokens
		if renderer.usage.CachedInputTokens != 0 {
			usage["cache_read_input_tokens"] = renderer.usage.CachedInputTokens
		}
		if renderer.usage.CacheCreationInputTokens != 0 {
			usage["cache_creation_input_tokens"] = renderer.usage.CacheCreationInputTokens
		}
	}
	message := map[string]any{"id": renderer.responseID, "type": "message", "role": "assistant", "model": renderer.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage}
	return renderer.writeSSEEvent("message_start", map[string]any{"type": "message_start", "message": message})
}

func (renderer *MaheshvaraStreamRenderer) ensureClaudeBlock(key, blockType string, event *MaheshvaraStreamEvent, contentBlock map[string]any) error {
	if err := renderer.startClaude(); err != nil {
		return err
	}
	if renderer.claude.active && renderer.claude.activeKey == key {
		return nil
	}
	if err := renderer.closeClaudeBlock(); err != nil {
		return err
	}
	if contentBlock == nil {
		contentBlock = map[string]any{"type": blockType}
		switch blockType {
		case "text":
			contentBlock["text"] = ""
		case "thinking":
			contentBlock["thinking"] = ""
		case "tool_use":
			contentBlock["id"] = firstNonEmptyString(event.ToolCallID, event.ToolName)
			contentBlock["name"] = event.ToolName
			contentBlock["input"] = map[string]any{}
		}
	}
	renderer.claude.active = true
	renderer.claude.activeKey = key
	renderer.claude.activeType = blockType
	renderer.claude.activeIndex = renderer.claude.nextIndex
	return renderer.writeSSEEvent("content_block_start", map[string]any{"type": "content_block_start", "index": renderer.claude.activeIndex, "content_block": contentBlock})
}

func (renderer *MaheshvaraStreamRenderer) closeClaudeBlock() error {
	if !renderer.claude.active {
		return nil
	}
	index := renderer.claude.activeIndex
	renderer.claude.active = false
	renderer.claude.activeKey = ""
	renderer.claude.activeType = ""
	renderer.claude.nextIndex++
	return renderer.writeSSEEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func (renderer *MaheshvaraStreamRenderer) writeClaudeToolEvent(event *MaheshvaraStreamEvent) error {
	index := event.ToolCallIndex
	if index == 0 && event.OutputIndex != 0 {
		index = event.OutputIndex
	}
	state := renderer.claude.tools[index]
	if state == nil {
		state = &maheshvaraClaudeToolRenderState{index: index}
		renderer.claude.tools[index] = state
		renderer.claude.toolOrder = append(renderer.claude.toolOrder, index)
	}
	state.id = firstNonEmptyString(event.ToolCallID, state.id)
	state.name = firstNonEmptyString(event.ToolName, state.name)
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
			argumentDelta = complete
		}
	}
	if argumentDelta != "" {
		state.arguments.WriteString(argumentDelta)
	}
	if event.Type != CanonicalEventFunctionCallArgumentsDone {
		return nil
	}
	return renderer.emitClaudeTool(state)
}

func (renderer *MaheshvaraStreamRenderer) emitClaudeTool(state *maheshvaraClaudeToolRenderState) error {
	if state == nil || state.emitted {
		return nil
	}
	if strings.TrimSpace(state.name) == "" {
		return fmt.Errorf("cannot render Anthropic tool_use without a function name")
	}
	event := &MaheshvaraStreamEvent{ToolCallID: state.id, ToolName: state.name}
	key := "tool:" + firstNonEmptyString(state.id, state.name, fmt.Sprintf("%d", state.index))
	if err := renderer.ensureClaudeBlock(key, "tool_use", event, nil); err != nil {
		return err
	}
	arguments := state.arguments.String()
	if arguments == "" {
		arguments = "{}"
	}
	if err := renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": arguments}}); err != nil {
		return err
	}
	state.emitted = true
	return renderer.closeClaudeBlock()
}

func (renderer *MaheshvaraStreamRenderer) writeClaudeContentPart(event *MaheshvaraStreamEvent) error {
	part := event.ContentPart
	if part == nil {
		return nil
	}
	switch part.Type {
	case CanonicalContentReasoning:
		return nil
	case CanonicalContentImage:
		if source := imagePartToClaudeSource(*part); source != nil {
			return renderer.writeClaudeStandaloneBlock(map[string]any{"type": "image", "source": source})
		}
	case CanonicalContentFile, CanonicalContentDocument:
		if block := canonicalDocumentToClaudeBlock(*part); block != nil {
			return renderer.writeClaudeStandaloneBlock(block)
		}
	case CanonicalContentAudio, CanonicalContentVideo:
		if block := canonicalMediaToClaudeBlock(*part); block != nil {
			return renderer.writeClaudeStandaloneBlock(block)
		}
	case CanonicalContentToolOutput:
		if part.ToolOutput != "" {
			return renderer.writeClaude(&MaheshvaraStreamEvent{Type: CanonicalEventTextDelta, Delta: part.ToolOutput, OutputIndex: event.OutputIndex, ContentIndex: event.ContentIndex})
		}
	default:
		if raw, ok := part.Raw.(map[string]any); ok && len(raw) > 0 {
			return renderer.writeClaudeStandaloneBlock(raw)
		}
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeClaudeStandaloneBlock(block map[string]any) error {
	key := fmt.Sprintf("standalone:%d", renderer.claude.nextIndex)
	if err := renderer.ensureClaudeBlock(key, stringValue(block["type"]), &MaheshvaraStreamEvent{}, block); err != nil {
		return err
	}
	return renderer.closeClaudeBlock()
}

func (renderer *MaheshvaraStreamRenderer) completeClaude(reason, stopSequence string) error {
	if renderer.claude.stopSent {
		return nil
	}
	if err := renderer.startClaude(); err != nil {
		return err
	}
	for _, index := range renderer.claude.toolOrder {
		if err := renderer.emitClaudeTool(renderer.claude.tools[index]); err != nil {
			return err
		}
	}
	if err := renderer.closeClaudeBlock(); err != nil {
		return err
	}
	usage := map[string]any{}
	if renderer.usage != nil {
		usage["output_tokens"] = renderer.usage.OutputTokens
		if renderer.usage.InputTokens != 0 {
			usage["input_tokens"] = renderer.usage.InputTokens
		}
	}
	if reason == "" {
		reason = "stop"
	}
	delta := map[string]any{"stop_reason": canonicalStopToClaude(reason), "stop_sequence": nil}
	if stopSequence != "" {
		delta["stop_sequence"] = stopSequence
	}
	if err := renderer.writeSSEEvent("message_delta", map[string]any{"type": "message_delta", "delta": delta, "usage": usage}); err != nil {
		return err
	}
	renderer.claude.stopSent = true
	return renderer.writeSSEEvent("message_stop", map[string]any{"type": "message_stop"})
}

func (renderer *MaheshvaraStreamRenderer) finishClaude() error {
	if renderer.claude.stopSent {
		return nil
	}
	return renderer.completeClaude("stop", "")
}

func (renderer *MaheshvaraStreamRenderer) abortClaude(streamErr error) error {
	_ = renderer.closeClaudeBlock()
	return renderer.writeSSEEvent("error", map[string]any{"type": "error", "error": map[string]any{"type": "upstream_stream_error", "message": streamErr.Error()}})
}

func claudeStreamKey(kind string, event *MaheshvaraStreamEvent) string {
	if event == nil {
		return kind
	}
	return fmt.Sprintf("%s:%d:%d:%d", kind, event.ChoiceIndex, event.OutputIndex, event.ContentIndex)
}
