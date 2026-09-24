package agent

import (
	"regexp"
	"strings"

	"github.com/elysia-api/backend/relay"
)

// MaskedPendingAction 复制待批快照并遮盖调用参数里的密钥类字段，供会话
// 视图与 SSE 事件使用。
func MaskedPendingAction(pending *PendingAction) *PendingAction {
	return maskedPendingAction(pending)
}

// maskedPendingAction 复制待批快照并遮盖调用参数里的密钥类字段。
// 落库的 PendingAction 保持原文（批准后要按原参数执行），只有 SSE 事件
// 与对外视图走这份副本。Kind/Question/Plan 不含密钥，原样带上——前端
// 靠它们区分审批卡 / 提问卡 / 方案确认卡。
func maskedPendingAction(pending *PendingAction) *PendingAction {
	if pending == nil {
		return nil
	}
	masked := &PendingAction{Kind: pending.Kind, Reason: pending.Reason, Calls: make([]relay.MaheshvaraToolCall, len(pending.Calls))}
	for index, call := range pending.Calls {
		masked.Calls[index] = call
		masked.Calls[index].Arguments = maskSecretInputs(call.Arguments)
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

func isSecretInputKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "apikey") || strings.Contains(lower, "api_key") ||
		lower == "token" || strings.Contains(lower, "secret") || strings.Contains(lower, "password") ||
		strings.Contains(lower, "authorization") || strings.Contains(lower, "credential")
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
