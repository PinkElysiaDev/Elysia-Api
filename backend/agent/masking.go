package agent

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/internal/clishell"
	"github.com/elysia-api/backend/internal/jsonutil"
)

// MaskedPendingAction 复制待批快照并遮盖调用参数里的密钥类字段，供会话
// 视图与 SSE 事件使用。落库的 PendingAction 保持原文（批准后要按原参数
// 执行），只有 SSE 事件与对外视图走这份副本。Kind/Question/Plan 不含
// 密钥，原样带上——前端靠它们区分审批卡 / 提问卡 / 方案确认卡。
func MaskedPendingAction(pending *PendingAction) *PendingAction {
	if pending == nil {
		return nil
	}
	masked := &PendingAction{Kind: pending.Kind, Reason: pending.Reason, Calls: make([]FunctionCall, len(pending.Calls))}
	for index, call := range pending.Calls {
		masked.Calls[index] = call
		masked.Calls[index].Arguments = MaskSecretInputs(call.Arguments)
	}
	if pending.Question != nil {
		question := *pending.Question
		question.Options = append([]AskOption(nil), pending.Question.Options...)
		masked.Question = &question
	}
	if pending.Plan != nil {
		masked.Plan = append([]PlanStep(nil), pending.Plan...)
	}
	masked.PlanSummary = pending.PlanSummary
	return masked
}

func maskSecretValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if text, isString := item.(string); isString && text != "" && isSecretInputKey(key) {
				typed[key] = "***"
				continue
			}
			// elysia_cli 类工具的 command 字段：命令行里的敏感 flag 值单独打码
			//（键名本身不含 secret 词根，通用规则拦不到）。
			if key == "command" {
				if text, isString := item.(string); isString {
					typed[key] = RedactCommandLine(text)
					continue
				}
			}
			typed[key] = maskSecretValue(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = maskSecretValue(item)
		}
		return typed
	default:
		return value
	}
}

// secretKeyMarkers 按子串匹配键名；secretKeyExact 按全等匹配。"token" 刻意
// 用全等而非子串：max_tokens / prompt_tokens 等计数字段含 "token" 子串，
// 子串匹配会把它们误打码。
var (
	secretKeyMarkers = []string{"apikey", "api_key", "secret", "password", "authorization", "credential"}
	secretKeyExact   = map[string]bool{"token": true}
)

func isSecretInputKey(key string) bool {
	lower := strings.ToLower(key)
	if secretKeyExact[lower] {
		return true
	}
	for _, marker := range secretKeyMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// RedactCommandLine 把命令行中敏感 flag 的值替换为 ***。审批卡说明、
// CLI 回显等一切会把命令行文本带出执行路径的出口共用此函数——密钥值
// 只允许留在落库原文里（批准后按原文执行）。
func RedactCommandLine(command string) string {
	return clishell.Redact(command)
}

// secretValueMask 是按值精确打码的替换文本。
const secretValueMask = "***"

// MaskSecretText 把文本里出现的已知明文密钥值替换为 ***（值来自工具自身
// 产物，见 ToolResult.SecretValues——精确按值匹配，不会误伤其它内容）。
func MaskSecretText(text string, secrets []string) string {
	type span struct{ start, end int }
	var matches []span
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for offset := 0; offset < len(text); {
			index := strings.Index(text[offset:], secret)
			if index < 0 {
				break
			}
			start := offset + index
			matches = append(matches, span{start, start + len(secret)})
			offset = start + 1
		}
	}
	if len(matches) == 0 {
		return text
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].start < matches[j].start })
	var masked strings.Builder
	previous, current := 0, matches[0]
	for _, next := range matches[1:] {
		if next.start <= current.end {
			current.end = max(current.end, next.end)
			continue
		}
		masked.WriteString(text[previous:current.start])
		masked.WriteString(secretValueMask)
		previous, current = current.end, next
	}
	masked.WriteString(text[previous:current.start])
	masked.WriteString(secretValueMask)
	masked.WriteString(text[current.end:])
	return masked.String()
}

// MaskSecretData masks declared secrets without rounding JSON numbers. Invalid
// JSON is replaced with an error envelope, never returned as unredacted text.
func MaskSecretData(raw []byte, secrets []string) []byte {
	return maskJSON(raw, func(value any) any { return maskSecretTextInValue(value, secrets) })
}

// MaskSecretInputs removes credentials from display and persisted arguments.
// Execution retains the separate original arguments for approval and tools.
func MaskSecretInputs(raw json.RawMessage) json.RawMessage {
	return maskJSON(raw, maskSecretValue)
}

func maskJSON(raw json.RawMessage, mask func(any) any) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	if !jsonutil.Valid(raw) {
		return json.RawMessage(`{"error":"invalid_tool_json"}`)
	}
	decoder := sonic.ConfigStd.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return json.RawMessage(`{"error":"invalid_tool_json"}`)
	}
	// The decoder produces only JSON values; masking changes strings alone.
	encoded, _ := sonic.ConfigStd.Marshal(mask(value))
	return encoded
}

func maskSecretTextInValue(value any, secrets []string) any {
	switch typed := value.(type) {
	case string:
		return MaskSecretText(typed, secrets)
	case map[string]any:
		for key, item := range typed {
			typed[key] = maskSecretTextInValue(item, secrets)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = maskSecretTextInValue(item, secrets)
		}
		return typed
	default:
		return value
	}
}
