package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

type agentModelReadiness struct {
	SourceID         string                     `json:"sourceId"`
	ModelID          string                     `json:"modelId"`
	ModelName        string                     `json:"modelName"`
	Available        bool                       `json:"available"`
	ModelTools       bool                       `json:"modelTools"`
	CapabilitySource string                     `json:"capabilitySource"`
	BindingKind      string                     `json:"bindingKind,omitempty"`
	BindingTools     bool                       `json:"bindingTools"`
	ProtocolID       string                     `json:"protocolId,omitempty"`
	Revision         string                     `json:"revision,omitempty"`
	CanRepair        bool                       `json:"canRepair"`
	ReasonCode       string                     `json:"reasonCode,omitempty"`
	Reason           string                     `json:"reason,omitempty"`
	Issues           []protocol.ConversionIssue `json:"issues,omitempty"`
}

func modelReference(model storage.Model) config.ModelRef {
	return config.ModelRef{ID: model.ID, Name: model.Name, SourceID: model.SourceID, BaseURL: model.BaseURL, APIKey: model.APIKey, Platform: model.Platform, ToolsCapable: model.ToolsCapable, VisionCapable: model.VisionCapable}
}

// agentReadinessError 让轮次错误映射透出就绪度原因码（model_tools_disabled
// 等），前端可据此程序化引导到「验证并启用」入口，而非统一 500/turn_failed。
type agentReadinessError struct{ status agentModelReadiness }

func (err agentReadinessError) Error() string { return err.status.Reason }

// ReasonCode 供 agent 引擎经接口提取（model_tools_disabled 等），随
// EventError 透出到前端与远程面。
func (err agentReadinessError) ReasonCode() string { return err.status.ReasonCode }

func agentTransport(binding protocol.Binding, compiled *protocol.Compiled) protocol.Transport {
	if binding.Operation != "" {
		return compiled.Operations()[binding.Operation].Transport
	}
	for _, transport := range []protocol.Transport{protocol.SSE, protocol.NDJSON, protocol.HTTPJSON} {
		if slices.Contains(binding.Transports, transport) {
			return transport
		}
	}
	return protocol.HTTPJSON
}

func checkAgentModel(view protocol.RegistryView, bindings []storage.ProtocolBinding, model storage.Model) agentModelReadiness {
	result := agentModelReadiness{SourceID: model.SourceID, ModelID: model.ID, ModelName: model.Name, ModelTools: model.ToolsCapable, CapabilitySource: model.CapabilitySource}
	fail := func(code, path, reason string) agentModelReadiness {
		result.ReasonCode, result.Reason = code, reason
		result.Issues = gatewayIssue(protocol.Identity{DefinitionID: result.ProtocolID}, protocol.UnsupportedCapability, path, reason).Issues
		return result
	}
	entry, found := selectProtocolBinding(bindings, modelReference(model))
	result.BindingKind = entry.Kind
	if !found || entry.Unbound {
		if entry.Kind == "model" {
			return fail("unbound", "/binding", "模型已明确取消绑定，请在模型编辑中重新选择协议")
		}
		return fail("unbound", "/binding", "模型未绑定协议，请先为模型源选择协议")
	}
	result.BindingKind, result.BindingTools, result.ProtocolID, result.Revision = entry.Kind, entry.Binding.Capabilities[protocol.FunctionToolsCapability], entry.Binding.ProtocolID, entry.Binding.RevisionHash
	compiled, found := view.Pin(result.ProtocolID)
	if !found {
		return fail("protocol_inactive", "/binding", "绑定的协议未启用，请先验证并启用协议")
	}
	if !compiled.Capabilities(protocol.EncodeRequest)[protocol.FunctionToolsCapability] || compiled.Definition().Agent == nil {
		return fail("protocol_mapping_missing", "/binding/protocol", "协议缺少函数工具映射或 Agent 参数映射")
	}
	if !model.Enabled {
		return fail("model_disabled", "/model/enabled", "模型已停用")
	}
	result.CanRepair = true
	if !model.ToolsCapable {
		return fail("model_tools_disabled", "/model/toolsCapable", "模型未声明工具调用能力（来源："+model.CapabilitySource+"），可验证并启用工具调用")
	}
	if !result.BindingTools {
		return fail("binding_tools_disabled", "/binding/capabilities/tools.function", "协议绑定未允许函数工具，可验证并启用工具调用")
	}
	if issues := protocol.CheckBinding(entry.Binding, compiled); protocol.IssuesError(issues) != nil {
		result.ReasonCode, result.Reason, result.Issues = "binding_verification_stale", "协议绑定验证过期或与当前修订不兼容，请重新验证", issues
		return result
	}
	candidate, failure := makeGatewayCandidate(view, bindings, modelReference(model), agentTransport(entry.Binding, compiled), "generate")
	if failure != nil || candidate.operation.Kind != "generate" {
		result.CanRepair = false
		return fail("protocol_mapping_missing", "/operations", "Agent 需要兼容的同步生成操作及响应映射")
	}
	result.Available = true
	result.CanRepair = false
	return result
}

func (s *Server) adminAgentModels(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	models, err := s.store.ListModelsFiltered(c.Request.Context(), storage.ModelListFilter{})
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	bindings, err := s.store.ListProtocolBindings(c.Request.Context())
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	items := []agentModelReadiness{}
	view := service.View()
	for _, model := range models {
		items = append(items, checkAgentModel(view, bindings, model))
	}
	respondOK(c, gin.H{"items": items})
}

func (s *Server) adminVerifyAgentTools(c *gin.Context) {
	var input struct {
		SourceID string `json:"sourceId"`
		ModelID  string `json:"modelId"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	// 与 Agent 模型调用同一超时口径（300s）：慢推理模型的探针不再因 60s 预算必败。
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.probeTimeout(agentCallTimeoutSec*time.Second))
	defer cancel()
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	baseline, err := s.store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	model, found := findCustomProtocolTestModel(ctx, s.store, input.SourceID, input.ModelID)
	if !found {
		respondFail(c, http.StatusNotFound, "model_not_found", "模型不存在")
		return
	}
	if err := applyAgentPermittedKey(ctx, s.store, &model); err != nil {
		respondProtocolError(c, err)
		return
	}
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	view := service.View()
	status := checkAgentModel(view, bindings, model)
	if !status.CanRepair && !status.Available {
		respondFail(c, http.StatusBadRequest, status.ReasonCode, status.Reason)
		return
	}
	if status.Available {
		// 幂等短路：已就绪的模型不重复消耗真实探针调用。
		respondOK(c, status)
		return
	}
	entry, _ := selectProtocolBinding(bindings, modelReference(model))
	compiled, _ := view.Pin(entry.Binding.ProtocolID)
	entry = repairedModelBinding(entry, compiled, model)
	if err := protocol.IssuesError(protocol.CheckBinding(entry.Binding, compiled)); err != nil {
		respondProtocolError(c, err)
		return
	}
	entry.Combinations = verifyGatewayBinding(ctx, view, compiled, entry.Binding.Capabilities)
	if !hasPassingGatewayCombination(entry.Combinations) {
		respondFail(c, http.StatusBadRequest, "incompatible_binding", "工具能力与当前入口协议未通过兼容性验证")
		return
	}
	model.ToolsCapable = true
	candidate, failure := makeGatewayCandidate(view, []storage.ProtocolBinding{entry}, modelReference(model), agentTransport(entry.Binding, compiled), "generate")
	if failure != nil {
		respondProtocolError(c, failure)
		return
	}
	// 探针是真实上游调用：与 Agent 调用同一口径入账，费用可审计。
	started := time.Now()
	logConfig := s.usageLogConfig()
	record := &usageRecord{RequestID: usageRequestID(started), StartedAt: started, KeyName: AgentUsageKeyName, RequestedModelGroup: model.Name, ModelName: model.Name, SourceID: model.SourceID, Platform: model.Platform, TargetFormat: entry.Binding.ProtocolID, UpstreamRevision: compiled.Hash(), RelayMode: agentRelayMode, Stream: agentTransport(entry.Binding, compiled) != protocol.HTTPJSON, StatusCode: http.StatusOK, bodyOpts: usageBodyOptions{maxBytes: logConfig.BodyMaxBytes, externalize: logConfig.ExternalizeMedia}}
	record.assets = newAssetSink(record.RequestID)
	defer func() {
		record.EndedAt = time.Now()
		record.DurationMs = record.EndedAt.Sub(started).Milliseconds()
		s.recordUsage(record)
	}()
	fail := func(status int, code, reason string) {
		record.StatusCode = status
		respondFail(c, status, code, reason)
	}
	nonce := rand.Text()
	schema, _ := protocol.EncodeValue(map[string]any{"type": "object", "properties": map[string]any{"nonce": map[string]string{"type": "string"}}, "required": []string{"nonce"}, "additionalProperties": false})
	choice, _ := protocol.EncodeValue(map[string]string{"mode": "required"})
	request, err := compiled.BuildAgentRequest(ctx, protocol.Request{SchemaVersion: protocol.SemanticSchemaVersion, Source: protocol.AgentIdentity(), Model: protocol.StringValue(model.Name), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("Call elysia_capability_probe once with nonce " + nonce + ". This is a capability test; no tool will be executed.")}}}}, Tools: []protocol.Tool{{Kind: protocol.FunctionTool, Name: protocol.StringValue("elysia_capability_probe"), InputSchema: schema}}, ToolChoice: choice}, protocol.AgentPreferences{MaxOutputTokens: 128, Stream: candidate.operation.Transport != protocol.HTTPJSON})
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	response, err := s.collectProtocolGeneration(ctx, candidate, request, record, nil)
	if err != nil {
		var upstream *gatewayFailure
		if errors.As(err, &upstream) && upstream.status >= http.StatusInternalServerError {
			fail(http.StatusBadGateway, "tool_probe_upstream_failed", "上游探测失败，未修改模型或绑定："+err.Error())
			return
		}
		fail(http.StatusBadRequest, "tool_probe_failed", "工具调用验证失败，未修改模型或绑定："+err.Error())
		return
	}
	if response == nil {
		fail(http.StatusBadRequest, "tool_probe_failed", "上游未返回有效响应，未修改配置")
		return
	}
	result, err := agentResultFromProtocol(response)
	if err != nil || len(result.ToolCalls) != 1 {
		fail(http.StatusBadRequest, "tool_probe_failed", "上游未返回有效的函数工具调用，未修改配置")
		return
	}
	call := result.ToolCalls[0]
	var args map[string]string
	if err := protocolValueArguments(call.Arguments, &args); err != nil || call.Name != "elysia_capability_probe" || args["nonce"] != nonce || len(args) != 1 {
		fail(http.StatusBadRequest, "tool_probe_failed", "上游工具名称或参数不符合验证要求，未修改配置")
		return
	}
	if err := s.store.EnableModelFunctionTools(ctx, model.SourceID, model.ID, baseline, entry); err != nil {
		respondProtocolError(c, err)
		return
	}
	s.invalidateRouteCache()
	model.CapabilitySource = "manual"
	respondOK(c, checkAgentModel(view, []storage.ProtocolBinding{entry}, model))
}

func protocolValueArguments(raw []byte, target any) error {
	value, err := protocol.ParseValue(raw)
	if err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return value.Decode(target)
}

// repairedModelBinding 在源级绑定基础上派生 model 级、带函数工具能力的
// 修复绑定：升级到目标修订、开启 tools.function、按模型视觉声明裁剪媒体。
func repairedModelBinding(entry storage.ProtocolBinding, compiled *protocol.Compiled, model storage.Model) storage.ProtocolBinding {
	entry.Kind, entry.ModelID, entry.GroupID = "model", model.ID, ""
	entry.Binding.RevisionHash = compiled.Hash()
	entry.Binding.Capabilities = maps.Clone(entry.Binding.Capabilities)
	if entry.Binding.Capabilities == nil {
		entry.Binding.Capabilities = protocol.CapabilitySet{}
	}
	entry.Binding.Capabilities[protocol.FunctionToolsCapability] = true
	stripMediaCapabilities(entry.Binding.Capabilities, model.VisionCapable)
	return entry
}
