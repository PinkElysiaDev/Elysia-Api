package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/elysia-api/backend/protocol"
)

// ImportLegacyProtocol converts a legacy configuration into an editable v2
// draft. Every unsupported feature is a blocking diagnostic; the original
// definition remains in metadata for repair, never as an executable fallback.
// Callers must supply reviewed capabilities and verify the result before use.
func ImportLegacyProtocol(raw []byte, capabilities protocol.CapabilitySet) (*protocol.Definition, []protocol.ConversionIssue) {
	var legacy CustomProtocolConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		return nil, importIssue("/", err)
	}
	original, err := protocol.ParseValue(raw)
	if err != nil {
		return nil, importIssue("/", err)
	}
	version := legacy.Version
	if version == "" {
		version = "1"
	}
	definition := &protocol.Definition{SchemaVersion: protocol.DefinitionSchemaVersion, ID: legacy.ID, Name: legacy.Name, Version: version, Family: "custom:" + legacy.ID, WireVersion: version, Capabilities: capabilities, Directions: map[protocol.Direction]protocol.Mapping{}, Operations: map[string]protocol.Operation{}, Extensions: protocol.Object{"legacyImport": original}, Samples: []protocol.Sample{}}
	var issues []protocol.ConversionIssue
	add := func(path, reason string) { issues = append(issues, importIssue(path, fmt.Errorf("%s", reason))...) }
	if legacy.Type != "" && legacy.Type != CustomProtocolTypeLLM {
		add("/type", "non-LLM operations require a supported semantic model")
	}
	if legacy.Aliases != nil {
		add("/aliases", "translate aliases into explicit typed expressions; alias precedence must be verified")
	}
	if legacy.Models != nil {
		add("/models", "model discovery mapping requires a separately declared operation")
	}
	if legacy.Request.BodyTemplate != "" || legacy.Request.SubmitBody != "" || len(legacy.Request.Body) == 0 {
		add("/request/body", "convert the string template to a structured mapping; string interpolation is not executed by v2")
	} else {
		body, err := protocol.ParseValue(legacy.Request.Body)
		if err != nil {
			issues = append(issues, importIssue("/request/body", err)...)
		} else {
			expression, err := importBodyExpression(body, legacy.Request.Shape, "/request/body")
			if err != nil {
				issues = append(issues, importIssue("/request/body", err)...)
			} else if legacy.Request.Shape == "" {
				definition.Directions[protocol.EncodeRequest] = protocol.Mapping{Transform: &expression}
			} else {
				definition.Directions[protocol.EncodeRequest] = protocol.Mapping{Module: legacy.Request.Shape, After: &expression}
			}
		}
	}
	if len(legacy.Request.OmitIfEmpty) > 0 {
		add("/request/omitIfEmpty", "move output omission rules into the corresponding field expressions")
	}
	if legacy.Request.ContentType != "" && legacy.Request.ContentType != "application/json" {
		add("/request/contentType", "only a declared JSON wire encoding can be imported")
	}
	method := legacy.Request.Method
	if method == "" {
		method = http.MethodPost
	}
	auth, err := importCredential(legacy.Request.Auth)
	if err != nil {
		issues = append(issues, importIssue("/request/auth", err)...)
	}
	definition.Operations["generate"] = protocol.Operation{Kind: "generate", Method: method, Path: legacy.Request.PathTemplate, Transport: protocol.HTTPJSON, Request: protocol.EncodeRequest, Response: protocol.DecodeResponse, Auth: auth, Headers: legacy.Request.Headers, Query: legacy.Request.Query}
	if strings.Contains(legacy.Request.PathTemplate, "{{") {
		add("/request/path", "convert endpoint interpolation to a declared operation parameter")
	}
	if legacy.Request.PathStream != "" || legacy.Response.Stream != nil {
		add("/response/stream", "stream migration requires explicit event rules and per-session state; HTTP mappings cannot prove streaming fidelity")
	}
	response := legacy.Response
	response.Stream = nil
	response.Sample = nil
	if response.Adapter == "" {
		add("/response", "author an independent response decoder from the original mapping and samples")
	} else {
		definition.Directions[protocol.DecodeResponse] = protocol.Mapping{Module: response.Adapter}
		response.Adapter = ""
		remaining, _ := json.Marshal(response)
		if string(remaining) != "{}" {
			add("/response", "response adapter overrides must be converted to explicit expressions")
		}
	}
	for index := range issues {
		issues[index].Protocol = definition.Identity()
	}
	return definition, issues
}

func importIssue(path string, err error) []protocol.ConversionIssue {
	return []protocol.ConversionIssue{{Code: protocol.InvalidDefinition, Severity: protocol.SeverityError, Stage: "import", Path: path, Reason: err.Error(), Suggestion: "Repair this mapping in the v2 draft and run offline verification before activation."}}
}

func importCredential(auth CustomProtocolAuth) (protocol.Credential, error) {
	switch auth.Mode {
	case "", "bearer":
		return protocol.Credential{Location: "header", Name: "Authorization", Prefix: "Bearer "}, nil
	case "none":
		return protocol.Credential{Location: "none"}, nil
	case "header":
		name := auth.Header
		if name == "" {
			name = DefaultAuthHeaderName
		}
		return protocol.Credential{Location: "header", Name: name, Prefix: auth.Prefix}, nil
	case "query":
		if auth.Query == "" {
			return protocol.Credential{}, fmt.Errorf("query credential name is required")
		}
		return protocol.Credential{Location: "query", Name: auth.Query, Prefix: auth.Prefix}, nil
	default:
		return protocol.Credential{}, fmt.Errorf("unsupported credential mode %q", auth.Mode)
	}
}

func importBodyExpression(value protocol.Value, shape, path string) (protocol.Expression, error) {
	if value.IsObject() {
		object, err := value.ReadObject()
		if err != nil {
			return protocol.Expression{}, err
		}
		if _, exists := object["field"]; exists {
			return importFieldExpression(object, shape, path)
		}
		if constant, exists := object["value"]; exists && len(object) == 1 {
			return protocol.Expression{Op: "literal", Value: constant}, nil
		}
		fields := make(map[string]protocol.Expression, len(object))
		for key, child := range object {
			fields[key], err = importBodyExpression(child, shape, path+"/"+key)
			if err != nil {
				return protocol.Expression{}, err
			}
		}
		return protocol.Expression{Op: "object", Fields: fields}, nil
	}
	if bytes.HasPrefix(bytes.TrimSpace(value.Bytes()), []byte("[")) {
		var items []protocol.Value
		if err := value.Decode(&items); err != nil {
			return protocol.Expression{}, err
		}
		result := protocol.Expression{Op: "array"}
		for index, child := range items {
			expression, err := importBodyExpression(child, shape, fmt.Sprintf("%s/%d", path, index))
			if err != nil {
				return protocol.Expression{}, err
			}
			result.Items = append(result.Items, expression)
		}
		return result, nil
	}
	return protocol.Expression{Op: "literal", Value: value}, nil
}

func importFieldExpression(fields protocol.Object, shape, path string) (protocol.Expression, error) {
	for key := range fields {
		if key != "field" {
			return protocol.Expression{}, fmt.Errorf("%s/%s requires an explicit conditional/type expression", path, key)
		}
	}
	var field string
	if err := fields["field"].Decode(&field); err != nil {
		return protocol.Expression{}, err
	}
	source, pointer, err := importFieldPointer(field, shape)
	if err != nil {
		return protocol.Expression{}, fmt.Errorf("%s: %w", path, err)
	}
	read := protocol.Expression{Op: "read", From: source, Path: pointer}
	// Legacy annotation references emit null for absent JSON values. Make that
	// default explicit instead of changing the author's omission behavior.
	null, err := protocol.ParseValue([]byte("null"))
	if err != nil {
		return protocol.Expression{}, err
	}
	fallback := protocol.Expression{Op: "literal", Value: null}
	if spec, exists := lookupRequestFieldSpec(field); exists && spec.Shape == "string" {
		fallback.Value = protocol.StringValue("")
	}
	return protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: &read}, Then: &read, Otherwise: &fallback}, nil
}

func importFieldPointer(field, shape string) (string, string, error) {
	if shape != "" {
		aliases := map[string]map[string]string{
			"openai-chat": {"model": "model", "messages": "messages", "tools": "tools", "tool_choice": "tool_choice", "prompt_cache_key": "prompt_cache_key", "prompt_cache_retention": "prompt_cache_retention"},
			"responses":   {"model": "model", "input_items": "input", "tools": "tools", "tool_choice": "tool_choice", "prompt_cache_key": "prompt_cache_key", "prompt_cache_retention": "prompt_cache_retention"},
			"anthropic":   {"model": "model", "messages": "messages", "tools": "tools", "tool_choice": "tool_choice", "anthropic_system": "system", "cache_control": "cache_control"},
			"gemini":      {"messages": "contents", "tools": "tools", "tool_config": "toolConfig", "gemini_system": "systemInstruction"},
		}
		if pointer, exists := aliases[shape][field]; exists {
			return "input", "/" + pointer, nil
		}
	}
	if field == "model" {
		return "root", "/model", nil
	}
	if field == "stream" {
		return "root", "/parameters/stream", nil
	}
	return "", "", fmt.Errorf("field %q has no proven equivalent in shape %q", field, shape)
}
