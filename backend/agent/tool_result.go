package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/internal/jsonutil"
)

const (
	summaryModelLimit        = 2000
	invalidToolResultSummary = "工具返回的数据不是合法 JSON，未重试已执行的工具"
)

func rejectToolResult(info ToolResultInfo) ToolResultInfo {
	info.OK = false
	info.Summary = invalidToolResultSummary
	info.Data = json.RawMessage(`{"error":"invalid_tool_result"}`)
	info.SecretValues = nil
	info.RedactedData = nil
	return info
}

func validateToolResult(info ToolResultInfo) ToolResultInfo {
	if (len(info.Data) > 0 && !jsonutil.Valid(info.Data)) || (info.RedactedData != nil && !jsonutil.Valid(info.RedactedData)) {
		return rejectToolResult(info)
	}
	return info
}

// redactToolResult runs before truncation so a cut through a secret cannot
// defeat exact-value masking. This copy is shared by storage and model calls.
func redactToolResult(info ToolResultInfo) ToolResultInfo {
	if info.RedactedData != nil {
		info.Data = info.RedactedData
	}
	info.RedactedData = nil
	info.Input = MaskSecretInputs(info.Input)
	if len(info.SecretValues) > 0 {
		info.Summary = MaskSecretText(info.Summary, info.SecretValues)
		info.Data = MaskSecretData(info.Data, info.SecretValues)
	}
	info.SecretValues = nil
	return info
}

func limitToolResult(info ToolResultInfo, limit int, direction clampDirection) ToolResultInfo {
	info.Summary = clampSummary(info.Summary, summaryModelLimit)
	info.Data = clampJSON(info.Data, limit, direction)
	return info
}

// appendToolOutput 只把结果追加进对话（runOneTool 已落库发事件）。
func appendToolOutput(conversation *[]conversationTurn, info ToolResultInfo, limit int) {
	*conversation = append(*conversation, toolResultTurn(info, limit))
}

// appendToolResult 落库一条工具结果（脱敏 + 事件）并追加进对话——
// resume/deny 路径自己合成的结果走这里；runOneTool 已落库的结果只追加。
func (e *Engine) appendToolResult(ctx context.Context, sessionID string, conversation []conversationTurn, info ToolResultInfo, limit int, events chan Event) []conversationTurn {
	info = e.persistToolResult(ctx, sessionID, info, events)
	return append(conversation, toolResultTurn(info, limit))
}

// persistToolResult publishes the one-time client copy and returns only the
// redacted copy, including when storage fails. Callers use it for model history.
func (e *Engine) persistToolResult(ctx context.Context, sessionID string, info ToolResultInfo, events chan Event) ToolResultInfo {
	info = validateToolResult(info)
	direction := MetaOf(e.tools.Get(info.Name)).PreviewDirection
	stored := limitToolResult(redactToolResult(info), e.opts.ToolResultStoreLimit, direction)
	live := limitToolResult(info, e.opts.ToolResultStoreLimit, direction)
	live.Input = stored.Input
	live.SecretValues = nil
	live.RedactedData = nil
	seq, err := e.store.AppendMessage(ctx, sessionID, RoleToolResult, stored, "", nil)
	if err != nil {
		emitEvent(events, Event{Type: EventStatus, Text: fmt.Sprintf("工具结果落库失败: %v", err)})
		return stored
	}
	encoded, _ := sonic.ConfigStd.Marshal(stored)
	emitEvent(events, Event{Type: EventToolResult, CallID: info.CallID, Name: info.Name, Result: &live, Message: &Message{Seq: seq, Role: RoleToolResult, Content: encoded, CreatedAt: time.Now()}})
	return stored
}

func deniedToolResult(call FunctionCall, message string) ToolResultInfo {
	// 拒绝理由随 Data 一起回传给模型（ToolOutput 只含 Data），模型才能据此调整行为。
	encoded, _ := sonic.ConfigStd.Marshal(map[string]string{"error": "denied", "message": message})
	return ToolResultInfo{
		CallID:  call.ID,
		Name:    call.Name,
		Input:   call.Arguments,
		OK:      false,
		Summary: message,
		Data:    encoded,
	}
}

// clampJSON 在字节上限内截断超大 JSON 文档。截断产物必须是合法 JSON：
// 它会作为 json.RawMessage 嵌进消息持久化与发往模型的对话。direction 为
// tail 时保留尾部（日志类结果的结论通常在末尾），否则保留头部。
func clampJSON(raw json.RawMessage, limit int, direction clampDirection) json.RawMessage {
	if limit <= 0 || len(raw) <= limit {
		return raw
	}
	value := string(raw)
	cut := limit / 2
	var preview string
	if direction == ClampTail {
		start := len(value) - cut
		for start < len(value) && !utf8.RuneStart(value[start]) {
			start++
		}
		preview = value[start:]
	} else {
		end := cut
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		preview = value[:end]
	}
	envelope := map[string]any{
		"truncated": true,
		"bytes":     len(value),
		"omitted":   len(value) - len(preview),
		"direction": direction,
		"preview":   preview,
	}
	encoded, _ := sonic.ConfigStd.Marshal(envelope)
	return encoded
}

// clampSummary 截断回传模型与 UI 的工具摘要，避免单条说明撑爆上下文。
func clampSummary(summary string, limit int) string {
	if limit <= 0 || len([]rune(summary)) <= limit {
		return summary
	}
	return truncateRunes(summary, limit) + "（摘要已截断）"
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
