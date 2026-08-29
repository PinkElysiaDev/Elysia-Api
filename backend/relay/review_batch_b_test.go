package relay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Review 批次 B 回归：citations 流合法事件、跨线块丢弃、空完成放行、
// Gemini 计数减法、provider-less 密文不回放、工具态不换键。

// Claude 流的 citations_delta 经网关转换后仍是合法 citations_delta 事件，
// 绝不成为 "citations_delta" 类型的独立 content block。
func TestCitationsDeltaStreamRendersLegalClaudeEvent(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"m1","role":"assistant","usage":{"input_tokens":3,"output_tokens":1}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"据实回答"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://example.com","title":"Example"}}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatClaude, FormatClaude, writer, "m"); err != nil {
		t.Fatalf("transform: %v\n%s", err, writer.String())
	}
	out := writer.String()
	if !strings.Contains(out, `"type":"citations_delta"`) || !strings.Contains(out, "example.com") {
		t.Fatalf("citations_delta must render as a legal delta event:\n%s", out)
	}
	if strings.Contains(out, `"content_block":{"type":"citations_delta"`) {
		t.Fatalf("citations must never become a standalone content block:\n%s", out)
	}
}

// Claude 的 server_tool_use 块不得原样发给 OpenAI 系上游（非法 content part）。
func TestUnknownClaudeBlockDroppedOnChatTarget(t *testing.T) {
	req, err := AnthropicToMaheshvara([]byte(`{"model":"grp","max_tokens":16,"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"server_tool_use","id":"s1","name":"web_search","input":{"query":"x"}},{"type":"text","text":"答"}]}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target, err := MaheshvaraToOpenAIChat(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(target), "server_tool_use") {
		t.Fatalf("claude-specific block leaked to chat upstream: %s", target)
	}
	if !strings.Contains(string(target), "答") {
		t.Fatalf("regular text must survive: %s", target)
	}
}

// content_filter 的空完成（有真实 finish_reason、无内容）不得被报为
// "无可表达输出"——那是合法拒答。
func TestEmptyContentFilterCompletionIsDelivered(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatOpenAIChat, FormatOpenAIChat, writer, "m"); err != nil {
		t.Fatalf("empty content_filter completion must be delivered: %v\n%s", err, writer.String())
	}
	out := writer.String()
	if !strings.Contains(out, `"finish_reason":"content_filter"`) {
		t.Fatalf("finish_reason must survive:\n%s", out)
	}
}

// 无 id 的两个 Gemini functionCall 分属不同 chunk：合成 id 不得相撞。
func TestGeminiSyntheticCallIDsUnique(t *testing.T) {
	decoder := NewMaheshvaraStreamDecoder(FormatGemini)
	ids := map[string]bool{}
	for chunk := 0; chunk < 2; chunk++ {
		events, err := decoder.Decode(SSEEvent{Data: `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"name":"f` + string(rune('0'+chunk)) + `"}}]}}]}`})
		if err != nil {
			t.Fatalf("decode chunk %d: %v", chunk, err)
		}
		for _, event := range events {
			if event.Type == CanonicalEventFunctionCallAdded && event.ToolCallID != "" {
				if ids[event.ToolCallID] {
					t.Fatalf("synthetic call id collision: %s", event.ToolCallID)
				}
				ids[event.ToolCallID] = true
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 distinct ids, got %v", ids)
	}
}

// candidateCount>1 与 chat n>1 一样显式拒绝。
func TestGeminiCandidateCountRejected(t *testing.T) {
	_, err := GeminiToMaheshvara([]byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"candidateCount":2}}`), "")
	if err == nil {
		t.Fatalf("candidateCount > 1 must be rejected")
	}
}

// Responses 并行调用缺 call_id 的输出按 FIFO 配对，不错位。
func TestInputCallIDFIFOPairing(t *testing.T) {
	req, _, err := OpenAIResponsesToMaheshvara([]byte(`{"model":"grp","input":[
		{"type":"function_call","call_id":"fc1","name":"a","arguments":"{}"},
		{"type":"function_call","call_id":"fc2","name":"b","arguments":"{}"},
		{"type":"function_call_output","output":"r1"},
		{"type":"function_call_output","output":"r2"}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	seen := []string{}
	for _, item := range req.InputItems {
		if item.Type == CanonicalInputFunctionCallOutput {
			seen = append(seen, item.CallID)
		}
	}
	if len(seen) != 2 || seen[0] != "fc1" || seen[1] != "fc2" {
		t.Fatalf("outputs must pair FIFO with calls, got %v", seen)
	}
}

// Usage details 指针化后不再序列化空 {} 覆盖原始子对象。
func TestUsageDetailsOmitEmpty(t *testing.T) {
	usage := openAIUsageFromCanonical(&CanonicalUsage{InputTokens: 10, OutputTokens: 5})
	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "_details") {
		t.Fatalf("empty details must be omitted: %s", encoded)
	}
}
