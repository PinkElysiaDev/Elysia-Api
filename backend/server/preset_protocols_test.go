package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func presetGroup(t *testing.T, platform, upstreamURL string) []config.ModelGroupConfig {
	t.Helper()
	return []config.ModelGroupConfig{{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "preset-model", Name: "preset-model", BaseURL: upstreamURL, APIKey: "k", Platform: platform, ToolsCapable: true, VisionCapable: true}},
	}}
}

// PresetProtocolConfigsMust 供测试取内嵌预置(失败即 Fatal)。
func PresetProtocolConfigsMust(t *testing.T) []relay.CustomProtocolConfig {
	t.Helper()
	configs, err := PresetProtocolConfigs()
	if err != nil {
		t.Fatalf("preset configs: %v", err)
	}
	return configs
}

// 预置播种（逐条补齐）：空表全量播种；已有自定义协议时只补缺失的预置且
// 不覆盖用户编辑过的预置；重复调用幂等；播种后走既有同步管线注册生效。
func TestSeedPresetProtocols(t *testing.T) {
	presets, err := PresetProtocolConfigs()
	if err != nil {
		t.Fatalf("preset configs: %v", err)
	}

	t.Run("empty table seeds all", func(t *testing.T) {
		s, _ := newProtocolAdminTestServer(t)
		s.seedPresetProtocols()
		rows, err := s.store.ListCustomProtocols(t.Context())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) != len(presets) {
			t.Fatalf("expected %d seeded presets, got %d", len(presets), len(rows))
		}
		// 幂等：重复播种不增行。
		s.seedPresetProtocols()
		rows, _ = s.store.ListCustomProtocols(t.Context())
		if len(rows) != len(presets) {
			t.Fatalf("re-seed must be a no-op, got %d rows", len(rows))
		}
	})

	t.Run("legacy db with custom protocols gets missing presets", func(t *testing.T) {
		s, _ := newProtocolAdminTestServer(t)
		ctx := t.Context()
		// 老库形态：一条用户自定义协议 + 一条被用户编辑过的预置（改 name）。
		if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
			ID: "vendor-legacy", Name: "老协议", Type: "llm",
			Config: `{"id":"vendor-legacy","request":{"method":"POST","path":"/v1/x"}}`,
		}); err != nil {
			t.Fatalf("seed custom: %v", err)
		}
		editedPreset := `{"id":"openai-chat-completions","name":"我的定制 Chat API","request":{"method":"POST","path":"/v1/chat/completions"}}`
		if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
			ID: "openai-chat-completions", Name: "我的定制 Chat API", Type: "llm", Config: editedPreset,
		}); err != nil {
			t.Fatalf("seed edited preset: %v", err)
		}

		s.seedPresetProtocols()
		rows, err := s.store.ListCustomProtocols(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		// 自定义协议 + 全部预置 ID 都在。
		if len(rows) != len(presets)+1 {
			t.Fatalf("expected %d rows (custom + all presets), got %d", len(presets)+1, len(rows))
		}
		for _, preset := range presets {
			found := false
			for _, row := range rows {
				if row.ID == preset.ID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("preset %q missing after fill-in seeding", preset.ID)
			}
		}
		// 用户编辑过的预置保持用户版本（不被覆盖）。
		for _, row := range rows {
			if row.ID == "openai-chat-completions" {
				if row.Name != "我的定制 Chat API" || row.Config != editedPreset {
					t.Fatalf("edited preset must keep user version, got name=%q", row.Name)
				}
			}
			if row.ID == "vendor-legacy" && row.Name != "老协议" {
				t.Fatalf("custom protocol must be untouched, got %q", row.Name)
			}
		}
		// 再次播种仍幂等。
		s.seedPresetProtocols()
		rows, _ = s.store.ListCustomProtocols(ctx)
		if len(rows) != len(presets)+1 {
			t.Fatalf("re-seed must be a no-op, got %d rows", len(rows))
		}
	})
}

// openai-chat-completions 预置端到端：请求为线制形状(system 提升/role 折叠)，流式覆盖
// 文本/推理/分帧工具参数拼装/usage 尾帧/finish。
func TestPresetOpenAIChatEndToEnd(t *testing.T) {

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hmm\"},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\"}}]},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"sh\\\"}\"}}]},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\",\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7},\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(t, presetGroup(t, "custom:openai-chat-completions", upstream.URL+"/v1"))
	c, rec := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"weather in sh?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`)
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

	s := newTestServer(t, presetGroup(t, "custom:anthropic-messages", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"weather?"}]}`)
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

// google-generate-content 预置端到端：双路径按流切换 + thought 谓词分流 + functionCall。
func TestPresetGeminiGenerateEndToEnd(t *testing.T) {
	for _, isNative := range []bool{true, false} {
		t.Run(fmt.Sprint(isNative), func(t *testing.T) {

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

			s := newTestServer(t, presetGroup(t, "custom:google-generate-content", upstream.URL))
			c, rec := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"weather?"}]}`)
			if isNative {
				c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/grp:streamGenerateContent", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"weather?"}]}]}`))
			}
			s.chatCompletions(c)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
			}
			if gotPath != "/v1beta/models/preset-model:streamGenerateContent?alt=sse" {
				t.Fatalf("stream request must use pathStream: %s", gotPath)
			}
			body := rec.Body.String()
			logs := latestUsageRecords(t, s)
			if !isNative {
				// Pair evidence does not narrow the original upstream contract.
				// A legitimate function call is rendered even when the narrower
				// request profile does not promise arbitrary tool-result inputs.
				for _, want := range []string{`"reasoning_content":"pondering"`, `"content":"sunny"`, `"name":"get_weather"`, `"tool_calls"`, `[DONE]`} {
					if !strings.Contains(body, want) {
						t.Fatal("converted Gemini stream lost", want, body)
					}
				}
				if rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || len(logs) != 1 || logs[0].StatusCode != http.StatusOK {
					t.Fatal(logs, body)
				}
				return
			}
			for _, want := range []string{`"thought":true`, `"text":"sunny"`, `"name":"get_weather"`, `"promptTokenCount":8`, `"candidatesTokenCount":9`, `"finishReason":"STOP"`} {
				if !strings.Contains(body, want) {
					t.Fatal("native Gemini stream lost", want, body)
				}
			}
			if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].TotalTokens != 17 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatal(logs, body)
			}
		})
	}
}

// openai-responses 预置端到端：类型化事件流 + 分帧工具拼装 + completed 终态。
func TestPresetOpenAIResponsesEndToEnd(t *testing.T) {

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.content_part.added\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]},\"sequence_number\":0,\"item_id\":\"msg1\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\",\"sequence_number\":1,\"output_index\":0,\"content_index\":0,\"item_id\":\"msg1\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\",\"sequence_number\":2,\"output_index\":0,\"content_index\":0,\"item_id\":\"msg1\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_9\",\"name\":\"get_weather\"},\"sequence_number\":3}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\\\"city\\\":\",\"sequence_number\":4}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"\\\"sh\\\"}\",\"sequence_number\":5}\n\n")
		// Complete the same message that supplied the deltas. A terminal item
		// cannot substitute a different identity or bypass content completion.
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"msg1","text":"Hello","sequence_number":6}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg1","part":{"type":"output_text","text":"Hello","annotations":[]},"sequence_number":7}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello","annotations":[]}],"id":"msg1","status":"completed"},{"type":"function_call","call_id":"call_9","name":"get_weather","arguments":"{\"city\":\"sh\"}","id":"item_1"}],"usage":{"input_tokens":3,"output_tokens":5},"id":"r1","model":"m","object":"response","created_at":1},"sequence_number":8}`+"\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(t, presetGroup(t, "custom:openai-responses", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"weather?"}]}`)
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

// 预置改名迁移：旧 ID 行改名 + custom:<旧> 平台引用重写；新旧并存跳过；幂等。
func TestCurrentPresetRestartPreservesConversionEvidence(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.store.ProtocolUpgradeBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for restart := 0; restart < 10; restart++ {
		s.migratePresetProtocolRenames()
		if err := s.refreshProtocolRuntime(t.Context(), service); err != nil {
			t.Fatal(err)
		}
		if err := service.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		after, err := s.store.ProtocolUpgradeBaseline(t.Context())
		if err != nil || before != after {
			t.Fatalf("restart %d rewrote current protocol revisions, activations or evidence: %v", restart, err)
		}
	}
}

func TestMigratePresetProtocolRenames(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()

	seedLegacy := func(id string) {
		t.Helper()
		if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
			ID: id, Name: "旧预置", Type: "llm",
			Config: `{"id":"` + id + `","request":{"method":"POST","path":"/v1/chat/completions","body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}}}`,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	// 老库形态：旧 ID 预置行 + 引用 custom:openai-chat 的源与其模型行。
	seedLegacy("openai-chat")
	source := storage.ModelSource{ID: "src1", Name: "src1", BaseURL: "https://up.example", Platform: "custom:openai-chat", Enabled: true}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := s.store.ReplaceSourceModels(ctx, source, []storage.Model{{
		ID: "m1", SourceID: "src1", Name: "m1", BaseURL: source.BaseURL,
		Platform: "custom:openai-chat", Type: "llm", Enabled: true, Available: true,
	}}); err != nil {
		t.Fatalf("seed model: %v", err)
	}

	s.migratePresetProtocolRenames()

	rows, _ := s.store.ListCustomProtocols(ctx)
	ids := map[string]bool{}
	for _, row := range rows {
		ids[row.ID] = true
	}
	if !ids["openai-chat-completions"] || ids["openai-chat"] {
		t.Fatalf("rename failed, rows = %v", ids)
	}
	sources, _ := s.store.ListSources(ctx)
	if len(sources) != 1 || sources[0].Platform != "custom:openai-chat-completions" {
		t.Fatalf("source platform not rewritten: %+v", sources)
	}
	models, _ := s.store.ListModels(ctx)
	if len(models) != 1 || models[0].Platform != "custom:openai-chat-completions" {
		t.Fatalf("model platform not rewritten: %+v", models)
	}
	// 迁移后 config 内部 id 同步改写,registry 可按新平台解析(修复旧版只改
	// 行 id 列、注册键残留旧 id 的脱节)。
	for _, row := range rows {
		if row.ID == "openai-chat-completions" && !strings.Contains(row.Config, `"id":"openai-chat-completions"`) {
			t.Fatalf("config id must be rewritten to the new id, got %s", row.Config)
		}
	}

	// 冲突：用户新建了 responses-api（gen2 中性名），最终 ID
	// openai-responses 行并存，保留双方，不能覆盖用户定义。
	seedLegacy("openai-responses")
	seedLegacy("responses-api")
	s.migratePresetProtocolRenames()
	rows, _ = s.store.ListCustomProtocols(ctx)
	count := 0
	for _, row := range rows {
		if row.ID == "openai-responses" || row.ID == "responses-api" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("conflict pair must be kept as-is, got %d rows", count)
	}

	// 幂等：再跑一次无变化。
	before := len(rows)
	s.migratePresetProtocolRenames()
	rows, _ = s.store.ListCustomProtocols(ctx)
	if len(rows) != before {
		t.Fatalf("re-migration must be a no-op: %d -> %d", before, len(rows))
	}
}

// 旧版迁移留下的现场：行 id 列与平台引用已是新 ID，config 内部 id 仍是旧 ID
// （注册键错位 → 保存源/切换协议报 not registered）。启动序列的对账步骤修复。
func TestReconcileCustomProtocolConfigIDsFixesLegacyRenameGap(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()
	// 旧版改名迁移的遗留现场：行 id 与平台引用是 gen2 名，config 内部 id 残留
	// gen1 名（anthropic-messages）。gen3 改名迁移把行改回 anthropic-messages
	// 并同步改写 config 内部 id，注册键恢复一致。
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
		ID: "anthropic-messages", Name: "Anthropic Messages（预置）", Type: "llm",
		Config: `{"id":"anthropic-messages","name":"Anthropic Messages（预置）","request":{"method":"POST","path":"/v1/messages","body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}}}`,
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	source := storage.ModelSource{ID: "src1", Name: "src1", BaseURL: "https://up.example", Platform: "custom:anthropic-messages", Enabled: true}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	// 启动序列等价路径：改名迁移（gen2 行改回 gen3，config 一并改写）+ 对账。
	s.migratePresetProtocolRenames()
	s.reconcileCustomProtocolConfigIDs()

	rows, err := s.store.ListCustomProtocols(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "anthropic-messages" || !strings.Contains(rows[0].Config, `"id":"anthropic-messages"`) {
		t.Fatalf("row must be renamed with its config id in sync, got %+v", rows)
	}
	sources, _ := s.store.ListSources(ctx)
	if len(sources) != 1 || sources[0].Platform != "custom:anthropic-messages" {
		t.Fatalf("source platform must follow the rename, got %+v", sources)
	}
	// ID reconciliation alone does not prove a legacy definition is executable.
	if err := s.validateSourceProtocol(&source); err == nil {
		t.Fatal("legacy registration bypassed verified activation")
	}
	// 幂等：一致后再跑对账无事发生。
	if fixed, err := s.store.ReconcileCustomProtocolConfigIDs(ctx); err != nil || fixed != 0 {
		t.Fatalf("reconcile must be idempotent: fixed=%d err=%v", fixed, err)
	}
}

// 注册表装配容错：坏行（非法 JSON / 校验不过）跳过并记日志，好行照常注册；
// 注册键以行 id 列为准（config 内部 id 与行 id 不一致时按行 id 注册）。
func TestUpgradeUnmodifiedLegacyPreset(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()

	// 构造与 v1 播种形态完全一致的行：unmarshal→marshal 的规范形态。
	legacyChat := `{"id":"openai-chat-completions","name":"Chat Completions API（预置）","version":"1","type":"llm","metadata":{"preset":true},"request":{"method":"POST","path":"/v1/chat/completions","shape":"openai-chat","body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}},"response":{"textPath":"choices[0].message.content"}}`
	// 用真实 v1 规范哈希需要完整 v1 文本；此处通过哈希表反向构造不可行，
	// 改用「直接把 legacyPresetHashes 的哈希对上」最小路径：写入哈希表登记
	// 的 v1 内容原文（从 git 提取后内联太长）——因此本测试改为验证机制：
	// 手工构造一行，使其 marshal 哈希 == 登记哈希。做法：取登记哈希对应的
	// v1 内容不可得时，跳过精确匹配，改为注入：临时把登记哈希指向本行。
	originalHashes := legacyPresetHashes["openai-chat-completions"]
	t.Cleanup(func() { legacyPresetHashes["openai-chat-completions"] = originalHashes })
	legacyPresetHashes["openai-chat-completions"] = []string{presetContentHash(legacyChat)}

	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
		ID: "openai-chat-completions", Name: "Chat Completions API（预置）", Type: "llm", Config: legacyChat,
	}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	s.seedPresetProtocols()
	// 升级写入的行文本应与当前内嵌预置的规范形态完全一致(不绑具体版本号)。
	var latestChat string
	for _, config := range PresetProtocolConfigsMust(t) {
		if config.ID == "openai-chat-completions" {
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatalf("marshal latest preset: %v", err)
			}
			latestChat = string(encoded)
		}
	}
	rows, _ := s.store.ListCustomProtocols(ctx)
	var upgraded bool
	for _, row := range rows {
		if row.ID == "openai-chat-completions" {
			upgraded = row.Config != legacyChat && row.Config == latestChat
		}
	}
	if !upgraded {
		t.Fatalf("unmodified legacy preset must upgrade to the latest version")
	}

	// 用户改过的行不升级：登记哈希恢复为未改动的历代值，改过的行哈希对不上。
	modified := strings.Replace(legacyChat, "/v1/chat/completions", "/custom/path", 1)
	legacyPresetHashes["openai-chat-completions"] = originalHashes
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
		ID: "openai-chat-completions", Name: "改过的预置", Type: "llm", Config: modified,
	}); err != nil {
		t.Fatalf("seed modified: %v", err)
	}
	s.seedPresetProtocols()
	rows, _ = s.store.ListCustomProtocols(ctx)
	for _, row := range rows {
		if row.ID == "openai-chat-completions" && row.Config != modified {
			t.Fatalf("user-modified preset must not be overwritten")
		}
	}
}

// 预置 v3 path 相对化:base 含 /v1(OpenAI 官方约定),模型发现端点由
// base + 相对路径拼出,不再产生 /v1/v1 重复。
func TestPresetModelDiscoveryEndToEnd(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected discovery path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x"},{"id":"gpt-y"}]}`))
	}))
	defer upstream.Close()

	source := storage.ModelSource{ID: "src1", Name: "src1", BaseURL: upstream.URL + "/v1", Platform: "custom:openai-chat-completions", Enabled: true}
	models, err := s.fetchModelsFromSource(t.Context(), source, "k")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(models) != 2 || models[0].ID != "gpt-x" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

// 预置 path 相对化(v3)的存量迁移:仅当行本次从未改动的旧版升级到新版时,
// 该协议的源 base 补版本段;用户改过的行(未升级)与其源、其他协议的源
// 一律保持原样;模型行 base 快照随源同步;幂等。
func TestMigratePresetRelativePathBases(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()

	// v2 形态旧预置行(完整路径),注入哈希使升级链命中。
	legacyChat := `{"id":"openai-chat-completions","name":"Chat Completions API（预置）","version":"2","type":"llm","request":{"method":"POST","path":"/v1/chat/completions","shape":"openai-chat","body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}}}`
	originalHashes := legacyPresetHashes["openai-chat-completions"]
	t.Cleanup(func() { legacyPresetHashes["openai-chat-completions"] = originalHashes })
	legacyPresetHashes["openai-chat-completions"] = append([]string{presetContentHash(legacyChat)}, originalHashes...)
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{ID: "openai-chat-completions", Type: "llm", Config: legacyChat}); err != nil {
		t.Fatalf("seed legacy preset: %v", err)
	}
	// openai-responses:用户改过的行(哈希必不匹配)→ 不升级 → 其源不迁移。
	modifiedResponses := `{"id":"openai-responses","name":"改过的预置","version":"2","type":"llm","request":{"method":"POST","path":"/v1/responses","shape":"responses","body":{"model":{"field":"model","mode":"string"},"input":{"field":"input_items"}}}}`
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{ID: "openai-responses", Type: "llm", Config: modifiedResponses}); err != nil {
		t.Fatalf("seed modified preset: %v", err)
	}

	seedSource := func(id, platform, base, fetchBase string) storage.ModelSource {
		t.Helper()
		source := storage.ModelSource{ID: id, Name: id, BaseURL: base, Platform: platform, Enabled: true, FetchBaseURL: fetchBase}
		if err := s.store.UpsertSource(ctx, source); err != nil {
			t.Fatalf("seed source %s: %v", id, err)
		}
		return source
	}
	rootSource := seedSource("src-root", "custom:openai-chat-completions", "https://up.example", "")
	seedSource("src-v1", "custom:openai-chat-completions", "https://up.example/v1/", "")
	seedSource("src-fetch", "custom:openai-chat-completions", "https://up.example", "https://fetch.example")
	seedSource("src-modified", "custom:openai-responses", "https://up.example", "")
	seedSource("src-anthropic", "custom:anthropic-messages", "https://up.example", "")
	if err := s.store.ReplaceSourceModels(ctx, rootSource, []storage.Model{{
		ID: "m1", SourceID: rootSource.ID, Name: "m1", BaseURL: rootSource.BaseURL,
		Platform: "custom:openai-chat-completions", Type: "llm", Enabled: true, Available: true,
	}}); err != nil {
		t.Fatalf("seed model snapshot: %v", err)
	}

	upgraded := s.seedPresetProtocols()
	upgradedSet := map[string]bool{}
	for _, id := range upgraded {
		upgradedSet[id] = true
	}
	if !upgradedSet["openai-chat-completions"] {
		t.Fatalf("legacy preset must upgrade, got %v", upgraded)
	}
	if upgradedSet["openai-responses"] {
		t.Fatal("user-modified preset must not upgrade")
	}
	s.migratePresetRelativePathBases(upgraded)

	sources, _ := s.store.ListSources(ctx)
	byID := map[string]storage.ModelSource{}
	for _, source := range sources {
		byID[source.ID] = source
	}
	if got := byID["src-root"].BaseURL; got != "https://up.example/v1" {
		t.Fatalf("root base must gain /v1, got %q", got)
	}
	if got := byID["src-v1"].BaseURL; got != "https://up.example/v1/" {
		t.Fatalf("already-versioned base must stay untouched, got %q", got)
	}
	if got := byID["src-fetch"].BaseURL; got != "https://up.example/v1" {
		t.Fatalf("fetch source base must gain /v1, got %q", got)
	}
	if got := byID["src-fetch"].FetchBaseURL; got != "https://fetch.example/v1" {
		t.Fatalf("fetch base must gain /v1, got %q", got)
	}
	if got := byID["src-modified"].BaseURL; got != "https://up.example" {
		t.Fatalf("source of a user-modified preset must stay untouched, got %q", got)
	}
	if got := byID["src-anthropic"].BaseURL; got != "https://up.example" {
		t.Fatalf("anthropic source must stay untouched, got %q", got)
	}
	models, _ := s.store.ListModels(ctx)
	if len(models) != 1 || models[0].BaseURL != "https://up.example/v1" {
		t.Fatalf("model snapshot must follow the new source base, got %+v", models)
	}

	// 幂等:重复迁移无事发生。
	if n, err := s.store.AppendSourceBaseURLSuffix(ctx, "custom:openai-chat-completions", "/v1"); err != nil || n != 0 {
		t.Fatalf("rebase must be idempotent: n=%d err=%v", n, err)
	}
}

// google-generate-content 预置模型发现:idStripPrefix 剥离 name 的 "models/" 集合前缀,
// 入库 ID 是裸名(转发路径模板不再拼出 /v1beta/models/models/<id>)。
func TestPresetGeminiModelDiscoveryStripsPrefix(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("unexpected discovery path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"}]}`))
	}))
	defer upstream.Close()

	source := storage.ModelSource{ID: "src1", Name: "src1", BaseURL: upstream.URL, Platform: "custom:google-generate-content", Enabled: true}
	models, err := s.fetchModelsFromSource(t.Context(), source, "k")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-2.5-flash" || models[0].Name != "gemini-2.5-flash" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

// Gemini 系模型历史 ID 前缀迁移:models 行剥前缀、组关联改写、多 key 权限
// 列表同步;干净行已存在时删旧行;幂等。
func TestStripGeminiModelIDPrefixes(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()

	seedModel := func(id string) {
		t.Helper()
		source := storage.ModelSource{ID: "src-g", Name: "gemini", BaseURL: "https://up.example", Platform: "custom:google-generate-content", Enabled: true}
		if err := s.store.UpsertSource(ctx, source); err != nil {
			t.Fatalf("seed source: %v", err)
		}
		if err := s.store.ReplaceSourceModels(ctx, source, []storage.Model{{
			ID: id, SourceID: source.ID, Name: id, BaseURL: source.BaseURL,
			Platform: source.Platform, Type: "llm", Enabled: true, Available: true,
		}}); err != nil {
			t.Fatalf("seed model %s: %v", id, err)
		}
	}
	seedModel("models/gemini-2.5-flash")

	fixed, err := s.store.StripGeminiModelIDPrefixes(ctx)
	if err != nil || fixed != 1 {
		t.Fatalf("strip must fix 1 row: fixed=%d err=%v", fixed, err)
	}
	models, _ := s.store.ListModels(ctx)
	if len(models) != 1 || models[0].ID != "gemini-2.5-flash" {
		t.Fatalf("model id must be stripped: %+v", models)
	}

	// 幂等:再跑无事发生。
	if fixed, err := s.store.StripGeminiModelIDPrefixes(ctx); err != nil || fixed != 0 {
		t.Fatalf("strip must be idempotent: fixed=%d err=%v", fixed, err)
	}
}

// chat 入口的 usage 协议链路补全:SourceFormat/SourceEndpoint 按入口记录,
// custom 上游的 TargetFormat=custom:<id>、TargetEndpoint=协议 path 模板,
// 转换链三段——与 responses 入口对齐,前端据此两端同名显示同线制对。
func TestChatUsageRecordProtocolChain(t *testing.T) {

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"id":"r1","model":"m","created":1,"object":"chat.completion"}`))
	}))
	defer upstream.Close()

	s := newTestServerWithStore(t, presetGroup(t, "custom:openai-chat-completions", upstream.URL+"/v1"))
	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	items := latestUsageRecords(t, s)
	if len(items) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(items))
	}
	payload, found, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
	if err != nil || !found {
		t.Fatalf("record json: found=%v err=%v", found, err)
	}
	detail := string(payload)
	for _, want := range []string{
		`"sourceFormat":"openai-chat-completions"`,
		`"sourceEndpoint":"/v1/chat/completions"`,
		`"targetFormat":"openai-chat-completions"`,
		`"targetEndpoint":"/v1/chat/completions"`,
		`"relayMode":"protocol_v2"`, `"ingressRevision":`, `"upstreamRevision":`,
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("usage record missing %s: %.400s", want, detail)
		}
	}
}

// Gemini 迁移半收敛自愈:模型行已剥过前缀(首跑提交成功)、per-key 权限
// 列表仍带前缀(首跑后半段失败)时,再跑迁移必须补写 per-key——提前返回
// 会把多 key 源永久挡在 fail-closed。
func TestStripGeminiModelIDPrefixesRecoversHalfConverged(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()

	source := storage.ModelSource{
		ID: "src-g", Name: "gemini", BaseURL: "https://up.example", Platform: "custom:google-generate-content", Enabled: true, AutoFetchModels: true,
		APIKeys: []storage.SourceAPIKey{{
			Value:         "k1",
			FetchedModels: []string{"models/gemini-2.5-flash", "models/gemini-2.5-pro"},
			AllowedModels: []string{"models/gemini-2.5-flash"},
		}},
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := s.store.UpdateSourceAPIKeys(ctx, source.ID, source.APIKeys); err != nil {
		t.Fatal(err)
	}
	// 模型行已是裸名(首跑的模型行修复已提交)。
	if err := s.store.ReplaceSourceModels(ctx, source, []storage.Model{{
		ID: "gemini-2.5-flash", SourceID: source.ID, Name: "gemini-2.5-flash", BaseURL: source.BaseURL,
		Platform: source.Platform, Type: "llm", Enabled: true, Available: true,
	}}); err != nil {
		t.Fatalf("seed model: %v", err)
	}

	fixed, err := s.store.StripGeminiModelIDPrefixes(ctx)
	if err != nil || fixed != 0 {
		t.Fatalf("no model rows to fix expected: fixed=%d err=%v", fixed, err)
	}
	sources, _ := s.store.ListSources(ctx)
	var keys []storage.SourceAPIKey
	for _, src := range sources {
		if src.ID == source.ID {
			keys = src.APIKeys
		}
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	wantFetched := []string{"gemini-2.5-flash", "gemini-2.5-pro"}
	wantAllowed := []string{"gemini-2.5-flash"}
	if strings.Join(keys[0].FetchedModels, ",") != strings.Join(wantFetched, ",") ||
		strings.Join(keys[0].AllowedModels, ",") != strings.Join(wantAllowed, ",") {
		t.Fatalf("key permission lists must be stripped: fetched=%v allowed=%v", keys[0].FetchedModels, keys[0].AllowedModels)
	}
}

// 哈希链代数一致性:每条预置登记的历史哈希数不得少于当前版本号-1(v1 起
// 每代一条),防止发新版时漏登记——漏登的那一代老库会被误判为「用户改过」
// 而永远不升级(连带 rebase 迁移不触发)。ID 改名迁移会把存量行内容重写,
// 同一代内容存在改写前后两种形态,链里允许出现多于代数的额外登记。
func TestLegacyPresetHashChainCoversEveryGeneration(t *testing.T) {
	configs := PresetProtocolConfigsMust(t)
	for _, config := range configs {
		version := 0
		if v, err := strconv.Atoi(strings.TrimSpace(config.Version)); err == nil {
			version = v
		}
		chain := legacyPresetHashes[config.ID]
		if version > 1 && len(chain) < version-1 {
			t.Fatalf("preset %s version=%d but hash chain has only %d entr(y|ies); register the previous version's hash when bumping", config.ID, version, len(chain))
		}
		if version <= 1 && len(chain) != 0 {
			t.Fatalf("preset %s version=%d must not carry legacy hashes", config.ID, version)
		}
	}
}

// 字段保真矩阵(防线):Claude 客户端的私有字段经 custom:anthropic-messages 预置
// 转发必须原样到达上游——system 块 / tool_use 块 / tool_result 块的
// cache_control 打点(缓存命中率的前提)。同源透传路径由 passthrough_test
// 覆盖;thinking 块的 cache_control 属罕见打点,当前为已知边界不回放。
func TestPresetAnthropicCacheControlFidelity(t *testing.T) {

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	s := newTestServer(t, presetGroup(t, "custom:anthropic-messages", upstream.URL))
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"grp","max_tokens":64,"stream":false,
		"system":[{"type":"text","text":"You are helpful.","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"get_weather","input":{},"cache_control":{"type":"ephemeral"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"sunny","cache_control":{"type":"ephemeral"}}]}
		]}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	s.chatCompletions(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("upstream body not json: %s", gotBody)
	}
	// system 必须是原始块数组(而非压扁字符串),且块上 cache_control 保留。
	system, ok := payload["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("system must stay the original block array, got %T %#v", payload["system"], payload["system"])
	}
	sysBlock, _ := system[0].(map[string]any)
	if sysBlock["cache_control"] == nil {
		t.Fatalf("system block lost cache_control: %s", gotBody)
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %s", len(messages), gotBody)
	}
	assistantBlocks, _ := messages[1].(map[string]any)["content"].([]any)
	if len(assistantBlocks) != 1 {
		t.Fatalf("expected 1 assistant block: %s", gotBody)
	}
	toolUse, _ := assistantBlocks[0].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["cache_control"] == nil {
		t.Fatalf("tool_use block lost cache_control: %s", gotBody)
	}
	userBlocks, _ := messages[2].(map[string]any)["content"].([]any)
	toolResult, _ := userBlocks[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["cache_control"] == nil {
		t.Fatalf("tool_result block lost cache_control: %s", gotBody)
	}
}
