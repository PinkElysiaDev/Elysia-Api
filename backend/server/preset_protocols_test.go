package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
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
		s.syncCustomProtocolsQuiet()
		if _, ok := relay.GetCustomProtocol("chat-completions-api"); !ok {
			t.Fatal("seeded preset must register after sync")
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
		editedPreset := `{"id":"chat-completions-api","name":"我的定制 Chat API","request":{"method":"POST","path":"/v1/chat/completions"}}`
		if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
			ID: "chat-completions-api", Name: "我的定制 Chat API", Type: "llm", Config: editedPreset,
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
			if row.ID == "chat-completions-api" {
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

// chat-completions-api 预置端到端：请求为线制形状(system 提升/role 折叠)，流式覆盖
// 文本/推理/分帧工具参数拼装/usage 尾帧/finish。
func TestPresetOpenAIChatEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "chat-completions-api")

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

	s := newTestServer(presetGroup(t, "custom:chat-completions-api", upstream.URL+"/v1"))
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

// anthropic-api 预置端到端：x-api-key 鉴权 + 事件名帧 + thinking 分块 +
// content_block 分帧工具拼装 + message_delta 终态。
func TestPresetAnthropicMessagesEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "anthropic-api")

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

	s := newTestServer(presetGroup(t, "custom:anthropic-api", upstream.URL))
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

// gemini-api 预置端到端：双路径按流切换 + thought 谓词分流 + functionCall。
func TestPresetGeminiGenerateEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "gemini-api")

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

	s := newTestServer(presetGroup(t, "custom:gemini-api", upstream.URL))
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

// responses-api 预置端到端：类型化事件流 + 分帧工具拼装 + completed 终态。
func TestPresetOpenAIResponsesEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "responses-api")

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

	s := newTestServer(presetGroup(t, "custom:responses-api", upstream.URL))
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

// 预置改名迁移：旧 ID 行改名 + custom:<旧> 平台引用重写；新旧并存跳过；幂等。
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
	if !ids["chat-completions-api"] || ids["openai-chat"] {
		t.Fatalf("rename failed, rows = %v", ids)
	}
	sources, _ := s.store.ListSources(ctx)
	if len(sources) != 1 || sources[0].Platform != "custom:chat-completions-api" {
		t.Fatalf("source platform not rewritten: %+v", sources)
	}
	models, _ := s.store.ListModels(ctx)
	if len(models) != 1 || models[0].Platform != "custom:chat-completions-api" {
		t.Fatalf("model platform not rewritten: %+v", models)
	}
	// 迁移后 config 内部 id 同步改写,registry 可按新平台解析(修复旧版只改
	// 行 id 列、注册键残留旧 id 的脱节)。
	for _, row := range rows {
		if row.ID == "chat-completions-api" && !strings.Contains(row.Config, `"id":"chat-completions-api"`) {
			t.Fatalf("config id must be rewritten to the new id, got %s", row.Config)
		}
	}
	s.syncCustomProtocolsQuiet()
	if _, ok := relay.GetCustomProtocol("chat-completions-api"); !ok {
		t.Fatal("renamed protocol must register under the new id after sync")
	}

	// 冲突：用户新建了 responses-api，旧 openai-responses 行并存 → 跳过该对。
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
//（注册键错位 → 保存源/切换协议报 not registered）。启动序列的对账步骤修复。
func TestReconcileCustomProtocolConfigIDsFixesLegacyRenameGap(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
		ID: "anthropic-api", Name: "Anthropic API（预置）", Type: "llm",
		Config: `{"id":"anthropic-messages","name":"Anthropic API（预置）","request":{"method":"POST","path":"/v1/messages","body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}}}`,
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	source := storage.ModelSource{ID: "src1", Name: "src1", BaseURL: "https://up.example", Platform: "custom:anthropic-api", Enabled: true}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	// 启动序列等价路径：改名迁移（旧行已不存在，不命中）+ 对账。
	s.migratePresetProtocolRenames()
	s.reconcileCustomProtocolConfigIDs()

	rows, err := s.store.ListCustomProtocols(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || strings.Contains(rows[0].Config, `"id":"anthropic-messages"`) || !strings.Contains(rows[0].Config, `"id":"anthropic-api"`) {
		t.Fatalf("config id must be reconciled to the row id, got %+v", rows)
	}
	s.syncCustomProtocolsQuiet()
	if _, ok := relay.GetCustomProtocol("anthropic-api"); !ok {
		t.Fatal("reconciled protocol must register under the row id")
	}
	// 保存/切换该源的校验路径随注册表修复而通过。
	if err := validateCustomSourceProtocol(&source); err != nil {
		t.Fatalf("source save must pass after reconciliation: %v", err)
	}
	// 幂等：一致后再跑对账无事发生。
	if fixed, err := s.store.ReconcileCustomProtocolConfigIDs(ctx); err != nil || fixed != 0 {
		t.Fatalf("reconcile must be idempotent: fixed=%d err=%v", fixed, err)
	}
}

// 注册表装配容错：坏行（非法 JSON / 校验不过）跳过并记日志，好行照常注册；
// 注册键以行 id 列为准（config 内部 id 与行 id 不一致时按行 id 注册）。
func TestSyncCustomProtocolsSkipsBrokenRows(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()
	body := `"body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}`
	rows := []storage.CustomProtocol{
		{ID: "good-one", Type: "llm", Config: `{"id":"good-one","request":{"method":"POST","path":"/v1/x",` + body + `}}`},
		{ID: "bad-json", Type: "llm", Config: `{"id":`},
		{ID: "bad-shape", Type: "llm", Config: `{"id":"bad-shape","request":{"method":"POST","path":"/v1/x","shape":"nope",` + body + `}}`},
		{ID: "row-id-wins", Type: "llm", Config: `{"id":"stale-inner-id","request":{"method":"POST","path":"/v1/y",` + body + `}}`},
	}
	for _, row := range rows {
		if err := s.store.UpsertCustomProtocol(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	if err := s.syncCustomProtocolsQuiet(); err != nil {
		t.Fatalf("sync must tolerate broken rows: %v", err)
	}
	if _, ok := relay.GetCustomProtocol("good-one"); !ok {
		t.Fatal("good row must register")
	}
	if _, ok := relay.GetCustomProtocol("row-id-wins"); !ok {
		t.Fatal("row id must win over a stale inner config id")
	}
	if _, ok := relay.GetCustomProtocol("stale-inner-id"); ok {
		t.Fatal("stale inner config id must not register")
	}
	for _, id := range []string{"bad-json", "bad-shape"} {
		if _, ok := relay.GetCustomProtocol(id); ok {
			t.Fatalf("broken row %q must not register", id)
		}
	}
}

// 预置版本升级：未被改动的 v1 预置行自动升级到 v2（哈希匹配）；用户改过的
// 行保持不动。
func TestUpgradeUnmodifiedLegacyPreset(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ctx := t.Context()

	// 构造与 v1 播种形态完全一致的行：unmarshal→marshal 的规范形态。
	legacyChat := `{"id":"chat-completions-api","name":"Chat Completions API（预置）","version":"1","type":"llm","metadata":{"preset":true},"request":{"method":"POST","path":"/v1/chat/completions","shape":"openai-chat","body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}},"response":{"textPath":"choices[0].message.content"}}`
	// 用真实 v1 规范哈希需要完整 v1 文本；此处通过哈希表反向构造不可行，
	// 改用「直接把 legacyPresetHashes 的哈希对上」最小路径：写入哈希表登记
	// 的 v1 内容原文（从 git 提取后内联太长）——因此本测试改为验证机制：
	// 手工构造一行，使其 marshal 哈希 == 登记哈希。做法：取登记哈希对应的
	// v1 内容不可得时，跳过精确匹配，改为注入：临时把登记哈希指向本行。
	originalHashes := legacyPresetHashes["chat-completions-api"]
	t.Cleanup(func() { legacyPresetHashes["chat-completions-api"] = originalHashes })
	legacyPresetHashes["chat-completions-api"] = []string{presetContentHash(legacyChat)}

	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
		ID: "chat-completions-api", Name: "Chat Completions API（预置）", Type: "llm", Config: legacyChat,
	}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	s.seedPresetProtocols()
	rows, _ := s.store.ListCustomProtocols(ctx)
	var upgraded bool
	for _, row := range rows {
		if row.ID == "chat-completions-api" {
			upgraded = row.Config != legacyChat && strings.Contains(row.Config, `"presetVersion":3`)
		}
	}
	if !upgraded {
		t.Fatalf("unmodified legacy preset must upgrade to the latest version")
	}

	// 用户改过的行不升级：登记哈希恢复为未改动的历代值，改过的行哈希对不上。
	modified := strings.Replace(legacyChat, "/v1/chat/completions", "/custom/path", 1)
	legacyPresetHashes["chat-completions-api"] = originalHashes
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{
		ID: "chat-completions-api", Name: "改过的预置", Type: "llm", Config: modified,
	}); err != nil {
		t.Fatalf("seed modified: %v", err)
	}
	s.seedPresetProtocols()
	rows, _ = s.store.ListCustomProtocols(ctx)
	for _, row := range rows {
		if row.ID == "chat-completions-api" && row.Config != modified {
			t.Fatalf("user-modified preset must not be overwritten")
		}
	}
}

// 预置 v3 path 相对化:base 含 /v1(OpenAI 官方约定),模型发现端点由
// base + 相对路径拼出,不再产生 /v1/v1 重复。
func TestPresetModelDiscoveryEndToEnd(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.seedPresetProtocols()
	s.syncCustomProtocolsQuiet()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected discovery path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x"},{"id":"gpt-y"}]}`))
	}))
	defer upstream.Close()

	source := storage.ModelSource{ID: "src1", Name: "src1", BaseURL: upstream.URL + "/v1", Platform: "custom:chat-completions-api", Enabled: true}
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
	legacyChat := `{"id":"chat-completions-api","name":"Chat Completions API（预置）","version":"2","type":"llm","request":{"method":"POST","path":"/v1/chat/completions","shape":"openai-chat","body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"stream":{"field":"stream"}}}}`
	originalHashes := legacyPresetHashes["chat-completions-api"]
	t.Cleanup(func() { legacyPresetHashes["chat-completions-api"] = originalHashes })
	legacyPresetHashes["chat-completions-api"] = append([]string{presetContentHash(legacyChat)}, originalHashes...)
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{ID: "chat-completions-api", Type: "llm", Config: legacyChat}); err != nil {
		t.Fatalf("seed legacy preset: %v", err)
	}
	// responses-api:用户改过的行(哈希必不匹配)→ 不升级 → 其源不迁移。
	modifiedResponses := `{"id":"responses-api","name":"改过的预置","version":"2","type":"llm","request":{"method":"POST","path":"/v1/responses","shape":"responses","body":{"model":{"field":"model","mode":"string"},"input":{"field":"input_items"}}}}`
	if err := s.store.UpsertCustomProtocol(ctx, storage.CustomProtocol{ID: "responses-api", Type: "llm", Config: modifiedResponses}); err != nil {
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
	rootSource := seedSource("src-root", "custom:chat-completions-api", "https://up.example", "")
	seedSource("src-v1", "custom:chat-completions-api", "https://up.example/v1/", "")
	seedSource("src-fetch", "custom:chat-completions-api", "https://up.example", "https://fetch.example")
	seedSource("src-modified", "custom:responses-api", "https://up.example", "")
	seedSource("src-anthropic", "custom:anthropic-api", "https://up.example", "")
	if err := s.store.ReplaceSourceModels(ctx, rootSource, []storage.Model{{
		ID: "m1", SourceID: rootSource.ID, Name: "m1", BaseURL: rootSource.BaseURL,
		Platform: "custom:chat-completions-api", Type: "llm", Enabled: true, Available: true,
	}}); err != nil {
		t.Fatalf("seed model snapshot: %v", err)
	}

	upgraded := s.seedPresetProtocols()
	upgradedSet := map[string]bool{}
	for _, id := range upgraded {
		upgradedSet[id] = true
	}
	if !upgradedSet["chat-completions-api"] {
		t.Fatalf("legacy preset must upgrade, got %v", upgraded)
	}
	if upgradedSet["responses-api"] {
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
	if n, err := s.store.AppendSourceBaseURLSuffix(ctx, "custom:chat-completions-api", "/v1"); err != nil || n != 0 {
		t.Fatalf("rebase must be idempotent: n=%d err=%v", n, err)
	}
}
