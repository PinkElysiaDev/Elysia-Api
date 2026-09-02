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
	case MaheshvaraEventResponseCreated, MaheshvaraEventResponseInProgress:
		return renderer.startClaude()
	case MaheshvaraEventUsageDelta:
		return nil
	case MaheshvaraEventAnnotationDelta:
		// 引用标注（citations）：翻译回 Claude 的合法 citations_delta 事件，
		// 挂在当前活动块上——绝不作为独立块回放（"citations_delta" 不是合法
		// content block 类型，严格 SDK 会报错）。
		if len(event.Annotations) == 0 || !renderer.claude.active {
			return nil
		}
		for _, citation := range event.Annotations {
			if err := renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "citations_delta", "citation": citation}}); err != nil {
				return err
			}
		}
		return nil
	case MaheshvaraEventTextDelta:
		if event.Delta == "" {
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("text", event), "text", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "text_delta", "text": event.Delta}})
	case MaheshvaraEventReasoningDelta, MaheshvaraEventReasoningSummaryDelta:
		if event.ReasoningDelta == "" {
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("thinking", event), "thinking", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "thinking_delta", "thinking": event.ReasoningDelta}})
	case MaheshvaraEventReasoningSignatureDelta:
		signature := event.ReasoningSignatureDelta
		if signature == "" {
			return nil
		}
		// 原生 anthropic 签名与已装信封的跨协议密文（maheshvara）可直接下发；
		// 其他厂商裸签名流式片段不下发——客户端无法续用，完整密文由非流式
		// 路径装信封后回放。
		switch strings.TrimSpace(event.ReasoningSignatureProvider) {
		case "", MaheshvaraSignatureProviderAnthropic, MaheshvaraSignatureProviderMaheshvara:
		default:
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("thinking", event), "thinking", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "signature_delta", "signature": signature}})
	case MaheshvaraEventRefusalDelta:
		if event.RefusalDelta == "" {
			return nil
		}
		if err := renderer.ensureClaudeBlock(claudeStreamKey("refusal", event), "text", event, nil); err != nil {
			return err
		}
		return renderer.writeSSEEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": renderer.claude.activeIndex, "delta": map[string]any{"type": "text_delta", "text": event.RefusalDelta}})
	case MaheshvaraEventContentPartAdded:
		return renderer.writeClaudeContentPart(event)
	case MaheshvaraEventFunctionCallAdded, MaheshvaraEventFunctionCallArgumentsDelta, MaheshvaraEventFunctionCallArgumentsDone:
		return renderer.writeClaudeToolEvent(event)
	case MaheshvaraEventOutputItemDone, MaheshvaraEventContentPartDone:
		return renderer.closeClaudeBlock()
	case MaheshvaraEventResponseCompleted:
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
		delta, replaced := deltaVsAccumulated(state.arguments.String(), event.ToolArgumentsDone)
		if replaced {
			// 终态值与累计增量分叉：丢弃脏前缀改写完整值（与 Gemini/Responses
			// 渲染器一致；不 Reset 会在下一次终态事件时双份拼接出非法 JSON）。
			state.arguments.Reset()
		}
		argumentDelta = delta
	}
	if argumentDelta != "" {
		state.arguments.WriteString(argumentDelta)
	}
	if event.Type != MaheshvaraEventFunctionCallArgumentsDone {
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
	case MaheshvaraContentReasoning:
		return nil
	case MaheshvaraContentImage:
		if source := imagePartToClaudeSource(*part); source != nil {
			return renderer.writeClaudeStandaloneBlock(map[string]any{"type": "image", "source": source})
		}
	case MaheshvaraContentFile, MaheshvaraContentDocument:
		if block := maheshvaraDocumentToClaudeBlock(*part); block != nil {
			return renderer.writeClaudeStandaloneBlock(block)
		}
	case MaheshvaraContentAudio, MaheshvaraContentVideo:
		if block := maheshvaraMediaToClaudeBlock(*part); block != nil {
			return renderer.writeClaudeStandaloneBlock(block)
		}
	case MaheshvaraContentToolOutput:
		if part.ToolOutput != "" {
			return renderer.writeClaude(&MaheshvaraStreamEvent{Type: MaheshvaraEventTextDelta, Delta: part.ToolOutput, OutputIndex: event.OutputIndex, ContentIndex: event.ContentIndex})
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
	delta := map[string]any{"stop_reason": maheshvaraStopToClaude(reason), "stop_sequence": nil}
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
