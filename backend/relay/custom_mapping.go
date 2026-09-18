package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type customPathToken struct {
	name  string
	index *int
}

// customPathMaxIndex 限制 fieldMappings 目标路径中的数组下标:
// setCustomPathValue 按需填充 nil 至目标下标,无界索引可在首个响应时 OOM。
const customPathMaxIndex = 4096

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
		// 目标路径在注册/ValidateCustomProtocol 时已校验（两条入口均经校验），
		// 逐事件重复校验属于双重保护。
		targetPath := normalizeMaheshvaraPath(mapping.Target)
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
	// 数组下标上限:setCustomPathValue 会按需填充 nil 到目标下标,无界索引
	// (如 output[2000000000])在首个上游响应映射时即 OOM。
	for _, token := range tokens {
		if token.index != nil && *token.index > customPathMaxIndex {
			return fmt.Errorf("array index %d exceeds the limit %d in target %q", *token.index, customPathMaxIndex, path)
		}
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
		value, err := decodeJSONUseNumber(mapping.Value)
		return value, err == nil, err
	}
	if source := strings.TrimSpace(mapping.Source); source != "" {
		if value, ok := customLookupPath(root, source); ok && !customEmptyValue(value) {
			return value, true, nil
		}
	}
	if len(mapping.Default) > 0 {
		value, err := decodeJSONUseNumber(mapping.Default)
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
			return decodeJSONUseNumber(json.RawMessage(text))
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
		return decodeJSONUseNumber(encoded)
	case "tool_calls":
		return customToolOutputItems(value), nil
	case "output_items":
		return customOutputItems(value), nil
	default:
		return nil, fmt.Errorf("unsupported transform %q", transform)
	}
}

func customTextValue(value any) string {
	return customTextValueWithKeys(value, nil)
}

// customTextValueWithKeys 按给定魔键提取文本；keys 为空时用内置默认表
// （可经 aliases.textKeys 整体替换）。
func customTextValueWithKeys(value any, keys []string) string {
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
			builder.WriteString(customTextValueWithKeys(item, keys))
		}
		return builder.String()
	case map[string]any:
		for _, key := range customEffectiveTextKeys(keys) {
			if text := customTextValueWithKeys(typed[key], keys); text != "" {
				return text
			}
		}
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	default:
		return customValueString(typed)
	}
}

func customEffectiveTextKeys(keys []string) []string {
	if len(keys) > 0 {
		return keys
	}
	return []string{"text", "content", "message", "value", "output"}
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
		call := customToolCallWithAliases(item, index, nil)
		if call.Name == "" {
			continue
		}
		result = append(result, map[string]any{
			"id": call.ID, "type": MaheshvaraOutputFunctionCall, "status": MaheshvaraStatusCompleted, "call_id": call.ID,
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
				result = append(result, map[string]any{"type": MaheshvaraOutputMessage, "status": MaheshvaraStatusCompleted, "role": "assistant", "content": []any{map[string]any{"type": MaheshvaraContentText, "text": text}}})
			}
			continue
		}
		text := customTextValue(firstNonNilValue(object["text"], object["content"], object["message"], object["output"]))
		if text != "" {
			result = append(result, map[string]any{
				"id":   firstNonEmptyString(stringValue(object["id"]), fmt.Sprintf("msg_%d", index)),
				"type": MaheshvaraOutputMessage, "status": MaheshvaraStatusCompleted, "role": "assistant",
				"content": []any{map[string]any{"type": MaheshvaraContentText, "text": text}},
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

// decodeJSONUseNumber 是全引擎统一的 JSON→any 解码入口(UseNumber:大整数
// 经 json.Number 保精度)。所有「解析载荷/规则值/渲染产物」的路径共用。
func decodeJSONUseNumber(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// deleteCustomPathForce 无条件删除路径（空值/条件省略统一删除阶段用）：
// 命中即删，值非空也删；子项删除后为空的父容器一并修剪（与 legacy 空值
// 删除的清理语义一致）。数组元素删除返回缩短后的新 slice。
func deleteCustomPathForce(root any, path string) any {
	tokens, err := parseCustomPath(path)
	if err != nil {
		return root
	}
	updated, _ := deleteCustomPathValueForce(root, tokens)
	return updated
}

func deleteCustomPathValueForce(current any, tokens []customPathToken) (any, bool) {
	if len(tokens) == 0 {
		return current, true
	}
	token := tokens[0]
	if token.index != nil {
		array, ok := current.([]any)
		if !ok || *token.index >= len(array) {
			return current, false
		}
		index := *token.index
		if len(tokens) == 1 {
			return append(array[:index], array[index+1:]...), true
		}
		updated, ok := deleteCustomPathValueForce(array[index], tokens[1:])
		if !ok {
			return current, false
		}
		array[index] = updated
		if customEmptyValue(array[index]) {
			return append(array[:index], array[index+1:]...), true
		}
		return array, true
	}
	object, ok := current.(map[string]any)
	if !ok {
		return current, false
	}
	value, exists := object[token.name]
	if !exists {
		return current, false
	}
	if len(tokens) == 1 {
		delete(object, token.name)
		return current, true
	}
	updated, deleted := deleteCustomPathValueForce(value, tokens[1:])
	if !deleted {
		return current, false
	}
	object[token.name] = updated
	if customEmptyValue(updated) {
		delete(object, token.name)
	}
	return current, true
}
