package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
)

const titleMaxRunes = 24

// UserContentRenderer builds ordered semantic content, independent of the
// target protocol. Wire adapters own provider-specific media serialization.
type UserContentRenderer interface {
	RenderUserContent(meta SessionMeta, content *UserContent) ([]protocol.Node, error)
}

// TextUserContentRenderer accepts text and rejects unsupported attachments.
type TextUserContentRenderer struct{}

func (TextUserContentRenderer) RenderUserContent(meta SessionMeta, content *UserContent) ([]protocol.Node, error) {
	if len(content.Documents) > 0 {
		return nil, fmt.Errorf("text content renderer cannot represent attachments")
	}
	if content.Text == "" {
		return nil, nil
	}
	return []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(content.Text)}}, nil
}

// conversationTurn keeps persistence and compaction boundaries without
// creating a second message/tool representation for protocol conversion.
type conversationTurn struct {
	Role    string
	Content []protocol.Node
}

func textTurn(role, text string) conversationTurn {
	return conversationTurn{Role: role, Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue(role), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(text)}}}}}
}

func flattenConversation(conversation []conversationTurn) []protocol.Node {
	var content []protocol.Node
	for _, turn := range conversation {
		content = append(content, turn.Content...)
	}
	return content
}

func assistantTurn(content AssistantContent) conversationTurn {
	return conversationTurn{Role: RoleAssistant, Content: content.Content}
}

// toolResultTurn consumes the checked, redacted result from persistence or replay.
func toolResultTurn(info ToolResultInfo, limit int) conversationTurn {
	// The Agent owns this result envelope. Target protocols declare whether its
	// JSON value is submitted as an object or serialized text.
	fields := map[string]any{"ok": info.OK, "summary": info.Summary}
	if len(info.Data) > 0 {
		fields["data"] = clampJSON(info.Data, limit, ClampHead)
	}
	encoded, _ := sonic.ConfigStd.Marshal(fields) // Data has been validated at tool/persistence boundaries.
	payload, _ := protocol.ParseValue(encoded)
	return conversationTurn{Role: "tool", Content: []protocol.Node{{Kind: protocol.ToolResultNode, CallID: protocol.StringValue(info.CallID), Name: protocol.StringValue(info.Name), Payload: payload,
		Source: &protocol.Provenance{Protocol: protocol.AgentIdentity(), Direction: protocol.EncodeRequest}}}}
}

// prepareTurnHistory closes superseded calls before adding the next user message.
// Failed tool-result writes retain the existing redacted in-memory model copy.
func (e *Engine) prepareTurnHistory(ctx context.Context, session *Session, input *UserContent, pending *PendingAction, events chan Event) ([]conversationTurn, error) {
	if pending == nil {
		if _, err := e.appendUserMessage(ctx, session.ID, session, input, events); err != nil {
			return nil, err
		}
	}
	conversation, seqs, err := e.loadConversation(ctx, session)
	if err != nil {
		return nil, err
	}
	// Retire the actionable approval before publishing synthetic receipts. A
	// failed retirement must not start a new model turn or execute the old calls.
	if pending != nil {
		if err := e.store.UpdateSessionState(ctx, session.ID, SessionStateUpdate{ClearPending: true}); err != nil {
			return nil, fmt.Errorf("清除已替代审批失败: %w", err)
		}
	}
	// Only loaded history has durable sequence boundaries. Supersession adds
	// its cancellation receipts and new message after this one summary pass.
	conversation = e.maybeSummarize(ctx, session.ID, session, conversation, seqs, events)
	if pending == nil {
		return conversation, nil
	}
	for _, call := range pending.Calls {
		info := deniedToolResult(call, "新轮次已替代待批操作；该调用未执行，如仍需要请重新发起")
		conversation = e.appendToolResult(ctx, session.ID, conversation, info, e.opts.ToolResultModelLimit, events)
	}
	seq, err := e.appendUserMessage(ctx, session.ID, session, input, events)
	if err != nil {
		return nil, err
	}
	if seq != 0 {
		message, err := e.renderUserTurn(session, input)
		if err != nil {
			return nil, fmt.Errorf("用户消息 #%d 渲染失败: %w", seq, err)
		}
		if len(message.Content) > 0 {
			conversation = append(conversation, message)
		}
	}
	return conversation, nil
}

// appendUserMessage persists and publishes user input. A zero sequence means
// no message was added; the first text also supplies an untitled session name.
func (e *Engine) appendUserMessage(ctx context.Context, sessionID string, session *Session, input *UserContent, events chan Event) (int, error) {
	if input == nil || (strings.TrimSpace(input.Text) == "" && len(input.Documents) == 0) {
		return 0, nil
	}
	seq, err := e.store.AppendMessage(ctx, sessionID, RoleUser, *input, "", nil)
	if err != nil {
		return 0, fmt.Errorf("写入用户消息失败: %w", err)
	}
	if encoded, err := sonic.ConfigStd.Marshal(*input); err == nil {
		emitEvent(events, Event{Type: EventMessage, Message: &Message{Seq: seq, Role: RoleUser, Content: encoded, CreatedAt: time.Now()}})
	}
	if strings.TrimSpace(session.Title) == "" && strings.TrimSpace(input.Text) != "" {
		title := truncateRunes(strings.TrimSpace(input.Text), titleMaxRunes)
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{Title: title}); err == nil {
			session.Title = title
		}
	}
	return seq, nil
}

func (e *Engine) renderUserTurn(session *Session, content *UserContent) (conversationTurn, error) {
	parts, err := e.render.RenderUserContent(metaFromSession(session), content)
	if err != nil || len(parts) == 0 {
		return conversationTurn{}, err
	}
	return conversationTurn{Role: RoleUser, Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue(RoleUser), Children: parts}}}, nil
}

// loadConversation 把持久化消息重建为发送对话。seqs 与 conversation 元素
// 一一对应（来源消息的 seq；摘要边界产生的合成首条为 0），供摘要压缩记录
// 覆盖边界。
func (e *Engine) loadConversation(ctx context.Context, session *Session) ([]conversationTurn, []int, error) {
	messages, err := e.store.ListMessages(ctx, session.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("读取会话消息失败: %w", err)
	}
	messages = applySummaryBoundary(messages)
	conversation := make([]conversationTurn, 0, len(messages))
	seqs := make([]int, 0, len(messages))
	add := func(seq int, message conversationTurn) {
		conversation = append(conversation, message)
		seqs = append(seqs, seq)
	}
	for _, message := range messages {
		switch message.Role {
		case RoleUser:
			var content UserContent
			if err := sonic.ConfigStd.Unmarshal(message.Content, &content); err != nil {
				return nil, nil, fmt.Errorf("用户消息 #%d 解析失败: %w", message.Seq, err)
			}
			turn, err := e.renderUserTurn(session, &content)
			if err != nil {
				return nil, nil, fmt.Errorf("用户消息 #%d 渲染失败: %w", message.Seq, err)
			}
			if len(turn.Content) > 0 {
				add(message.Seq, turn)
			}
		case RoleAssistant:
			var content AssistantContent
			if err := sonic.ConfigStd.Unmarshal(message.Content, &content); err != nil {
				return nil, nil, fmt.Errorf("助手消息 #%d 解析失败: %w", message.Seq, err)
			}
			// Interrupted provider output remains visible in the transcript, but
			// cannot become valid signed reasoning or executable tool history.
			if content.IsIncomplete {
				continue
			}
			if content.Content == nil {
				return nil, nil, fmt.Errorf("assistant message #%d is missing semantic content", message.Seq)
			}
			add(message.Seq, assistantTurn(content))
		case RoleToolResult:
			var info ToolResultInfo
			if err := sonic.ConfigStd.Unmarshal(message.Content, &info); err != nil {
				return nil, nil, fmt.Errorf("工具结果 #%d 解析失败: %w", message.Seq, err)
			}
			info = redactToolResult(validateToolResult(info))
			// 历史回放统一用默认上限而非 per-tool 预算：落库内容本已过
			// Store 截断，且跨版本升级后旧工具可能已不在注册表（查不到
			// MaxModelBytes），按名字回退默认值是稳妥口径。
			add(message.Seq, toolResultTurn(info, e.opts.ToolResultModelLimit))
		}
	}
	return conversation, seqs, nil
}
