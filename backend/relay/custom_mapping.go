package relay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

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

type customPathToken struct {
	name  string
	index *int
}

func parseCustomPath(path string) ([]customPathToken, error) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	var tokens []customPathToken
	for offset := 0; offset < len(path); {
		if path[offset] == '.' {
			offset++
			continue
		}
		start := offset
		for offset < len(path) && path[offset] != '.' && path[offset] != '[' {
			offset++
		}
		if start != offset {
			name := strings.TrimSpace(path[start:offset])
			if name == "" {
				return nil, fmt.Errorf("empty path segment in %q", path)
			}
			tokens = append(tokens, customPathToken{name: name})
		}
		for offset < len(path) && path[offset] == '[' {
			end := strings.IndexByte(path[offset+1:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated array segment in %q", path)
			}
			end += offset + 1
			value := strings.TrimSpace(path[offset+1 : end])
			if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
				tokens = append(tokens, customPathToken{name: value[1 : len(value)-1]})
			} else {
				index, err := strconv.Atoi(value)
				if err != nil || index < 0 {
					return nil, fmt.Errorf("invalid array index %q in %q", value, path)
				}
				tokens = append(tokens, customPathToken{index: &index})
			}
			offset = end + 1
		}
		if offset < len(path) && path[offset] != '.' {
			return nil, fmt.Errorf("invalid path syntax near %q", path[offset:])
		}
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty path %q", path)
	}
	return tokens, nil
}

func customLookupPath(root any, path string) (any, bool) {
	tokens, err := parseCustomPath(path)
	if err != nil {
		return nil, false
	}
	var current any = root
	for _, token := range tokens {
		if token.index != nil {
			array, ok := current.([]any)
			if !ok || *token.index >= len(array) {
				return nil, false
			}
			current = array[*token.index]
			continue
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[token.name]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setCustomPath(root map[string]any, path string, value any) error {
	tokens, err := parseCustomPath(path)
	if err != nil {
		return err
	}
	_, err = setCustomPathValue(root, tokens, value)
	return err
}

func setCustomPathValue(current any, tokens []customPathToken, value any) (any, error) {
	if len(tokens) == 0 {
		return value, nil
	}
	token := tokens[0]
	if token.index != nil {
		var array []any
		if current != nil {
			var ok bool
			array, ok = current.([]any)
			if !ok {
				return nil, fmt.Errorf("path expects an array")
			}
		}
		for len(array) <= *token.index {
			array = append(array, nil)
		}
		updated, err := setCustomPathValue(array[*token.index], tokens[1:], value)
		if err != nil {
			return nil, err
		}
		array[*token.index] = updated
		return array, nil
	}
	object, ok := current.(map[string]any)
	if current == nil {
		object = map[string]any{}
		ok = true
	}
	if !ok {
		return nil, fmt.Errorf("path expects an object before %q", token.name)
	}
	updated, err := setCustomPathValue(object[token.name], tokens[1:], value)
	if err != nil {
		return nil, err
	}
	object[token.name] = updated
	return object, nil
}

func deleteCustomPath(root any, path string) {
	tokens, err := parseCustomPath(path)
	if err != nil {
		return
	}
	deleteCustomPathValue(root, tokens)
}

func deleteCustomPathValue(current any, tokens []customPathToken) bool {
	if len(tokens) == 0 {
		return true
	}
	token := tokens[0]
	if token.index != nil {
		array, ok := current.([]any)
		if !ok || *token.index >= len(array) {
			return false
		}
		if len(tokens) == 1 {
			if customEmptyValue(array[*token.index]) {
				array[*token.index] = nil
				return true
			}
			return false
		}
		if deleteCustomPathValue(array[*token.index], tokens[1:]) && customEmptyValue(array[*token.index]) {
			array[*token.index] = nil
			return true
		}
		return false
	}
	object, ok := current.(map[string]any)
	if !ok {
		return false
	}
	value, exists := object[token.name]
	if !exists {
		return false
	}
	if len(tokens) == 1 {
		if customEmptyValue(value) {
			delete(object, token.name)
			return true
		}
		return false
	}
	if deleteCustomPathValue(value, tokens[1:]) && customEmptyValue(value) {
		delete(object, token.name)
		return true
	}
	return false
}

func applyCustomFieldMappings(response *MaheshvaraResponse, root any, mappings []CustomProtocolFieldMapping) (*MaheshvaraResponse, error) {
	if response == nil || len(mappings) == 0 {
		return response, nil
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("serialize mapped Maheshvara response: %w", err)
	}
	var target map[string]any
	if err := json.Unmarshal(encoded, &target); err != nil {
		return nil, fmt.Errorf("prepare mapped Maheshvara response: %w", err)
	}
	for index, mapping := range mappings {
		targetPath := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(mapping.Target, "maheshvara."), "canonical."))
		if err := validateCustomResponseTarget(targetPath); err != nil {
			return nil, fmt.Errorf("fieldMappings[%d]: %w", index, err)
		}
		value, ok, err := customMappingValue(root, mapping)
		if err != nil {
			return nil, fmt.Errorf("fieldMappings[%d]: %w", index, err)
		}
		if !ok || (mapping.OmitIfEmpty && customEmptyValue(value)) {
			continue
		}
		value, err = transformCustomMappingValue(value, mapping.Transform)
		if err != nil {
			return nil, fmt.Errorf("fieldMappings[%d] transform: %w", index, err)
		}
		if err := setCustomPath(target, targetPath, value); err != nil {
			return nil, fmt.Errorf("fieldMappings[%d] target: %w", index, err)
		}
	}
	encoded, err = json.Marshal(target)
	if err != nil {
		return nil, fmt.Errorf("serialize field-mapped response: %w", err)
	}
	var mapped MaheshvaraResponse
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil, fmt.Errorf("decode field-mapped Maheshvara response: %w", err)
	}
	return &mapped, nil
}

func validateCustomResponseTarget(path string) error {
	tokens, err := parseCustomPath(path)
	if err != nil {
		return err
	}
	if tokens[0].index != nil {
		return fmt.Errorf("target must start with a Maheshvara response field")
	}
	switch tokens[0].name {
	case "id", "model", "created_at", "status", "stop_reason", "incomplete_details", "metadata", "service_tier", "system_fingerprint", "output", "usage", "error":
		return nil
	default:
		return fmt.Errorf("target %q is not a supported Maheshvara response field", tokens[0].name)
	}
}

func customMappingValue(root any, mapping CustomProtocolFieldMapping) (any, bool, error) {
	if len(mapping.Value) > 0 {
		value, err := jsonRawToNumberValue(mapping.Value)
		return value, err == nil, err
	}
	if source := strings.TrimSpace(mapping.Source); source != "" {
		if value, ok := customLookupPath(root, source); ok && !customEmptyValue(value) {
			return value, true, nil
		}
	}
	if len(mapping.Default) > 0 {
		value, err := jsonRawToNumberValue(mapping.Default)
		return value, err == nil, err
	}
	return nil, false, nil
}

func isSupportedCustomMappingTransform(transform string) bool {
	switch strings.ToLower(strings.TrimSpace(transform)) {
	case "", "identity", "raw", "string", "text", "join", "int", "integer", "number", "float",
		"bool", "boolean", "json", "parse_json", "json_string", "timestamp_ms", "first", "usage",
		"content_parts", "tool_calls", "output_items":
		return true
	default:
		return false
	}
}

func transformCustomMappingValue(value any, transform string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(transform)) {
	case "", "identity", "raw":
		return value, nil
	case "string", "text", "join":
		return customTextValue(value), nil
	case "int", "integer", "number":
		number, ok := numberValue(value)
		if !ok {
			return nil, fmt.Errorf("value is not numeric")
		}
		return int(number), nil
	case "float":
		number, ok := numberValue(value)
		if !ok {
			return nil, fmt.Errorf("value is not numeric")
		}
		return number, nil
	case "bool", "boolean":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(customValueString(value)))
		if err != nil {
			return nil, fmt.Errorf("value is not boolean")
		}
		return parsed, nil
	case "json", "parse_json":
		if text, ok := value.(string); ok {
			return jsonRawToNumberValue(json.RawMessage(text))
		}
		return value, nil
	case "json_string":
		encoded, err := json.Marshal(value)
		return string(encoded), err
	case "timestamp_ms":
		number, ok := numberValue(value)
		if !ok {
			return nil, fmt.Errorf("value is not numeric")
		}
		return int64(number / 1000), nil
	case "first":
		if array, ok := value.([]any); ok && len(array) > 0 {
			return array[0], nil
		}
		return value, nil
	case "usage":
		return customUsageMap(value), nil
	case "content_parts":
		parts := interfaceToContentParts(value)
		encoded, err := json.Marshal(parts)
		if err != nil {
			return nil, err
		}
		return jsonRawToNumberValue(encoded)
	case "tool_calls":
		return customToolOutputItems(value), nil
	case "output_items":
		return customOutputItems(value), nil
	default:
		return nil, fmt.Errorf("unsupported transform %q", transform)
	}
}

func customTextValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	case []any:
		var builder strings.Builder
		for _, item := range typed {
			builder.WriteString(customTextValue(item))
		}
		return builder.String()
	case map[string]any:
		for _, key := range []string{"text", "content", "message", "value", "output"} {
			if text := customTextValue(typed[key]); text != "" {
				return text
			}
		}
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	default:
		return customValueString(typed)
	}
}

func customUsageMap(value any) map[string]any {
	object, _ := value.(map[string]any)
	if object == nil {
		return map[string]any{}
	}
	result := map[string]any{}
	copyUsage := func(target string, keys ...string) {
		for _, key := range keys {
			if item, ok := object[key]; ok {
				result[target] = item
				return
			}
		}
	}
	copyUsage("input_tokens", "input_tokens", "inputTokens", "prompt_tokens", "promptTokenCount")
	copyUsage("output_tokens", "output_tokens", "outputTokens", "completion_tokens", "candidatesTokenCount")
	copyUsage("total_tokens", "total_tokens", "totalTokens", "totalTokenCount")
	copyUsage("cached_input_tokens", "cached_input_tokens", "cachedInputTokens", "cached_tokens", "cachedContentTokenCount")
	copyUsage("reasoning_tokens", "reasoning_tokens", "reasoningTokens", "thoughtsTokenCount")
	if _, ok := result["total_tokens"]; !ok {
		input, _ := numberValue(result["input_tokens"])
		output, _ := numberValue(result["output_tokens"])
		if input != 0 || output != 0 {
			result["total_tokens"] = int(input + output)
		}
	}
	return result
}

func customToolOutputItems(value any) []any {
	array := customArrayValue(value)
	result := make([]any, 0, len(array))
	for index, item := range array {
		call := customToolCall(item, index)
		if call.Name == "" {
			continue
		}
		result = append(result, map[string]any{
			"id": call.ID, "type": CanonicalOutputFunctionCall, "status": "completed", "call_id": call.ID,
			"name": call.Name, "arguments": jsonRawToAny(call.Arguments),
		})
	}
	return result
}

func customOutputItems(value any) []any {
	array := customArrayValue(value)
	result := make([]any, 0, len(array))
	for index, item := range array {
		object, _ := item.(map[string]any)
		if object == nil {
			if text := customTextValue(item); text != "" {
				result = append(result, map[string]any{"type": CanonicalOutputMessage, "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": CanonicalContentText, "text": text}}})
			}
			continue
		}
		text := customTextValue(firstNonNilValue(object["text"], object["content"], object["message"], object["output"]))
		if text != "" {
			result = append(result, map[string]any{
				"id":   firstNonEmptyString(stringValue(object["id"]), fmt.Sprintf("msg_%d", index)),
				"type": CanonicalOutputMessage, "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": CanonicalContentText, "text": text}},
			})
		}
		calls := firstNonNilValue(object["tool_calls"], object["function_calls"], object["calls"])
		for _, generated := range customToolOutputItems(calls) {
			result = append(result, generated)
		}
	}
	return result
}

func customArrayValue(value any) []any {
	if array, ok := value.([]any); ok {
		return array
	}
	if value == nil {
		return nil
	}
	return []any{value}
}

func jsonRawToNumberValue(raw json.RawMessage) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
