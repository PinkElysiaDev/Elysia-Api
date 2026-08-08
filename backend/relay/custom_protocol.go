package relay

import (
	"bytes"
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
	Request  CustomProtocolRequest  `json:"request"`
	Response CustomProtocolResponse `json:"response,omitempty"`
	Metadata map[string]any         `json:"metadata,omitempty"`
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
	IDPath           string                       `json:"idPath,omitempty"`
	ModelPath        string                       `json:"modelPath,omitempty"`
	StatusPath       string                       `json:"statusPath,omitempty"`
	TextPath         string                       `json:"textPath,omitempty"`
	ReasoningPath    string                       `json:"reasoningPath,omitempty"`
	ToolCallsPath    string                       `json:"toolCallsPath,omitempty"`
	UsagePath        string                       `json:"usagePath,omitempty"`
	FinishReasonPath string                       `json:"finishReasonPath,omitempty"`
	ErrorPath        string                       `json:"errorPath,omitempty"`
	Mappings         map[string]string            `json:"mappings,omitempty"`
	FieldMappings    []CustomProtocolFieldMapping `json:"fieldMappings,omitempty"`
	Stream           *CustomProtocolStreamMapping `json:"stream,omitempty"`
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
	PayloadPath string                  `json:"payloadPath,omitempty"`
	Mode        string                  `json:"mode,omitempty"`
	DoneValues  []string                `json:"doneValues,omitempty"`
	Events      []string                `json:"events,omitempty"`
	Response    *CustomProtocolResponse `json:"response,omitempty"`
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
	customProtocolRegistry.Lock()
	customProtocolRegistry.items[strings.ToLower(strings.TrimSpace(config.ID))] = cloneCustomProtocol(config)
	customProtocolRegistry.Unlock()
	return nil
}

func RegisterCustomProtocols(configs []CustomProtocolConfig) error {
	validated := make(map[string]CustomProtocolConfig, len(configs))
	for _, config := range configs {
		if err := ValidateCustomProtocol(config); err != nil {
			return err
		}
		id := strings.ToLower(strings.TrimSpace(config.ID))
		if _, exists := validated[id]; exists {
			return fmt.Errorf("custom protocol %q is duplicated", config.ID)
		}
		validated[id] = cloneCustomProtocol(config)
	}
	customProtocolRegistry.Lock()
	next := make(map[string]CustomProtocolConfig, len(customProtocolRegistry.items)+len(validated))
	for id, config := range customProtocolRegistry.items {
		next[id] = config
	}
	for id, config := range validated {
		next[id] = config
	}
	customProtocolRegistry.items = next
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

func RemoveCustomProtocol(id string) {
	customProtocolRegistry.Lock()
	delete(customProtocolRegistry.items, strings.ToLower(strings.TrimSpace(id)))
	customProtocolRegistry.Unlock()
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
	method := strings.ToUpper(strings.TrimSpace(config.Request.Method))
	if method == "" {
		method = http.MethodPost
	}
	template := config.Request.bodyTemplate()
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
		if _, err := renderCustomTemplate(template, maheshvaraTemplateContext(&CanonicalRequest{}), nil); err != nil {
			return fmt.Errorf("custom protocol %q has invalid body template: %w", config.ID, err)
		}
	}
	if err := validateCustomStringTemplate(config.Request.PathTemplate); err != nil {
		return fmt.Errorf("custom protocol %q request.path: %w", config.ID, err)
	}
	for key, value := range config.Request.Headers {
		if err := validateCustomStringTemplate(value); err != nil {
			return fmt.Errorf("custom protocol %q request.headers[%q]: %w", config.ID, key, err)
		}
	}
	for key, value := range config.Request.Query {
		if err := validateCustomStringTemplate(value); err != nil {
			return fmt.Errorf("custom protocol %q request.query[%q]: %w", config.ID, key, err)
		}
	}
	for key := range config.Request.Headers {
		name := strings.TrimSpace(key)
		if name == "" {
			return fmt.Errorf("custom protocol %q contains an empty header name", config.ID)
		}
		if !isValidCustomHeaderName(name) {
			return fmt.Errorf("custom protocol %q contains invalid header name %q", config.ID, key)
		}
		if isProtectedCustomHeader(name) {
			return fmt.Errorf("custom protocol %q header %q is managed by the relay; use request.auth", config.ID, key)
		}
		if strings.ContainsAny(config.Request.Headers[key], "\r\n") {
			return fmt.Errorf("custom protocol %q header %q contains a line break", config.ID, key)
		}
	}
	if contentType := strings.TrimSpace(config.Request.ContentType); contentType != "" && strings.ContainsAny(contentType, "\r\n") {
		return fmt.Errorf("custom protocol %q has invalid content type", config.ID)
	}
	if err := validateCustomAuth(config.Request.Auth); err != nil {
		return fmt.Errorf("custom protocol %q auth: %w", config.ID, err)
	}
	return validateCustomProtocolResponse(config.ID, "response", config.Response, true)
}

func validateCustomProtocolResponse(configID, location string, response CustomProtocolResponse, allowStream bool) error {
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
		target := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(mapping.Target), "maheshvara."), "canonical.")
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
	if stream.Response != nil {
		return validateCustomProtocolResponse(configID, location+".stream.response", *stream.Response, false)
	}
	return nil
}

func RenderCustomProtocolRequest(req *MaheshvaraRequest, config CustomProtocolConfig) (*CustomProtocolRequestResult, error) {
	if req == nil {
		return nil, fmt.Errorf("cannot render custom protocol request from nil Maheshvara request")
	}
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	ctx := maheshvaraTemplateContext(req)
	var body []byte
	if template := config.Request.bodyTemplate(); template != "" {
		var err error
		body, err = renderCustomTemplate(template, ctx, config.Request.OmitIfEmpty)
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
	return RenderCustomProtocolRequest(req, config)
}

func (a *OpenAIAdapter) SendCustomProtocolRequest(baseURL, apiKey string, request *CustomProtocolRequestResult, stream bool) (*http.Response, error) {
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
	httpRequest, err := buildHTTPRequest(method, parsed.String(), requestAPIKey, request.Body, extraHeaders)
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
	client := a.client
	if stream {
		client = a.streamClient
		httpRequest.Header.Set("Accept", "text/event-stream")
	}
	return client.Do(httpRequest)
}

func CustomProtocolResponseToCanonical(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToCanonical(body, config, false)
}

// CustomProtocolStreamEventToCanonical applies the same response mapping to a
// single streaming event. Empty events are valid and are represented by an
// otherwise empty Maheshvara response so callers can continue scanning until a
// later event carries text, a tool call, usage, or a finish reason.
func CustomProtocolStreamEventToCanonical(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToCanonical(body, config, true)
}

func customProtocolStreamEventToCanonicalValidated(body []byte, config CustomProtocolConfig) (*MaheshvaraResponse, error) {
	return customProtocolResponseToCanonicalValidated(body, config, true)
}

func customProtocolResponseToCanonical(body []byte, config CustomProtocolConfig, allowEmpty bool) (*MaheshvaraResponse, error) {
	if err := ValidateCustomProtocol(config); err != nil {
		return nil, err
	}
	return customProtocolResponseToCanonicalValidated(body, config, allowEmpty)
}

func customProtocolResponseToCanonicalValidated(body []byte, config CustomProtocolConfig, allowEmpty bool) (*MaheshvaraResponse, error) {
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse custom protocol %q response: %w", config.ID, err)
	}
	mapping := config.Response
	if allowEmpty && mapping.Stream != nil {
		if payloadPath := strings.TrimSpace(mapping.Stream.PayloadPath); payloadPath != "" {
			if payload, ok := customLookupPath(raw, payloadPath); ok {
				raw = payload
			}
		}
		if mapping.Stream.Response != nil {
			mapping = *mapping.Stream.Response
		}
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
	response := &CanonicalResponse{
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
			response.Error = &CanonicalError{Message: customValueString(value), Raw: customMap(value)}
		}
	}
	if text := customTextAt(raw, mapping.TextPath); text != "" {
		response.Output = append(response.Output, CanonicalOutputItem{
			ID: newCanonicalResponseID("msg"), Type: CanonicalOutputMessage, Status: "completed", Role: "assistant",
			Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: text}},
		})
	}
	if reasoning := customTextAt(raw, mapping.ReasoningPath); reasoning != "" {
		response.Output = append(response.Output, CanonicalOutputItem{
			ID: newCanonicalResponseID("rs"), Type: CanonicalOutputReasoning, Status: "completed",
			Content: []CanonicalContentPart{{Type: CanonicalContentReasoning, Text: reasoning, ReasoningText: reasoning}},
		})
	}
	if mapping.ToolCallsPath != "" {
		for index, item := range customArrayAt(raw, mapping.ToolCallsPath) {
			call := customToolCall(item, index)
			if call.Name == "" {
				continue
			}
			response.Output = append(response.Output, CanonicalOutputItem{
				ID: firstNonEmptyString(call.ID, newCanonicalResponseID("call")), Type: CanonicalOutputFunctionCall,
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
	_ = json.Unmarshal(encoded, &value)
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
		"canonical":  value,
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
		deleteEmptyPath(value, strings.TrimPrefix(strings.TrimSpace(path), "maheshvara."))
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
		resolved, ok := customLookup(context, path)
		if !ok || customEmptyValue(resolved) {
			resolved = defaultValue
		}
		prefix := template[:start]
		suffix := template[end+2:]
		quoted := len(prefix) > 0 && prefix[len(prefix)-1] == '"' && len(suffix) > 0 && suffix[0] == '"'
		if quoted {
			builder.WriteString(escapeJSONString(customValueString(resolved)))
		} else if forceJSON || !quoted {
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
		case option == "json":
			forceJSON = true
		case strings.HasPrefix(option, "default:"):
			literal := strings.TrimSpace(strings.TrimPrefix(option, "default:"))
			if literal == "" {
				defaultValue = ""
				continue
			}
			if err := json.Unmarshal([]byte(literal), &defaultValue); err != nil {
				defaultValue = strings.Trim(literal, "\"")
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
		resolved, ok := customLookup(context, path)
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

func customLookup(root map[string]any, path string) (any, bool) {
	return customLookupPath(root, path)
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

func customToolCall(value any, index int) CanonicalToolCall {
	object, _ := value.(map[string]any)
	if object == nil {
		return CanonicalToolCall{}
	}
	function := customMap(object["function"])
	call := CanonicalToolCall{
		ID:   firstNonEmptyString(stringValue(object["id"]), stringValue(object["call_id"]), stringValue(object["tool_call_id"]), stringValue(function["id"]), fmt.Sprintf("call_%d", index)),
		Name: firstNonEmptyString(stringValue(object["name"]), stringValue(object["function_name"]), stringValue(function["name"])),
		Type: CanonicalToolFunction,
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

func customUsageAt(root any, path string) *CanonicalUsage {
	object, _ := customValueAt(root, path).(map[string]any)
	if object == nil {
		return nil
	}
	usage := &CanonicalUsage{Source: "provider_response"}
	usage.InputTokens = customInt(object, "input_tokens", "inputTokens", "prompt_tokens", "promptTokenCount")
	usage.OutputTokens = customInt(object, "output_tokens", "outputTokens", "completion_tokens", "candidatesTokenCount")
	usage.TotalTokens = customInt(object, "total_tokens", "totalTokens", "totalTokenCount")
	usage.CachedInputTokens = customInt(object, "cached_input_tokens", "cachedInputTokens", "cached_tokens", "cachedContentTokenCount")
	usage.ReasoningTokens = customInt(object, "reasoning_tokens", "reasoningTokens", "thoughtsTokenCount")
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func customInt(object map[string]any, keys ...string) int {
	for _, key := range keys {
		if number, ok := object[key].(json.Number); ok {
			value, _ := number.Int64()
			return int(value)
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

func deleteEmptyPath(root any, path string) {
	deleteCustomPath(root, path)
}

func deleteEmptyPathParts(current any, parts []string) {
	if len(parts) == 0 {
		return
	}
	object, ok := current.(map[string]any)
	if !ok {
		return
	}
	key := parts[0]
	value, exists := object[key]
	if !exists {
		return
	}
	if len(parts) == 1 {
		if customEmptyValue(value) {
			delete(object, key)
		}
		return
	}
	deleteEmptyPathParts(value, parts[1:])
	if customEmptyValue(value) {
		delete(object, key)
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

func timeNowUnix() int64 {
	return time.Now().Unix()
}
