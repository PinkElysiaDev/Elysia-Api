package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

type maheshvaraGeminiToolRenderState struct {
	id        string
	name      string
	arguments strings.Builder
	emitted   bool
	index     int
}

type maheshvaraGeminiRenderState struct {
	tools             map[string]*maheshvaraGeminiToolRenderState
	toolOrder         []string
	pendingSignature  string
	toolSignatureSent map[int]bool
	finishSent        map[int]bool
}

func newMaheshvaraGeminiRenderState() *maheshvaraGeminiRenderState {
	return &maheshvaraGeminiRenderState{tools: make(map[string]*maheshvaraGeminiToolRenderState), toolSignatureSent: make(map[int]bool), finishSent: make(map[int]bool)}
}

func (renderer *MaheshvaraStreamRenderer) writeGemini(event *MaheshvaraStreamEvent) error {
	if event == nil {
		return nil
	}
	switch event.Type {
	case CanonicalEventUsageDelta:
		if renderer.usage == nil {
			return nil
		}
		return renderer.writeSSEData(map[string]any{"usageMetadata": geminiUsageFromCanonical(renderer.usage)})
	case CanonicalEventTextDelta:
		if event.Delta == "" {
			return nil
		}
		part := map[string]any{"text": event.Delta}
		renderer.attachGeminiSignature(part)
		return renderer.writeGeminiPart(event.ChoiceIndex, part)
	case CanonicalEventReasoningDelta, CanonicalEventReasoningSummaryDelta:
		if event.ReasoningDelta == "" {
			return nil
		}
		part := map[string]any{"text": event.ReasoningDelta, "thought": true}
		renderer.attachGeminiSignature(part)
		return renderer.writeGeminiPart(event.ChoiceIndex, part)
	case CanonicalEventReasoningSignatureDelta:
		if signature := canonicalSignatureForProvider(event.ReasoningSignatureDelta, event.ReasoningSignatureProvider, CanonicalSignatureProviderGemini); signature != "" {
			renderer.gemini.pendingSignature += signature
		}
		return nil
	case CanonicalEventRefusalDelta:
		if event.RefusalDelta == "" {
			return nil
		}
		return renderer.writeGeminiPart(event.ChoiceIndex, map[string]any{"text": event.RefusalDelta})
	case CanonicalEventContentPartAdded:
		return renderer.writeGeminiContentPart(event)
	case CanonicalEventFunctionCallAdded, CanonicalEventFunctionCallArgumentsDelta, CanonicalEventFunctionCallArgumentsDone:
		return renderer.writeGeminiToolEvent(event)
	case CanonicalEventResponseCompleted:
		if err := renderer.flushGeminiTools(); err != nil {
			return err
		}
		if renderer.gemini.finishSent[event.ChoiceIndex] {
			return nil
		}
		reason := event.FinishReason
		if reason == "" {
			reason = "stop"
		}
		payload := map[string]any{"candidates": []any{map[string]any{"index": event.ChoiceIndex, "finishReason": canonicalStopToGemini(reason)}}}
		if renderer.usage != nil {
			payload["usageMetadata"] = geminiUsageFromCanonical(renderer.usage)
		}
		renderer.gemini.finishSent[event.ChoiceIndex] = true
		return renderer.writeSSEData(payload)
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeGeminiToolEvent(event *MaheshvaraStreamEvent) error {
	key := firstNonEmptyString(event.ToolCallID, fmt.Sprintf("tool_%d", event.ToolCallIndex))
	state := renderer.gemini.tools[key]
	if state == nil {
		state = &maheshvaraGeminiToolRenderState{index: event.ToolCallIndex}
		renderer.gemini.tools[key] = state
		renderer.gemini.toolOrder = append(renderer.gemini.toolOrder, key)
	}
	state.id = firstNonEmptyString(event.ToolCallID, state.id)
	state.name = firstNonEmptyString(event.ToolName, state.name)
	if event.ToolArgumentsDelta != "" {
		state.arguments.WriteString(event.ToolArgumentsDelta)
	}
	if event.ToolArgumentsDone != "" {
		complete := event.ToolArgumentsDone
		current := state.arguments.String()
		switch {
		case complete == current:
		case strings.HasPrefix(complete, current):
			state.arguments.WriteString(strings.TrimPrefix(complete, current))
		default:
			state.arguments.Reset()
			state.arguments.WriteString(complete)
		}
		return renderer.emitGeminiTool(state, event.ChoiceIndex)
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) emitGeminiTool(state *maheshvaraGeminiToolRenderState, choiceIndex int) error {
	if state == nil || state.emitted {
		return nil
	}
	if strings.TrimSpace(state.name) == "" {
		return fmt.Errorf("cannot render Gemini functionCall without a function name")
	}
	argumentsText := strings.TrimSpace(state.arguments.String())
	if argumentsText == "" {
		argumentsText = "{}"
	}
	var arguments any
	if err := json.Unmarshal([]byte(argumentsText), &arguments); err != nil {
		return fmt.Errorf("cannot render Gemini functionCall %q: invalid JSON arguments: %w", state.name, err)
	}
	if _, ok := arguments.(map[string]any); !ok {
		arguments = map[string]any{"value": arguments}
	}
	functionCall := map[string]any{"name": state.name, "args": arguments}
	if state.id != "" {
		functionCall["id"] = state.id
	}
	part := map[string]any{"functionCall": functionCall}
	renderer.attachGeminiSignature(part)
	if stringValue(part["thoughtSignature"]) == "" && !renderer.gemini.toolSignatureSent[choiceIndex] {
		part["thoughtSignature"] = geminiCrossProviderThoughtSignature
	}
	if stringValue(part["thoughtSignature"]) != "" {
		renderer.gemini.toolSignatureSent[choiceIndex] = true
	}
	state.emitted = true
	return renderer.writeGeminiPart(choiceIndex, part)
}

func (renderer *MaheshvaraStreamRenderer) flushGeminiTools() error {
	for _, key := range renderer.gemini.toolOrder {
		if err := renderer.emitGeminiTool(renderer.gemini.tools[key], 0); err != nil {
			return err
		}
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeGeminiContentPart(event *MaheshvaraStreamEvent) error {
	part := event.ContentPart
	if part == nil {
		return nil
	}
	if part.Type == CanonicalContentToolOutput {
		name := part.ToolCallID
		responseID := ""
		if raw, ok := part.Raw.(map[string]any); ok {
			name = firstNonEmptyString(stringValue(raw["name"]), name)
			responseID = firstNonEmptyString(stringValue(raw["id"]), stringValue(raw["call_id"]))
		}
		if name == "" {
			return fmt.Errorf("cannot render Gemini functionResponse without a function name")
		}
		response := map[string]any{"name": name, "response": geminiFunctionResponsePayload(part.ToolOutput)}
		if responseID == "" && part.ToolCallID != "" && part.ToolCallID != name {
			responseID = part.ToolCallID
		}
		if responseID != "" {
			response["id"] = responseID
		}
		return renderer.writeGeminiPart(event.ChoiceIndex, map[string]any{"functionResponse": response})
	}
	if rendered := canonicalPartToGeminiPart(*part); rendered != nil {
		renderer.attachGeminiSignature(rendered)
		return renderer.writeGeminiPart(event.ChoiceIndex, rendered)
	}
	if raw, ok := part.Raw.(map[string]any); ok && validGeminiStreamPart(raw) {
		return renderer.writeGeminiPart(event.ChoiceIndex, raw)
	}
	return nil
}

func (renderer *MaheshvaraStreamRenderer) writeGeminiPart(choiceIndex int, part map[string]any) error {
	if !validGeminiStreamPart(part) {
		return fmt.Errorf("refusing to emit an empty or invalid Gemini Part")
	}
	return renderer.writeSSEData(map[string]any{"candidates": []any{map[string]any{"index": choiceIndex, "content": map[string]any{"role": "model", "parts": []any{part}}}}})
}

func (renderer *MaheshvaraStreamRenderer) attachGeminiSignature(part map[string]any) {
	if renderer.gemini.pendingSignature == "" || part == nil {
		return
	}
	part["thoughtSignature"] = renderer.gemini.pendingSignature
	renderer.gemini.pendingSignature = ""
}

func validGeminiStreamPart(part map[string]any) bool {
	if part == nil {
		return false
	}
	if text, ok := part["text"].(string); ok && text != "" {
		return true
	}
	if call := mapValue(part["functionCall"]); call != nil && strings.TrimSpace(stringValue(call["name"])) != "" {
		return true
	}
	if response := mapValue(part["functionResponse"]); response != nil && strings.TrimSpace(stringValue(response["name"])) != "" {
		return true
	}
	if inline := mapValue(part["inlineData"]); inline != nil && stringValue(inline["data"]) != "" {
		return true
	}
	if file := mapValue(part["fileData"]); file != nil && stringValue(file["fileUri"]) != "" {
		return true
	}
	if executable := mapValue(part["executableCode"]); executable != nil && stringValue(executable["code"]) != "" {
		return true
	}
	if execution := mapValue(part["codeExecutionResult"]); execution != nil && (stringValue(execution["output"]) != "" || stringValue(execution["outcome"]) != "") {
		return true
	}
	return false
}

func (renderer *MaheshvaraStreamRenderer) finishGemini() error {
	if err := renderer.flushGeminiTools(); err != nil {
		return err
	}
	if renderer.gemini.finishSent[0] {
		return nil
	}
	renderer.gemini.finishSent[0] = true
	payload := map[string]any{"candidates": []any{map[string]any{"index": 0, "finishReason": "STOP"}}}
	if renderer.usage != nil {
		payload["usageMetadata"] = geminiUsageFromCanonical(renderer.usage)
	}
	return renderer.writeSSEData(payload)
}

func (renderer *MaheshvaraStreamRenderer) abortGemini(streamErr error) error {
	return renderer.writeSSEData(map[string]any{"error": map[string]any{"code": 502, "status": "UNAVAILABLE", "message": streamErr.Error()}})
}
