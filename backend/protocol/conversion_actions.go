package protocol

import (
	"fmt"
	"slices"
	"strings"
)

type conversionAction struct {
	parseOption  func(Value) conversionOption
	name         string
	phases       []ConversionPhase
	valueSchema  map[string]any
	description  string
	codecValue   string
	isWholeValue bool
}

// Build fresh editor schemas so callers cannot mutate compiler constraints.
func conversionActionCatalog() []conversionAction {
	return []conversionAction{
		{name: "chat_response_model", phases: []ConversionPhase{ConversionResponse}, description: "仅补齐成功非流式 Chat 原始响应中缺失的 model；使用本次实际出站模型并告警，严格模式拒绝。null、空值、非法类型和映射删除不补齐；需要 responseModel 来源上下文。", isWholeValue: true},
		{name: "responses_turn_state", phases: []ConversionPhase{ConversionEvent}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "限定处理 response.metadata 的 x-codex-turn-state 控制帧。同协议保留；跨协议兼容模式省略并告警，严格模式拒绝。不提供跨请求轮次状态恢复，未知字段仍拒绝。", codecValue: "required", isWholeValue: true},
		{name: "chat_stream_identity", phases: []ConversionPhase{ConversionSourceEvent}, description: "暂存一条精确匹配的 Chat 空启动帧。仅当第二条也是同模型空启动帧且 ID 改变时，以第二帧 ID 和创建时间统一首帧；兼容模式诊断，严格模式拒绝。后续身份变化仍拒绝。", isWholeValue: true},
		{name: "error_metadata", parseOption: parseConversionOption[errorProjectionOptions], phases: []ConversionPhase{ConversionResponse, ConversionEvent}, valueSchema: map[string]any{"type": "object", "required": []string{"sourceCodec", "targetCodec"}, "properties": map[string]any{"sourceCodec": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "targetCodec": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}}}, description: "投影已识别错误的 code、param 和 new_api_error 包装类型。目标无法结构化表达时，在兼容模式保留为消息附注并诊断，严格模式拒绝结构变化；空 param 规范化。未知扩展不作省略，同协议保持原形态。", isWholeValue: true},
		{name: "set", phases: valueConversionPhases()},
		{name: "remove", phases: valueConversionPhases()},
		{name: "transform", phases: valueConversionPhases()},
		{name: "warn", phases: valueConversionPhases()},
		{name: "reject", phases: valueConversionPhases()},
		{name: "signatures", phases: []ConversionPhase{ConversionRequest, ConversionResponse, ConversionEvent}, description: "将外协议 signature 与 encrypted_content 纳入认证续传；成功封装后诊断为可恢复，恢复仍校验身份、账号、模型及原节点。不转换文件或会话引用，不将加密内容冒充供应商签名。"},
		{name: "stream_options", phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "处理客户端用量输出和工具输入分片偏好。value 指定实际目标编码器时，对无法表达的工具流式时序偏好进行信息级有损诊断，严格模式拒绝；非流式或默认空值按无效果规范化。未知扩展仍需明确映射。", codecValue: "optional", isWholeValue: true},
		{name: "request_projection", parseOption: parseConversionOption[requestProjectionOptions], phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "object", "required": []string{"targetCodec"}, "default": map[string]any{"targetCodec": "responses"}, "properties": map[string]any{"sourceCodec": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "targetCodec": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}}}, description: "保留可映射的缓存提示、跟踪元数据和工具 strict；源编码器用于保持工具默认语义，未知来源不推断。兼容模式信息级省略无法映射的已知提示，工具 strict 损失保留警告；严格模式拒绝实际损失。未知缓存策略、扩展和资源不作省略。", isWholeValue: true},
		{name: "tool_result_object", parseOption: parseConversionOption[string], phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "string", "minLength": 1}, description: "将文本工具结果包装到 value 指定字段（默认 result），合并纯文本结果块，类型或块边界变化时警告并在严格模式拒绝。true 错误状态映射到 error 字段，false 规范化为普通结果；保护正文中的同名字段，保留调用关联，未处理媒体、扩展及资源明确拒绝。"},
		{name: "tool_result_text", phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "将对象工具结果序列化为 JSON 文本，保留数值精度及调用关联。value 可指定实际目标编码器；Chat／Responses 的 false 错误标记无效果规范化，true 转为明确错误文本并警告，严格模式拒绝该降级。未指定目标时保留错误状态；对象文本化仍按类型变化诊断。", codecValue: "optional"},
		{name: "system_instruction_hoist", phases: []ConversionPhase{ConversionRequest}, description: "按原顺序将 system/developer 上提为顶层系统要求。作用范围或角色优先级变化时记录降级；严格模式拒绝。"},
		{name: "chat_history", phases: []ConversionPhase{ConversionRequest}, description: "将同一助手回合的思考、正文及并行工具项归入 Chat 历史消息，保留正文顺序和调用 ID，不跨用户或工具结果边界。相邻的无状态可见思考可合并为文本。合并块或消息边界、改变正文与工具的交错顺序时诊断，严格模式拒绝损失。摘要、带状态或交错思考不自动拼接；孤立思考、未完成或不相邻的工具结果拒绝。"},
		{name: "assistant_history", phases: []ConversionPhase{ConversionRequest}, description: "将独立思考、正文和工具项归入同一助手历史消息，保留块顺序、调用 ID 和签名关联；不跨用户或工具结果边界。合并消息边界或丢失项目身份时诊断，严格模式拒绝；带无法移动的消息元数据时明确拒绝。"},
		{name: "response_metadata", phases: []ConversionPhase{ConversionRequest, ConversionResponse, ConversionEvent}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "逐项转换已识别的响应元数据，保留所属文本、候选及来源路径。Responses 访问档位、消息轮次标签和流式填充按实际损失诊断；转换后的载荷不能承诺保留源协议的长度混淆保护，严格模式拒绝。未知扩展、文件引用和上下文状态仍受保护。", codecValue: "required"},
		{name: "response_shape", phases: []ConversionPhase{ConversionResponse, ConversionEvent}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "转换一轮回答的有序输出项；消息边界或交错关系丢失时诊断，严格模式拒绝。不会将多个候选答案混成一轮。", codecValue: "required"},
		{name: "response_envelope", phases: []ConversionPhase{ConversionResponse, ConversionEvent}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "补齐目标响应的稳定 ID、时间和项目状态，不改变上游观测用量。合法原生输出保持原样。", codecValue: "required"},
		{name: "buffer_node", phases: []ConversionPhase{ConversionEvent}},
		{name: "provider_signature", phases: []ConversionPhase{ConversionRequest}},
		{name: "responses_include", phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "分离 Responses 输出选择；reasoning.encrypted_content 接入认证续传，未知选择项跨协议拒绝。", codecValue: "required", isWholeValue: true},
		{name: "responses_context", phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "跨协议将 null 上一轮引用规范化为无引用并记录信息级诊断；非空引用和未实现的截断约束明确拒绝。原生 Responses 保留原值，不扩展其会话能力。", codecValue: "required", isWholeValue: true},
		{name: "gemini_safety_settings", phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "Gemini 安全分类设置：原生保留；跨协议空值规范化。已知分类的 BLOCK_NONE/OFF 在兼容模式诊断后使用目标自身审核策略，不承诺关闭目标审核；严格模式拒绝。实际阻断阈值、未知分类和附加字段不自动省略。", codecValue: "required", isWholeValue: true},
		{name: "usage_projection", phases: []ConversionPhase{ConversionResponse, ConversionEvent}, valueSchema: map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, description: "按目标投影思考、工具提示、缓存、Responses 工具独立计数及消息用量归属明细。原始计量完整保留，分项不重复加进模型总量；明确的零值省略仍诊断，严格模式拒绝信息损失。", codecValue: "required", isWholeValue: true},
		{name: "responses_storage", parseOption: parseConversionOption[responsesStorageOptions], phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "object", "required": []string{"sourceCodec", "targetCodec", "onUnsupported"}, "default": map[string]any{"sourceCodec": "responses", "targetCodec": "gemini", "onUnsupported": "degrade"}, "properties": map[string]any{"sourceCodec": map[string]any{"type": "string", "enum": []string{"responses", "gemini", "anthropic", "openai-chat"}}, "targetCodec": map[string]any{"type": "string", "enum": []string{"responses", "gemini", "anthropic", "openai-chat"}}, "onUnsupported": map[string]any{"type": "string", "enum": []string{"degrade", "reject"}}}}, description: "保存意图：false 跨协议保持关闭；Responses 缺省为开启，Chat 缺省未知。开启或未知意图跨协议有损时严格模式拒绝候选；无保存语义的来源进入 OpenAI 时显式关闭。不承诺供应商实际持久化或原 API 检索。", isWholeValue: true},
		{name: "reasoning_controls", parseOption: parseConversionOption[reasoningOptions], phases: []ConversionPhase{ConversionRequest}, valueSchema: map[string]any{"type": "object", "required": []string{"sourceCodec", "targetCodec"}, "properties": map[string]any{"sourceCodec": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "targetCodec": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}}}, description: "统一思考模式、计算强度、预算和摘要偏好；按模型及绑定约束解析。近似、预算裁剪或作用范围变化在兼容模式诊断，严格模式拒绝当前候选。无法保持关闭思考或显式输出上限时，两种模式均拒绝当前候选。", isWholeValue: true},
		{name: "anthropic_usage_envelope", phases: []ConversionPhase{ConversionResponse, ConversionEvent}, description: "补齐 Anthropic 必填用量。兼容模式缺失计数补客户端专用零值并诊断；严格模式拒绝。内部计量不变，旧客户端可能保留首帧占位数。", isWholeValue: true},
	}
}

func conversionPhases() []ConversionPhase {
	return append(valueConversionPhases(), ConversionSourceEvent)
}

func valueConversionPhases() []ConversionPhase {
	return []ConversionPhase{ConversionIngress, ConversionRequest, ConversionResponse, ConversionEvent, ConversionWire}
}

func conversionActionSchemas() (map[string]any, []string) {
	schemas := map[string]any{}
	var names []string
	for _, action := range conversionActionCatalog() {
		names = append(names, action.name)
		schema := map[string]any{"phases": action.phases}
		if action.valueSchema != nil {
			schema["value"] = action.valueSchema
		}
		if action.description != "" {
			schema["description"] = action.description
		}
		schemas[action.name] = schema
	}
	return schemas, names
}

func (action conversionAction) validate(rule ConversionRule) error {
	if !slices.Contains(action.phases, rule.Phase) {
		phases := make([]string, len(action.phases))
		for i, phase := range action.phases {
			phases[i] = string(phase)
		}
		return fmt.Errorf("%s requires %s phase", action.name, strings.Join(phases, " or "))
	}
	if action.codecValue == "required" || action.codecValue == "optional" && !rule.Value.IsZero() {
		var codec string
		if rule.Value.Decode(&codec) != nil || !knownConversionCodec(codec) {
			return fmt.Errorf("%s requires a known target codec in value", action.name)
		}
	}
	if action.isWholeValue && rule.Match.NodeKind != "" {
		return fmt.Errorf("%s matches the complete semantic value, not an individual node", action.name)
	}
	switch action.name {
	case "chat_stream_identity", "chat_response_model":
		if rule.Path != "" || !rule.Value.IsZero() || rule.Expression != nil || rule.Match.Path != "" || rule.Match.Present != nil {
			return fmt.Errorf("%s accepts route conditions only; its field boundaries are fixed", action.name)
		}
	case "error_metadata":
		var options errorProjectionOptions
		if rule.Value.Decode(&options) != nil || !knownConversionCodec(options.SourceCodec) || !knownConversionCodec(options.TargetCodec) {
			return fmt.Errorf("error_metadata requires sourceCodec and targetCodec")
		}
	case "request_projection":
		var options requestProjectionOptions
		if rule.Value.Decode(&options) != nil || !knownConversionCodec(options.TargetCodec) || (options.SourceCodec != "" && !knownConversionCodec(options.SourceCodec)) {
			return fmt.Errorf("request_projection requires targetCodec and an optional known sourceCodec")
		}
	case "reasoning_controls":
		var options reasoningOptions
		if rule.Value.Decode(&options) != nil || !knownConversionCodec(options.SourceCodec) || !knownConversionCodec(options.TargetCodec) {
			return fmt.Errorf("reasoning_controls requires sourceCodec and targetCodec")
		}
	case "responses_storage":
		var options responsesStorageOptions
		if rule.Value.Decode(&options) != nil || !knownConversionCodec(options.TargetCodec) || !knownConversionCodec(options.SourceCodec) || (options.OnUnsupported != "degrade" && options.OnUnsupported != "reject") {
			return fmt.Errorf("responses_storage requires sourceCodec, targetCodec and onUnsupported: degrade or reject")
		}
	case "provider_signature":
		if rule.Match.NodeKind != ToolCallNode || rule.Match.TargetFamily == "" {
			return fmt.Errorf("provider_signature requires a request tool_call rule with an explicit target family")
		}
		_, err := ProviderSignatureValue(rule.Value)
		return err
	case "tool_result_object", "tool_result_text":
		if rule.Match.NodeKind != ToolResultNode {
			return fmt.Errorf("%s requires request tool_result nodes", action.name)
		}
	}
	return nil
}
