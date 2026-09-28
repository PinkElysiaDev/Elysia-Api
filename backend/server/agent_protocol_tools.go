package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elysia-api/backend/relay"
)

// 协议 CLI handler。测试/预览内核复用协议设计器的同名实现（线上行为一致）。

const (
	agentToolUpdateDraft  = "update_protocol_draft"
	agentToolPreview      = "preview_request"
	agentToolTestUpstream = "test_upstream"
	agentToolTestModels   = "test_model_list"
	agentToolSave         = "save_protocol"
	agentToolRead         = "read_protocol"
)

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// 协议工具共享的取值与提示文案。
const missingTestTargetSummary = "测试目标 baseUrl 未配置：请向用户询问上游 baseUrl（以及需要鉴权时的 API key）；用户在对话中给出后作为本工具参数传入"

// testModelPlaceholder 是测试请求带的占位模型名（上游只关心请求形状）。
const testModelPlaceholder = "test-model"

// draftProtocol 取当前草稿并反序列化；四个协议工具共用的前置步骤。
// ok=false 时返回值是给模型的失败结果。
func draftProtocol(tctx CLIContext) (relay.CustomProtocolConfig, CLIResult, bool) {
	draft := tctx.Draft()
	if len(draft) == 0 {
		return relay.CustomProtocolConfig{}, CLIError("尚无草稿，先运行 elysia protocol draft '<配置JSON>'", "no_draft"), false
	}
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(draft, &protocol); err != nil {
		return relay.CustomProtocolConfig{}, CLIError("草稿解析失败", err.Error()), false
	}
	return protocol, CLIResult{}, true
}

// resolveTestTarget 合成真实测试目标：参数凭证优先，缺失项回退当前 CLI
// 上下文已有的目标；拿到新凭证则写回当前上下文，供同一批处理后续命令复用。
// baseUrl 仍为空时返回给模型的追问结果。
func resolveTestTarget(tctx CLIContext, paramBaseURL, paramAPIKey string) (baseURL, apiKey string, failure CLIResult, ok bool) {
	baseURL, apiKey = tctx.TestTarget()
	if strings.TrimSpace(paramBaseURL) != "" {
		baseURL = strings.TrimSpace(paramBaseURL)
	}
	if strings.TrimSpace(paramAPIKey) != "" {
		apiKey = paramAPIKey
	}
	if strings.TrimSpace(baseURL) == "" {
		return "", "", CLIResult{OK: false, Summary: missingTestTargetSummary, Data: map[string]any{"error": "missing_test_target"}}, false
	}
	_ = tctx.SetTestTarget(baseURL, apiKey)
	return baseURL, apiKey, CLIResult{}, true
}

// ---- update_protocol_draft ----

type updateDraftTool struct{}

func (t *updateDraftTool) Name() string      { return agentToolUpdateDraft }
func (t *updateDraftTool) CLIEffect() string { return "" }

func (t *updateDraftTool) Description() string {
	return "提交完整的自定义协议配置 JSON（整份覆盖当前草稿）。服务端会做声明式校验并用样例请求离线渲染；" +
		"校验或渲染问题会原样返回，需修复后重新提交。文档中有响应示例时一并传 exampleResponse 以检验映射。"
}

func (t *updateDraftTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	var params struct {
		Config          json.RawMessage `json:"config"`
		ExampleResponse json.RawMessage `json:"exampleResponse,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return CLIError("参数解析失败", err.Error())
	}
	if len(params.Config) == 0 {
		return CLIError("缺少 config 参数", "missing config")
	}
	var protocol relay.CustomProtocolConfig
	if err := json.Unmarshal(params.Config, &protocol); err != nil {
		return CLIResult{OK: false, Summary: "配置无法解析为协议结构",
			Data: map[string]any{"valid": false, "issues": err.Error()}}
	}
	if err := relay.ValidateCustomProtocol(protocol); err != nil {
		return CLIResult{OK: false, Summary: "配置校验失败，请按 issues 修复后重交",
			Data: map[string]any{"valid": false, "issues": err.Error()}}
	}
	var compact json.RawMessage
	if buf, err := json.Marshal(protocol); err == nil {
		compact = buf
	} else {
		compact = params.Config
	}
	if err := tctx.SetDraft(compact); err != nil {
		return CLIError("草稿保存失败", err.Error())
	}
	return CLIResult{OK: true, Summary: fmt.Sprintf("草稿已更新（id=%s）", protocol.ID),
		Data: offlineValidationData(tctx, protocol, params.ExampleResponse)}
}

// offlineValidationData 组装草稿的离线自检结果：编辑模式 id 提醒 + 样例请求
// 渲染 + 示例响应映射（均不发出真实请求）。
func offlineValidationData(tctx CLIContext, protocol relay.CustomProtocolConfig, exampleResponse json.RawMessage) map[string]any {
	data := map[string]any{"valid": true, "id": protocol.ID}
	editID := tctx.EditProtocolID()
	if editID != "" && !strings.EqualFold(protocol.ID, editID) {
		data["warning"] = fmt.Sprintf("当前为编辑模式，目标协议 id 为 %q，请保持 id 不变（elysia protocol save 会拒绝不一致的 id）", editID)
	}
	if preview, err := previewCustomProtocolRequest(protocol, defaultCustomProtocolSampleRequest()); err != nil {
		data["previewError"] = err.Error()
	} else {
		data["preview"] = map[string]any{
			"method": preview.Method, "path": preview.Path, "query": preview.Query,
			"contentType": preview.ContentType, "authPreview": preview.AuthPreview,
			"body": preview.Body,
		}
	}
	if len(exampleResponse) > 0 {
		if mapped, err := relay.CustomProtocolResponseToMaheshvara(exampleResponse, protocol); err != nil {
			data["mappingError"] = err.Error()
		} else {
			data["mappedResponse"] = mapped
		}
	}
	return data
}

// ---- preview_request ----

type previewRequestTool struct{}

func (t *previewRequestTool) Name() string      { return agentToolPreview }
func (t *previewRequestTool) CLIEffect() string { return "" }

func (t *previewRequestTool) Description() string {
	return "用样例 Maheshvara 请求离线渲染当前草稿的出站请求（method/path/query/headers/body/凭证注入形态）。" +
		"不发起真实上游请求。用于提交草稿后自查请求形状。"
}

func (t *previewRequestTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	protocol, failure, ok := draftProtocol(tctx)
	if !ok {
		return failure
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
		return CLIError("渲染失败（草稿可能不完整）", err.Error())
	}
	return CLIResult{OK: true, Summary: fmt.Sprintf("渲染成功：%s %s", preview.Method, preview.Path), Data: preview}
}

// ---- test_upstream（门控）----

type testUpstreamTool struct{ server *Server }

func (t *testUpstreamTool) Name() string      { return agentToolTestUpstream }
func (t *testUpstreamTool) CLIEffect() string { return CLIEffectOutbound }
func (t *testUpstreamTool) CLIMeta() CLICommandMeta {
	return CLICommandMeta{Timeout: 120 * time.Second}
}

func (t *testUpstreamTool) Description() string {
	return "把当前草稿渲染成请求并发送到真实上游（受服务端权限与业务策略控制），返回 HTTP 状态、原文、映射结果；" +
		"stream=true 时采样 SSE 事件与解码结果。用户在对话中给出 baseUrl / API key 时作为参数传入；" +
		"仅在当前 CLI 上下文中复用本命令或 protocol models 传入并成功记录的 baseUrl/API key；其他命令的密钥不在此复用。"
}

func (t *testUpstreamTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	var params struct {
		Stream        bool                     `json:"stream"`
		SampleRequest *relay.MaheshvaraRequest `json:"sampleRequest,omitempty"`
		BaseURL       string                   `json:"baseUrl"`
		APIKey        string                   `json:"apiKey"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}
	// 先验草稿再解析凭证：草稿为空时不能先把用户带来的凭证落库，报错也要
	// 指向「先建草稿」而不是「缺 baseUrl」。
	protocol, failure, ok := draftProtocol(tctx)
	if !ok {
		return failure
	}
	baseURL, apiKey, failure, ok := resolveTestTarget(tctx, params.BaseURL, params.APIKey)
	if !ok {
		return failure
	}
	result, err := t.server.runCustomProtocolLiveTest(ctx, protocol, customProtocolTestTarget{
		BaseURL: strings.TrimSpace(baseURL), APIKey: apiKey, ModelName: testModelPlaceholder,
	}, params.Stream, params.SampleRequest)
	if err != nil {
		return CLIError("测试发送失败: "+err.Error(), "send_failed")
	}
	summary := fmt.Sprintf("上游返回 %d（耗时 %dms）", result.StatusCode, result.DurationMs)
	if result.MappingError != "" {
		summary += "；映射失败"
	}
	if result.StreamError != "" {
		summary += "；流式异常"
	}
	return CLIResult{OK: true, Summary: summary, Data: result}
}

// ---- test_model_list（门控）----

type testModelsTool struct{ server *Server }

func (t *testModelsTool) Name() string      { return agentToolTestModels }
func (t *testModelsTool) CLIEffect() string { return CLIEffectOutbound }
func (t *testModelsTool) CLIMeta() CLICommandMeta {
	return CLICommandMeta{Timeout: 60 * time.Second}
}

func (t *testModelsTool) Description() string {
	return "按当前草稿的 models 发现配置向真实上游（受服务端权限与业务策略控制）请求模型列表并解析，用于验证发现端点配置。" +
		"草稿未声明 models 配置时会报错。用户在对话中给出 baseUrl / API key 时作为参数传入；仅在当前 CLI 上下文中复用本命令或 protocol test 已成功记录的测试目标；其他命令的密钥不在此复用。"
}

func (t *testModelsTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	var credParams struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &credParams)
	}
	// 同 test_upstream：先草稿后凭证（草稿为空不落库凭证、报「先建草稿」）。
	protocol, failure, ok := draftProtocol(tctx)
	if !ok {
		return failure
	}
	baseURL, apiKey, failure, ok := resolveTestTarget(tctx, credParams.BaseURL, credParams.APIKey)
	if !ok {
		return failure
	}
	result, err := t.server.runCustomProtocolModelsTest(ctx, protocol, customProtocolTestTarget{
		BaseURL: strings.TrimSpace(baseURL), APIKey: apiKey,
	})
	if err != nil {
		return CLIError("模型发现请求失败: "+err.Error(), "models_fetch_failed")
	}
	summary := fmt.Sprintf("上游返回 %d", result.StatusCode)
	if len(result.Models) > 0 {
		summary += fmt.Sprintf("，发现 %d 个模型", len(result.Models))
	}
	return CLIResult{OK: true, Summary: summary, Data: result}
}

// ---- save_protocol（门控）----

type saveProtocolTool struct{ server *Server }

func (t *saveProtocolTool) Name() string      { return agentToolSave }
func (t *saveProtocolTool) CLIEffect() string { return CLIEffectWrite }

func (t *saveProtocolTool) Description() string {
	return "把当前草稿保存进协议注册表（受服务端权限与业务策略控制，写入即热生效）。编辑模式必须保持原协议 id；" +
		"其余上下文默认新建，同名协议会被拒绝；更新已有协议须显式传 --update <协议id>，目标必须存在且与草稿 id 一致。建议在真实测试通过后再请求保存。"
}

func (t *saveProtocolTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	protocol, failure, ok := draftProtocol(tctx)
	if !ok {
		return failure
	}
	if err := relay.ValidateCustomProtocol(protocol); err != nil {
		return CLIError("草稿校验失败: "+err.Error(), "validation_failed")
	}
	var params struct {
		UpdateID string `json:"updateId"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return CLIError("参数解析失败", err.Error())
		}
	}
	updateID := strings.TrimSpace(params.UpdateID)
	if updateID != "" && !strings.EqualFold(protocol.ID, updateID) {
		return CLIError(fmt.Sprintf("更新目标 %q 与草稿 id %q 不一致", updateID, protocol.ID), "id_mismatch")
	}
	editID := tctx.EditProtocolID()
	if editID != "" && !strings.EqualFold(protocol.ID, editID) {
		return CLIResult{OK: false,
			Summary: fmt.Sprintf("编辑模式不允许改变协议 id（目标 %q，草稿 %q）", editID, protocol.ID),
			Data:    map[string]any{"error": "id_mismatch"}}
	}
	store, unavailableResult := toolStore(t.server)
	if store == nil {
		return unavailableResult
	}
	existing, err := store.ListCustomProtocols(ctx)
	if err != nil {
		return CLIError("读取现有协议失败", err.Error())
	}
	found := false
	for _, row := range existing {
		if !strings.EqualFold(row.ID, protocol.ID) {
			continue
		}
		if editID == "" && updateID == "" {
			return CLIResult{OK: false,
				Summary: fmt.Sprintf("协议 id %q 已存在，请换一个 id，或用 --update <协议id> 显式更新", protocol.ID),
				Data:    map[string]any{"error": "id_conflict", "existingId": row.ID}}
		}
		// 按已存 ID 更新，避免仅大小写不同的草稿插入第二条协议。
		protocol.ID = row.ID
		found = true
		break
	}
	if updateID != "" && !found {
		return CLIError(fmt.Sprintf("待更新的协议 %q 不存在", updateID), "not_found")
	}
	compact, _ := json.Marshal(protocol)
	if err := store.UpsertCustomProtocol(ctx, customProtocolRow(protocol, string(compact))); err != nil {
		return CLIError("保存失败: "+err.Error(), "persist_failed")
	}
	syncErr := t.server.syncCustomProtocolsQuiet()
	data := map[string]any{"saved": true, "id": protocol.ID, "synced": syncErr == nil}
	if syncErr != nil {
		data["warning"] = "已保存但注册表同步失败：" + syncErr.Error()
	}
	return CLIResult{OK: true, Summary: fmt.Sprintf("协议 %q 已保存并生效", protocol.ID), Data: data}
}

// ---- read_protocol ----

type readProtocolTool struct{ server *Server }

func (t *readProtocolTool) Name() string      { return agentToolRead }
func (t *readProtocolTool) CLIEffect() string { return "" }

func (t *readProtocolTool) Description() string {
	return "按 id 读取一条已保存协议（含内置预置协议）的完整配置 JSON，作为写法参考或编辑基准。"
}

func (t *readProtocolTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return CLIError("参数解析失败", err.Error())
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return CLIError("缺少 id", "missing_id")
	}
	// 预置协议优先（未落库也可读），其次查库。
	if config, ok := findPresetConfig(id); ok {
		encoded, _ := json.Marshal(config)
		return CLIResult{OK: true, Summary: "预置协议 " + id, Data: map[string]any{"id": id, "source": "preset", "config": json.RawMessage(encoded)}}
	}
	store, unavailableResult := toolStore(t.server)
	if store == nil {
		return unavailableResult
	}
	rows, err := store.ListCustomProtocols(ctx)
	if err != nil {
		return CLIError("读取失败", err.Error())
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
			return CLIResult{OK: true, Summary: "已读取协议 " + row.ID, Data: data}
		}
	}
	return CLIError(fmt.Sprintf("协议 %q 不存在", id), "not_found")
}
