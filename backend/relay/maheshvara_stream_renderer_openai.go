package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

type maheshvaraOpenAIToolRenderState struct {
	id        string
	name      string
	arguments strings.Builder
}

type maheshvaraOpenAIRenderState struct {
	roleSent                map[int]bool
	finishSent              map[int]bool
	tools                   map[int]*maheshvaraOpenAIToolRenderState
	pendingGeminiSignatures map[int]string
	doneSent                bool
}

func newMaheshvaraOpenAIRenderState() *maheshvaraOpenAIRenderState {
	return &maheshvaraOpenAIRenderState{
		roleSent:                make(map[int]bool),
		finishSent:              make(map[int]bool),
		tools:                   make(map[int]*maheshvaraOpenAIToolRenderState),
		pendingGeminiSignatures: make(map[int]string),
	}
}

func (renderer *MaheshvaraStreamRenderer) writeOpenAIChat(event *MaheshvaraStreamEvent) error {
	if event == nil {
		return nil
	}
	choiceIndex := event.ChoiceIndex
	switch event.Type {
	case CanonicalEventResponseCreated, CanonicalEventResponseInProgress:
		if event.Role == "" || renderer.openAI.roleSent[choiceIndex] {
			return nil
		}
		return renderer.writeOpenAIChatChunk(choiceIndex, map[string]any{"role": event.Role}, "", nil)
	case CanonicalEventUsageDelta:
		return renderer.writeOpenAIChatChunk(choiceIndex, nil, "", renderer.usage)
	case CanonicalEventTextDelta:
		if event.Delta == "" {
			return nil
		}
		return renderer.writeOpenAIChatChunk(choiceIndex, map[string]any{"content": event.Delta}, "", nil)
	case CanonicalEventReasoningDelta, CanonicalEventReasoningSummaryDelta:
		if event.ReasoningDelta == "" {
			return nil
		}
		return renderer.writeOpenAIChatChunk(choiceIndex, map[string]any{"reasoning_content": event.ReasoningDelta}, "", nil)
	case CanonicalEventReasoningSignatureDelta:
		if event.ReasoningSignatureDelta == "" {
			return nil
		}
		if signature := canonicalSignatureForProvider(event.ReasoningSignatureDelta, event.ReasoningSignatureProvider, CanonicalSignatureProviderGemini); signature != "" {
			renderer.openAI.pendingGeminiSignatures[choiceIndex] += signature
		}
		delta := map[string]any{"reasoning_signature": event.ReasoningSignatureDelta}
		if event.ReasoningSignatureProvider != "" {
			delta["reasoning_signature_provider"] = event.ReasoningSignatureProvider
		}
		return renderer.writeOpenAIChatChunk(choiceIndex, delta, "", nil)
	case CanonicalEventRefusalDelta:
		if event.RefusalDelta == "" {
			return nil
		}
		return renderer.writeOpenAIChatChunk(choiceIndex, map[string]any{"refusal": event.RefusalDelta}, "", nil)
	case CanonicalEventContentPartAdded:
		content := canonicalPartToOpenAIStreamContent(event.ContentPart)
		if content == nil {
			return nil
		}
		return renderer.writeOpenAIChatChunk(choiceIndex, map[string]any{"content": []any{content}}, "", nil)
	case CanonicalEventFunctionCallAdded, CanonicalEventFunctionCallArgumentsDelta, CanonicalEventFunctionCallArgumentsDone:
		return renderer.writeOpenAIToolEvent(event)
	case CanonicalEventOutputItemAdded:
		if event.OutputItem != nil && event.OutputItem.Type == CanonicalOutputFunctionCall {
			copy := *event
			copy.Type = CanonicalEventFunctionCallAdded
			copy.ToolCallID = firstNonEmptyString(copy.ToolCallID, event.OutputItem.CallID)
			copy.ToolName = firstNonEmptyString(copy.ToolName, event.OutputItem.Name)
			return renderer.writeOpenAIToolEvent(&copy)
		}
	case CanonicalEventResponseCompleted:
		if renderer.openAI.finishSent[choiceIndex] {
			return nil
		}
		reason := event.FinishReason
		if reason == "" && len(renderer.openAI.tools) > 0 {
			reason = "tool_calls"
		}
		if reason == "" {
			reason = "stop"
		}
		return renderer.writeOpenAIChatChunk(choiceIndex, map[string]any{}, reason, nil)
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeOpenAIToolEvent(event *MaheshvaraStreamEvent) error {
	index := event.ToolCallIndex
	if index == 0 && event.OutputIndex != 0 {
		index = event.OutputIndex
	}
	state := renderer.openAI.tools[index]
	if state == nil {
		state = &maheshvaraOpenAIToolRenderState{}
		renderer.openAI.tools[index] = state
	}
	state.id = firstNonEmptyString(event.ToolCallID, state.id)
	if state.id == "" {
		// 上游分块可能遗漏 id，合成一个稳定 ID，保证下游客户端的
		// assistant tool_calls[].id 与后续 tool 消息能够对齐。
		state.id = ensureToolCallID("", event.ChoiceIndex, index)
	}
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
	function := map[string]any{}
	if state.name != "" && event.Type == CanonicalEventFunctionCallAdded {
		function["name"] = state.name
	}
	if argumentDelta != "" {
		function["arguments"] = argumentDelta
	} else if event.Type == CanonicalEventFunctionCallAdded {
		function["arguments"] = ""
	}
	toolCall := map[string]any{"index": index, "type": "function", "function": function}
	if event.Type == CanonicalEventFunctionCallAdded {
		if signature := renderer.openAI.pendingGeminiSignatures[event.ChoiceIndex]; signature != "" {
			toolCall["extra_content"] = map[string]any{"google": map[string]any{"thought_signature": signature}}
			delete(renderer.openAI.pendingGeminiSignatures, event.ChoiceIndex)
		}
	}
	toolCall["id"] = state.id
	if len(function) == 0 {
		return nil
	}
	return renderer.writeOpenAIChatChunk(event.ChoiceIndex, map[string]any{"tool_calls": []any{toolCall}}, "", nil)
}

func (renderer *MaheshvaraStreamRenderer) writeOpenAIChatChunk(choiceIndex int, delta map[string]any, finishReason string, usage *CanonicalUsage) error {
	chunk := map[string]any{
		"id":      renderer.responseID,
		"object":  "chat.completion.chunk",
		"created": renderer.createdAt,
		"model":   renderer.model,
	}
	if usage != nil && delta == nil && finishReason == "" {
		chunk["choices"] = []any{}
		chunk["usage"] = openAIUsageFromCanonical(usage)
		return renderer.writeSSEData(chunk)
	}
	if delta == nil {
		delta = map[string]any{}
	}
	if !renderer.openAI.roleSent[choiceIndex] && finishReason == "" {
		if _, exists := delta["role"]; !exists {
			delta["role"] = "assistant"
		}
		renderer.openAI.roleSent[choiceIndex] = true
	} else if _, exists := delta["role"]; exists {
		renderer.openAI.roleSent[choiceIndex] = true
	}
	choice := map[string]any{"index": choiceIndex, "delta": delta, "finish_reason": nil}
	if finishReason != "" {
		choice["finish_reason"] = canonicalStopToOpenAI(finishReason)
		renderer.openAI.finishSent[choiceIndex] = true
	}
	chunk["choices"] = []any{choice}
	return renderer.writeSSEData(chunk)
}

func (renderer *MaheshvaraStreamRenderer) finishOpenAIChat() error {
	if !renderer.openAI.finishSent[0] {
		if err := renderer.writeOpenAIChatChunk(0, map[string]any{}, "stop", nil); err != nil {
			return err
		}
	}
	if renderer.openAI.doneSent {
		return nil
	}
	renderer.openAI.doneSent = true
	return renderer.writeSSEDataString("[DONE]")
}

func (renderer *MaheshvaraStreamRenderer) abortOpenAIChat(streamErr error) error {
	payload := map[string]any{"error": map[string]any{"type": "upstream_stream_error", "message": streamErr.Error()}}
	if err := renderer.writeSSEData(payload); err != nil {
		return err
	}
	if renderer.openAI.doneSent {
		return nil
	}
	renderer.openAI.doneSent = true
	return renderer.writeSSEDataString("[DONE]")
}

func canonicalPartToOpenAIStreamContent(part *CanonicalContentPart) any {
	if part == nil {
		return nil
	}
	switch part.Type {
	case CanonicalContentText:
		if part.Text != "" {
			return map[string]any{"type": "text", "text": part.Text}
		}
	case CanonicalContentImage:
		if imageURL := imagePartToOpenAIURL(*part); imageURL != "" {
			image := map[string]any{"url": imageURL}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			return map[string]any{"type": "image_url", "image_url": image}
		}
	case CanonicalContentAudio:
		audio := map[string]any{}
		if data := firstNonEmptyString(part.AudioBase64, part.Data); data != "" {
			audio["data"] = data
		}
		if part.AudioURL != "" {
			audio["url"] = part.AudioURL
		}
		if part.Text != "" {
			audio["transcript"] = part.Text
		}
		if part.MediaType != "" {
			audio["format"] = part.MediaType
		}
		if len(audio) > 0 {
			return map[string]any{"type": "output_audio", "audio": audio}
		}
	case CanonicalContentVideo:
		if uri := firstNonEmptyString(part.VideoURL, part.URI); uri != "" {
			return map[string]any{"type": "video_url", "video_url": map[string]any{"url": uri}}
		}
	case CanonicalContentFile, CanonicalContentDocument:
		file := map[string]any{"type": "file"}
		if part.FileID != "" {
			file["file_id"] = part.FileID
		}
		if part.FileName != "" {
			file["filename"] = part.FileName
		}
		if part.FileData != "" {
			file["file_data"] = part.FileData
		}
		if part.URI != "" {
			file["file_url"] = part.URI
		}
		if len(file) > 1 {
			return file
		}
	case CanonicalContentToolOutput:
		return map[string]any{"type": "tool_result", "tool_call_id": part.ToolCallID, "content": part.ToolOutput}
	default:
		if raw, ok := part.Raw.(map[string]any); ok && len(raw) > 0 {
			return raw
		}
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeSSEData(payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return renderer.writeSSEDataString(string(body))
}

func (renderer *MaheshvaraStreamRenderer) writeSSEDataString(payload string) error {
	if renderer.writer == nil {
		return fmt.Errorf("nil stream writer")
	}
	if _, err := renderer.writer.WriteString("data: " + payload + "\n\n"); err != nil {
		return err
	}
	return renderer.writer.Flush()
}

func (renderer *MaheshvaraStreamRenderer) writeSSEEvent(eventType string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if renderer.writer == nil {
		return fmt.Errorf("nil stream writer")
	}
	if _, err := renderer.writer.WriteString("event: " + eventType + "\n"); err != nil {
		return err
	}
	if _, err := renderer.writer.WriteString("data: " + string(body) + "\n\n"); err != nil {
		return err
	}
	return renderer.writer.Flush()
}
