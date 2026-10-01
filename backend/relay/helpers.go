// 全包通用的取值/类型强制助手（原散落于转换与扩展文件中）。
package relay

import (
	"encoding/json"
	"strconv"
	"strings"
)

// firstNonEmptyString 返回第一个非空字符串。
func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolValue(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// numberValue 只覆盖解码产物实际会出现的类型：json.Unmarshal 的 float64、
// decodeJSONUseNumber 的 json.Number，以及历史路径遗留的 string 数值。
// 窄整型分支（int8/uint16 等）从未出现过，已删除。
func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// rawBlockCacheControl 从入站保留的原始线制块(tool_result 的 part.Raw /
// tool_use 的 call.Raw)读取 cache_control 标记——Claude 客户端在这些块上的
// 缓存打点靠它往返保真;thinking 块的打点属罕见形态,当前不回放(已知边界)。
func rawBlockCacheControl(raw any) any {
	if blocks, ok := raw.(map[string]any); ok {
		if cc, present := blocks["cache_control"]; present && cc != nil {
			return cc
		}
	}
	return nil
}

func jsonRawToAny(raw json.RawMessage) any {
	var out any
	if len(raw) > 0 && decodeWireJSON(raw, &out) == nil {
		return out
	}
	return map[string]any{}
}

func intValue(value any) int {
	number, ok := numberValue(value)
	if !ok {
		return 0
	}
	return int(number)
}

func firstNonNilMap(values ...map[string]any) map[string]any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if values[key] != nil {
			return values[key]
		}
	}
	return nil
}
