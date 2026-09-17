package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	Metadata map[string]any         `json:"metadata,omitempty"`
}

// CustomProtocolModels 声明该协议的模型列表发现端点：配置后 custom:<id> 模型源
// 可开启自动拉取。请求构造与鉴权注入复用自定义协议管线，响应侧以 listPath
// 定位模型数组，idPath/namePath 在每个元素内取标识与展示名。
type CustomProtocolModels struct {
	Method   string              `json:"method,omitempty"` // 默认 GET，仅 GET/POST
	Path     string              `json:"path"`             // 必填，相对源 baseUrl
	Headers  map[string]string   `json:"headers,omitempty"`
	Query    map[string]string   `json:"query,omitempty"`
	Auth     *CustomProtocolAuth `json:"auth,omitempty"` // 缺省复用 request.auth
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
	Method       string             `json:"method,omitempty"`
	PathTemplate string             `json:"path,omitempty"`
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
	Body             json.RawMessage                      `json:"body,omitempty"`
	IDPath           string                               `json:"idPath,omitempty"`
	ModelPath        string                               `json:"modelPath,omitempty"`
	StatusPath       string                               `json:"statusPath,omitempty"`
	TextPath         string                               `json:"textPath,omitempty"`
	ReasoningPath    string                               `json:"reasoningPath,omitempty"`
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
	PayloadPath string                      `json:"payloadPath,omitempty"`
	Mode        string                      `json:"mode,omitempty"`
	DoneValues  []string                    `json:"doneValues,omitempty"`
	Events      []string                    `json:"events,omitempty"`
	Frames      []CustomProtocolStreamFrame `json:"frames,omitempty"`
	Response    *CustomProtocolResponse     `json:"response,omitempty"`
}

// CustomProtocolStreamFrame 是异构流的一类帧的映射规则：按事件名（SSE event
// 字段，缺省时取 JSON type/event 字段）匹配，命中后以 frame.response 映射该
// 帧（缺省回退流级默认映射）。terminal=true 时命中即判定流终态，覆盖
// response.completed / message_stop 这类「以事件名收尾」的协议。frames 存在
// 时优先于 legacy events 白名单；未匹配任何帧的事件跳过（需要兜底映射时用
// stream.response 声明）。
type CustomProtocolStreamFrame struct {
	Event       string                  `json:"event"`
	PayloadPath string                  `json:"payloadPath,omitempty"`
	Response    *CustomProtocolResponse `json:"response,omitempty"`
	Terminal    bool                    `json:"terminal,omitempty"`
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
func (request CustomProtocolRequest) effectiveBodyTemplate() (string, []string, error) {
	if !hasCustomProtocolAnnotationBody(request.Body) {
		return request.bodyTemplate(), request.OmitIfEmpty, nil
	}
	compiled, err := compileCustomProtocolBody(request.Body)
	if err != nil {
		return "", nil, err
	}
	omit := append([]string(nil), request.OmitIfEmpty...)
	omit = append(omit, compiled.OmitIfEmpty...)
	return compiled.Template, omit, nil
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

var customProtocolRegistry = struct {
	sync.RWMutex
	items map[string]CustomProtocolConfig
}{items: make(map[string]CustomProtocolConfig)}

// RegisterCustomProtocol validates and atomically installs a custom protocol.
func RegisterCustomProtocol(config CustomProtocolConfig) error {
	if err := ValidateCustomProtocol(config); err != nil {
		return err
	}
	config.Type = NormalizeCustomProtocolType(config.Type)
	customProtocolRegistry.Lock()
	customProtocolRegistry.items[strings.ToLower(strings.TrimSpace(config.ID))] = cloneCustomProtocol(config)
	customProtocolRegistry.Unlock()
	return nil
}

// ReplaceCustomProtocols validates the complete set and swaps it atomically.
// A failed reload leaves the previously registered protocols untouched.
func ReplaceCustomProtocols(configs []CustomProtocolConfig) error {
	next := make(map[string]CustomProtocolConfig, len(configs))
	for _, config := range configs {
		if err := ValidateCustomProtocol(config); err != nil {
			return err
		}
		id := strings.ToLower(strings.TrimSpace(config.ID))
		if _, exists := next[id]; exists {
			return fmt.Errorf("custom protocol %q is duplicated", config.ID)
		}
		config.Type = NormalizeCustomProtocolType(config.Type)
		next[id] = cloneCustomProtocol(config)
	}
	customProtocolRegistry.Lock()
	customProtocolRegistry.items = next
	customProtocolRegistry.Unlock()
	return nil
}

func GetCustomProtocol(id string) (CustomProtocolConfig, bool) {
	customProtocolRegistry.RLock()
	config, ok := customProtocolRegistry.items[strings.ToLower(strings.TrimSpace(id))]
	customProtocolRegistry.RUnlock()
	if !ok {
		return CustomProtocolConfig{}, false
	}
	return cloneCustomProtocol(config), true
}

func ClearCustomProtocols() {
	customProtocolRegistry.Lock()
	customProtocolRegistry.items = make(map[string]CustomProtocolConfig)
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
	template, omitIfEmpty, err := config.Request.effectiveBodyTemplate()
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
		if _, err := renderCustomTemplate(template, maheshvaraTemplateContext(&MaheshvaraRequest{}), nil); err != nil {
			return fmt.Errorf("custom protocol %q has invalid body template: %w", config.ID, err)
		}
		// 校验二(非空示例值):空值以字符串嵌入时仍是合法 JSON 字符串,会
		// 放过「引号内占位符+其它文本」的缺陷模板(运行时非空字符串裸嵌
		// 进字符串字面量 → rendered body is not valid JSON,每请求必炸)。
		sample := maheshvaraTemplateContext(&MaheshvaraRequest{
			Model: "x", Instructions: "x", Stream: true,
			Messages: []MaheshvaraMessage{{Role: "user", Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: "x"}}}},
		})
		if _, err := renderCustomTemplate(template, sample, nil); err != nil {
			return fmt.Errorf("custom protocol %q body template fails with non-empty sample values (quoted placeholder mixed with literal text?): %w", config.ID, err)
		}
	}
	for _, path := range omitIfEmpty {
		if _, err := parseCustomPath(strings.TrimPrefix(strings.TrimSpace(path), "maheshvara.")); err != nil {
			return fmt.Errorf("custom protocol %q omitIfEmpty path %q: %w", config.ID, path, err)
		}
	}
	if err := validateCustomStringTemplate(config.Request.PathTemplate); err != nil {
		return fmt.Errorf("custom protocol %q request.path: %w", config.ID, err)
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
	return validateCustomProtocolResponse(config.ID, "response", config.Response, true)
}

// validateCustomHeaders 校验一处自定义协议头的模板语法与保护头规则
// (request.headers 与 models.headers 共用)。
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
	for _, doneValue := range stream.DoneValues {
		if strings.ContainsAny(doneValue, "\r\n") {
			return fmt.Errorf("custom protocol %q %s.stream done value contains a line break", configID, location)
		}
	}
	for index, frame := range stream.Frames {
		if strings.TrimSpace(frame.Event) == "" {
			return fmt.Errorf("custom protocol %q %s.stream.frames[%d].event is required", configID, location, index)
		}
		if strings.ContainsAny(frame.Event, "\r\n") {
			return fmt.Errorf("custom protocol %q %s.stream.frames[%d].event contains a line break", configID, location, index)
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
	if stream.Response != nil {
		return validateCustomProtocolResponse(configID, location+".stream.response", *stream.Response, false)
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
	if req == nil {
		return nil, fmt.Errorf("cannot render custom protocol request from nil Maheshvara request")
	}
	ctx := maheshvaraTemplateContext(req)
	var body []byte
	template, omitIfEmpty, err := config.Request.effectiveBodyTemplate()
	if err != nil {
		return nil, fmt.Errorf("custom protocol %q request.body: %w", config.ID, err)
	}
	if template != "" {
		body, err = renderCustomTemplate(template, ctx, omitIfEmpty)
		if err != nil {
			return nil, fmt.Errorf("custom protocol %q request body: %w", config.ID, err)
		}
	}
	result := &CustomProtocolRequestResult{
		Method:      strings.ToUpper(strings.TrimSpace(config.Request.Method)),
		Path:        renderCustomString(config.Request.PathTemplate, ctx),
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
	config, ok := GetCustomProtocol(id)
	if !ok {
		return nil, fmt.Errorf("custom protocol %q is not registered", id)
	}
	// 入注册表时已整体校验（校验含模板空渲染，代价不低），热路径不再重复。
	return renderCustomProtocolRequest(req, config)
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
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
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
		return a.streamClient.Do(httpRequest)
	}
	return a.client.Do(httpRequest)
}

func CustomProtocolResponseToMaheshvara(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToMaheshvara(body, config, false)
}

// CustomProtocolResponseToMaheshvaraRegistered 映射已注册协议（入库时已整体校验）
// 的上游响应，转发热路径免每请求重复校验。
func CustomProtocolResponseToMaheshvaraRegistered(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToMaheshvaraValidated(body, config, false)
}

// CustomProtocolStreamEventToMaheshvara applies the same response mapping to a
// single streaming event. Empty events are valid and are represented by an
// otherwise empty Maheshvara response so callers can continue scanning until a
// later event carries text, a tool call, usage, or a finish reason.

func customProtocolStreamEventToMaheshvaraValidated(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToMaheshvaraValidated(body, config, true)
}

func customProtocolResponseToMaheshvara(body []byte, config CustomProtocolConfig, allowEmpty bool) (*MaheshvaraResponse, error) {
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	return customProtocolResponseToMaheshvaraValidated(body, config, allowEmpty)
}

func customProtocolResponseToMaheshvaraValidated(body []byte, config CustomProtocolConfig, allowEmpty bool) (*MaheshvaraResponse, error) {
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse custom protocol %q response: %w", config.ID, err)
	}
	if allowEmpty && config.Response.Stream != nil {
		if payloadPath := strings.TrimSpace(config.Response.Stream.PayloadPath); payloadPath != "" {
			if payload, ok := customLookupPath(raw, payloadPath); ok {
				raw = payload
			}
		}
	}
	mapping, err := effectiveCustomProtocolRuntimeMapping(config, allowEmpty)
	if err != nil {
		return nil, fmt.Errorf("custom protocol %q: %w", config.ID, err)
	}
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
	response := &MaheshvaraResponse{
		ID:         customStringAt(raw, mapping.IDPath),
		Model:      customStringAt(raw, mapping.ModelPath),
		Status:     customStringAt(raw, mapping.StatusPath),
		CreatedAt:  timeNowUnix(),
		StopReason: customStringAt(raw, mapping.FinishReasonPath),
	}
	if response.Status == "" {
		if allowEmpty {
			response.Status = "in_progress"
		} else {
			response.Status = "completed"
		}
	}
	if mapping.ErrorPath != "" {
		if value := customValueAt(raw, mapping.ErrorPath); value != nil {
			response.Error = &MaheshvaraError{Message: customValueString(value), Class: ErrorClassUpstream, Raw: customMap(value)}
		}
	}
	if text := customTextAt(raw, mapping.TextPath); text != "" {
		response.Output = append(response.Output, MaheshvaraOutputItem{
			ID: newMaheshvaraResponseID("msg"), Type: MaheshvaraOutputMessage, Status: "completed", Role: "assistant",
			Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: text}},
		})
	}
	if reasoning := customTextAt(raw, mapping.ReasoningPath); reasoning != "" {
		response.Output = append(response.Output, MaheshvaraOutputItem{
			ID: newMaheshvaraResponseID("rs"), Type: MaheshvaraOutputReasoning, Status: "completed",
			Content: []MaheshvaraContentPart{{Type: MaheshvaraContentReasoning, Text: reasoning, ReasoningText: reasoning}},
		})
	}
	if mapping.ToolCallsPath != "" {
		for index, item := range customArrayAt(raw, mapping.ToolCallsPath) {
			call := customToolCall(item, index)
			if call.Name == "" {
				continue
			}
			response.Output = append(response.Output, MaheshvaraOutputItem{
				ID: firstNonEmptyString(call.ID, newMaheshvaraResponseID("call")), Type: MaheshvaraOutputFunctionCall,
				Status: "completed", CallID: call.ID, Name: call.Name, Arguments: call.Arguments,
			})
		}
	}
	if mapping.UsagePath != "" {
		response.Usage = customUsageAt(raw, mapping.UsagePath)
	}
	var mappingErr error
	response, mappingErr = applyCustomFieldMappings(response, raw, mapping.FieldMappings)
	if mappingErr != nil {
		return nil, fmt.Errorf("custom protocol %q field mapping: %w", config.ID, mappingErr)
	}
	if len(response.Output) == 0 && response.Error == nil && !allowEmpty {
		return nil, fmt.Errorf("custom protocol %q response has no mapped text, reasoning, or tool call", config.ID)
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

func renderCustomTemplate(template string, context map[string]any, omitIfEmpty []string) ([]byte, error) {
	if len(template) > customProtocolMaxTemplateBytes {
		return nil, fmt.Errorf("template exceeds %d bytes", customProtocolMaxTemplateBytes)
	}
	value, err := renderCustomJSON(template, context)
	if err != nil {
		return nil, err
	}
	for _, path := range omitIfEmpty {
		value = deleteCustomPath(value, strings.TrimPrefix(strings.TrimSpace(path), "maheshvara."))
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
		path, defaultValue, forceJSON, err := parseCustomExpression(expression)
		if err != nil {
			return nil, err
		}
		resolved, ok := customLookupPath(context, path)
		if !ok || customEmptyValue(resolved) {
			resolved = defaultValue
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
	var value any
	decoder := json.NewDecoder(strings.NewReader(builder.String()))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("rendered body is not valid JSON: %w", err)
	}
	return value, nil
}

func parseCustomExpression(expression string) (string, any, bool, error) {
	parts := strings.Split(expression, "|")
	path := strings.TrimSpace(parts[0])
	if path == "" {
		return "", nil, false, fmt.Errorf("empty template path")
	}
	var defaultValue any
	forceJSON := false
	for _, rawOption := range parts[1:] {
		option := strings.TrimSpace(rawOption)
		switch {
		// |json：显式声明占位符按 JSON 值嵌入（无引号占位默认即如此；
		// 带引号模板配 |json 则跳出字符串转义路径）。
		case option == "json":
			forceJSON = true
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
			return "", nil, false, fmt.Errorf("unsupported template option %q", option)
		}
	}
	return path, defaultValue, forceJSON, nil
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
		path, defaultValue, _, err := parseCustomExpression(strings.TrimSpace(template[start+2 : end]))
		if err != nil {
			builder.WriteString(template[start : end+2])
			offset = end + 2
			continue
		}
		resolved, ok := customLookupPath(context, path)
		if !ok || customEmptyValue(resolved) {
			resolved = defaultValue
		}
		builder.WriteString(customValueString(resolved))
		offset = end + 2
	}
	return builder.String()
}

func validateCustomStringTemplate(template string) error {
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
		if _, _, _, err := parseCustomExpression(strings.TrimSpace(template[start+2 : end])); err != nil {
			return err
		}
		offset = end + 2
	}
	return nil
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

func customTextAt(root any, path string) string {
	return customTextValue(customValueAt(root, path))
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

func customToolCall(value any, index int) MaheshvaraToolCall {
	object, _ := value.(map[string]any)
	if object == nil {
		return MaheshvaraToolCall{}
	}
	function := customMap(object["function"])
	call := MaheshvaraToolCall{
		ID:   firstNonEmptyString(stringValue(object["id"]), stringValue(object["call_id"]), stringValue(object["tool_call_id"]), stringValue(function["id"]), fmt.Sprintf("call_%d", index)),
		Name: firstNonEmptyString(stringValue(object["name"]), stringValue(object["function_name"]), stringValue(function["name"])),
		Type: MaheshvaraToolFunction,
	}
	arguments := object["arguments"]
	if arguments == nil {
		arguments = object["args"]
	}
	if arguments == nil {
		arguments = object["input"]
	}
	if arguments == nil && function != nil {
		arguments = firstNonNilValue(function["arguments"], function["args"], function["input"])
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

func customUsageAt(root any, path string) *MaheshvaraUsage {
	object, _ := customValueAt(root, path).(map[string]any)
	if object == nil {
		return nil
	}
	usage := &MaheshvaraUsage{Source: "provider_response"}
	usage.InputTokens = customInt(object, "input_tokens", "inputTokens", "prompt_tokens", "promptTokenCount")
	usage.OutputTokens = customInt(object, "output_tokens", "outputTokens", "completion_tokens", "candidatesTokenCount")
	usage.TotalTokens = customInt(object, "total_tokens", "totalTokens", "totalTokenCount")
	usage.CachedInputTokens = customInt(object, "cached_input_tokens", "cachedInputTokens", "cached_tokens", "cachedContentTokenCount")
	usage.ReasoningTokens = customInt(object, "reasoning_tokens", "reasoningTokens", "thoughtsTokenCount")
	usage.TotalTokens = valueOrSum(usage.TotalTokens, usage.InputTokens, usage.OutputTokens)
	return usage
}

func customInt(object map[string]any, keys ...string) int {
	for _, key := range keys {
		if number, ok := object[key].(json.Number); ok {
			// 合法 JSON Number 可能带小数尾缀/科学计数(Java/Python 服务常见
			// 187.0 / 1e3):Int64 失败回落 Float64 取整,而不是把整个计数归零。
			if value, err := number.Int64(); err == nil {
				return int(value)
			}
			if f, err := number.Float64(); err == nil {
				return int(f)
			}
			continue
		}
		if value, ok := numberValue(object[key]); ok {
			return int(value)
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
