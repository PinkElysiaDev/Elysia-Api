package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// 批次四回归：往返保真——n>1 拒绝、user 映射、缓存寿命回写、服务端工具项
// 整项往返、Claude system 块级缓存标记。

func TestChatRequestRejectsMultipleCandidates(t *testing.T) {
	_, err := OpenAIChatToMaheshvara([]byte(`{"model":"grp","n":3,"messages":[{"role":"user","content":"hi"}]}`))
	if err == nil {
		t.Fatalf("n != 1 must be rejected explicitly")
	}
	if !strings.Contains(err.Error(), "n must be 1") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := OpenAIChatToMaheshvara([]byte(`{"model":"grp","n":1,"messages":[{"role":"user","content":"hi"}]}`)); err != nil {
		t.Fatalf("n == 1 must be accepted: %v", err)
	}
}

func TestUserMappedToClaudeMetadataAndResponses(t *testing.T) {
	req, err := OpenAIChatToMaheshvara([]byte(`{"model":"grp","user":"caller-42","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	claudeTarget, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatalf("render claude: %v", err)
	}
	var claudePayload map[string]any
	if err := json.Unmarshal(claudeTarget, &claudePayload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	metadata, _ := claudePayload["metadata"].(map[string]any)
	if metadata == nil || metadata["user_id"] != "caller-42" {
		t.Fatalf("user must map to metadata.user_id on Claude target: %s", claudeTarget)
	}
	responsesTarget, err := MaheshvaraToOpenAIResponses(req, nil)
	if err != nil {
		t.Fatalf("render responses: %v", err)
	}
	if !strings.Contains(string(responsesTarget), `"user":"caller-42"`) {
		t.Fatalf("user missing on Responses rebuild path: %s", responsesTarget)
	}
}

func TestPromptCacheRetentionWrittenToChatTarget(t *testing.T) {
	req, err := OpenAIChatToMaheshvara([]byte(`{"model":"grp","prompt_cache_key":"k","prompt_cache_retention":"24h","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	target, err := MaheshvaraToOpenAIChat(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(target), `"prompt_cache_retention":"24h"`) {
		t.Fatalf("prompt_cache_retention must be written back to chat target: %s", target)
	}
}

// Responses 服务端工具项（web_search_call 等）整项零损往返：action/results
// 载荷不再只剩 ID/Type 空壳。
func TestResponsesServerToolItemRoundTrip(t *testing.T) {
	body := `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[` +
		`{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"olympics"},"results":[{"title":"Example","url":"https://example.com"}]}` +
		`],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`
	var resp OpenAIResponsesResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.RawOutputs) != 1 {
		t.Fatalf("raw outputs must be captured: %+v", resp.RawOutputs)
	}
	maheshvara, err := OpenAIResponsesResponseToMaheshvara(&resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rendered, err := MaheshvaraToOpenAIResponsesResponse(maheshvara)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	encoded, err := json.Marshal(rendered)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, required := range []string{`"web_search_call"`, "olympics", "https://example.com", `"id":"ws_1"`} {
		if !strings.Contains(string(encoded), required) {
			t.Fatalf("server-tool payload lost (%s): %s", required, encoded)
		}
	}
}

// Claude 服务端工具块（server_tool_use / web_search_tool_result）整块保真。
func TestClaudeServerToolBlockRoundTrip(t *testing.T) {
	var resp ClaudeResponse
	body := `{"id":"msg_1","model":"claude-x","stop_reason":"end_turn","role":"assistant","content":[` +
		`{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"olympics"}},` +
		`{"type":"text","text":"回答"}]}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	maheshvara, err := AnthropicResponseToMaheshvara(&resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rendered, err := MaheshvaraToAnthropicResponse(maheshvara)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	encoded, err := json.Marshal(rendered.Content)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"server_tool_use"`) || !strings.Contains(string(encoded), "olympics") || !strings.Contains(string(encoded), "srvtoolu_1") {
		t.Fatalf("server tool block payload lost: %s", encoded)
	}

	// 请求侧：客户端回传服务端工具块，转发 Claude 上游时整块回放。
	req, err := AnthropicToMaheshvara([]byte(`{"model":"grp","max_tokens":16,"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"olympics"}},{"type":"text","text":"回答"}]}]}`))
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	target, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatalf("render request: %v", err)
	}
	if !strings.Contains(string(target), "srvtoolu_1") || !strings.Contains(string(target), "olympics") {
		t.Fatalf("server tool block lost on request path: %s", target)
	}
}

// Claude system 数组块的块级 cache_control 标记（缓存省钱点）不因拍平丢失。
func TestClaudeSystemBlockCacheControlRoundTrip(t *testing.T) {
	req, err := AnthropicToMaheshvara([]byte(`{"model":"grp","max_tokens":16,"system":[{"type":"text","text":"长系统提示词","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	target, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(target), "cache_control") || !strings.Contains(string(target), "ephemeral") {
		t.Fatalf("block-level cache_control lost on system: %s", target)
	}
	if !strings.Contains(string(target), "长系统提示词") {
		t.Fatalf("system text lost: %s", target)
	}
}
