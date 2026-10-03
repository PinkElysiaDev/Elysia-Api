package relay

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Historical configuration types exist only for lossless import and preset hashes.
// No registry, template compiler or conversion executor uses these types.

const (
	customProtocolMaxTemplateBytes = 4 << 20
	customProtocolMaxDepth         = 64
	customProtocolMaxPlaceholders  = 2048
)
const DefaultAuthHeaderName = "x-api-key"

type CustomProtocolConfig struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name,omitempty"`
	Version  string                 `json:"version,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Request  CustomProtocolRequest  `json:"request"`
	Response CustomProtocolResponse `json:"response,omitempty"`
	Models   *CustomProtocolModels  `json:"models,omitempty"`
	Aliases  *CustomProtocolAliases `json:"aliases,omitempty"`
	Metadata map[string]any         `json:"metadata,omitempty"`
}
type CustomProtocolAliases struct {
	TextKeys []string            `json:"textKeys,omitempty"`
	Usage    map[string][]string `json:"usage,omitempty"`    // input/output/total/cached/reasoning
	ToolCall map[string][]string `json:"toolCall,omitempty"` // id/name/arguments
}
type CustomProtocolModels struct {
	Method        string              `json:"method,omitempty"` // 默认 GET，仅 GET/POST
	Path          string              `json:"path"`             // 必填，相对源 baseUrl
	Headers       map[string]string   `json:"headers,omitempty"`
	Query         map[string]string   `json:"query,omitempty"`
	Auth          *CustomProtocolAuth `json:"auth,omitempty"`     // 缺省复用 request.auth
	ListPath      string              `json:"listPath"`           // 必填，点路径到模型数组
	IDPath        string              `json:"idPath,omitempty"`   // 元素内，默认 "id"
	NamePath      string              `json:"namePath,omitempty"` // 元素内
	IDStripPrefix string              `json:"idStripPrefix,omitempty"`
}

const (
	CustomProtocolTypeLLM       = "llm"
	CustomProtocolTypeReranker  = "reranker"
	CustomProtocolTypeEmbedding = "embedding"
)

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
	PathStream   string             `json:"pathStream,omitempty"`
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
	Adapter               string                               `json:"adapter,omitempty"`
	Body                  json.RawMessage                      `json:"body,omitempty"`
	IDPath                string                               `json:"idPath,omitempty"`
	ModelPath             string                               `json:"modelPath,omitempty"`
	StatusPath            string                               `json:"statusPath,omitempty"`
	TextPath              string                               `json:"textPath,omitempty"`
	TextFilter            CustomProtocolMatchSet               `json:"textFilter,omitempty"`
	ReasoningPath         string                               `json:"reasoningPath,omitempty"`
	ReasoningFilter       CustomProtocolMatchSet               `json:"reasoningFilter,omitempty"`
	SignaturePath         string                               `json:"signaturePath,omitempty"`
	SignatureProviderPath string                               `json:"signatureProviderPath,omitempty"`
	SignatureProvider     string                               `json:"signatureProvider,omitempty"`
	EncryptedContentPath  string                               `json:"encryptedContentPath,omitempty"`
	RefusalPath           string                               `json:"refusalPath,omitempty"`
	CitationsPath         string                               `json:"citationsPath,omitempty"`
	ToolCallsPath         string                               `json:"toolCallsPath,omitempty"`
	UsagePath             string                               `json:"usagePath,omitempty"`
	FinishReasonPath      string                               `json:"finishReasonPath,omitempty"`
	ErrorPath             string                               `json:"errorPath,omitempty"`
	Mappings              map[string]string                    `json:"mappings,omitempty"`
	FieldMappings         []CustomProtocolFieldMapping         `json:"fieldMappings,omitempty"`
	Fields                []CustomProtocolResponseFieldMapping `json:"fields,omitempty"`
	Sample                json.RawMessage                      `json:"sample,omitempty"`
	Stream                *CustomProtocolStreamMapping         `json:"stream,omitempty"`
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
	Adapter           string                      `json:"adapter,omitempty"`
	PayloadPath       string                      `json:"payloadPath,omitempty"`
	Mode              string                      `json:"mode,omitempty"`
	DoneValues        []string                    `json:"doneValues,omitempty"`
	Done              []CustomProtocolDoneValue   `json:"done,omitempty"`
	DoneValuesReplace bool                        `json:"doneValuesReplace,omitempty"` // 移除默认 [DONE]
	Events            []string                    `json:"events,omitempty"`
	EventKeys         []string                    `json:"eventKeys,omitempty"`
	FinishWhen        *CustomProtocolMatch        `json:"finishWhen,omitempty"`
	StatusWhen        *CustomProtocolMatch        `json:"statusWhen,omitempty"`
	Modes             *CustomProtocolStreamModes  `json:"modes,omitempty"`
	Frames            []CustomProtocolStreamFrame `json:"frames,omitempty"`
	Response          *CustomProtocolResponse     `json:"response,omitempty"`
}
type CustomProtocolStreamModes struct {
	Text      string `json:"text,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}
type CustomProtocolDoneValue struct {
	Raw  string          `json:"raw,omitempty"`
	JSON json.RawMessage `json:"json,omitempty"`
}
type CustomProtocolStreamFrame struct {
	Event       string                    `json:"event,omitempty"`
	Match       *CustomProtocolMatch      `json:"match,omitempty"`
	PayloadPath string                    `json:"payloadPath,omitempty"`
	Tool        *CustomProtocolStreamTool `json:"tool,omitempty"`
	Response    *CustomProtocolResponse   `json:"response,omitempty"`
	Terminal    bool                      `json:"terminal,omitempty"`
	ToolDone    bool                      `json:"toolDone,omitempty"`
}
type CustomProtocolStreamTool struct {
	Path          string `json:"path,omitempty"`
	IDPath        string `json:"idPath,omitempty"`
	IndexPath     string `json:"indexPath,omitempty"`
	NamePath      string `json:"namePath,omitempty"`
	ArgumentsPath string `json:"argumentsPath,omitempty"`
	ArgumentsMode string `json:"argumentsMode,omitempty"`
}

type CustomProtocolMatch struct {
	Path  string          `json:"path"`
	Op    string          `json:"op,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

type CustomProtocolMatchSet []CustomProtocolMatch

func (set *CustomProtocolMatchSet) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var matches []CustomProtocolMatch
		if err := json.Unmarshal(data, &matches); err != nil {
			return err
		}
		*set = matches
		return nil
	}
	var single CustomProtocolMatch
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	*set = CustomProtocolMatchSet{single}
	return nil
}

type CustomProtocolResponseFieldMapping struct {
	Path      string `json:"path"`
	Field     string `json:"field"`
	Transform string `json:"transform,omitempty"`
}
