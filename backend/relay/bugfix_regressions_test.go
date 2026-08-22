package relay

// 全项目 bug 审查回归测试（批次 A：协议转换层）。
// 每个用例对应一个已修复的真实缺陷，防止回归。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A1 回归：客户端显式设置的小 max_tokens 必须原样透传给 Claude，
// 不能被默认值 65536 强制抬高。
func TestCanonicalToClaudeRequestRespectsExplicitMaxTokens(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":128}`)
	canonical, err := OpenAIChatRequestToCanonical(body)
	if err != nil {
		t.Fatalf("OpenAIChatRequestToCanonical: %v", err)
	}
	out, err := CanonicalToClaudeRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToClaudeRequest: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal claude request: %v", err)
	}
	if req["max_tokens"] != float64(128) {
		t.Fatalf("explicit max_tokens must pass through, got %v", req["max_tokens"])
	}

	// 未设置时仍兜底默认值。
	canonical.MaxOutputTokens = 0
	out, err = CanonicalToClaudeRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToClaudeRequest(default): %v", err)
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal claude default request: %v", err)
	}
	if req["max_tokens"] != float64(ClaudeDefaultMaxTokens) {
		t.Fatalf("missing max_tokens must default, got %v", req["max_tokens"])
	}
}

// A2 回归：Anthropic 的 cache_creation.ephemeral_* 是 cache_creation_input_tokens
// 的明细拆分，不得重复计入 input。
func TestClaudeUsageCacheCreationNotDoubleCounted(t *testing.T) {
	usage := ClaudeUsage{
		InputTokens:              70,
		OutputTokens:             20,
		CacheReadInputTokens:     25,
		CacheCreationInputTokens: 5,
		CacheCreation:            &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 5},
	}
	canonical := canonicalUsageFromClaudeUsage(usage)
	if canonical.InputTokens != 70+25+5 {
		t.Fatalf("ephemeral breakdown must not be added on top of the total, got input=%d", canonical.InputTokens)
	}

	// 只有明细、没有总数时（部分兼容实现）才用明细求和。
	usage.CacheCreationInputTokens = 0
	canonical = canonicalUsageFromClaudeUsage(usage)
	if canonical.InputTokens != 70+25+5 {
		t.Fatalf("breakdown-only usage should be summed, got input=%d", canonical.InputTokens)
	}

	// Claude → canonical → Claude 往返：input + cache_read + cache_creation 保持一致。
	roundTrip := claudeUsageFromCanonical(canonicalUsageFromClaudeUsage(ClaudeUsage{
		InputTokens: 70, OutputTokens: 20, CacheReadInputTokens: 25, CacheCreationInputTokens: 5,
	}))
	total := roundTrip.InputTokens + roundTrip.CacheReadInputTokens + roundTrip.CacheCreationInputTokens
	if total != 100 {
		t.Fatalf("round trip totals must stay consistent, got %d", total)
	}
}

// A3 回归：OpenAI role:"tool" 消息转 Claude/Gemini 必须产生 tool_result/
// functionResponse，而不是普通文本。
func TestOpenAIToolHistoryConvertsToToolResults(t *testing.T) {
	body := []byte(`{"model":"m","messages":[` +
		`{"role":"user","content":"run the tool"},` +
		`{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},` +
		`{"role":"tool","tool_call_id":"call_1","content":"42"}]}`)
	canonical, err := OpenAIChatRequestToCanonical(body)
	if err != nil {
		t.Fatalf("OpenAIChatRequestToCanonical: %v", err)
	}

	claudeBody, err := CanonicalToClaudeRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToClaudeRequest: %v", err)
	}
	var claudeReq struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content []ClaudeContent `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(claudeBody, &claudeReq); err != nil {
		t.Fatalf("unmarshal claude request: %v", err)
	}
	var sawToolUse, sawToolResult bool
	for _, msg := range claudeReq.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" && block.ID == "call_1" {
				sawToolUse = true
			}
			if block.Type == "tool_result" && block.ToolUseID == "call_1" && fmt.Sprint(block.Content) == "42" {
				sawToolResult = true
			}
		}
	}
	if !sawToolUse || !sawToolResult {
		t.Fatalf("claude conversion missing tool_use/tool_result: %s", claudeBody)
	}

	geminiBody, err := CanonicalToGeminiRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToGeminiRequest: %v", err)
	}
	if !strings.Contains(string(geminiBody), `"functionCall"`) || !strings.Contains(string(geminiBody), `"functionResponse"`) {
		t.Fatalf("gemini conversion missing functionCall/functionResponse: %s", geminiBody)
	}

	// OpenAI → canonical → OpenAI 往返不得产生重复的 tool 消息。
	openAIBody, err := CanonicalToOpenAIChatRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToOpenAIChatRequest: %v", err)
	}
	if strings.Count(string(openAIBody), `"role":"tool"`) != 1 {
		t.Fatalf("round trip must emit exactly one tool message: %s", openAIBody)
	}
}

// A4 回归：非枚举终止原因必须归一化到目标协议的合法枚举。
func TestStopReasonNormalization(t *testing.T) {
	cases := []struct {
		name                               string
		reason                             string
		wantOpenAI, wantClaude, wantGemini string
	}{
		{"claude stop_sequence", "stop_sequence", "stop", "stop_sequence", "STOP_SEQUENCE"},
		{"claude refusal", "refusal", "content_filter", "refusal", "SAFETY"},
		{"gemini safety", "SAFETY", "content_filter", "refusal", "SAFETY"},
		{"gemini recitation", "RECITATION", "content_filter", "refusal", "RECITATION"},
		{"openai content_filter", "content_filter", "content_filter", "refusal", "SAFETY"},
		{"length round trip", "length", "length", "max_tokens", "MAX_TOKENS"},
		{"tool_use", "tool_use", "tool_calls", "tool_use", "STOP"},
		{"totally unknown", "weird_vnd_reason", "stop", "end_turn", "STOP"},
		{"empty", "", "stop", "end_turn", "STOP"},
	}
	for _, tc := range cases {
		if got := canonicalStopToOpenAI(tc.reason); got != tc.wantOpenAI {
			t.Errorf("%s: canonicalStopToOpenAI = %q, want %q", tc.name, got, tc.wantOpenAI)
		}
		if got := canonicalStopToClaude(tc.reason); got != tc.wantClaude {
			t.Errorf("%s: canonicalStopToClaude = %q, want %q", tc.name, got, tc.wantClaude)
		}
		if got := canonicalStopToGemini(tc.reason); got != tc.wantGemini {
			t.Errorf("%s: canonicalStopToGemini = %q, want %q", tc.name, got, tc.wantGemini)
		}
	}
}

// A5 回归：OpenAI assistant 消息的 reasoning_content 转 Claude 时 thinking
// 块必须位于 content 首位。
func TestOpenAIReasoningPrependedAsFirstClaudeBlock(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"answer text","reasoning_content":"because"}]}`)
	canonical, err := OpenAIChatRequestToCanonical(body)
	if err != nil {
		t.Fatalf("OpenAIChatRequestToCanonical: %v", err)
	}
	out, err := CanonicalToClaudeRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToClaudeRequest: %v", err)
	}
	var req struct {
		Messages []struct {
			Content []ClaudeContent `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	last := req.Messages[len(req.Messages)-1]
	if len(last.Content) == 0 || last.Content[0].Type != "thinking" {
		t.Fatalf("thinking block must be first, got %s", out)
	}
}

// A5 回归：Claude 响应 [thinking, text] 经 canonical 往返后 thinking 仍居首。
func TestClaudeResponseRoundTripKeepsThinkingFirst(t *testing.T) {
	resp := &ClaudeResponse{
		ID: "msg_1", Type: "message", Role: "assistant",
		Content: []ClaudeContent{
			{Type: "thinking", Thinking: "plan"},
			{Type: "text", Text: "answer"},
		},
	}
	canonical, err := ClaudeResponseToCanonical(resp)
	if err != nil {
		t.Fatalf("ClaudeResponseToCanonical: %v", err)
	}
	out, err := CanonicalToClaudeResponse(canonical)
	if err != nil {
		t.Fatalf("CanonicalToClaudeResponse: %v", err)
	}
	if len(out.Content) == 0 || out.Content[0].Type != "thinking" {
		t.Fatalf("thinking must stay first after round trip, got %+v", out.Content)
	}
}

// A6 回归：Claude user 轮混合 [tool_result, text] 转 OpenAI 时，
// role:"tool" 消息必须先于补充文本输出，紧跟 assistant tool_calls。
func TestClaudeMixedToolResultAndTextOrderToOpenAI(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,"system":"sys","messages":[` +
		`{"role":"user","content":"run"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"x"}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"42"},{"type":"text","text":"now summarize"}]}]}`)
	canonical, err := ClaudeRequestToCanonical(body)
	if err != nil {
		t.Fatalf("ClaudeRequestToCanonical: %v", err)
	}
	out, err := CanonicalToOpenAIChatRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToOpenAIChatRequest: %v", err)
	}
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	roles := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		roles = append(roles, m.Role)
	}
	got := strings.Join(roles, ",")
	if got != "system,user,assistant,tool,user" {
		t.Fatalf("tool message must directly follow assistant tool_calls, got %s (%s)", got, out)
	}
}

// A7 回归：Gemini 历史里 functionCall/functionResponse 都不带 id 时，
// 转换后 tool_call_id 必须与 tool_calls[].id 对齐。
func TestGeminiNameOnlyFunctionResponseAligned(t *testing.T) {
	body := []byte(`{"contents":[` +
		`{"role":"user","parts":[{"text":"run"}]},` +
		`{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"q":"x"}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"content":"42"}}}]}]}`)
	canonical, err := GeminiRequestToCanonical(body, "m")
	if err != nil {
		t.Fatalf("GeminiRequestToCanonical: %v", err)
	}
	out, err := CanonicalToOpenAIChatRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToOpenAIChatRequest: %v", err)
	}
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id,omitempty"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls,omitempty"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var callID string
	var toolID string
	for _, m := range req.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			callID = m.ToolCalls[0].ID
		}
		if m.Role == "tool" {
			toolID = m.ToolCallID
		}
	}
	if callID == "" || toolID != callID {
		t.Fatalf("tool_call_id %q must match tool_calls[].id %q: %s", toolID, callID, out)
	}
}

// A8 回归：音频内容渲染到 Chat Completions 时 input_audio.data 必须是
// 裸 base64、format 是 mp3/wav 短格式；输入侧 data: URI 应被剥离。
func TestAudioInputRenderedAsBareBase64(t *testing.T) {
	uriInput := []byte(`{"model":"m","messages":[{"role":"user","content":[` +
		`{"type":"input_audio","input_audio":{"data":"data:audio/mpeg;base64,QUJD","format":"mp3"}}]}]}`)
	canonical, err := OpenAIChatRequestToCanonical(uriInput)
	if err != nil {
		t.Fatalf("OpenAIChatRequestToCanonical: %v", err)
	}
	out, err := CanonicalToOpenAIChatRequest(canonical)
	if err != nil {
		t.Fatalf("CanonicalToOpenAIChatRequest: %v", err)
	}
	if !strings.Contains(string(out), `"data":"QUJD"`) || strings.Contains(string(out), "data:audio") {
		t.Fatalf("audio data must be bare base64: %s", out)
	}
	if !strings.Contains(string(out), `"format":"mp3"`) {
		t.Fatalf("audio format must be short form: %s", out)
	}

	// 完整 MIME 类型归一化为短格式。
	canonical2 := &CanonicalRequest{
		Model: "m",
		Messages: []CanonicalMessage{{Role: "user", Content: []CanonicalContentPart{
			{Type: CanonicalContentAudio, AudioBase64: "REVG", MediaType: "audio/wav"},
		}}},
	}
	out2, err := CanonicalToOpenAIChatRequest(canonical2)
	if err != nil {
		t.Fatalf("CanonicalToOpenAIChatRequest(mime): %v", err)
	}
	if !strings.Contains(string(out2), `"format":"wav"`) {
		t.Fatalf("audio/wav must map to short format: %s", out2)
	}
}

// A9 回归：SSE 流中的未知字段行必须被忽略，不能混入 data 破坏 JSON 解析。
func TestSSEUnknownFieldLinesIgnored(t *testing.T) {
	stream := "foo: bar\ndata: {\"answer\":\"ok\"}\n\ngateway-debug: x\ndata: {\"next\":true}\n\n"
	reader := NewSSEEventReader(strings.NewReader(stream))
	defer reader.Close()

	first, ok, err := reader.Read(context.Background(), DefaultSSEIdleTimeout)
	if err != nil || !ok {
		t.Fatalf("read first event: ok=%v err=%v", ok, err)
	}
	if first.Data != `{"answer":"ok"}` {
		t.Fatalf("unknown field lines must not pollute data: %#v", first)
	}
	second, ok, err := reader.Read(context.Background(), DefaultSSEIdleTimeout)
	if err != nil || !ok {
		t.Fatalf("read second event: ok=%v err=%v", ok, err)
	}
	if second.Data != `{"next":true}` {
		t.Fatalf("second event data polluted: %#v", second)
	}
}
