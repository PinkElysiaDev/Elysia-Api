package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
)

// 协议 Agent 的工具集。本文件是 Agent 体系中唯一的「协议领域知识」载体：
// 每个工具实现 agent.Tool，经 newProtocolAgentTools 注册进引擎，引擎对其
// 行为一无所知。测试/预览内核复用协议设计器的同名实现（线上行为一致）。

const (
	agentToolUpdateDraft  = "update_protocol_draft"
	agentToolPreview      = "preview_request"
	agentToolTestUpstream = "test_upstream"
	agentToolTestModels   = "test_model_list"
	agentToolSave         = "save_protocol"
	agentToolRead         = "read_protocol"
)

// newProtocolAgentTools 返回协议领域全量工具（注册顺序即提示词顺序）。
func newProtocolAgentTools(s *Server) []agent.Tool {
	return []agent.Tool{
		&updateDraftTool{server: s},
		&previewRequestTool{server: s},
		&testUpstreamTool{server: s},
		&testModelsTool{server: s},
		&saveProtocolTool{server: s},
		&readProtocolTool{server: s},
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// ---- update_protocol_draft ----

type updateDraftTool struct{ server *Server }

func (t *updateDraftTool) Name() string { return agentToolUpdateDraft }
func (t *updateDraftTool) Description() string {
	return "写入/更新协议配置草稿（立即校验并离线验证）"
}
func (t *updateDraftTool) Gated() bool           { return false }
func (t *updateDraftTool) PermissionKey() string { return "" }

func (t *updateDraftTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function",
		Name: agentToolUpdateDraft,
		Description: "提交完整的自定义协议配置 JSON（整份覆盖当前草稿）。服务端会做声明式校验并用样例请求离线渲染；" +
			"校验或渲染问题会原样返回，需修复后重新提交。文档中有响应示例时一并传 exampleResponse 以检验映射。",
		Parameters: objectSchema(map[string]any{
			"config":          map[string]any{"type": "object", "description": "完整的 CustomProtocolConfig 对象（含 id 与 request）"},
			"exampleResponse": map[string]any{"type": "object", "description": "可选：上游响应示例（JSON 对象），用于离线检验 response 映射"},
		}, "config"),
	}
}

func (t *updateDraftTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	var params struct {
		Config          json.RawMessage `json:"config"`
		ExampleResponse json.RawMessage `json:"exampleResponse,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	if len(params.Config) == 0 {
		return agent.ToolError("缺少 config 参数", "missing config")
	}
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(params.Config, &protocol); err != nil {
		return agent.ToolResult{OK: false, Summary: "配置无法解析为协议结构",
			Data: map[string]any{"valid": false, "issues": err.Error()}}
	}
	if err := relay.ValidateCustomProtocol(protocol); err != nil {
		return agent.ToolResult{OK: false, Summary: "配置校验失败，请按 issues 修复后重交",
			Data: map[string]any{"valid": false, "issues": err.Error()}}
	}
	var compact json.RawMessage
	if buf, err := json.Marshal(protocol); err == nil {
		compact = buf
	} else {
		compact = params.Config
	}
	if err := tctx.SetDraft(compact); err != nil {
		return agent.ToolError("草稿保存失败", err.Error())
	}

	summary := fmt.Sprintf("草稿已更新（id=%s）", protocol.ID)
	data := map[string]any{"valid": true, "id": protocol.ID}
	if tctx.SessionMeta().Mode == agent.ModeEdit && tctx.SessionMeta().ProtocolID != "" &&
		!strings.EqualFold(protocol.ID, tctx.SessionMeta().ProtocolID) {
		data["warning"] = fmt.Sprintf("当前为编辑模式，目标协议 id 为 %q，请保持 id 不变（save_protocol 会拒绝不一致的 id）", tctx.SessionMeta().ProtocolID)
	}
	// 离线验证：样例请求渲染 + 示例响应映射（均不发出真实请求）。
	if preview, err := previewCustomProtocolRequest(protocol, defaultCustomProtocolSampleRequest()); err != nil {
		data["previewError"] = err.Error()
	} else {
		data["preview"] = map[string]any{
			"method": preview.Method, "path": preview.Path, "query": preview.Query,
			"contentType": preview.ContentType, "authPreview": preview.AuthPreview,
			"body": preview.Body,
		}
	}
	if len(params.ExampleResponse) > 0 {
		if mapped, err := relay.CustomProtocolResponseToMaheshvara(params.ExampleResponse, protocol); err != nil {
			data["mappingError"] = err.Error()
		} else {
			data["mappedResponse"] = mapped
		}
	}
	return agent.ToolResult{OK: true, Summary: summary, Data: data}
}

// ---- preview_request ----

type previewRequestTool struct{ server *Server }

func (t *previewRequestTool) Name() string          { return agentToolPreview }
func (t *previewRequestTool) Description() string   { return "离线渲染草稿请求（不发送）" }
func (t *previewRequestTool) Gated() bool           { return false }
func (t *previewRequestTool) PermissionKey() string { return "" }

func (t *previewRequestTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function",
		Name: agentToolPreview,
		Description: "用样例 Maheshvara 请求离线渲染当前草稿的出站请求（method/path/query/headers/body/凭证注入形态）。" +
			"不发起真实上游请求。用于提交草稿后自查请求形状。",
		Parameters: objectSchema(map[string]any{
			"sampleRequest": map[string]any{"type": "object", "description": "可选：自定义样例 Maheshvara 请求；缺省用内置默认样例"},
		}),
	}
}

func (t *previewRequestTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	draft := tctx.Draft()
	if len(draft) == 0 {
		return agent.ToolError("尚无草稿，先调用 update_protocol_draft", "no_draft")
	}
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(draft, &protocol); err != nil {
		return agent.ToolError("草稿解析失败", err.Error())
	}
	var params struct {
		SampleRequest *relay.MaheshvaraRequest `json:"sampleRequest,omitempty"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}
	sample := params.SampleRequest
	if sample == nil {
		sample = defaultCustomProtocolSampleRequest()
	}
	preview, err := previewCustomProtocolRequest(protocol, sample)
	if err != nil {
		return agent.ToolError("渲染失败（草稿可能不完整）", err.Error())
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("渲染成功：%s %s", preview.Method, preview.Path), Data: preview}
}

// ---- test_upstream（门控）----

type testUpstreamTool struct{ server *Server }

func (t *testUpstreamTool) Name() string          { return agentToolTestUpstream }
func (t *testUpstreamTool) Description() string   { return "向真实上游发送一次测试请求" }
func (t *testUpstreamTool) Gated() bool           { return true }
func (t *testUpstreamTool) PermissionKey() string { return "live_test" }

func (t *testUpstreamTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function",
		Name: agentToolTestUpstream,
		Description: "把当前草稿渲染成请求并发送到真实上游（用户审批后执行），返回 HTTP 状态、原文、映射结果；" +
			"stream=true 时采样 SSE 事件与解码结果。用户在对话中给出 baseUrl / API key 时作为参数传入；" +
			"已提供过的凭证本会话会自动记住，无需重复索要。",
		Parameters: objectSchema(map[string]any{
			"stream":        map[string]any{"type": "boolean", "description": "可选：是否按流式（SSE）测试，默认 false"},
			"sampleRequest": map[string]any{"type": "object", "description": "可选：自定义样例请求"},
			"baseUrl":       map[string]any{"type": "string", "description": "可选：用户提供的上游 baseUrl（缺省用会话已记住的）"},
			"apiKey":        map[string]any{"type": "string", "description": "可选：用户提供的 API key（缺省用会话已记住的）"},
		}),
	}
}

func (t *testUpstreamTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	draft := tctx.Draft()
	if len(draft) == 0 {
		return agent.ToolError("尚无草稿，先调用 update_protocol_draft", "no_draft")
	}
	var params struct {
		Stream        bool                     `json:"stream"`
		SampleRequest *relay.MaheshvaraRequest `json:"sampleRequest,omitempty"`
		BaseURL       string                   `json:"baseUrl"`
		APIKey        string                   `json:"apiKey"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}
	// 参数凭证优先；缺失项回退会话已记住的；拿到新凭证则记住（同会话复用）。
	baseURL, apiKey := tctx.TestTarget()
	if strings.TrimSpace(params.BaseURL) != "" {
		baseURL = strings.TrimSpace(params.BaseURL)
	}
	if strings.TrimSpace(params.APIKey) != "" {
		apiKey = params.APIKey
	}
	if strings.TrimSpace(baseURL) == "" {
		return agent.ToolResult{OK: false, Summary: "测试目标 baseUrl 未配置：请向用户询问上游 baseUrl（以及需要鉴权时的 API key）；用户在对话中给出后作为本工具参数传入",
			Data: map[string]any{"error": "missing_test_target"}}
	}
	_ = tctx.SetTestTarget(baseURL, apiKey)
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(draft, &protocol); err != nil {
		return agent.ToolError("草稿解析失败", err.Error())
	}
	result, err := t.server.runCustomProtocolLiveTest(ctx, protocol, customProtocolTestTarget{
		BaseURL: strings.TrimSpace(baseURL), APIKey: apiKey, ModelName: "test-model",
	}, params.Stream, params.SampleRequest)
	if err != nil {
		return agent.ToolError("测试发送失败: "+err.Error(), err.Error())
	}
	summary := fmt.Sprintf("上游返回 %d（耗时 %dms）", result.StatusCode, result.DurationMs)
	if result.MappingError != "" {
		summary += "；映射失败"
	}
	if result.StreamError != "" {
		summary += "；流式异常"
	}
	return agent.ToolResult{OK: true, Summary: summary, Data: result}
}

// ---- test_model_list（门控）----

type testModelsTool struct{ server *Server }

func (t *testModelsTool) Name() string { return agentToolTestModels }
func (t *testModelsTool) Description() string {
	return "试拉上游模型列表（按草稿 models 发现配置）"
}
func (t *testModelsTool) Gated() bool           { return true }
func (t *testModelsTool) PermissionKey() string { return "live_test" }

func (t *testModelsTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function",
		Name: agentToolTestModels,
		Description: "按当前草稿的 models 发现配置向真实上游（用户审批后执行）请求模型列表并解析，用于验证发现端点配置。" +
			"草稿未声明 models 配置时会报错。用户在对话中给出 baseUrl / API key 时作为参数传入；已提供过的凭证本会话自动记住。",
		Parameters: objectSchema(map[string]any{
			"baseUrl": map[string]any{"type": "string", "description": "可选：用户提供的上游 baseUrl"},
			"apiKey":  map[string]any{"type": "string", "description": "可选：用户提供的 API key"},
		}),
	}
}

func (t *testModelsTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	draft := tctx.Draft()
	if len(draft) == 0 {
		return agent.ToolError("尚无草稿，先调用 update_protocol_draft", "no_draft")
	}
	var credParams struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &credParams)
	}
	baseURL, apiKey := tctx.TestTarget()
	if strings.TrimSpace(credParams.BaseURL) != "" {
		baseURL = strings.TrimSpace(credParams.BaseURL)
	}
	if strings.TrimSpace(credParams.APIKey) != "" {
		apiKey = credParams.APIKey
	}
	if strings.TrimSpace(baseURL) == "" {
		return agent.ToolResult{OK: false, Summary: "测试目标 baseUrl 未配置：请向用户询问上游 baseUrl；用户在对话中给出后作为本工具参数传入",
			Data: map[string]any{"error": "missing_test_target"}}
	}
	_ = tctx.SetTestTarget(baseURL, apiKey)
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(draft, &protocol); err != nil {
		return agent.ToolError("草稿解析失败", err.Error())
	}
	result, err := t.server.runCustomProtocolModelsTest(ctx, protocol, customProtocolTestTarget{
		BaseURL: strings.TrimSpace(baseURL), APIKey: apiKey,
	})
	if err != nil {
		return agent.ToolError("模型发现请求失败: "+err.Error(), err.Error())
	}
	summary := fmt.Sprintf("上游返回 %d", result.StatusCode)
	if len(result.Models) > 0 {
		summary += fmt.Sprintf("，发现 %d 个模型", len(result.Models))
	}
	return agent.ToolResult{OK: true, Summary: summary, Data: result}
}

// ---- save_protocol（门控）----

type saveProtocolTool struct{ server *Server }

func (t *saveProtocolTool) Name() string          { return agentToolSave }
func (t *saveProtocolTool) Description() string   { return "把当前草稿保存为正式协议" }
func (t *saveProtocolTool) Gated() bool           { return true }
func (t *saveProtocolTool) PermissionKey() string { return "save" }

func (t *saveProtocolTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function",
		Name: agentToolSave,
		Description: "把当前草稿保存进协议注册表（用户审批后执行，写入即热生效）。编辑模式必须保持原协议 id；" +
			"新建模式若 id 与现有协议冲突会被拒绝（换一个 id 再试）。建议在真实测试通过后再请求保存。",
		Parameters: objectSchema(map[string]any{}),
	}
}

func (t *saveProtocolTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	draft := tctx.Draft()
	if len(draft) == 0 {
		return agent.ToolError("尚无草稿，先调用 update_protocol_draft", "no_draft")
	}
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(draft, &protocol); err != nil {
		return agent.ToolError("草稿解析失败", err.Error())
	}
	if err := relay.ValidateCustomProtocol(protocol); err != nil {
		return agent.ToolError("草稿校验失败: "+err.Error(), err.Error())
	}
	meta := tctx.SessionMeta()
	if meta.Mode == agent.ModeEdit && meta.ProtocolID != "" && !strings.EqualFold(protocol.ID, meta.ProtocolID) {
		return agent.ToolResult{OK: false,
			Summary: fmt.Sprintf("编辑模式不允许改变协议 id（目标 %q，草稿 %q）", meta.ProtocolID, protocol.ID),
			Data:    map[string]any{"error": "id_mismatch"}}
	}
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	existing, err := store.ListCustomProtocols(ctx)
	if err != nil {
		return agent.ToolError("读取现有协议失败", err.Error())
	}
	for _, row := range existing {
		if strings.EqualFold(row.ID, protocol.ID) && meta.Mode != agent.ModeEdit {
			return agent.ToolResult{OK: false,
				Summary: fmt.Sprintf("协议 id %q 已存在，请换一个 id（更新已有协议请用编辑模式会话）", protocol.ID),
				Data:    map[string]any{"error": "id_conflict", "existingId": row.ID}}
		}
	}
	compact, _ := json.Marshal(protocol)
	if err := store.UpsertCustomProtocol(ctx, customProtocolRow(protocol, string(compact))); err != nil {
		return agent.ToolError("保存失败: "+err.Error(), err.Error())
	}
	syncErr := t.server.syncCustomProtocolsQuiet()
	data := map[string]any{"saved": true, "id": protocol.ID, "synced": syncErr == nil}
	if syncErr != nil {
		data["warning"] = "已保存但注册表同步失败：" + syncErr.Error()
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("协议 %q 已保存并生效", protocol.ID), Data: data}
}

// ---- read_protocol ----

type readProtocolTool struct{ server *Server }

func (t *readProtocolTool) Name() string          { return agentToolRead }
func (t *readProtocolTool) Description() string   { return "读取已保存协议的完整配置" }
func (t *readProtocolTool) Gated() bool           { return false }
func (t *readProtocolTool) PermissionKey() string { return "" }

func (t *readProtocolTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type:        "function",
		Name:        agentToolRead,
		Description: "按 id 读取一条已保存协议（含内置预置协议）的完整配置 JSON，作为写法参考或编辑基准。",
		Parameters: objectSchema(map[string]any{
			"id": map[string]any{"type": "string", "description": "协议 id，如 anthropic-api"},
		}, "id"),
	}
}

func (t *readProtocolTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return agent.ToolError("缺少 id", "missing_id")
	}
	// 预置协议优先（未落库也可读），其次查库。
	if config, ok := findPresetConfig(id); ok {
		encoded, _ := json.Marshal(config)
		return agent.ToolResult{OK: true, Summary: "预置协议 " + id, Data: map[string]any{"id": id, "source": "preset", "config": json.RawMessage(encoded)}}
	}
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	rows, err := store.ListCustomProtocols(ctx)
	if err != nil {
		return agent.ToolError("读取失败", err.Error())
	}
	for _, row := range rows {
		if strings.EqualFold(row.ID, id) {
			valid := ""
			var protocol relay.CustomProtocolConfig
			if err := json.Unmarshal([]byte(row.Config), &protocol); err != nil {
				valid = err.Error()
			} else if err := relay.ValidateCustomProtocol(protocol); err != nil {
				valid = err.Error()
			}
			data := map[string]any{"id": row.ID, "source": "saved", "config": json.RawMessage(row.Config)}
			if valid != "" {
				data["warning"] = "该协议当前校验失败：" + valid
			}
			return agent.ToolResult{OK: true, Summary: "已读取协议 " + row.ID, Data: data}
		}
	}
	return agent.ToolError(fmt.Sprintf("协议 %q 不存在", id), "not_found")
}
