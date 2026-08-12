package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func openAIChatMessagesFromCanonical(t *testing.T, body []byte, format FormatType) []map[string]any {
	t.Helper()
	req, _, err := ConvertRequestToCanonical(body, format, "")
	if err != nil {
		t.Fatalf("ConvertRequestToCanonical: %v", err)
	}
	out, err := CanonicalToOpenAIChatRequest(req)
	if err != nil {
		t.Fatalf("CanonicalToOpenAIChatRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("unmarshal OpenAI wire body: %v", err)
	}
	raw, ok := wire["messages"].([]any)
	if !ok {
		t.Fatalf("missing messages in wire body: %s", out)
	}
	messages := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		messages = append(messages, item.(map[string]any))
	}
	return messages
}

func assertAssistantToolIDMatchesToolMessage(t *testing.T, messages []map[string]any, wantID string) {
	t.Helper()
	var assistantToolID string
	for _, msg := range messages {
		if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
			call := calls[0].(map[string]any)
			assistantToolID, _ = call["id"].(string)
		}
	}
	if assistantToolID != wantID {
		t.Fatalf("assistant tool_calls[].id = %q, want %q", assistantToolID, wantID)
	}
	var toolCallID string
	for _, msg := range messages {
		if role, _ := msg["role"].(string); role == "tool" {
			toolCallID, _ = msg["tool_call_id"].(string)
		}
	}
	if toolCallID != wantID {
		t.Fatalf("role:tool tool_call_id = %q, want %q", toolCallID, wantID)
	}
}

// 回归：Claude tool_use 缺 id 且 tool_result 缺 tool_use_id 时，转换出的
// OpenAI Chat 请求必须有稳定的非空 id，且 assistant 与 tool 消息对齐。
func TestClaudeToolUseMissingIDConvertsToStableOpenAIID(t *testing.T) {
	body := []byte(`{
		"model": "grp",
		"max_tokens": 100,
		"messages": [
			{"role": "assistant", "content": [{"type": "tool_use", "name": "lookup", "input": {"q": "x"}}]},
			{"role": "user", "content": [{"type": "tool_result", "content": "42"}]}
		]
	}`)
	messages := openAIChatMessagesFromCanonical(t, body, FormatClaude)
	assertAssistantToolIDMatchesToolMessage(t, messages, "call_0_0")
}

// 回归：OpenAI 输入本身的 tool_calls[].id 与 tool_call_id 为空时，
// canonical 往返后也必须补齐并对齐。
func TestOpenAIChatMissingToolCallIDGetsSynthesized(t *testing.T) {
	body := []byte(`{
		"model": "grp",
		"messages": [
			{"role": "assistant", "content": null, "tool_calls": [{"type": "function", "function": {"name": "lookup", "arguments": "{\"q\":1}"}}]},
			{"role": "tool", "tool_call_id": "", "content": "42"}
		]
	}`)
	messages := openAIChatMessagesFromCanonical(t, body, FormatOpenAI)
	assertAssistantToolIDMatchesToolMessage(t, messages, "call_0_0")
}

// 回归：Responses 输入的 function_call/function_call_output 缺 call_id 时，
// 转换为 OpenAI Chat 后 id 必须补齐；同时 InputItems 内两处 id 保持一致。
func TestResponsesFunctionCallMissingCallIDGetsSynthesized(t *testing.T) {
	body := []byte(`{
		"model": "grp",
		"input": [
			{"type": "function_call", "name": "lookup", "arguments": "{}"},
			{"type": "function_call_output", "output": "42"}
		]
	}`)
	req, _, err := ResponsesRequestToCanonical(body)
	if err != nil {
		t.Fatalf("ResponsesRequestToCanonical: %v", err)
	}
	messages := openAIChatMessagesFromCanonical(t, body, FormatResponses)
	assertAssistantToolIDMatchesToolMessage(t, messages, "call_0_0")

	if len(req.InputItems) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(req.InputItems))
	}
	if req.InputItems[0].CallID != "call_0_0" || req.InputItems[1].CallID != "call_0_0" {
		t.Fatalf("input item call ids = %q/%q, want call_0_0/call_0_0", req.InputItems[0].CallID, req.InputItems[1].CallID)
	}
}

// 回归：透传路径的 OpenAI 请求只有在确实缺 id 时才重写；完整请求必须逐字节不变。
func TestNormalizeOpenAIToolCallIDsRepairsAndPreserves(t *testing.T) {
	missing := []byte(`{"model":"grp","messages":[{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"","content":"42"}],"unknown_field":{"a":1}}`)
	repaired, err := NormalizeOpenAIToolCallIDs(missing)
	if err != nil {
		t.Fatalf("NormalizeOpenAIToolCallIDs: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(repaired, &wire); err != nil {
		t.Fatalf("repaired body invalid json: %v", err)
	}
	if _, ok := wire["unknown_field"]; !ok {
		t.Fatal("unknown_field lost during repair")
	}
	messages := wire["messages"].([]any)
	assistant := messages[0].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	call := calls[0].(map[string]any)
	if call["id"] != "call_0_0" {
		t.Fatalf("repaired assistant tool id = %v, want call_0_0", call["id"])
	}
	tool := messages[1].(map[string]any)
	if tool["tool_call_id"] != "call_0_0" {
		t.Fatalf("repaired tool_call_id = %v, want call_0_0", tool["tool_call_id"])
	}

	complete := []byte(`{"model":"grp","messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"42"}]}`)
	out, err := NormalizeOpenAIToolCallIDs(complete)
	if err != nil {
		t.Fatalf("NormalizeOpenAIToolCallIDs(complete): %v", err)
	}
	if string(out) != string(complete) {
		t.Fatalf("complete body must be returned byte-identical, got %s", out)
	}
}

// 回归：流式事件缺 id 时，OpenAI chunk 渲染必须补出非空 id。
func TestStreamRenderersSynthesizeMissingToolCallID(t *testing.T) {
	event := &MaheshvaraStreamEvent{
		Type:               CanonicalEventFunctionCallArgumentsDelta,
		ChoiceIndex:        0,
		ToolCallIndex:      0,
		ToolName:           "lookup",
		ToolArgumentsDelta: "{}",
	}
	chunk, err := MaheshvaraStreamEventToOpenAIChatChunk(event)
	if err != nil {
		t.Fatalf("MaheshvaraStreamEventToOpenAIChatChunk: %v", err)
	}
	if !strings.Contains(string(chunk), `"id":"call_0_0"`) {
		t.Fatalf("chunk missing synthesized tool id: %s", chunk)
	}

	writer := &captureStreamWriter{}
	renderer := NewMaheshvaraStreamRenderer(FormatOpenAIChat, writer, "model")
	if err := renderer.Write(&MaheshvaraStreamEvent{Type: CanonicalEventFunctionCallAdded, ChoiceIndex: 0, ToolCallIndex: 0, ToolName: "lookup"}); err != nil {
		t.Fatalf("renderer.Write: %v", err)
	}
	if !strings.Contains(writer.String(), `"id":"call_0_0"`) {
		t.Fatalf("renderer output missing synthesized tool id: %s", writer.String())
	}
}
