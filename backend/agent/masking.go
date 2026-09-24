package agent

import (
	"regexp"
	"strings"

	"github.com/elysia-api/backend/relay"
)

// MaskedPendingAction 复制待批快照并遮盖调用参数里的密钥类字段，供会话
// 视图与 SSE 事件使用。落库的 PendingAction 保持原文（批准后要按原参数
// 执行），只有 SSE 事件与对外视图走这份副本。Kind/Question/Plan 不含
// 密钥，原样带上——前端靠它们区分审批卡 / 提问卡 / 方案确认卡。
func MaskedPendingAction(pending *PendingAction) *PendingAction {
	if pending == nil {
		return nil
	}
	masked := &PendingAction{Kind: pending.Kind, Reason: pending.Reason, Calls: make([]relay.MaheshvaraToolCall, len(pending.Calls))}
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
			// bash 类工具的 command 字段：命令行里的敏感 flag 值单独打码
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

// cliSecretFlagPattern 匹配命令行中的敏感 flag 及其值（--api-key、
// --secret、--new-secret、--token；--key 是用量过滤的 Key 名，非密钥，
// 刻意不在列）。值支持引号包裹或裸串。
var cliSecretFlagPattern = regexp.MustCompile(
	`(?i)(--(?:api-key|secret|new-secret|token)(?:=|\s+))("[^"]*"|'[^']*'|[^\s&;|]+)`)

// RedactCommandLine 把命令行中敏感 flag 的值替换为 ***。审批卡说明、
// CLI 回显等一切会把命令行文本带出执行路径的出口共用此函数——密钥值
// 只允许留在落库原文里（批准后按原文执行）。
func RedactCommandLine(command string) string {
	return cliSecretFlagPattern.ReplaceAllString(command, "${1}***")
}
