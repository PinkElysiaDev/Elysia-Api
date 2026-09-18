package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	customProtocolMaxTemplateBytes = 4 << 20
	customProtocolMaxDepth         = 64
	customProtocolMaxPlaceholders  = 2048
)

// CustomProtocolConfig describes a provider-specific wire protocol without
// adding provider logic to the Maheshvara core. The request body is a JSON
// template; placeholders can insert either escaped strings or native JSON
// values. Response paths are dot paths rooted at the decoded provider body.
type CustomProtocolConfig struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name,omitempty"`
	Version  string                 `json:"version,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Request  CustomProtocolRequest  `json:"request"`
	Response CustomProtocolResponse `json:"response,omitempty"`
	Models   *CustomProtocolModels  `json:"models,omitempty"`
	// Aliases 覆盖提取阶段的键名别名表；提供即整体替换该类默认表。
	Aliases  *CustomProtocolAliases `json:"aliases,omitempty"`
	Metadata map[string]any         `json:"metadata,omitempty"`
}

// CustomProtocolAliases 让键名不符合内置别名表的供应商可声明自己的取值键：
// textKeys 为文本提取魔键（默认 text/content/message/value/output）；usage 与
// toolCall 按类别给出键列表，条目支持点路径（如 prompt_tokens_details.
// cached_tokens、function.arguments），提供即替换该类默认。
type CustomProtocolAliases struct {
	TextKeys []string            `json:"textKeys,omitempty"`
	Usage    map[string][]string `json:"usage,omitempty"`    // input/output/total/cached/reasoning
	ToolCall map[string][]string `json:"toolCall,omitempty"` // id/name/arguments
}

// CustomProtocolModels 声明该协议的模型列表发现端点：配置后 custom:<id> 模型源
// 可开启自动拉取。请求构造与鉴权注入复用自定义协议管线，响应侧以 listPath
// 定位模型数组，idPath/namePath 在每个元素内取标识与展示名。
type CustomProtocolModels struct {
	Method   string              `json:"method,omitempty"` // 默认 GET，仅 GET/POST
	Path     string              `json:"path"`             // 必填，相对源 baseUrl
	Headers  map[string]string   `json:"headers,omitempty"`
	Query    map[string]string   `json:"query,omitempty"`
	Auth     *CustomProtocolAuth `json:"auth,omitempty"`     // 缺省复用 request.auth
	ListPath string              `json:"listPath"`           // 必填，点路径到模型数组
	IDPath   string              `json:"idPath,omitempty"`   // 元素内，默认 "id"
	NamePath string              `json:"namePath,omitempty"` // 元素内
}

// CustomProtocolModelInfo 是发现端点解析出的单个模型标识。
type CustomProtocolModelInfo struct {
	ID   string
	Name string
}

// 协议任务类型的内置约定。当前运行时中转只实现 LLM 语义；reranker/embedding
// 是声明式预留：注册、模板渲染与响应映射照常可用，等待对应端点接入后生效。
// x- 前缀保留给外部扩展，核心不做任何解释。
const (
	CustomProtocolTypeLLM       = "llm"
	CustomProtocolTypeReranker  = "reranker"
	CustomProtocolTypeEmbedding = "embedding"
)

// NormalizeCustomProtocolType 归一化协议类型：空值回落 llm；接受 llm/reranker/
// embedding 与 x- 前缀扩展名。返回空串表示非法值。
func NormalizeCustomProtocolType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "":
		return CustomProtocolTypeLLM
	case CustomProtocolTypeLLM, CustomProtocolTypeReranker, CustomProtocolTypeEmbedding:
		return normalized
	}
	if strings.HasPrefix(normalized, "x-") && len(strings.TrimSpace(normalized)) > 2 {
		return normalized
	}
	return ""
}

type CustomProtocolRequest struct {
	Method       string `json:"method,omitempty"`
	PathTemplate string `json:"path,omitempty"`
	// PathStream 流式请求的路径覆盖（Gemini :generateContent vs
	// :streamGenerateContent?alt=sse 这类按流切换动词的端点）；缺省同 path。
	PathStream string `json:"pathStream,omitempty"`
	// Shape 让模板上下文的 messages/tools（responses 另含 input/input_items）
	// 切换为对应线制形状——复用内置四协议的整形器，自定义协议免费获得重型
	// 消息整形。取值 openai-chat / anthropic / gemini / responses。
	Shape        string             `json:"shape,omitempty"`
	Headers      map[string]string  `json:"headers,omitempty"`
	Query        map[string]string  `json:"query,omitempty"`
	ContentType  string             `json:"contentType,omitempty"`
	Auth         CustomProtocolAuth `json:"auth,omitempty"`
	BodyTemplate string             `json:"bodyTemplate,omitempty"`
	SubmitBody   string             `json:"submitBody,omitempty"`
	Body         json.RawMessage    `json:"body,omitempty"`
	OmitIfEmpty  []string           `json:"omitIfEmpty,omitempty"`
}

type CustomProtocolAuth struct {
	Mode   string `json:"mode,omitempty"`
	Header string `json:"header,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	Query  string `json:"query,omitempty"`
}

type CustomProtocolResponse struct {
	// Body 是返回体构造树：容器为普通 JSON 对象/数组，叶子为
	// {"field": "<响应字段>", "value"?: <示例值>, "transform"?} 映射标注或
	// {"value": ...} / 裸标量结构占位。编译时从中提取字段映射。
	Body       json.RawMessage `json:"body,omitempty"`
	IDPath     string          `json:"idPath,omitempty"`
	ModelPath  string          `json:"modelPath,omitempty"`
	StatusPath string          `json:"statusPath,omitempty"`
	TextPath   string          `json:"textPath,omitempty"`
	// TextFilter 在 textPath 指向对象数组时按元素过滤（如 Anthropic 分离
	// thinking/text 块、Gemini 分离 thought 部件）再提取文本；接受单条件或
	// 条件数组（数组=全部成立）。
	TextFilter       CustomProtocolMatchSet               `json:"textFilter,omitempty"`
	ReasoningPath    string                               `json:"reasoningPath,omitempty"`
	ReasoningFilter  CustomProtocolMatchSet               `json:"reasoningFilter,omitempty"`
	ToolCallsPath    string                               `json:"toolCallsPath,omitempty"`
	UsagePath        string                               `json:"usagePath,omitempty"`
	FinishReasonPath string                               `json:"finishReasonPath,omitempty"`
	ErrorPath        string                               `json:"errorPath,omitempty"`
	Mappings         map[string]string                    `json:"mappings,omitempty"`
	FieldMappings    []CustomProtocolFieldMapping         `json:"fieldMappings,omitempty"`
	Fields           []CustomProtocolResponseFieldMapping `json:"fields,omitempty"`
	Sample           json.RawMessage                      `json:"sample,omitempty"`
	Stream           *CustomProtocolStreamMapping         `json:"stream,omitempty"`
}

type CustomProtocolFieldMapping struct {
	Target      string          `json:"target"`
	Source      string          `json:"source,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Transform   string          `json:"transform,omitempty"`
	OmitIfEmpty bool            `json:"omitIfEmpty,omitempty"`
}

type CustomProtocolStreamMapping struct {
	PayloadPath string   `json:"payloadPath,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	DoneValues  []string `json:"doneValues,omitempty"`
	// Done 携带类型化终止值：Raw 为整串文本字面量（与 DoneValues 同语义），
	// JSON 按解析后的载荷值做类型化匹配（如 {"json":true} 命中 data: true）。
	Done              []CustomProtocolDoneValue `json:"done,omitempty"`
	DoneValuesReplace bool                      `json:"doneValuesReplace,omitempty"` // 移除默认 [DONE]
	Events            []string                  `json:"events,omitempty"`
	// EventKeys 定义 JSON 载荷内事件名判别键（缺省 ["type","event"]）。
	EventKeys []string `json:"eventKeys,omitempty"`
	// FinishWhen/StatusWhen 覆盖终止判定：缺省沿用 legacy（finishReasonPath
	// 字符串化非空 / status=="completed"）；配置后按 Match 语义判定。
	FinishWhen *CustomProtocolMatch `json:"finishWhen,omitempty"`
	StatusWhen *CustomProtocolMatch `json:"statusWhen,omitempty"`
	// Modes 按字段族（text/reasoning/arguments）覆盖全局 mode——文本累计、
	// 工具参数增量可混用。
	Modes    *CustomProtocolStreamModes  `json:"modes,omitempty"`
	Frames   []CustomProtocolStreamFrame `json:"frames,omitempty"`
	Response *CustomProtocolResponse     `json:"response,omitempty"`
}

// CustomProtocolStreamModes 是按字段族的 delta/cumulative 覆盖；未声明的
// 族沿用流级全局 mode。
type CustomProtocolStreamModes struct {
	Text      string `json:"text,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// CustomProtocolDoneValue 是一个流终止值：Raw（文本字面量）与 JSON（类型化
// 值）二选一。
type CustomProtocolDoneValue struct {
	Raw  string          `json:"raw,omitempty"`
	JSON json.RawMessage `json:"json,omitempty"`
}

// CustomProtocolStreamFrame 是异构流的一类帧的映射规则：按事件名（SSE event
// 字段，缺省时取 JSON type/event 字段）匹配，命中后以 frame.response 映射该
// 帧（缺省回退流级默认映射）。terminal=true 时命中即判定流终态，覆盖
// response.completed / message_stop 这类「以事件名收尾」的协议。frames 存在
// 时优先于 legacy events 白名单；未匹配任何帧的事件跳过（需要兜底映射时用
// stream.response 声明）。
type CustomProtocolStreamFrame struct {
	// Event 按 SSE event 字段（或 eventKeys 判别键）匹配；Match 按帧 JSON
	// 谓词匹配（无事件名协议如 Gemini data-only 帧）。两者都给出时须同时成立，
	// 至少给一个。
	Event       string               `json:"event,omitempty"`
	Match       *CustomProtocolMatch `json:"match,omitempty"`
	PayloadPath string               `json:"payloadPath,omitempty"`
	// Tool 声明分帧工具拼装：身份帧给 id/name，参数帧给增量参数片段；按
	// idPath 或 indexPath（关联身份帧）关联到同一工具调用。
	Tool     *CustomProtocolStreamTool `json:"tool,omitempty"`
	Response *CustomProtocolResponse   `json:"response,omitempty"`
	Terminal bool                      `json:"terminal,omitempty"`
}

// CustomProtocolStreamTool 是帧级工具调用拼装规则：路径相对帧原始 JSON。
// argumentsMode 缺省 delta（片段原样追加），cumulative 时片段为累计快照。
type CustomProtocolStreamTool struct {
	// Path 指向工具对象数组（如 choices[0].delta.tool_calls）：设定时按元素
	// 遍历（单帧多工具），以下路径相对每个元素；缺省时相对帧根，单工具。
	Path          string `json:"path,omitempty"`
	IDPath        string `json:"idPath,omitempty"`
	IndexPath     string `json:"indexPath,omitempty"`
	NamePath      string `json:"namePath,omitempty"`
	ArgumentsPath string `json:"argumentsPath,omitempty"`
	ArgumentsMode string `json:"argumentsMode,omitempty"`
}

func (request CustomProtocolRequest) bodyTemplate() string {
	if strings.TrimSpace(request.BodyTemplate) != "" {
		return request.BodyTemplate
	}
	if strings.TrimSpace(request.SubmitBody) != "" {
		return request.SubmitBody
	}
	if len(request.Body) > 0 {
		return string(request.Body)
	}
	return ""
}

// effectiveBodyTemplate 返回生效的请求体模板与 omitIfEmpty 路径：字段引用树
// （新模型）编译为模板并自动收集 omit 路径；否则维持 legacy 行为。
func (request CustomProtocolRequest) effectiveBodyTemplate() (string, []string, []customOmitRule, error) {
	if !hasCustomProtocolAnnotationBody(request.Body) {
		return request.bodyTemplate(), request.OmitIfEmpty, nil, nil
	}
	compiled, err := compileCustomProtocolBody(request.Body)
	if err != nil {
		return "", nil, nil, err
	}
	omit := append([]string(nil), request.OmitIfEmpty...)
	omit = append(omit, compiled.OmitIfEmpty...)
	return compiled.Template, omit, compiled.OmitRules, nil
}

type CustomProtocolRequestResult struct {
	Method      string
	Path        string
	Headers     map[string]string
	Query       map[string]string
	Body        []byte
	ContentType string
	Auth        CustomProtocolAuth
}

// compiledCustomProtocol 在注册时一次性完成请求体构造树的编译,热路径直接
// 取用(每请求的 hasCustomProtocolAnnotationBody+双重 unmarshal 由注册吸收)。
type compiledCustomProtocol struct {
	config       CustomProtocolConfig
	bodyTemplate string
	omitIfEmpty  []string
	omitRules    []customOmitRule
}

var customProtocolRegistry = struct {
	sync.RWMutex
	items map[string]compiledCustomProtocol
}{items: make(map[string]compiledCustomProtocol)}

// compileCustomProtocol 编译请求体构造树;须在 ValidateCustomProtocol 通过后
// 调用(编译错误此时不可能出现,仍以防御性错误返回)。
func compileCustomProtocol(config CustomProtocolConfig) (compiledCustomProtocol, error) {
	template, omitIfEmpty, omitRules, err := config.Request.effectiveBodyTemplate()
	if err != nil {
		return compiledCustomProtocol{}, err
	}
	return compiledCustomProtocol{
		config:       config,
		bodyTemplate: template,
		omitIfEmpty:  append([]string(nil), omitIfEmpty...),
		omitRules:    omitRules,
	}, nil
}

// RegisterCustomProtocol validates and atomically installs a custom protocol.
func RegisterCustomProtocol(config CustomProtocolConfig) error {
	if err := ValidateCustomProtocol(config); err != nil {
		return err
	}
	config = normalizeCustomProtocol(config)
	compiled, err := compileCustomProtocol(config)
	if err != nil {
		return err
	}
	customProtocolRegistry.Lock()
	customProtocolRegistry.items[strings.ToLower(strings.TrimSpace(config.ID))] = compiled
	customProtocolRegistry.Unlock()
	return nil
}

// ReplaceCustomProtocols validates the complete set and swaps it atomically.
// A failed reload leaves the previously registered protocols untouched.
func ReplaceCustomProtocols(configs []CustomProtocolConfig) error {
	next := make(map[string]compiledCustomProtocol, len(configs))
	for _, config := range configs {
		if err := ValidateCustomProtocol(config); err != nil {
			return err
		}
		id := strings.ToLower(strings.TrimSpace(config.ID))
		if _, exists := next[id]; exists {
			return fmt.Errorf("custom protocol %q is duplicated", config.ID)
		}
		config = normalizeCustomProtocol(config)
		compiled, err := compileCustomProtocol(config)
		if err != nil {
			return err
		}
		next[id] = compiled
	}
	customProtocolRegistry.Lock()
	customProtocolRegistry.items = next
	customProtocolRegistry.Unlock()
	return nil
}

// normalizeCustomProtocol 在入库/注册时净化已知的历史踩坑形态：设计器旧版
// 「流式映射」开关写入的空嵌套映射会在运行时顶掉顶层映射，加载即改为
// 「继承顶层」（与 effectiveCustomProtocolRuntimeMapping 的容错语义一致）。
func normalizeCustomProtocol(config CustomProtocolConfig) CustomProtocolConfig {
	config.Type = NormalizeCustomProtocolType(config.Type)
	if stream := config.Response.Stream; stream != nil && stream.Response != nil &&
		!customProtocolResponseHasMapping(*stream.Response) {
		streamCopy := *stream
		streamCopy.Response = nil
		config.Response.Stream = &streamCopy
	}
	return config
}

func GetCustomProtocol(id string) (CustomProtocolConfig, bool) {
	customProtocolRegistry.RLock()
	compiled, ok := customProtocolRegistry.items[strings.ToLower(strings.TrimSpace(id))]
	customProtocolRegistry.RUnlock()
	if !ok {
		return CustomProtocolConfig{}, false
	}
	return cloneCustomProtocol(compiled.config), true
}

func getCompiledCustomProtocol(id string) (compiledCustomProtocol, bool) {
	customProtocolRegistry.RLock()
	compiled, ok := customProtocolRegistry.items[strings.ToLower(strings.TrimSpace(id))]
	customProtocolRegistry.RUnlock()
	return compiled, ok
}

func ClearCustomProtocols() {
	customProtocolRegistry.Lock()
	customProtocolRegistry.items = make(map[string]compiledCustomProtocol)
	customProtocolRegistry.Unlock()
}

func ValidateCustomProtocol(config CustomProtocolConfig) error {
	config.ID = strings.TrimSpace(config.ID)
	if config.ID == "" {
		return fmt.Errorf("custom protocol id is required")
	}
	if strings.ContainsAny(config.ID, " /\\\t\r\n") {
		return fmt.Errorf("custom protocol %q has invalid id", config.ID)
	}
	if NormalizeCustomProtocolType(config.Type) == "" {
		return fmt.Errorf("custom protocol %q has invalid type %q (allowed: llm, reranker, embedding, x-*)", config.ID, config.Type)
	}
	if err := validateCustomProtocolDeclarative(config); err != nil {
		return err
	}
	method := strings.ToUpper(strings.TrimSpace(config.Request.Method))
	if method == "" {
		method = http.MethodPost
	}
	template, omitIfEmpty, omitRules, err := config.Request.effectiveBodyTemplate()
	if err != nil {
		return fmt.Errorf("custom protocol %q request.body: %w", config.ID, err)
	}
	if len(template) == 0 && method != http.MethodGet && method != http.MethodDelete {
		return fmt.Errorf("custom protocol %q request.bodyTemplate is required", config.ID)
	}
	if len(template) > customProtocolMaxTemplateBytes {
		return fmt.Errorf("custom protocol %q request template exceeds %d bytes", config.ID, customProtocolMaxTemplateBytes)
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("custom protocol %q uses unsupported method %q", config.ID, method)
	}
	if template != "" {
		// 校验一(空上下文):空值嵌 null 后的合法性(既有行为)。
		if _, err := renderCustomTemplate(template, maheshvaraTemplateContext(&MaheshvaraRequest{}), nil, omitRules); err != nil {
			return fmt.Errorf("custom protocol %q has invalid body template: %w", config.ID, err)
		}
		// 校验二(非空示例值):空值以字符串嵌入时仍是合法 JSON 字符串,会
		// 放过「引号内占位符+其它文本」的缺陷模板(运行时非空字符串裸嵌
		// 进字符串字面量 → rendered body is not valid JSON,每请求必炸)。
		sample := maheshvaraTemplateContext(&MaheshvaraRequest{
			Model: "x", Instructions: "x", Stream: true,
			Messages: []MaheshvaraMessage{{Role: "user", Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: "x"}}}},
		})
		if _, err := renderCustomTemplate(template, sample, nil, omitRules); err != nil {
			return fmt.Errorf("custom protocol %q body template fails with non-empty sample values (quoted placeholder mixed with literal text?): %w", config.ID, err)
		}
	}
	for _, path := range omitIfEmpty {
		if _, err := parseCustomPath(normalizeMaheshvaraPath(path)); err != nil {
			return fmt.Errorf("custom protocol %q omitIfEmpty path %q: %w", config.ID, path, err)
		}
	}
	if err := validateCustomStringTemplate(config.Request.PathTemplate); err != nil {
		return fmt.Errorf("custom protocol %q request.path: %w", config.ID, err)
	}
	if err := validateCustomStringTemplate(config.Request.PathStream); err != nil {
		return fmt.Errorf("custom protocol %q request.pathStream: %w", config.ID, err)
	}
	switch strings.ToLower(strings.TrimSpace(config.Request.Shape)) {
	case "", "openai-chat", "anthropic", "gemini", "responses":
	default:
		return fmt.Errorf("custom protocol %q request.shape %q is unsupported (allowed: openai-chat, anthropic, gemini, responses)", config.ID, config.Request.Shape)
	}
	if err := validateCustomHeaders(config.ID, "request.headers", config.Request.Headers); err != nil {
		return err
	}
	for key, value := range config.Request.Query {
		if err := validateCustomStringTemplate(value); err != nil {
			return fmt.Errorf("custom protocol %q request.query[%q]: %w", config.ID, key, err)
		}
	}
	if contentType := strings.TrimSpace(config.Request.ContentType); contentType != "" && strings.ContainsAny(contentType, "\r\n") {
		return fmt.Errorf("custom protocol %q has invalid content type", config.ID)
	}
	if err := validateCustomAuth(config.Request.Auth); err != nil {
		return fmt.Errorf("custom protocol %q auth: %w", config.ID, err)
	}
	if err := validateCustomProtocolModels(config.ID, config.Models); err != nil {
		return err
	}
	if err := validateCustomProtocolAliases(config.ID, config.Aliases); err != nil {
		return err
	}
	return validateCustomProtocolResponse(config.ID, "response", config.Response, true)
}

// validateCustomProtocolAliases 校验别名覆盖：类别名受限，条目为合法点路径
// 且非空。
func validateCustomProtocolAliases(configID string, aliases *CustomProtocolAliases) error {
	if aliases == nil {
		return nil
	}
	for _, key := range aliases.TextKeys {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, ".[]{}\r\n") {
			return fmt.Errorf("custom protocol %q aliases.textKeys entry %q must be a plain key", configID, key)
		}
	}
	usageCategories := map[string]bool{"input": true, "output": true, "total": true, "cached": true, "reasoning": true}
	for category, keys := range aliases.Usage {
		if !usageCategories[category] {
			return fmt.Errorf("custom protocol %q aliases.usage has unknown category %q (allowed: input, output, total, cached, reasoning)", configID, category)
		}
		if len(keys) == 0 {
			return fmt.Errorf("custom protocol %q aliases.usage.%s is empty", configID, category)
		}
		for _, key := range keys {
			if _, err := parseCustomPath(key); err != nil {
				return fmt.Errorf("custom protocol %q aliases.usage.%s entry %q: %w", configID, category, key, err)
			}
		}
	}
	toolCategories := map[string]bool{"id": true, "name": true, "arguments": true}
	for category, keys := range aliases.ToolCall {
		if !toolCategories[category] {
			return fmt.Errorf("custom protocol %q aliases.toolCall has unknown category %q (allowed: id, name, arguments)", configID, category)
		}
		if len(keys) == 0 {
			return fmt.Errorf("custom protocol %q aliases.toolCall.%s is empty", configID, category)
		}
		for _, key := range keys {
			if _, err := parseCustomPath(key); err != nil {
				return fmt.Errorf("custom protocol %q aliases.toolCall.%s entry %q: %w", configID, category, key, err)
			}
		}
	}
	return nil
}

// 保护头清单与鉴权头校验:请求/模型发现两处的 headers 共用。
var protectedCustomHeaders = map[string]struct{}{
	"authorization":       {},
	"x-api-key":           {},
	"x-goog-api-key":      {},
	"host":                {},
	"content-length":      {},
	"transfer-encoding":   {},
	"connection":          {},
	"proxy-authorization": {},
}

func isValidCustomHeaderName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		default:
			return false
		}
	}
	return true
}

func isProtectedCustomHeader(name string) bool {
	_, ok := protectedCustomHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func validateCustomAuth(auth CustomProtocolAuth) error {
	mode := strings.ToLower(strings.TrimSpace(auth.Mode))
	if mode == "" {
		mode = "bearer"
	}
	switch mode {
	case "bearer", "none":
		return nil
	case "header":
		header := firstNonEmptyString(strings.TrimSpace(auth.Header), "x-api-key")
		if !isValidCustomHeaderName(header) || isUnsafeCustomAuthHeader(header) {
			return fmt.Errorf("header auth requires a valid end-to-end header name")
		}
		if strings.ContainsAny(auth.Prefix, "\r\n") {
			return fmt.Errorf("auth prefix contains a line break")
		}
		return nil
	case "query":
		if strings.TrimSpace(auth.Query) == "" {
			return fmt.Errorf("query auth requires query")
		}
		if strings.ContainsAny(auth.Query, "\r\n") {
			return fmt.Errorf("auth query contains a line break")
		}
		return nil
	default:
		return fmt.Errorf("unsupported auth mode %q", auth.Mode)
	}
}

func isUnsafeCustomAuthHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "host", "content-length", "transfer-encoding", "connection", "proxy-authorization":
		return true
	default:
		return false
	}
}

// validateCustomHeaders 校验一处自定义协议头的模板语法与保护头规则
// (request.headers 与 models.headers 共用)。
// customMaheshvaraPrefix 是模板/映射路径的 Maheshvara 请求根前缀;规范化
// 逻辑集中于此,防止校验与运行时两处各写一份而漂移。
const customMaheshvaraPrefix = "maheshvara."

// normalizeMaheshvaraPath 去空白并剥掉可选的 maheshvara. 前缀。
func normalizeMaheshvaraPath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), customMaheshvaraPrefix)
}

func validateCustomHeaders(configID, location string, headers map[string]string) error {
	for key, value := range headers {
		if err := validateCustomStringTemplate(value); err != nil {
			return fmt.Errorf("custom protocol %q %s[%q]: %w", configID, location, key, err)
		}
	}
	for key := range headers {
		name := strings.TrimSpace(key)
		if name == "" {
			return fmt.Errorf("custom protocol %q contains an empty header name", configID)
		}
		if !isValidCustomHeaderName(name) {
			return fmt.Errorf("custom protocol %q contains invalid header name %q", configID, key)
		}
		if isProtectedCustomHeader(name) {
			return fmt.Errorf("custom protocol %q header %q is managed by the relay; use auth", configID, key)
		}
		if strings.ContainsAny(headers[key], "\r\n") {
			return fmt.Errorf("custom protocol %q header %q contains a line break", configID, key)
		}
	}
	return nil
}

func validateCustomProtocolModels(configID string, models *CustomProtocolModels) error {
	if models == nil {
		return nil
	}
	if strings.TrimSpace(models.Path) == "" {
		return fmt.Errorf("custom protocol %q models.path is required", configID)
	}
	if err := validateCustomStringTemplate(models.Path); err != nil {
		return fmt.Errorf("custom protocol %q models.path: %w", configID, err)
	}
	method := strings.ToUpper(strings.TrimSpace(models.Method))
	switch method {
	case "", http.MethodGet, http.MethodPost:
	default:
		return fmt.Errorf("custom protocol %q models method %q is unsupported (GET or POST)", configID, models.Method)
	}
	if err := validateCustomHeaders(configID, "models.headers", models.Headers); err != nil {
		return err
	}
	for key, value := range models.Query {
		if err := validateCustomStringTemplate(value); err != nil {
			return fmt.Errorf("custom protocol %q models.query[%q]: %w", configID, key, err)
		}
	}
	if models.Auth != nil {
		if err := validateCustomAuth(*models.Auth); err != nil {
			return fmt.Errorf("custom protocol %q models.auth: %w", configID, err)
		}
	}
	if strings.TrimSpace(models.ListPath) == "" {
		return fmt.Errorf("custom protocol %q models.listPath is required", configID)
	}
	if _, err := parseCustomPath(models.ListPath); err != nil {
		return fmt.Errorf("custom protocol %q models.listPath: %w", configID, err)
	}
	if idPath := strings.TrimSpace(models.IDPath); idPath != "" {
		if _, err := parseCustomPath(idPath); err != nil {
			return fmt.Errorf("custom protocol %q models.idPath: %w", configID, err)
		}
	}
	if namePath := strings.TrimSpace(models.NamePath); namePath != "" {
		if _, err := parseCustomPath(namePath); err != nil {
			return fmt.Errorf("custom protocol %q models.namePath: %w", configID, err)
		}
	}
	return nil
}

func validateCustomProtocolResponse(configID, location string, response CustomProtocolResponse, allowStream bool) error {
	effective, err := effectiveCustomProtocolResponse(location, response)
	if err != nil {
		return fmt.Errorf("custom protocol %q %s: %w", configID, location, err)
	}
	response = effective
	paths := map[string]string{
		"idPath": response.IDPath, "modelPath": response.ModelPath, "statusPath": response.StatusPath,
		"textPath": response.TextPath, "reasoningPath": response.ReasoningPath, "toolCallsPath": response.ToolCallsPath,
		"usagePath": response.UsagePath, "finishReasonPath": response.FinishReasonPath, "errorPath": response.ErrorPath,
	}
	for field, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := parseCustomPath(path); err != nil {
			return fmt.Errorf("custom protocol %q %s.%s: %w", configID, location, field, err)
		}
	}
	for index, match := range response.TextFilter {
		if err := validateCustomProtocolMatch(fmt.Sprintf("%s.textFilter[%d]", location, index), match); err != nil {
			return fmt.Errorf("custom protocol %q: %w", configID, err)
		}
	}
	for index, match := range response.ReasoningFilter {
		if err := validateCustomProtocolMatch(fmt.Sprintf("%s.reasoningFilter[%d]", location, index), match); err != nil {
			return fmt.Errorf("custom protocol %q: %w", configID, err)
		}
	}
	for key, path := range response.Mappings {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := parseCustomPath(path); err != nil {
			return fmt.Errorf("custom protocol %q %s.mappings[%q]: %w", configID, location, key, err)
		}
	}
	for index, mapping := range response.FieldMappings {
		target := strings.TrimPrefix(strings.TrimSpace(mapping.Target), "maheshvara.")
		if err := validateCustomResponseTarget(target); err != nil {
			return fmt.Errorf("custom protocol %q %s.fieldMappings[%d]: %w", configID, location, index, err)
		}
		if source := strings.TrimSpace(mapping.Source); source != "" {
			if _, err := parseCustomPath(source); err != nil {
				return fmt.Errorf("custom protocol %q %s.fieldMappings[%d].source: %w", configID, location, index, err)
			}
		}
		if strings.TrimSpace(mapping.Source) == "" && len(mapping.Value) == 0 && len(mapping.Default) == 0 {
			return fmt.Errorf("custom protocol %q %s.fieldMappings[%d] requires source, value, or default", configID, location, index)
		}
		if !isSupportedCustomMappingTransform(mapping.Transform) {
			return fmt.Errorf("custom protocol %q %s.fieldMappings[%d] uses unsupported transform %q", configID, location, index, mapping.Transform)
		}
	}

	stream := response.Stream
	if stream == nil {
		return nil
	}
	if !allowStream {
		return fmt.Errorf("custom protocol %q %s.stream cannot contain another stream mapping", configID, location)
	}
	mode := strings.ToLower(strings.TrimSpace(stream.Mode))
	if mode != "" && mode != "delta" && mode != "cumulative" {
		return fmt.Errorf("custom protocol %q %s.stream mode %q is unsupported", configID, location, stream.Mode)
	}
	if stream.Modes != nil {
		families := map[string]string{"text": stream.Modes.Text, "reasoning": stream.Modes.Reasoning, "arguments": stream.Modes.Arguments}
		for family, familyMode := range families {
			switch strings.ToLower(strings.TrimSpace(familyMode)) {
			case "", "delta", "cumulative":
			default:
				return fmt.Errorf("custom protocol %q %s.stream.modes.%s %q is unsupported", configID, location, family, familyMode)
			}
		}
	}
	if payloadPath := strings.TrimSpace(stream.PayloadPath); payloadPath != "" {
		if _, err := parseCustomPath(payloadPath); err != nil {
			return fmt.Errorf("custom protocol %q %s.stream.payloadPath: %w", configID, location, err)
		}
	}
	for _, eventName := range stream.Events {
		if strings.ContainsAny(eventName, "\r\n") {
			return fmt.Errorf("custom protocol %q %s.stream event contains a line break", configID, location)
		}
	}
	for _, key := range stream.EventKeys {
		if strings.ContainsAny(key, "\r\n") || strings.TrimSpace(key) == "" {
			return fmt.Errorf("custom protocol %q %s.stream.eventKeys entry is empty or contains a line break", configID, location)
		}
	}
	if stream.FinishWhen != nil {
		if err := validateCustomProtocolMatch(fmt.Sprintf("%s.stream.finishWhen", location), *stream.FinishWhen); err != nil {
			return fmt.Errorf("custom protocol %q: %w", configID, err)
		}
	}
	if stream.StatusWhen != nil {
		if err := validateCustomProtocolMatch(fmt.Sprintf("%s.stream.statusWhen", location), *stream.StatusWhen); err != nil {
			return fmt.Errorf("custom protocol %q: %w", configID, err)
		}
	}
	for index, done := range stream.Done {
		hasRaw := strings.TrimSpace(done.Raw) != ""
		hasJSON := len(done.JSON) > 0
		if hasRaw == hasJSON {
			return fmt.Errorf("custom protocol %q %s.stream.done[%d] requires exactly one of raw or json", configID, location, index)
		}
		if hasJSON {
			if _, ok := customMatchValue(done.JSON); !ok {
				return fmt.Errorf("custom protocol %q %s.stream.done[%d].json is not valid JSON", configID, location, index)
			}
		}
	}
	for _, doneValue := range stream.DoneValues {
		if strings.ContainsAny(doneValue, "\r\n") {
			return fmt.Errorf("custom protocol %q %s.stream done value contains a line break", configID, location)
		}
	}
	if err := validateStreamFrames(configID, location, stream.Frames); err != nil {
		return err
	}
	if stream.Response != nil {
		return validateCustomProtocolResponse(configID, location+".stream.response", *stream.Response, false)
	}
	return nil
}

// validateStreamFrames 校验流帧声明:事件名/谓词二选一、谓词与工具规则
// 合法、帧内 response 不得再嵌套流配置。
func validateStreamFrames(configID, location string, frames []CustomProtocolStreamFrame) error {
	for index, frame := range frames {
		if strings.TrimSpace(frame.Event) == "" && frame.Match == nil {
			return fmt.Errorf("custom protocol %q %s.stream.frames[%d] requires event or match", configID, location, index)
		}
		if strings.ContainsAny(frame.Event, "\r\n") {
			return fmt.Errorf("custom protocol %q %s.stream.frames[%d].event contains a line break", configID, location, index)
		}
		if frame.Match != nil {
			if err := validateCustomProtocolMatch(fmt.Sprintf("%s.stream.frames[%d].match", location, index), *frame.Match); err != nil {
				return fmt.Errorf("custom protocol %q: %w", configID, err)
			}
		}
		if frame.Tool != nil {
			if err := validateCustomProtocolStreamTool(configID, fmt.Sprintf("%s.stream.frames[%d].tool", location, index), frame.Tool); err != nil {
				return err
			}
		}
		if payloadPath := strings.TrimSpace(frame.PayloadPath); payloadPath != "" {
			if _, err := parseCustomPath(payloadPath); err != nil {
				return fmt.Errorf("custom protocol %q %s.stream.frames[%d].payloadPath: %w", configID, location, index, err)
			}
		}
		if frame.Response != nil {
			if err := validateCustomProtocolResponse(configID, fmt.Sprintf("%s.stream.frames[%d].response", location, index), *frame.Response, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateCustomProtocolStreamTool 校验帧级工具拼装规则：至少一条路径；路径
// 合法；argumentsMode 仅 delta/cumulative。
func validateCustomProtocolStreamTool(configID, location string, tool *CustomProtocolStreamTool) error {
	paths := map[string]string{
		"path": tool.Path, "idPath": tool.IDPath, "indexPath": tool.IndexPath, "namePath": tool.NamePath, "argumentsPath": tool.ArgumentsPath,
	}
	declared := 0
	for field, value := range paths {
		if strings.TrimSpace(value) == "" {
			continue
		}
		declared++
		if _, err := parseCustomPath(value); err != nil {
			return fmt.Errorf("custom protocol %q %s.%s: %w", configID, location, field, err)
		}
	}
	if declared == 0 {
		return fmt.Errorf("custom protocol %q %s requires at least one of idPath, indexPath, namePath, argumentsPath", configID, location)
	}
	switch mode := strings.ToLower(strings.TrimSpace(tool.ArgumentsMode)); mode {
	case "", "delta", "cumulative":
	default:
		return fmt.Errorf("custom protocol %q %s.argumentsMode %q is unsupported", configID, location, tool.ArgumentsMode)
	}
	return nil
}

// RenderCustomProtocolRequest 渲染外部传入的协议配置（预览/测试等非注册路径，
// 先整体校验）。注册表内的协议走 RenderRegisteredCustomProtocolRequest 免校验。
func RenderCustomProtocolRequest(req *MaheshvaraRequest, config CustomProtocolConfig) (*CustomProtocolRequestResult, error) {
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	return renderCustomProtocolRequest(req, config)
}

func renderCustomProtocolRequest(req *MaheshvaraRequest, config CustomProtocolConfig) (*CustomProtocolRequestResult, error) {
	template, omitIfEmpty, omitRules, err := config.Request.effectiveBodyTemplate()
	if err != nil {
		return nil, fmt.Errorf("custom protocol %q request.body: %w", config.ID, err)
	}
	return renderCustomProtocolRequestWithBody(req, config, template, omitIfEmpty, omitRules)
}

func renderCustomProtocolRequestWithBody(req *MaheshvaraRequest, config CustomProtocolConfig, template string, omitIfEmpty []string, omitRules []customOmitRule) (*CustomProtocolRequestResult, error) {
	if req == nil {
		return nil, fmt.Errorf("cannot render custom protocol request from nil Maheshvara request")
	}
	ctx := maheshvaraTemplateContext(req)
	if err := applyCustomProtocolShape(strings.ToLower(strings.TrimSpace(config.Request.Shape)), req, ctx); err != nil {
		return nil, fmt.Errorf("custom protocol %q request.shape: %w", config.ID, err)
	}
	var body []byte
	var err error
	if template != "" {
		body, err = renderCustomTemplate(template, ctx, omitIfEmpty, omitRules)
		if err != nil {
			return nil, fmt.Errorf("custom protocol %q request body: %w", config.ID, err)
		}
	}
	// 流式请求切换到 pathStream（Gemini :generateContent vs
	// :streamGenerateContent?alt=sse 这类按流切换动词的端点）。
	pathTemplate := config.Request.PathTemplate
	if req.Stream && strings.TrimSpace(config.Request.PathStream) != "" {
		pathTemplate = config.Request.PathStream
	}
	result := &CustomProtocolRequestResult{
		Method:      strings.ToUpper(strings.TrimSpace(config.Request.Method)),
		Path:        renderCustomString(pathTemplate, ctx),
		Headers:     make(map[string]string, len(config.Request.Headers)+1),
		Query:       make(map[string]string, len(config.Request.Query)),
		Body:        body,
		ContentType: firstNonEmptyString(strings.TrimSpace(config.Request.ContentType), "application/json"),
		Auth:        config.Request.Auth,
	}
	if result.Method == "" {
		result.Method = http.MethodPost
	}
	if strings.ContainsAny(result.Path, "\r\n") {
		return nil, fmt.Errorf("custom protocol %q rendered path contains a line break", config.ID)
	}
	result.Headers["Content-Type"] = result.ContentType
	for key, value := range config.Request.Headers {
		rendered := renderCustomString(value, ctx)
		if strings.ContainsAny(rendered, "\r\n") {
			return nil, fmt.Errorf("custom protocol %q rendered header %q contains a line break", config.ID, key)
		}
		result.Headers[key] = rendered
	}
	for key, value := range config.Request.Query {
		result.Query[key] = renderCustomString(value, ctx)
	}
	return result, nil
}

func RenderRegisteredCustomProtocolRequest(req *MaheshvaraRequest, id string) (*CustomProtocolRequestResult, error) {
	compiled, ok := getCompiledCustomProtocol(id)
	if !ok {
		return nil, fmt.Errorf("custom protocol %q is not registered", id)
	}
	// 入注册表时已整体校验并预编译请求体构造树,热路径不再重复。
	return renderCustomProtocolRequestWithBody(req, compiled.config, compiled.bodyTemplate, compiled.omitIfEmpty, compiled.omitRules)
}

// RenderCustomProtocolModelsRequest 构造模型列表发现请求。发现端点没有请求
// 上下文可渲染，占位符按空值处理；鉴权缺省继承 request.auth，可被 models.auth
// 覆盖（如转发走 bearer、拉取走 query 的双面供应商）。
func RenderCustomProtocolModelsRequest(config CustomProtocolConfig) (*CustomProtocolRequestResult, error) {
	models := config.Models
	if models == nil {
		return nil, fmt.Errorf("custom protocol %q does not define model discovery", config.ID)
	}
	method := strings.ToUpper(strings.TrimSpace(models.Method))
	if method == "" {
		method = http.MethodGet
	}
	auth := config.Request.Auth
	if models.Auth != nil {
		auth = *models.Auth
	}
	empty := map[string]any{"maheshvara": map[string]any{}, "request": map[string]any{}}
	result := &CustomProtocolRequestResult{
		Method:      method,
		Path:        renderCustomString(models.Path, empty),
		Headers:     make(map[string]string, len(models.Headers)+1),
		Query:       make(map[string]string, len(models.Query)),
		ContentType: "application/json",
		Auth:        auth,
	}
	for key, value := range models.Headers {
		result.Headers[key] = renderCustomString(value, empty)
	}
	for key, value := range models.Query {
		result.Query[key] = renderCustomString(value, empty)
	}
	if strings.ContainsAny(result.Path, "\r\n") {
		return nil, fmt.Errorf("custom protocol %q rendered models path contains a line break", config.ID)
	}
	return result, nil
}

// ParseCustomProtocolModels 解析模型列表响应：listPath 定位数组，idPath/
// namePath 在每个元素内取标识与展示名（缺省 id）。无 id 的元素跳过。
func ParseCustomProtocolModels(body []byte, config CustomProtocolConfig) ([]CustomProtocolModelInfo, error) {
	models := config.Models
	if models == nil {
		return nil, fmt.Errorf("custom protocol %q does not define model discovery", config.ID)
	}
	raw, err := decodeJSONUseNumber(body)
	if err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	list, ok := customLookupPath(raw, models.ListPath)
	if !ok {
		return nil, fmt.Errorf("models list path %q not found in response", models.ListPath)
	}
	items, ok := list.([]any)
	if !ok {
		return nil, fmt.Errorf("models list path %q does not point to an array", models.ListPath)
	}
	idPath := firstNonEmptyString(strings.TrimSpace(models.IDPath), "id")
	namePath := strings.TrimSpace(models.NamePath)
	result := make([]CustomProtocolModelInfo, 0, len(items))
	for _, item := range items {
		id := customStringAt(item, idPath)
		if id == "" {
			continue
		}
		info := CustomProtocolModelInfo{ID: id, Name: id}
		if namePath != "" {
			if name := customStringAt(item, namePath); name != "" {
				info.Name = name
			}
		}
		result = append(result, info)
	}
	return result, nil
}

func (a *OpenAIAdapter) SendCustomProtocolRequest(ctx context.Context, baseURL, apiKey string, request *CustomProtocolRequestResult, stream bool) (*http.Response, error) {
	if a == nil || request == nil {
		return nil, fmt.Errorf("custom protocol request is nil")
	}
	target := strings.TrimSpace(baseURL)
	if strings.TrimSpace(request.Path) != "" {
		if strings.Contains(request.Path, "://") {
			return nil, fmt.Errorf("custom protocol path must be relative to the configured base URL")
		}
		target = strings.TrimRight(target, "/") + "/" + strings.TrimLeft(request.Path, "/")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid custom protocol target URL: %w", err)
	}
	query := parsed.Query()
	for key, value := range request.Query {
		query.Set(key, value)
	}
	auth := request.Auth
	authMode := strings.ToLower(strings.TrimSpace(auth.Mode))
	if authMode == "" {
		authMode = "bearer"
	}
	if authMode == "query" && strings.TrimSpace(auth.Query) != "" && apiKey != "" {
		query.Set(auth.Query, apiKey)
	}
	if (authMode == "bearer" || authMode == "header") && strings.ContainsAny(apiKey, "\r\n") {
		return nil, fmt.Errorf("custom protocol API key contains a line break")
	}
	parsed.RawQuery = query.Encode()
	extraHeaders := cloneStringMap(request.Headers)
	if extraHeaders == nil {
		extraHeaders = map[string]string{}
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodPost
	}
	requestAPIKey := ""
	if authMode == "bearer" {
		requestAPIKey = apiKey
	}
	httpRequest, err := buildHTTPRequest(ctx, method, parsed.String(), requestAPIKey, request.Body, extraHeaders)
	if err != nil {
		return nil, err
	}
	if request.ContentType != "" {
		httpRequest.Header.Set("Content-Type", request.ContentType)
	}
	if authMode == "header" && apiKey != "" {
		header := firstNonEmptyString(auth.Header, "x-api-key")
		prefix := auth.Prefix
		httpRequest.Header.Set(header, prefix+apiKey)
	}
	if stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
		response, err := a.streamClient.Do(httpRequest)
		return response, sanitizeCustomTransportError(err)
	}
	response, err := a.client.Do(httpRequest)
	return response, sanitizeCustomTransportError(err)
}

// sanitizeCustomTransportError 剥离传输错误里的 URL 查询串再放行错误:
// query 鉴权的 API Key 会随 *url.Error 的文本形式流向下游客户端与日志
// （如 `Post "http://host/path?api_key=SECRET": EOF`），凭据不得离开网关。
func sanitizeCustomTransportError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) || urlErr.URL == "" {
		return err
	}
	parsed, parseErr := url.Parse(urlErr.URL)
	if parseErr != nil {
		return fmt.Errorf("%s <redacted url>: %w", urlErr.Op, urlErr.Err)
	}
	parsed.RawQuery = ""
	return fmt.Errorf("%s %q: %w", urlErr.Op, parsed.String(), urlErr.Err)
}

func CustomProtocolResponseToMaheshvara(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToMaheshvara(body, config, false)
}

// CustomProtocolResponseToMaheshvaraRegistered 映射已注册协议（入库时已整体校验）
// 的上游响应，转发热路径免每请求重复校验。
func CustomProtocolResponseToMaheshvaraRegistered(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToMaheshvaraValidated(body, config, false)
}

func customProtocolResponseToMaheshvara(body []byte, config CustomProtocolConfig, allowEmpty bool) (*MaheshvaraResponse, error) {
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	return customProtocolResponseToMaheshvaraValidated(body, config, allowEmpty)
}

func customProtocolResponseToMaheshvaraValidated(body []byte, config CustomProtocolConfig, allowEmpty bool) (*MaheshvaraResponse, error) {
	raw, err := decodeJSONUseNumber(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse custom protocol %q response: %w", config.ID, err)
	}
	resolved, err := resolveCustomMapping(config, allowEmpty)
	if err != nil {
		return nil, fmt.Errorf("custom protocol %q: %w", config.ID, err)
	}
	return customProtocolResponseFromRoot(raw, resolved, config.Aliases, config.ID, allowEmpty)
}

// customResolvedMapping 是一次性解析完成的运行时映射:payloadPath 为流事件的
// 载荷解包路径(非流式为空),mapping 为 legacy 合并后的生效映射。流解码器在
// 构造时对默认路径与每个帧各预编译一份,事件循环零编译。
type customResolvedMapping struct {
	payloadPath string
	mapping     CustomProtocolResponse
}

// resolveCustomMapping 解析运行时生效映射;须在整体校验通过后调用(编译
// 错误此时不可能出现,仍防御性返回)。
func resolveCustomMapping(config CustomProtocolConfig, allowStreamEvent bool) (customResolvedMapping, error) {
	resolved := customResolvedMapping{}
	if allowStreamEvent && config.Response.Stream != nil {
		resolved.payloadPath = strings.TrimSpace(config.Response.Stream.PayloadPath)
	}
	mapping, err := effectiveCustomProtocolRuntimeMapping(config, allowStreamEvent)
	if err != nil {
		return resolved, err
	}
	// legacy mappings 键在此合并为直接路径(仅填补空缺)。
	if mapping.Mappings != nil {
		mapping.IDPath = firstNonEmptyString(mapping.IDPath, mapping.Mappings["id"])
		mapping.ModelPath = firstNonEmptyString(mapping.ModelPath, mapping.Mappings["model"])
		mapping.StatusPath = firstNonEmptyString(mapping.StatusPath, mapping.Mappings["status"])
		mapping.TextPath = firstNonEmptyString(mapping.TextPath, mapping.Mappings["text"])
		mapping.ReasoningPath = firstNonEmptyString(mapping.ReasoningPath, mapping.Mappings["reasoning"])
		mapping.ToolCallsPath = firstNonEmptyString(mapping.ToolCallsPath, mapping.Mappings["tool_calls"])
		mapping.UsagePath = firstNonEmptyString(mapping.UsagePath, mapping.Mappings["usage"])
		mapping.FinishReasonPath = firstNonEmptyString(mapping.FinishReasonPath, mapping.Mappings["finish_reason"])
		mapping.ErrorPath = firstNonEmptyString(mapping.ErrorPath, mapping.Mappings["error"])
	}
	resolved.mapping = mapping
	return resolved, nil
}

// customProtocolResponseFromRoot 把已解析的载荷按预解析映射转为 Maheshvara。
func customProtocolResponseFromRoot(root any, resolved customResolvedMapping, aliases *CustomProtocolAliases, configID string, allowEmpty bool) (*MaheshvaraResponse, error) {
	if resolved.payloadPath != "" {
		if payload, ok := customLookupPath(root, resolved.payloadPath); ok {
			root = payload
		}
	}
	mapping := resolved.mapping
	var textKeys []string
	var usageAliases, toolAliases map[string][]string
	if aliases != nil {
		textKeys = aliases.TextKeys
		usageAliases = aliases.Usage
		toolAliases = aliases.ToolCall
	}
	response := &MaheshvaraResponse{
		ID:         customStringAt(root, mapping.IDPath),
		Model:      customStringAt(root, mapping.ModelPath),
		Status:     customStringAt(root, mapping.StatusPath),
		CreatedAt:  timeNowUnix(),
		StopReason: customStringAt(root, mapping.FinishReasonPath),
	}
	if response.Status == "" {
		if allowEmpty {
			response.Status = MaheshvaraStatusInProgress
		} else {
			response.Status = "completed"
		}
	}
	if mapping.ErrorPath != "" {
		if value := customValueAt(root, mapping.ErrorPath); value != nil {
			response.Error = &MaheshvaraError{Message: customValueString(value), Class: ErrorClassUpstream, Raw: customMap(value)}
		}
	}
	if text := customTextAtFilter(root, mapping.TextPath, textKeys, mapping.TextFilter); text != "" {
		response.Output = append(response.Output, MaheshvaraOutputItem{
			ID: newMaheshvaraResponseID("msg"), Type: MaheshvaraOutputMessage, Status: MaheshvaraStatusCompleted, Role: "assistant",
			Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: text}},
		})
	}
	if reasoning := customTextAtFilter(root, mapping.ReasoningPath, textKeys, mapping.ReasoningFilter); reasoning != "" {
		response.Output = append(response.Output, MaheshvaraOutputItem{
			ID: newMaheshvaraResponseID("rs"), Type: MaheshvaraOutputReasoning, Status: MaheshvaraStatusCompleted,
			Content: []MaheshvaraContentPart{{Type: MaheshvaraContentReasoning, Text: reasoning, ReasoningText: reasoning}},
		})
	}
	if mapping.ToolCallsPath != "" {
		for index, item := range customArrayAt(root, mapping.ToolCallsPath) {
			call := customToolCallWithAliases(item, index, toolAliases)
			if call.Name == "" {
				continue
			}
			response.Output = append(response.Output, MaheshvaraOutputItem{
				ID: firstNonEmptyString(call.ID, newMaheshvaraResponseID("call")), Type: MaheshvaraOutputFunctionCall,
				Status: MaheshvaraStatusCompleted, CallID: call.ID, Name: call.Name, Arguments: call.Arguments,
			})
		}
	}
	if mapping.UsagePath != "" {
		response.Usage = customUsageAtWithAliases(root, mapping.UsagePath, usageAliases)
	}
	var mappingErr error
	response, mappingErr = applyCustomFieldMappings(response, root, mapping.FieldMappings)
	if mappingErr != nil {
		return nil, fmt.Errorf("custom protocol %q field mapping: %w", configID, mappingErr)
	}
	if len(response.Output) == 0 && response.Error == nil && !allowEmpty {
		return nil, fmt.Errorf("custom protocol %q response has no mapped text, reasoning, or tool call", configID)
	}
	return response, nil
}

func maheshvaraTemplateContext(req *MaheshvaraRequest) map[string]any {
	encoded, _ := json.Marshal(req)
	var value map[string]any
	// UseNumber:数字以 json.Number 进入上下文,避免 >2^53 的整数(如 seed)
	// 经 float64 中转丢精度、>=1e21 被改写成科学计数法文本。
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	if value == nil {
		value = map[string]any{}
	}
	if len(req.RawExtra) > 0 {
		extra := make(map[string]any, len(req.RawExtra))
		for key, raw := range req.RawExtra {
			extra[key] = jsonRawToAny(raw)
		}
		value["raw_extra"] = extra
		value["extra"] = extra
	}
	return map[string]any{
		"maheshvara": value,
		"request":    value,
	}
}

// applyCustomProtocolShape 把模板上下文中的 messages/tools（responses 另含
// input/input_items）替换为对应线制形状，复用内置四协议的整形器——自定义
// 协议作者不再需要手写消息/工具的字段级转换。
func applyCustomProtocolShape(shape string, req *MaheshvaraRequest, context map[string]any) error {
	if shape == "" {
		return nil
	}
	root, _ := context["maheshvara"].(map[string]any)
	if root == nil {
		return nil
	}
	redecodeWithJSONNumbers := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			return value
		}
		decoded, decodeErr := decodeJSONUseNumber(encoded)
		if decodeErr != nil {
			return value
		}
		return decoded
	}
	setTools := func(tools []map[string]any, err error) error {
		if err != nil {
			return err
		}
		if len(tools) > 0 {
			root["tools"] = redecodeWithJSONNumbers(tools)
		}
		return nil
	}
	switch shape {
	case "openai-chat":
		root["messages"] = redecodeWithJSONNumbers(maheshvaraMessagesToOpenAI(req))
		return setTools(maheshvaraToolsToOpenAI(req.Tools))
	case "anthropic":
		messages, err := maheshvaraMessagesToClaude(req)
		if err != nil {
			return err
		}
		root["messages"] = redecodeWithJSONNumbers(messages)
		// tool_choice 同步转为目标形状(公共形状 "required" 等 Claude 不识别)。
		if converted := applyClaudeDisableParallelToolUse(maheshvaraToolChoiceToClaude(req.ToolChoice), req.ParallelToolCalls); converted != nil {
			root["tool_choice"] = redecodeWithJSONNumbers(converted)
		}
		return setTools(maheshvaraToolsToClaude(req.Tools))
	case "gemini":
		messages, err := maheshvaraMessagesToGemini(req)
		if err != nil {
			return err
		}
		root["messages"] = redecodeWithJSONNumbers(messages)
		return setTools(maheshvaraToolsToGemini(req.Tools))
	case "responses":
		input := redecodeWithJSONNumbers(maheshvaraInputToResponses(req))
		root["input"] = input
		root["input_items"] = input
		root["messages"] = input
		if tools := maheshvaraToolsToResponses(req.Tools); len(tools) > 0 {
			root["tools"] = redecodeWithJSONNumbers(tools)
		}
		return nil
	}
	return fmt.Errorf("%q is unsupported", shape)
}

// forEachCustomPlaceholder 遍历模板中的全部 {{...}} 占位符,回调收到去空白后的
// 表达式原文。渲染/取串/校验三条扫描路径共用此骨架;回调返回错误立即中止。
func forEachCustomPlaceholder(template string, fn func(expression string) error) error {
	placeholders := 0
	for offset := 0; offset < len(template); {
		start := strings.Index(template[offset:], "{{")
		if start < 0 {
			return nil
		}
		start += offset
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			return fmt.Errorf("unterminated placeholder at byte %d", start)
		}
		end += start + 2
		placeholders++
		if placeholders > customProtocolMaxPlaceholders {
			return fmt.Errorf("template contains more than %d placeholders", customProtocolMaxPlaceholders)
		}
		if err := fn(strings.TrimSpace(template[start+2 : end])); err != nil {
			return err
		}
		offset = end + 2
	}
	return nil
}

func renderCustomTemplate(template string, context map[string]any, omitIfEmpty []string, omitRules []customOmitRule) ([]byte, error) {
	if len(template) > customProtocolMaxTemplateBytes {
		return nil, fmt.Errorf("template exceeds %d bytes", customProtocolMaxTemplateBytes)
	}
	value, err := renderCustomJSON(template, context)
	if err != nil {
		return nil, err
	}
	// 空值/条件省略统一在渲染结果上求值并删除：先收集全部命中（判定阶段
	// 不改树），再按路径降序执行——同父数组先删高下标，防止删除位移导致
	// 后续下标越界漏删。omitIf 对渲染后的最终值比较（default/过滤器生效后），
	// when 仍对请求上下文求值。
	deletions := make([]string, 0, len(omitIfEmpty)+len(omitRules))
	for _, path := range omitIfEmpty {
		trimmed := normalizeMaheshvaraPath(path)
		if resolved, found := customLookupPath(value, trimmed); found && customEmptyValue(resolved) {
			deletions = append(deletions, trimmed)
		}
	}
	for _, rule := range omitRules {
		hit := rule.When != nil && !customMatchEval(context, *rule.When)
		if !hit && len(rule.OmitIf) > 0 {
			if expected, ok := customMatchValue(rule.OmitIf); ok {
				if resolved, found := customLookupPath(value, rule.Path); found && customJSONValuesEqual(resolved, expected) {
					hit = true
				}
			}
		}
		if hit {
			deletions = append(deletions, rule.Path)
		}
	}
	sort.Strings(deletions)
	for index := len(deletions) - 1; index >= 0; index-- {
		value = deleteCustomPathForce(value, deletions[index])
	}
	if err := validateCustomJSONDepth(value, 0); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func renderCustomJSON(template string, context map[string]any) (any, error) {
	var builder strings.Builder
	placeholders := 0
	for offset := 0; offset < len(template); {
		start := strings.Index(template[offset:], "{{")
		if start < 0 {
			builder.WriteString(template[offset:])
			break
		}
		start += offset
		builder.WriteString(template[offset:start])
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("unterminated placeholder at byte %d", start)
		}
		end += start + 2
		placeholders++
		if placeholders > customProtocolMaxPlaceholders {
			return nil, fmt.Errorf("template contains more than %d placeholders", customProtocolMaxPlaceholders)
		}
		expression := strings.TrimSpace(template[start+2 : end])
		path, defaultValue, forceJSON, filter, err := parseCustomExpression(expression)
		if err != nil {
			return nil, err
		}
		resolved, ok := customLookupPath(context, path)
		if !ok || customEmptyValue(resolved) {
			resolved = defaultValue
		}
		if resolved, err = applyCustomTemplateFilter(resolved, filter); err != nil {
			return nil, fmt.Errorf("placeholder %q: %w", expression, err)
		}
		prefix := template[:start]
		suffix := template[end+2:]
		quoted := len(prefix) > 0 && prefix[len(prefix)-1] == '"' && len(suffix) > 0 && suffix[0] == '"'
		// 引号包裹且未显式 |json：值按字符串转义嵌入；其余（含无引号占位与
		// |json 显式声明）一律按 JSON 值嵌入，保证模板整体仍是合法 JSON。
		if quoted && !forceJSON {
			builder.WriteString(escapeJSONString(customValueString(resolved)))
		} else {
			encoded, marshalErr := json.Marshal(resolved)
			if marshalErr != nil {
				return nil, fmt.Errorf("placeholder %q: %w", expression, marshalErr)
			}
			builder.Write(encoded)
		}
		offset = end + 2
	}
	value, err := decodeJSONUseNumber([]byte(builder.String()))
	if err != nil {
		return nil, fmt.Errorf("rendered body is not valid JSON: %w", err)
	}
	return value, nil
}

func parseCustomExpression(expression string) (string, any, bool, string, error) {
	parts := strings.Split(expression, "|")
	path := strings.TrimSpace(parts[0])
	if path == "" {
		return "", nil, false, "", fmt.Errorf("empty template path")
	}
	var defaultValue any
	forceJSON := false
	filter := ""
	for _, rawOption := range parts[1:] {
		option := strings.TrimSpace(rawOption)
		switch {
		// |json：显式声明占位符按 JSON 值嵌入（无引号占位默认即如此；
		// 带引号模板配 |json 则跳出字符串转义路径）。
		case option == "json":
			forceJSON = true
		// |bool |int |string：解析后的值做类型收敛（nil 不动）。
		case option == "bool" || option == "int" || option == "string":
			filter = option
		case strings.HasPrefix(option, "default:"):
			literal := strings.TrimSpace(strings.TrimPrefix(option, "default:"))
			if literal == "" {
				defaultValue = ""
				continue
			}
			if err := json.Unmarshal([]byte(literal), &defaultValue); err != nil {
				defaultValue = strings.Trim(strings.Trim(literal, "\""), "'")
			}
		default:
			return "", nil, false, "", fmt.Errorf("unsupported template option %q", option)
		}
	}
	return path, defaultValue, forceJSON, filter, nil
}

// applyCustomTemplateFilter 对解析出的占位符值做类型收敛；nil 与转换失败
// 保持原值（空上下文校验时占位符缺失不应因过滤器报错）。
func applyCustomTemplateFilter(value any, filter string) (any, error) {
	if value == nil || filter == "" {
		return value, nil
	}
	switch filter {
	case "bool":
		if _, ok := value.(bool); ok {
			return value, nil
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(customValueString(value)))
		if err != nil {
			return value, nil
		}
		return parsed, nil
	case "int":
		if number, ok := numberValue(value); ok {
			return json.Number(strconv.Itoa(int(number))), nil
		}
		return value, nil
	case "string":
		return customValueString(value), nil
	}
	return value, nil
}

func renderCustomString(template string, context map[string]any) string {
	if strings.TrimSpace(template) == "" {
		return ""
	}
	var builder strings.Builder
	for offset := 0; offset < len(template); {
		start := strings.Index(template[offset:], "{{")
		if start < 0 {
			builder.WriteString(template[offset:])
			break
		}
		start += offset
		builder.WriteString(template[offset:start])
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			builder.WriteString(template[start:])
			break
		}
		end += start + 2
		path, defaultValue, _, filter, err := parseCustomExpression(strings.TrimSpace(template[start+2 : end]))
		if err != nil {
			builder.WriteString(template[start : end+2])
			offset = end + 2
			continue
		}
		resolved, ok := customLookupPath(context, path)
		if !ok || customEmptyValue(resolved) {
			resolved = defaultValue
		}
		if resolved, err = applyCustomTemplateFilter(resolved, filter); err == nil {
			builder.WriteString(customValueString(resolved))
		} else {
			builder.WriteString(customValueString(defaultValue))
		}
		offset = end + 2
	}
	return builder.String()
}

func validateCustomStringTemplate(template string) error {
	return forEachCustomPlaceholder(template, func(expression string) error {
		_, _, _, _, err := parseCustomExpression(expression)
		return err
	})
}

func customValueAt(root any, path string) any {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	value, _ := customLookupPath(root, path)
	return value
}

func customStringAt(root any, path string) string {
	return customValueString(customValueAt(root, path))
}

// customAliasKeys 解析某类别的生效键列表:声明了非空覆盖即整体替换默认表
// （usage/toolCall 提取共用）。
func customAliasKeys(aliases map[string][]string, category string, defaults ...string) []string {
	if custom, ok := aliases[category]; ok && len(custom) > 0 {
		return custom
	}
	return defaults
}

// customTextAtFilter 提取文本并可按元素过滤：textPath 指向对象数组时先按
// filter（单条件或 AND 条件集）过滤元素（如仅保留 type=="text" 的块），
// 再按 keys 提取。
func customTextAtFilter(root any, path string, keys []string, filter CustomProtocolMatchSet) string {
	value := customValueAt(root, path)
	if len(filter) > 0 {
		if array, ok := value.([]any); ok {
			kept := make([]any, 0, len(array))
			for _, item := range array {
				if filter.evalAll(item) {
					kept = append(kept, item)
				}
			}
			value = kept
		}
	}
	return customTextValueWithKeys(value, keys)
}

func customArrayAt(root any, path string) []any {
	value := customValueAt(root, path)
	if array, ok := value.([]any); ok {
		return array
	}
	if value != nil {
		return []any{value}
	}
	return nil
}

// customToolCallWithAliases 按别名表读取工具调用字段；别名条目支持点路径
// （如 function.arguments）。类别缺省时用内置默认表。
func customToolCallWithAliases(value any, index int, aliases map[string][]string) MaheshvaraToolCall {
	object, _ := value.(map[string]any)
	if object == nil {
		return MaheshvaraToolCall{}
	}
	lookupString := func(keys []string) string {
		for _, key := range keys {
			if value, ok := customLookupPath(object, key); ok {
				if text := stringValue(value); text != "" {
					return text
				}
			}
		}
		return ""
	}
	call := MaheshvaraToolCall{
		ID:   firstNonEmptyString(lookupString(customAliasKeys(aliases, "id", "id", "call_id", "tool_call_id", "function.id")), fmt.Sprintf("call_%d", index)),
		Name: lookupString(customAliasKeys(aliases, "name", "name", "function_name", "function.name")),
		Type: MaheshvaraToolFunction,
	}
	var arguments any
	for _, key := range customAliasKeys(aliases, "arguments", "arguments", "args", "input", "function.arguments", "function.args", "function.input") {
		if value, ok := customLookupPath(object, key); ok && value != nil {
			arguments = value
			break
		}
	}
	if text, ok := arguments.(string); ok {
		call.Arguments = json.RawMessage(text)
		if !json.Valid(call.Arguments) {
			call.Arguments = json.RawMessage(strconv.Quote(text))
		}
	} else if arguments != nil {
		call.Arguments, _ = json.Marshal(arguments)
	}
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage(`{}`)
	}
	return call
}

// customUsageAtWithAliases 按别名表读取用量；别名条目支持点路径（如
// prompt_tokens_details.cached_tokens）。类别缺省时用内置默认表。
func customUsageAtWithAliases(root any, path string, aliases map[string][]string) *MaheshvaraUsage {
	object, _ := customValueAt(root, path).(map[string]any)
	if object == nil {
		return nil
	}
	usage := &MaheshvaraUsage{Source: "provider_response"}
	usage.InputTokens = customIntPath(object, customAliasKeys(aliases, "input", "input_tokens", "inputTokens", "prompt_tokens", "promptTokenCount")...)
	usage.OutputTokens = customIntPath(object, customAliasKeys(aliases, "output", "output_tokens", "outputTokens", "completion_tokens", "candidatesTokenCount")...)
	usage.TotalTokens = customIntPath(object, customAliasKeys(aliases, "total", "total_tokens", "totalTokens", "totalTokenCount")...)
	usage.CachedInputTokens = customIntPath(object, customAliasKeys(aliases, "cached", "cached_input_tokens", "cachedInputTokens", "cached_tokens", "cachedContentTokenCount", "prompt_tokens_details.cached_tokens", "input_tokens_details.cached_tokens", "cache_read_tokens")...)
	usage.ReasoningTokens = customIntPath(object, customAliasKeys(aliases, "reasoning", "reasoning_tokens", "reasoningTokens", "thoughtsTokenCount", "completion_tokens_details.reasoning_tokens")...)
	usage.TotalTokens = valueOrSum(usage.TotalTokens, usage.InputTokens, usage.OutputTokens)
	return usage
}

// customIntPath 按点路径键列表取第一个存在的数值（与 customInt 同语义，
// 支持别名条目里的嵌套路径）。合法 JSON Number 可能带小数尾缀/科学计数
// （Java/Python 服务常见 187.0 / 1e3）：Int64 失败回落 Float64 取整。
func customIntPath(object map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := customLookupPath(object, key)
		if !ok || value == nil {
			continue
		}
		if number, ok := value.(json.Number); ok {
			if converted, err := number.Int64(); err == nil {
				return int(converted)
			}
			if f, err := number.Float64(); err == nil {
				return int(f)
			}
			continue
		}
		if number, ok := numberValue(value); ok {
			return int(number)
		}
	}
	return 0
}

func customValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}

func customMap(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func customEmptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func validateCustomJSONDepth(value any, depth int) error {
	if depth > customProtocolMaxDepth {
		return fmt.Errorf("rendered JSON exceeds maximum depth %d", customProtocolMaxDepth)
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if err := validateCustomJSONDepth(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range typed {
			if err := validateCustomJSONDepth(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneCustomProtocol(config CustomProtocolConfig) CustomProtocolConfig {
	clone := config
	clone.Request.Body = append(json.RawMessage(nil), config.Request.Body...)
	clone.Request.Headers = cloneStringMap(config.Request.Headers)
	clone.Request.Query = cloneStringMap(config.Request.Query)
	clone.Request.OmitIfEmpty = append([]string(nil), config.Request.OmitIfEmpty...)
	clone.Response.Mappings = cloneStringMap(config.Response.Mappings)
	clone.Response.FieldMappings = cloneCustomFieldMappings(config.Response.FieldMappings)
	clone.Response.Fields = append([]CustomProtocolResponseFieldMapping(nil), config.Response.Fields...)
	clone.Response.Sample = append(json.RawMessage(nil), config.Response.Sample...)
	clone.Response.Body = append(json.RawMessage(nil), config.Response.Body...)
	clone.Response.Stream = cloneCustomStreamMapping(config.Response.Stream)
	if config.Metadata != nil {
		clone.Metadata = make(map[string]any, len(config.Metadata))
		for key, value := range config.Metadata {
			clone.Metadata[key] = value
		}
	}
	return clone
}

func cloneCustomFieldMappings(input []CustomProtocolFieldMapping) []CustomProtocolFieldMapping {
	if input == nil {
		return nil
	}
	output := make([]CustomProtocolFieldMapping, len(input))
	for index, mapping := range input {
		output[index] = mapping
		output[index].Value = append(json.RawMessage(nil), mapping.Value...)
		output[index].Default = append(json.RawMessage(nil), mapping.Default...)
	}
	return output
}

func cloneCustomStreamMapping(input *CustomProtocolStreamMapping) *CustomProtocolStreamMapping {
	if input == nil {
		return nil
	}
	output := *input
	output.DoneValues = append([]string(nil), input.DoneValues...)
	output.Events = append([]string(nil), input.Events...)
	if input.Response != nil {
		response := cloneCustomProtocol(CustomProtocolConfig{Response: *input.Response}).Response
		output.Response = &response
	}
	return &output
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func escapeJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return strings.Trim(string(encoded), "\"")
}

func timeNowUnix() int64 { return time.Now().Unix() }
