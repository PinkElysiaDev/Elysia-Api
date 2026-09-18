package relay

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 字段级双向映射模型：用户从零构造请求体/返回体结构，并声明每个叶子对应
// Maheshvara 的哪个字段。注册时编译为引擎的内部表示（bodyTemplate + 直接
// 路径 + fieldMappings），运行时渲染/映射链路零改动。
//
// request.body 是"结构即配置"的树：容器为普通 JSON 对象/数组；叶子为字段
// 引用 {"field": "...", "mode": "string|json", "default"?, "omitIfEmpty"?}
// 或常量 {"value": ...}。含字段引用叶子的 body 按新模型编译；不含的 body
// 维持 legacy 语义（原样作为模板文本）。

// CustomProtocolResponseFieldMapping 声明一条响应字段映射：上游响应路径 →
// Maheshvara 响应字段（见响应字段目录，如 text / usage.input_tokens /
// metadata.vendor），可选 transform。
type CustomProtocolResponseFieldMapping struct {
	Path      string `json:"path"`
	Field     string `json:"field"`
	Transform string `json:"transform,omitempty"`
}

// MaheshvaraFieldSpec 描述目录中的一个可映射字段。
type MaheshvaraFieldSpec struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// string：文本（占位符放引号内）；native：对象/数组（原生 JSON 插入）；
	// scalar：数字/布尔（原生 JSON 插入）
	Shape string `json:"shape"`
	Group string `json:"group"`
}

// CustomProtocolSchema 是协议设计器的字段目录与约束，供 WebUI 下拉、AI
// harness 提示词与校验共用（GET /api/admin/custom-protocols/schema）。
type CustomProtocolSchema struct {
	RequestFields  []MaheshvaraFieldSpec    `json:"requestFields"`
	ResponseFields []MaheshvaraFieldSpec    `json:"responseFields"`
	Transforms     []string                 `json:"transforms"`
	Modes          []string                 `json:"modes"`
	Types          []CustomProtocolTypeSpec `json:"types"`
}

type CustomProtocolTypeSpec struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// 请求字段目录：name 即模板上下文 maheshvara.<name> 的字段路径。
var customProtocolRequestFieldCatalog = []MaheshvaraFieldSpec{
	{Name: "model", Label: "模型名（路由后实际使用的上游模型）", Shape: "string", Group: "基础"},
	{Name: "instructions", Label: "系统指令", Shape: "string", Group: "基础"},
	{Name: "messages", Label: "消息数组（role + content）", Shape: "native", Group: "基础"},
	{Name: "input_items", Label: "Responses 风格输入项", Shape: "native", Group: "基础"},
	{Name: "stream", Label: "是否流式", Shape: "scalar", Group: "基础"},
	{Name: "stream_options", Label: "流式选项", Shape: "native", Group: "基础"},
	{Name: "tools", Label: "工具定义数组", Shape: "native", Group: "工具"},
	{Name: "tool_choice", Label: "工具选择策略", Shape: "native", Group: "工具"},
	{Name: "parallel_tool_calls", Label: "允许并行工具调用", Shape: "scalar", Group: "工具"},
	{Name: "max_output_tokens", Label: "最大输出 token 数", Shape: "scalar", Group: "生成参数"},
	{Name: "min_output_tokens", Label: "最小输出 token 数", Shape: "scalar", Group: "生成参数"},
	{Name: "temperature", Label: "温度", Shape: "scalar", Group: "生成参数"},
	{Name: "top_p", Label: "Top-P", Shape: "scalar", Group: "生成参数"},
	{Name: "top_k", Label: "Top-K", Shape: "scalar", Group: "生成参数"},
	{Name: "stop", Label: "停止序列", Shape: "native", Group: "生成参数"},
	{Name: "seed", Label: "随机种子", Shape: "scalar", Group: "生成参数"},
	{Name: "presence_penalty", Label: "Presence 惩罚", Shape: "scalar", Group: "生成参数"},
	{Name: "frequency_penalty", Label: "Frequency 惩罚", Shape: "scalar", Group: "生成参数"},
	{Name: "repetition_penalty", Label: "重复惩罚", Shape: "scalar", Group: "生成参数"},
	{Name: "logprobs", Label: "是否返回 logprobs", Shape: "scalar", Group: "生成参数"},
	{Name: "top_logprobs", Label: "logprobs 数量", Shape: "scalar", Group: "生成参数"},
	{Name: "typical_p", Label: "Typical-P", Shape: "scalar", Group: "生成参数"},
	{Name: "min_p", Label: "Min-P", Shape: "scalar", Group: "生成参数"},
	{Name: "top_a", Label: "Top-A", Shape: "scalar", Group: "生成参数"},
	{Name: "response_format", Label: "响应格式（JSON schema 等）", Shape: "native", Group: "结构化输出"},
	{Name: "reasoning", Label: "推理配置（effort 等）", Shape: "native", Group: "推理"},
	{Name: "thinking", Label: "思考配置（budget_tokens 等）", Shape: "native", Group: "推理"},
	{Name: "modalities", Label: "输出模态", Shape: "native", Group: "多模态"},
	{Name: "audio", Label: "音频配置", Shape: "native", Group: "多模态"},
	{Name: "safety_settings", Label: "安全设置", Shape: "native", Group: "其他"},
	{Name: "service_tier", Label: "服务层级", Shape: "string", Group: "其他"},
	{Name: "verbosity", Label: "输出详细度", Shape: "string", Group: "其他"},
	{Name: "user", Label: "终端用户标识", Shape: "string", Group: "其他"},
	{Name: "metadata", Label: "元数据", Shape: "native", Group: "其他"},
	{Name: "raw_extra", Label: "客户端透传的未知字段（别名 extra）", Shape: "native", Group: "其他"},
}

// 响应字段目录：name 为语义映射目标，编译时展开为直接路径或 fieldMappings。
var customProtocolResponseFieldCatalog = []MaheshvaraFieldSpec{
	{Name: "text", Label: "正文文本", Shape: "string", Group: "内容"},
	{Name: "reasoning", Label: "思考文本", Shape: "string", Group: "内容"},
	{Name: "tool_calls", Label: "工具调用数组", Shape: "native", Group: "内容"},
	{Name: "usage", Label: "用量对象（多键名自动识别）", Shape: "native", Group: "用量"},
	{Name: "usage.input_tokens", Label: "输入 token 数", Shape: "scalar", Group: "用量"},
	{Name: "usage.output_tokens", Label: "输出 token 数", Shape: "scalar", Group: "用量"},
	{Name: "usage.total_tokens", Label: "总 token 数", Shape: "scalar", Group: "用量"},
	{Name: "usage.cached_input_tokens", Label: "缓存命中输入 token 数", Shape: "scalar", Group: "用量"},
	{Name: "usage.reasoning_tokens", Label: "思考 token 数", Shape: "scalar", Group: "用量"},
	{Name: "stop_reason", Label: "结束原因", Shape: "string", Group: "元信息"},
	{Name: "id", Label: "响应 ID", Shape: "string", Group: "元信息"},
	{Name: "model", Label: "模型名", Shape: "string", Group: "元信息"},
	{Name: "status", Label: "状态", Shape: "string", Group: "元信息"},
	{Name: "error", Label: "错误对象", Shape: "native", Group: "元信息"},
	{Name: "created_at", Label: "创建时间戳（秒）", Shape: "scalar", Group: "元信息"},
	{Name: "service_tier", Label: "服务层级", Shape: "string", Group: "元信息"},
	{Name: "system_fingerprint", Label: "系统指纹", Shape: "string", Group: "元信息"},
	{Name: "metadata", Label: "元数据（可用 metadata.<key> 子键）", Shape: "native", Group: "元信息"},
	{Name: "output", Label: "输出项（结构化，配 output_items 等 transform）", Shape: "native", Group: "高级"},
}

var customProtocolTransformCatalog = []string{
	"", "identity", "raw", "string", "text", "join", "int", "integer", "number", "float",
	"bool", "boolean", "json", "parse_json", "json_string", "timestamp_ms", "first",
	"usage", "content_parts", "tool_calls", "output_items",
}

// CustomProtocolSchemaFor 返回协议设计器的完整目录（单一事实来源）。
func CustomProtocolSchemaFor() CustomProtocolSchema {
	return CustomProtocolSchema{
		RequestFields:  append([]MaheshvaraFieldSpec(nil), customProtocolRequestFieldCatalog...),
		ResponseFields: append([]MaheshvaraFieldSpec(nil), customProtocolResponseFieldCatalog...),
		Transforms:     append([]string(nil), customProtocolTransformCatalog...),
		Modes:          []string{"json", "string"},
		Types: []CustomProtocolTypeSpec{
			{Value: CustomProtocolTypeLLM, Label: "LLM（聊天/生成）", Hint: "完整支持中转，四协议出入"},
			{Value: CustomProtocolTypeReranker, Label: "Reranker（预留）", Hint: "声明式预留：可注册与编辑，中转端点待接入"},
			{Value: CustomProtocolTypeEmbedding, Label: "Embedding（预留）", Hint: "声明式预留：可注册与编辑，中转端点待接入"},
		},
	}
}

func lookupRequestFieldSpec(name string) (MaheshvaraFieldSpec, bool) {
	for _, spec := range customProtocolRequestFieldCatalog {
		if spec.Name == name {
			return spec, true
		}
	}
	return MaheshvaraFieldSpec{}, false
}

// validateResponseFieldName 校验响应映射目标：目录命中，或 metadata.<key> /
// usage.<key> 子键（后者仍需在目录中，避免拼错静默失效）。
func validateResponseFieldName(name string) error {
	for _, spec := range customProtocolResponseFieldCatalog {
		if spec.Name == name {
			return nil
		}
	}
	if strings.HasPrefix(name, "metadata.") && strings.TrimSpace(strings.TrimPrefix(name, "metadata.")) != "" {
		return nil
	}
	return fmt.Errorf("field %q is not a Maheshvara response field", name)
}

// hasCustomProtocolAnnotationBody 判断 request.body 是否为含字段引用的新模型
// 树。不含任何字段引用叶子的 body 维持 legacy 语义（原样模板）。
func hasCustomProtocolAnnotationBody(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return scanAnnotationNode(value)
}

// scanAnnotationNode 深度扫描：发现首个字段引用叶子即返回 true。
func scanAnnotationNode(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["field"]; ok {
			return true
		}
		for _, child := range typed {
			if scanAnnotationNode(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if scanAnnotationNode(child) {
				return true
			}
		}
	}
	return false
}

type customBodyCompileResult struct {
	Template    string
	OmitIfEmpty []string
	OmitRules   []customOmitRule
}

// customOmitRule 是一条请求体的条件省略规则：when 条件不成立（对模板上下文
// 求值）或字段值类型化等于 omitIf 时，渲染后强制删除该叶子路径。
type customOmitRule struct {
	Path   string
	Field  string
	When   *CustomProtocolMatch
	OmitIf json.RawMessage
}

// compileCustomProtocolBody 把字段引用树编译为 bodyTemplate 文本与自动收集
// 的 omitIfEmpty 路径。叶子语义：mode string → 引号内占位符（字符串转义）；
// mode json（默认）→ 裸占位符（原生 JSON 值）；default 追加 | default: 过滤器。
func compileCustomProtocolBody(raw json.RawMessage) (customBodyCompileResult, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return customBodyCompileResult{}, fmt.Errorf("request.body is not valid JSON: %w", err)
	}
	var builder strings.Builder
	var omit []string
	var rules []customOmitRule
	if err := compileBodyNode(value, "", &builder, &omit, &rules); err != nil {
		return customBodyCompileResult{}, err
	}
	return customBodyCompileResult{Template: builder.String(), OmitIfEmpty: omit, OmitRules: rules}, nil
}

func compileBodyNode(value any, path string, builder *strings.Builder, omit *[]string, rules *[]customOmitRule) error {
	switch typed := value.(type) {
	case map[string]any:
		if _, isRef := typed["field"]; isRef {
			return compileBodyLeaf(typed, path, builder, omit, rules)
		}
		if constant, isConst := typed["value"]; isConst && len(typed) == 1 {
			encoded, err := json.Marshal(constant)
			if err != nil {
				return fmt.Errorf("request.body constant at %q: %w", path, err)
			}
			builder.Write(encoded)
			return nil
		}
		// 注解键出现但缺 field：几乎必是写错的字段引用，按错误处理而不是
		// 静默落成字面量（确需含这些键名的常量对象可用 {"value": {...}} 表达）。
		for _, suspicious := range []string{"mode", "default", "omitIfEmpty", "omitIf", "when"} {
			if _, present := typed[suspicious]; present {
				return fmt.Errorf("request.body node at %q has %q but is missing \"field\"", path, suspicious)
			}
		}
		if len(typed) == 0 {
			builder.WriteString("{}")
			return nil
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteString("{")
		for index, key := range keys {
			if index > 0 {
				builder.WriteString(", ")
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return err
			}
			builder.Write(encoded)
			builder.WriteString(": ")
			childPath := joinBodyPath(path, key)
			if err := compileBodyNode(typed[key], childPath, builder, omit, rules); err != nil {
				return err
			}
		}
		builder.WriteString("}")
		return nil
	case []any:
		if len(typed) == 0 {
			builder.WriteString("[]")
			return nil
		}
		builder.WriteString("[")
		for index, item := range typed {
			if index > 0 {
				builder.WriteString(", ")
			}
			childPath := fmt.Sprintf("%s[%d]", path, index)
			if err := compileBodyNode(item, childPath, builder, omit, rules); err != nil {
				return err
			}
		}
		builder.WriteString("]")
		return nil
	default:
		// 标量直接落到树里按常量处理（等价 {"value": x}，书写更自然）。
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		builder.Write(encoded)
		return nil
	}
}

func compileBodyLeaf(node map[string]any, path string, builder *strings.Builder, omit *[]string, rules *[]customOmitRule) error {
	for key := range node {
		switch key {
		case "field", "mode", "default", "omitIfEmpty", "omitIf", "when":
		default:
			return fmt.Errorf("request.body field reference at %q has unknown key %q (allowed: field, mode, default, omitIfEmpty, omitIf, when)", path, key)
		}
	}
	field, _ := node["field"].(string)
	field = strings.TrimSpace(field)
	if field == "" {
		return fmt.Errorf("request.body field reference at %q is missing \"field\"", path)
	}
	// field 允许「目录字段.子路径」（如 thinking.enabled）：基名必须在请求
	// 字段目录中，子路径交给渲染引擎的点路径解析（嵌套访问此前仅 legacy
	// 模板可用）。
	base, subpath, _ := strings.Cut(field, ".")
	spec, ok := lookupRequestFieldSpec(base)
	if !ok {
		return fmt.Errorf("request.body references unknown Maheshvara field %q at %q", field, path)
	}
	if subpath != "" {
		if _, err := parseCustomPath(subpath); err != nil {
			return fmt.Errorf("request.body field %q at %q has invalid subpath: %w", field, path, err)
		}
	}
	mode, _ := node["mode"].(string)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		mode = "json"
	case "json":
		mode = "json"
	case "string":
		mode = "string"
	default:
		return fmt.Errorf("request.body field %q at %q uses unsupported mode %q (allowed: json, string)", field, path, mode)
	}
	if _, shapeOK := node["mode"]; !shapeOK {
		// 未显式指定时按字段形态选默认：string 形态放引号内更符合直觉。
		if spec.Shape == "string" {
			mode = "string"
		}
	}
	expression := "maheshvara." + field
	if defaultValue, has := node["default"]; has {
		encoded, err := json.Marshal(defaultValue)
		if err != nil {
			return fmt.Errorf("request.body default at %q: %w", path, err)
		}
		expression += " | default:" + string(encoded)
	}
	if omitIfEmpty, _ := node["omitIfEmpty"].(bool); omitIfEmpty {
		if path == "" {
			return fmt.Errorf("request.body omitIfEmpty is not allowed at the root")
		}
		*omit = append(*omit, path)
	}
	rule := customOmitRule{Path: path, Field: field}
	if when, has := node["when"]; has {
		if path == "" {
			return fmt.Errorf("request.body when is not allowed at the root")
		}
		encoded, err := json.Marshal(when)
		if err != nil {
			return fmt.Errorf("request.body when at %q: %w", path, err)
		}
		var match CustomProtocolMatch
		if err := json.Unmarshal(encoded, &match); err != nil {
			return fmt.Errorf("request.body when at %q is not a condition object: %w", path, err)
		}
		// 条件路径相对 Maheshvara 请求根（与字段引用一致）；显式 maheshvara./
		// request. 前缀原样保留。
		if !strings.HasPrefix(match.Path, "maheshvara.") && !strings.HasPrefix(match.Path, "request.") {
			match.Path = "maheshvara." + match.Path
		}
		if err := validateCustomProtocolMatch(fmt.Sprintf("request.body when at %q", path), match); err != nil {
			return err
		}
		rule.When = &match
	}
	if omitIf, has := node["omitIf"]; has {
		if path == "" {
			return fmt.Errorf("request.body omitIf is not allowed at the root")
		}
		encoded, err := json.Marshal(omitIf)
		if err != nil {
			return fmt.Errorf("request.body omitIf at %q: %w", path, err)
		}
		rule.OmitIf = encoded
	}
	if rule.When != nil || rule.OmitIf != nil {
		*rules = append(*rules, rule)
	}
	if mode == "string" {
		builder.WriteString("\"{{" + expression + "}}\"")
	} else {
		builder.WriteString("{{" + expression + "}}")
	}
	return nil
}

func joinBodyPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// compileCustomProtocolResponseFields 把字段映射列表编译为引擎的响应结构：
// 语义直接字段（text/usage/...）展开为 *Path；usage.* 与其余目录字段展开为
// fieldMappings（usage.* 默认 transform int）。
func compileCustomProtocolResponseFields(location string, fields []CustomProtocolResponseFieldMapping) (CustomProtocolResponse, error) {
	compiled := CustomProtocolResponse{}
	directSeen := map[string]bool{}
	for _, field := range fields {
		path := strings.TrimSpace(field.Path)
		name := strings.TrimSpace(field.Field)
		if path == "" || name == "" {
			return CustomProtocolResponse{}, fmt.Errorf("%s: field mapping requires path and field", location)
		}
		if _, err := parseCustomPath(path); err != nil {
			return CustomProtocolResponse{}, fmt.Errorf("%s: path %q: %w", location, path, err)
		}
		if err := validateResponseFieldName(name); err != nil {
			return CustomProtocolResponse{}, fmt.Errorf("%s: %w", location, err)
		}
		if field.Transform != "" && !isSupportedCustomMappingTransform(field.Transform) {
			return CustomProtocolResponse{}, fmt.Errorf("%s: unsupported transform %q", location, field.Transform)
		}
		targets := map[string]*string{
			"text": &compiled.TextPath, "reasoning": &compiled.ReasoningPath, "tool_calls": &compiled.ToolCallsPath,
			"usage": &compiled.UsagePath, "stop_reason": &compiled.FinishReasonPath, "id": &compiled.IDPath,
			"model": &compiled.ModelPath, "status": &compiled.StatusPath, "error": &compiled.ErrorPath,
		}
		if target, ok := targets[name]; ok {
			if *target != "" {
				return CustomProtocolResponse{}, fmt.Errorf("%s: %s is mapped twice", location, name)
			}
			*target = path
			continue
		}
		// 其余目录字段(usage.*、metadata.* 等)落为 fieldMappings 行。
		if directSeen[name] {
			return CustomProtocolResponse{}, fmt.Errorf("%s: %s is mapped twice", location, name)
		}
		directSeen[name] = true
		mapping := CustomProtocolFieldMapping{Target: name, Source: path, Transform: strings.TrimSpace(field.Transform)}
		if strings.HasPrefix(name, "usage.") && mapping.Transform == "" {
			mapping.Transform = "int"
		}
		compiled.FieldMappings = append(compiled.FieldMappings, mapping)
	}
	return compiled, nil
}

// validateCustomProtocolDeclarative 校验新模型（字段树 + 返回体构造树 +
// 响应字段映射），成功即保证可编译。在 ValidateCustomProtocol 中调用。
func validateCustomProtocolDeclarative(config CustomProtocolConfig) error {
	if hasCustomProtocolAnnotationBody(config.Request.Body) {
		if _, err := compileCustomProtocolBody(config.Request.Body); err != nil {
			return fmt.Errorf("custom protocol %q request.body: %w", config.ID, err)
		}
	}
	merged, err := effectiveResponseFieldMappings("response", config.Response)
	if err != nil {
		return fmt.Errorf("custom protocol %q %s", config.ID, err)
	}
	if err := validateResponseFieldMappings("response", merged); err != nil {
		return fmt.Errorf("custom protocol %q %s", config.ID, err)
	}
	if config.Response.Stream != nil && config.Response.Stream.Response != nil {
		nested, nestedErr := effectiveResponseFieldMappings("response.stream.response", *config.Response.Stream.Response)
		if nestedErr != nil {
			return fmt.Errorf("custom protocol %q %s", config.ID, nestedErr)
		}
		if err := validateResponseFieldMappings("response.stream.response", nested); err != nil {
			return fmt.Errorf("custom protocol %q %s", config.ID, err)
		}
	}
	return nil
}

func validateResponseFieldMappings(location string, fields []CustomProtocolResponseFieldMapping) error {
	if len(fields) == 0 {
		return nil
	}
	seen := map[string]string{}
	for index, field := range fields {
		path := strings.TrimSpace(field.Path)
		name := strings.TrimSpace(field.Field)
		if path == "" || name == "" {
			return fmt.Errorf("%s.fields[%d] requires path and field", location, index)
		}
		if _, err := parseCustomPath(path); err != nil {
			return fmt.Errorf("%s.fields[%d].path: %w", location, index, err)
		}
		if err := validateResponseFieldName(name); err != nil {
			return fmt.Errorf("%s.fields[%d]: %w", location, index, err)
		}
		if field.Transform != "" && !isSupportedCustomMappingTransform(field.Transform) {
			return fmt.Errorf("%s.fields[%d] uses unsupported transform %q", location, index, field.Transform)
		}
		if previous, duplicated := seen[name]; duplicated {
			return fmt.Errorf("%s.fields[%d] maps %q twice (also %s)", location, index, name, previous)
		}
		seen[name] = path
	}
	if _, err := compileCustomProtocolResponseFields(location, fields); err != nil {
		return err
	}
	return nil
}

// effectiveCustomProtocolResponse 返回"编译结果覆盖 legacy 字段"的响应映射。
func effectiveCustomProtocolResponse(location string, response CustomProtocolResponse) (CustomProtocolResponse, error) {
	fields, err := effectiveResponseFieldMappings(location, response)
	if err != nil {
		return response, err
	}
	if len(fields) == 0 {
		return response, nil
	}
	compiled, err := compileCustomProtocolResponseFields(location, fields)
	if err != nil {
		return response, err
	}
	merged := response
	if compiled.IDPath != "" {
		merged.IDPath = compiled.IDPath
	}
	if compiled.ModelPath != "" {
		merged.ModelPath = compiled.ModelPath
	}
	if compiled.StatusPath != "" {
		merged.StatusPath = compiled.StatusPath
	}
	if compiled.TextPath != "" {
		merged.TextPath = compiled.TextPath
	}
	if compiled.ReasoningPath != "" {
		merged.ReasoningPath = compiled.ReasoningPath
	}
	if compiled.ToolCallsPath != "" {
		merged.ToolCallsPath = compiled.ToolCallsPath
	}
	if compiled.UsagePath != "" {
		merged.UsagePath = compiled.UsagePath
	}
	if compiled.FinishReasonPath != "" {
		merged.FinishReasonPath = compiled.FinishReasonPath
	}
	if compiled.ErrorPath != "" {
		merged.ErrorPath = compiled.ErrorPath
	}
	if len(compiled.FieldMappings) > 0 {
		merged.FieldMappings = append(append([]CustomProtocolFieldMapping(nil), compiled.FieldMappings...), response.FieldMappings...)
	}
	return merged, nil
}

// extractResponseBodyFields 从返回体构造树提取字段映射：叶子为映射标注
// {"field": ..., "value"?: <示例值>, "transform"?}；{"value": ...} 与裸标量
// 为纯结构占位。路径按树位置生成（特殊键名用 ['key'] 形式）。
func extractResponseBodyFields(location string, raw json.RawMessage) ([]CustomProtocolResponseFieldMapping, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s.body is not valid JSON: %w", location, err)
	}
	var fields []CustomProtocolResponseFieldMapping
	seen := map[string]string{}
	var walk func(node any, path string) error
	walk = func(node any, path string) error {
		switch typed := node.(type) {
		case map[string]any:
			if fieldText, ok := typed["field"].(string); ok {
				name := strings.TrimSpace(fieldText)
				if name == "" {
					return fmt.Errorf("%s.body node at %q has empty \"field\"", location, path)
				}
				for key := range typed {
					switch key {
					case "field", "value", "transform":
					default:
						return fmt.Errorf("%s.body field node at %q has unknown key %q (allowed: field, value, transform)", location, path, key)
					}
				}
				transform, _ := typed["transform"].(string)
				if previous, duplicated := seen[name]; duplicated {
					return fmt.Errorf("%s.body maps %q twice (%s and %s)", location, name, previous, path)
				}
				seen[name] = path
				fields = append(fields, CustomProtocolResponseFieldMapping{
					Path:      path,
					Field:     name,
					Transform: strings.TrimSpace(transform),
				})
				return nil
			}
			if _, hasTransform := typed["transform"]; hasTransform {
				return fmt.Errorf("%s.body node at %q has \"transform\" but is missing \"field\"", location, path)
			}
			if _, hasValue := typed["value"]; hasValue && len(typed) == 1 {
				return nil // 纯结构占位
			}
			if _, suspicious := typed["mode"]; suspicious {
				return fmt.Errorf("%s.body node at %q uses request-side key \"mode\" (response fields allow: field, value, transform)", location, path)
			}
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if err := walk(typed[key], joinResponseBodyPath(path, key)); err != nil {
					return err
				}
			}
			return nil
		case []any:
			for index, item := range typed {
				if err := walk(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(value, "$"); err != nil {
		return nil, err
	}
	return fields, nil
}

func joinResponseBodyPath(parent, key string) string {
	if len(key) > 0 && key[0] != '[' && regexpIdentifierOnly.MatchString(key) {
		return parent + "." + key
	}
	return fmt.Sprintf("%s['%s']", parent, strings.ReplaceAll(key, "'", "\\'"))
}

var regexpIdentifierOnly = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// effectiveResponseFieldMappings 合并返回体构造树的提取结果与显式 fields：
// 两者重复声明同一字段视为配置错误（body 与 fields 二选一），重复即报错。
func effectiveResponseFieldMappings(location string, response CustomProtocolResponse) ([]CustomProtocolResponseFieldMapping, error) {
	fromBody, err := extractResponseBodyFields(location, response.Body)
	if err != nil {
		return nil, err
	}
	merged := append([]CustomProtocolResponseFieldMapping(nil), fromBody...)
	seen := make(map[string]string, len(fromBody))
	for _, field := range fromBody {
		seen[strings.TrimSpace(field.Field)] = field.Path
	}
	for _, field := range response.Fields {
		name := strings.TrimSpace(field.Field)
		if previous, duplicated := seen[name]; duplicated {
			return nil, fmt.Errorf("%s maps %q twice (previously at %s)", location, name, previous)
		}
		seen[name] = field.Path
		merged = append(merged, field)
	}
	return merged, nil
}

// effectiveCustomProtocolRuntimeMapping 计算运行时生效的响应映射：字段级
// 映射编译结果覆盖 legacy 字段；流式事件再切换到 stream.response（其字段
// 映射同样编译覆盖）。payloadPath 解包由调用方在原始响应上完成。
func effectiveCustomProtocolRuntimeMapping(config CustomProtocolConfig, allowStreamEvent bool) (CustomProtocolResponse, error) {
	mapping := config.Response
	if len(mapping.Fields) > 0 || len(mapping.Body) > 0 {
		compiled, err := effectiveCustomProtocolResponse("response", mapping)
		if err != nil {
			return mapping, err
		}
		mapping = compiled
	}
	// 空嵌套映射（设计器旧版开关写入的 {body:{}}）视为未声明：保留顶层映射，
	// 否则流帧什么都映射不到，[DONE] 后必然 502「无可呈现输出」。
	if allowStreamEvent && mapping.Stream != nil && mapping.Stream.Response != nil &&
		customProtocolResponseHasMapping(*mapping.Stream.Response) {
		nested := *mapping.Stream.Response
		if len(nested.Fields) > 0 || len(nested.Body) > 0 {
			compiled, err := effectiveCustomProtocolResponse("response.stream.response", nested)
			if err != nil {
				return mapping, err
			}
			nested = compiled
		}
		mapping = nested
	}
	return mapping, nil
}

// customProtocolResponseHasMapping 判定一份响应映射是否声明了任何可产出的
// 字段：九个直接路径 / mappings / fieldMappings / fields 任一非空，或 body
// 构造树为非空 JSON（"{}"/null/空白视为空）。
func customProtocolResponseHasMapping(response CustomProtocolResponse) bool {
	if response.IDPath != "" || response.ModelPath != "" || response.StatusPath != "" ||
		response.TextPath != "" || response.ReasoningPath != "" || response.ToolCallsPath != "" ||
		response.UsagePath != "" || response.FinishReasonPath != "" || response.ErrorPath != "" {
		return true
	}
	if len(response.Mappings) > 0 || len(response.FieldMappings) > 0 || len(response.Fields) > 0 {
		return true
	}
	trimmed := strings.TrimSpace(string(response.Body))
	return trimmed != "" && trimmed != "{}" && trimmed != "null"
}
