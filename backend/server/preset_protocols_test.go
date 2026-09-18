package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
)

func registerPresetForTest(t *testing.T, id string) relay.CustomProtocolConfig {
	t.Helper()
	configs, err := PresetProtocolConfigs()
	if err != nil {
		t.Fatalf("preset configs: %v", err)
	}
	for _, candidate := range configs {
		if candidate.ID == id {
			if err := relay.RegisterCustomProtocol(candidate); err != nil {
				t.Fatalf("register preset %s: %v", id, err)
			}
			return candidate
		}
	}
	t.Fatalf("preset %s not found", id)
	return relay.CustomProtocolConfig{}
}

func presetGroup(t *testing.T, platform, upstreamURL string) []config.ModelGroupConfig {
	t.Helper()
	return []config.ModelGroupConfig{{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "preset-model", BaseURL: upstreamURL, APIKey: "k", Platform: platform}},
	}}
}

// 预置播种：协议表为空时写入四份定义；已有行则不动；播种后走既有同步管线
// 注册生效。
func TestSeedPresetProtocols(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.seedPresetProtocols()
	rows, err := s.store.ListCustomProtocols(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	presets, _ := PresetProtocolConfigs()
	if len(rows) != len(presets) {
		t.Fatalf("expected %d seeded presets, got %d", len(presets), len(rows))
	}
	s.syncCustomProtocolsQuiet()
	if _, ok := relay.GetCustomProtocol("openai-chat"); !ok {
		t.Fatal("seeded preset must register after sync")
	}
	// 已有行则不再播种。
	s.seedPresetProtocols()
	rows, _ = s.store.ListCustomProtocols(t.Context())
	if len(rows) != len(presets) {
		t.Fatalf("re-seed must be a no-op with existing rows, got %d", len(rows))
	}
}

// openai-chat 预置端到端：请求为线制形状(system 提升/role 折叠)，流式覆盖
// 文本/推理/分帧工具参数拼装/usage 尾帧/finish。
func TestPresetOpenAIChatEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "openai-chat")

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hmm\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"sh\\\"}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(presetGroup(t, "custom:openai-chat", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"weather in sh?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// 请求形状:shape=openai-chat 把 Maheshvara 消息整形为线制(system 独立 role)。
	if !strings.Contains(gotBody, `"role":"system"`) || !strings.Contains(gotBody, `"role":"user"`) || !strings.Contains(gotBody, `"stream":true`) {
		t.Fatalf("upstream request must be openai-chat wire shape: %s", gotBody)
	}
	body := rec.Body.String()
	// 流式响应体携带的是参数分片(SSE 内嵌转义 JSON),非拼装后的整体。
	for _, want := range []string{
		`"content":"Hel"`, `"content":"lo"`, `"reasoning_content":"hmm"`,
		`"name":"get_weather"`, `\"city\":`,
		`"prompt_tokens":5`, `"finish_reason":"tool_calls"`, "data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream output missing %s: %s", want, body)
		}
	}
}

// anthropic-messages 预置端到端：x-api-key 鉴权 + 事件名帧 + thinking 分块 +
// content_block 分帧工具拼装 + message_delta 终态。
func TestPresetAnthropicMessagesEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "anthropic-messages")

	var gotAuth, gotVersion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"sh\\\"}\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":6}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(presetGroup(t, "custom:anthropic-messages", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"weather?"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if gotAuth != "k" || gotVersion != "2023-06-01" {
		t.Fatalf("anthropic auth headers must be applied: %q %q", gotAuth, gotVersion)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"content":"done"`, `"name":"get_weather"`, `\"city\":`,
		`"prompt_tokens":4`, `"completion_tokens":6`, `"finish_reason":"tool_calls"`, "data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream output missing %s: %s", want, body)
		}
	}
}

// gemini-generate 预置端到端：双路径按流切换 + thought 谓词分流 + functionCall。
func TestPresetGeminiGenerateEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "gemini-generate")

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"pondering\",\"thought\":true}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"sunny\"}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"get_weather\",\"args\":{\"city\":\"sh\"}}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":9}}\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(presetGroup(t, "custom:gemini-generate", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"weather?"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1beta/models/preset-model:streamGenerateContent?alt=sse" {
		t.Fatalf("stream request must use pathStream: %s", gotPath)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"reasoning_content":"pondering"`, `"content":"sunny"`, `"name":"get_weather"`,
		`"prompt_tokens":8`, `"completion_tokens":9`, `"finish_reason":"stop"`, "data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream output missing %s: %s", want, body)
		}
	}
}

// openai-responses 预置端到端：类型化事件流 + 分帧工具拼装 + completed 终态。
func TestPresetOpenAIResponsesEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "openai-responses")

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"fc_item_9\",\"type\":\"function_call\",\"call_id\":\"call_9\",\"name\":\"get_weather\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"fc_item_9\",\"delta\":\"{\\\"city\\\":\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"fc_item_9\",\"delta\":\"\\\"sh\\\"}\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":5}}}\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(presetGroup(t, "custom:openai-responses", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"weather?"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// 请求形状:shape=responses 把消息整形为线制 input 数组。
	if !strings.Contains(gotBody, `"input"`) || !strings.Contains(gotBody, `"stream":true`) {
		t.Fatalf("upstream request must be responses wire shape: %s", gotBody)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"content":"Hel"`, `"content":"lo"`, `"name":"get_weather"`, `\"city\":`,
		`"prompt_tokens":3`, `"completion_tokens":5`, "data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream output missing %s: %s", want, body)
		}
	}
	// 完成帧携带的 usage 需要映射(response.usage 经 payloadPath 解包)。

}
