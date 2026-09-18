package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CustomProtocolMatch 是协议定义中的通用条件原语：在事件载荷（或请求上下文）
// 上按点路径取值，与任意类型的预期值做类型化比较。流终止判定
// （finishWhen/statusWhen）、谓词帧（frame.match）、请求叶子条件（when）、
// 数组元素过滤（textFilter）共用这一套语义。
//
// op 语义（缺省 nonEmpty）：
//   - nonEmpty / isEmpty：值存在且非 null；字符串需非空白，false/0/[]/{} 视为空
//   - equals / notEquals：与 value 类型化相等（数字按数值、布尔按布尔、
//     对象/数组按规范化 JSON 文本）
//   - in / notIn：value 须为数组，成员类型化包含
//   - contains：值为字符串且包含 value 字符串
//   - isNull / notNull：缺失或 JSON null 算 null
//   - isTrue / isFalse：布尔判定；缺失按 false（isFalse 对缺失亦为真）
//   - gt / gte / lt / lte：数值比较（字符串数字可解析则参与）
type CustomProtocolMatch struct {
	Path  string          `json:"path"`
	Op    string          `json:"op,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

const (
	MatchOpNonEmpty  = "nonEmpty"
	MatchOpIsEmpty   = "isEmpty"
	MatchOpEquals    = "equals"
	MatchOpNotEquals = "notEquals"
	MatchOpIn        = "in"
	MatchOpNotIn     = "notIn"
	MatchOpContains  = "contains"
	MatchOpIsNull    = "isNull"
	MatchOpNotNull   = "notNull"
	MatchOpIsTrue    = "isTrue"
	MatchOpIsFalse   = "isFalse"
	MatchOpGt        = "gt"
	MatchOpGte       = "gte"
	MatchOpLt        = "lt"
	MatchOpLte       = "lte"
)

var customMatchOps = map[string]struct{}{
	MatchOpNonEmpty: {}, MatchOpIsEmpty: {}, MatchOpEquals: {}, MatchOpNotEquals: {},
	MatchOpIn: {}, MatchOpNotIn: {}, MatchOpContains: {}, MatchOpIsNull: {}, MatchOpNotNull: {},
	MatchOpIsTrue: {}, MatchOpIsFalse: {}, MatchOpGt: {}, MatchOpGte: {}, MatchOpLt: {}, MatchOpLte: {},
}

// 需要 value 的操作符。
func matchOpNeedsValue(op string) bool {
	switch op {
	case MatchOpEquals, MatchOpNotEquals, MatchOpIn, MatchOpNotIn, MatchOpContains,
		MatchOpGt, MatchOpGte, MatchOpLt, MatchOpLte:
		return true
	}
	return false
}

// validateCustomProtocolMatch 校验条件原语：路径合法、操作符已知、需要 value
// 的操作符必须携带且类型正确（in/notIn 须为数组）。
func validateCustomProtocolMatch(location string, match CustomProtocolMatch) error {
	if strings.TrimSpace(match.Path) == "" {
		return fmt.Errorf("%s.path is required", location)
	}
	if _, err := parseCustomPath(match.Path); err != nil {
		return fmt.Errorf("%s.path: %w", location, err)
	}
	op := strings.TrimSpace(match.Op)
	if op == "" {
		op = MatchOpNonEmpty
	}
	if _, ok := customMatchOps[op]; !ok {
		return fmt.Errorf("%s.op %q is unsupported", location, match.Op)
	}
	if !matchOpNeedsValue(op) {
		return nil
	}
	if len(match.Value) == 0 {
		return fmt.Errorf("%s.op %q requires value", location, op)
	}
	parsed, err := decodeJSONUseNumber(match.Value)
	if err != nil {
		return fmt.Errorf("%s.value is not valid JSON: %w", location, err)
	}
	if op == MatchOpIn || op == MatchOpNotIn {
		if _, ok := parsed.([]any); !ok {
			return fmt.Errorf("%s.op %q requires value to be an array", location, op)
		}
	}
	return nil
}

// CustomProtocolMatchSet 兼容单条件对象与条件数组：数组语义为全部成立（AND），
// 供 textFilter/reasoningFilter 等需要复合谓词的场合（如 Gemini 的
// 「非 thought 且非 functionCall」正文过滤）。
type CustomProtocolMatchSet []CustomProtocolMatch

// UnmarshalJSON 接受单个 Match 对象或 Match 数组。
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

// eval 在载荷根上求值：全部条件成立才为真；空集恒真。
func (set CustomProtocolMatchSet) evalAll(root any) bool {
	for _, match := range set {
		if !customMatchEval(root, match) {
			return false
		}
	}
	return true
}

// customMatchValue 把 JSON 原文解析为 UseNumber 的 any（比较前统一口径）。
func customMatchValue(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	parsed, err := decodeJSONUseNumber(raw)
	return parsed, err == nil
}

// customMatchEval 在载荷根上求值条件。root 为 nil 时仅 isNull/isEmpty 族为真。
func customMatchEval(root any, match CustomProtocolMatch) bool {
	value, present := customLookupPath(root, match.Path)
	op := strings.TrimSpace(match.Op)
	if op == "" {
		op = MatchOpNonEmpty
	}
	if op == MatchOpIsNull {
		return !present || value == nil
	}
	if op == MatchOpNotNull {
		return present && value != nil
	}
	if !present || value == nil {
		// 缺失/null：除 isFalse（缺失按 false）外全部不成立。
		return op == MatchOpIsFalse
	}
	expected, hasExpected := customMatchValue(match.Value)
	switch op {
	case MatchOpNonEmpty:
		return !customMatchEmpty(value)
	case MatchOpIsEmpty:
		return customMatchEmpty(value)
	case MatchOpEquals:
		return hasExpected && customJSONValuesEqual(value, expected)
	case MatchOpNotEquals:
		return hasExpected && !customJSONValuesEqual(value, expected)
	case MatchOpIn, MatchOpNotIn:
		list, ok := expected.([]any)
		member := false
		if ok {
			for _, item := range list {
				if customJSONValuesEqual(value, item) {
					member = true
					break
				}
			}
		}
		if op == MatchOpIn {
			return member
		}
		return !member
	case MatchOpContains:
		if !hasExpected {
			return false
		}
		return strings.Contains(customValueString(value), customValueString(expected))
	case MatchOpIsTrue:
		_, ok := value.(bool)
		return ok && value.(bool)
	case MatchOpIsFalse:
		truthy, ok := value.(bool)
		return !ok || !truthy
	case MatchOpGt, MatchOpGte, MatchOpLt, MatchOpLte:
		actual, ok1 := customMatchNumber(value)
		bound, ok2 := customMatchNumber(expected)
		if !ok1 || !ok2 {
			return false
		}
		switch op {
		case MatchOpGt:
			return actual > bound
		case MatchOpGte:
			return actual >= bound
		case MatchOpLt:
			return actual < bound
		default:
			return actual <= bound
		}
	}
	return false
}

// customMatchEmpty 定义 Match 语境下的「空」：空串/空白、false、0、空数组、
// 空对象。与 legacy customEmptyValue（false/0 非空）不同——Match 未配置时
// 走 legacy 代码路径，配置后按本语义，用户显式选择即显式表达。
func customMatchEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case bool:
		return !typed
	case json.Number:
		number, err := typed.Float64()
		return err == nil && number == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

// customJSONValuesEqual 类型化相等：数字按数值、布尔/字符串严格、
// 对象/数组按序列化文本（Go map 序列化键有序，可作规范形）。
func customJSONValuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch left := a.(type) {
	case json.Number:
		if right, ok := b.(json.Number); ok {
			lf, lerr := left.Float64()
			rf, rerr := right.Float64()
			if lerr == nil && rerr == nil {
				return lf == rf
			}
			return left.String() == right.String()
		}
		return false
	case string:
		right, ok := b.(string)
		return ok && left == right
	case bool:
		right, ok := b.(bool)
		return ok && left == right
	case []any, map[string]any:
		leftJSON, lerr := json.Marshal(a)
		rightJSON, rerr := json.Marshal(b)
		return lerr == nil && rerr == nil && bytes.Equal(leftJSON, rightJSON)
	}
	return false
}

// customMatchNumber 提取数值：json.Number 直接取，字符串可解析则解析。
func customMatchNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	}
	return 0, false
}
