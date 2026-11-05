package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
)

const planStaleCalls = 8

// handleCallFailure 收尾一次失败的模型调用：取消路径下 result 可能带部分
// 聚合文本，照常落库保证可追溯；随后写入 system 错误记录并发出终态错误
// 事件（区分轮次停止/超时/上游失败的可重试性）。落库一律走 WithoutCancel
// ——Stop 取消 turnCtx 只应打断模型调用与工具执行，收尾写库（部分稿、
// 错误记录、状态回 idle）若也被取消，会话将永卡 running。
func (e *Engine) handleCallFailure(ctx context.Context, sessionID string, session *Session, handle *turnHandle, result *CallResult, err error, events chan Event) {
	finCtx := context.WithoutCancel(ctx)
	if result != nil && (result.Text != "" || result.Reasoning != "") {
		e.persistAssistant(finCtx, sessionID, session, AssistantContent{IsIncomplete: true, Content: result.Content, Text: result.Text, Reasoning: result.Reasoning}, result.Usage, events)
	}
	reason := "模型调用失败"
	retryable := true
	if ctx.Err() != nil {
		if handle.isStopped.Load() {
			reason, retryable = "轮次已停止", false
		} else {
			reason = "轮次超时"
		}
	}
	_, _ = e.store.AppendMessage(finCtx, sessionID, RoleSystem, SystemContent{Kind: "error", Text: fmt.Sprintf("%s: %v", reason, err)}, "", nil)
	e.setStatus(finCtx, sessionID, StatusIdle, false, events)
	failure := Event{Type: EventError, Text: fmt.Sprintf("%s: %v", reason, err), Retryable: retryable}
	var coder interface{ ReasonCode() string }
	if errors.As(err, &coder) {
		failure.ReasonCode = coder.ReasonCode()
	}
	emitTerminal(ctx, events, failure)
}

// modelLoop 运行「模型调用 → 工具执行」循环直到模型给出终稿正文、暂停审批、
// 出错或到达上限。
func (e *Engine) modelLoop(ctx context.Context, sessionID string, session *Session, handle *turnHandle, conversation []conversationTurn, started time.Time, events chan Event) {
	var usageTotal *protocol.Usage
	rounds := 0
	paused := false

	defer e.finalizeTurn(ctx, sessionID, session, started, &usageTotal, &rounds, &paused, events)

	maxModelCalls := e.opts.MaxModelCalls
	if e.opts.ModelCallLimit != nil {
		if limit := e.opts.ModelCallLimit(); limit > 0 {
			maxModelCalls = limit
		}
	}
	if session.Settings.MaxModelCalls > 0 {
		maxModelCalls = session.Settings.MaxModelCalls
	}
	for round := 0; round < maxModelCalls; round++ {
		if ctx.Err() != nil {
			return
		}
		rounds = round + 1
		// 方案陈旧计数在轮次入口推进（composeInstructions 保持纯函数，
		// 调试/预览复用它不会污染计数）。
		nudgePlan := advancePlanStaleRounds(session)
		emitEvent(events, Event{Type: EventStatus, Text: "正在调用模型…"})
		conversation = e.prepareContext(conversation, events)

		result, err := e.caller.Call(ctx, e.buildTurnRequest(session, conversation, nudgePlan), StreamCallbacks{
			OnText:      func(delta string) { emitEvent(events, Event{Type: EventTextDelta, Delta: delta}) },
			OnReasoning: func(delta string) { emitEvent(events, Event{Type: EventReasoningDelta, Delta: delta}) },
		})
		if result != nil && result.Usage != nil {
			// 个别协议轮次不回用量时保留既有累计，而非清空。
			var usageErr error
			if usageTotal == nil {
				usageTotal = protocol.MergeUsage(nil, result.Usage)
			} else {
				usageTotal, usageErr = addUsage(usageTotal, result.Usage)
			}
			if usageErr != nil && err == nil {
				err = usageErr
			}
		}
		if err != nil {
			e.handleCallFailure(ctx, sessionID, session, handle, result, err, events)
			return
		}
		if result == nil {
			// 契约上 err==nil 时 result 恒非 nil,但 StreamCaller 是接口——
			// 新实现返回 (nil, nil) 会在这里空指针 panic,显式走失败路径。
			e.handleCallFailure(ctx, sessionID, session, handle, nil, fmt.Errorf("模型调用返回空结果"), events)
			return
		}

		content := AssistantContent{Content: result.Content, Text: result.Text, Reasoning: result.Reasoning, ToolCalls: result.ToolCalls}
		e.persistAssistant(ctx, sessionID, session, content, result.Usage, events)

		if len(result.ToolCalls) == 0 {
			return // 终稿
		}
		conversation = append(conversation, assistantTurn(content))

		paused, err = e.executeCalls(ctx, sessionID, session, &conversation, result.ToolCalls, result.Text, nil, events)
		if err != nil {
			e.failTurn(ctx, sessionID, events, err.Error())
			paused = false
			return
		}
		if paused {
			return
		}
		if session.PlanReady {
			session.PlanReady = false
			if err := e.pauseForPlan(ctx, sessionID, session, result.Text, events); err != nil {
				e.failTurn(ctx, sessionID, events, err.Error())
				return
			}
			paused = true
			return
		}
	}

	// 到达循环上限：如实告知，等待用户指示。
	note := fmt.Sprintf("已达到单轮命令循环上限（%d 次模型调用），请检查命令结果或继续对话", maxModelCalls)
	_, _ = e.store.AppendMessage(ctx, sessionID, RoleSystem, SystemContent{Kind: "info", Text: note}, "", nil)
	emitEvent(events, Event{Type: EventStatus, Text: note})
}

// buildTurnRequest 组装一次模型调用：会话设置映射为请求参数 + 系统提示词。
func (e *Engine) buildTurnRequest(session *Session, conversation []conversationTurn, nudgePlan bool) CallRequest {
	req := CallRequest{
		Model:         session.Settings.ModelName,
		ModelSourceID: session.Settings.ModelSourceID,
		Content:       flattenConversation(conversation),
		Tools:         e.tools.Definitions(),
		Preferences:   protocol.AgentPreferences{ThinkingEnabled: session.Settings.ThinkingEnabled, ThinkingEffort: strings.ToLower(strings.TrimSpace(session.Settings.ThinkingEffort))},
	}
	req.Instructions = e.composeInstructions(session, nudgePlan)
	return req
}

// advancePlanStaleRounds 推进「方案清单连续未更新」计数（每轮模型调用一
// 次），返回本轮是否应提醒模型同步方案。update_plan 执行时计数归零。
func advancePlanStaleRounds(session *Session) bool {
	if len(session.Plan) == 0 {
		return false
	}
	session.PlanStaleRounds++
	if session.PlanStaleRounds >= planStaleCalls {
		session.PlanStaleRounds = 0
		return true
	}
	return false
}

// finalizeTurn 是 modelLoop 的统一收尾：panic 防护 + 未暂停时置回 idle 并发
// turn_done（含累计用量）。全部指针参数——defer 注册时求值会冻结布尔快照。
// 写库走 WithoutCancel：Stop/超时取消 turnCtx 后收尾（状态回 idle）仍要落库。
func (e *Engine) finalizeTurn(ctx context.Context, sessionID string, session *Session, started time.Time, usageTotal **protocol.Usage, rounds *int, paused *bool, events chan Event) {
	if r := recover(); r != nil {
		emitEvent(events, Event{Type: EventError, Text: e.reportTurnPanic(context.Background(), sessionID, r, "modelLoop"), Retryable: true})
	}
	if *paused {
		return
	}
	e.setStatus(context.WithoutCancel(ctx), sessionID, StatusIdle, false, events)
	event := Event{Type: EventTurnDone, DurationMs: time.Since(started).Milliseconds(), Rounds: *rounds, Model: session.Settings.ModelName}
	event.Usage = *usageTotal
	emitTerminal(ctx, events, event)
}

// composeInstructions 组装每次模型调用的系统提示词：宿主领域提示 + 方案
// 陈旧提醒 + 当前草稿状态块。纯函数——计数推进在 modelLoop 轮次入口。
func (e *Engine) composeInstructions(session *Session, nudgePlan bool) string {
	var b strings.Builder
	if e.prompt != nil {
		b.WriteString(e.prompt(session))
	}
	if nudgePlan {
		b.WriteString("\n\n方案清单已连续多轮未更新。若步骤状态有变化，请调用 update_plan 同步；没有变化就忽略这条提醒。")
	}
	if len(session.DraftConfig) > 0 {
		b.WriteString("\n\n## 当前工作草稿（`elysia protocol draft` 的最新产物，后续修改以它为基准）\n```json\n")
		b.Write(session.DraftConfig)
		b.WriteString("\n```")
	} else if session.ProtocolID != "" || session.Mode == ModeEdit {
		// 协议向会话才引导建草稿；通用运维会话不注入本节。
		b.WriteString("\n\n## 当前工作草稿\n（尚无草稿——用 `elysia protocol draft '<配置JSON>'` 生成第一版，工作流与字段目录见 `elysia help protocol draft`。）")
	}
	return b.String()
}

func (e *Engine) persistAssistant(ctx context.Context, sessionID string, session *Session, content AssistantContent, usage *protocol.Usage, events chan Event) {
	var usageJSON json.RawMessage
	if usage != nil {
		if encoded, err := sonic.ConfigStd.Marshal(usage); err == nil {
			usageJSON = encoded
		}
	}
	seq, err := e.store.AppendMessage(ctx, sessionID, RoleAssistant, content, session.Settings.ModelName, usageJSON)
	if err != nil {
		emitEvent(events, Event{Type: EventStatus, Text: fmt.Sprintf("助手消息落库失败: %v", err)})
		return
	}
	encoded, _ := sonic.ConfigStd.Marshal(content)
	emitEvent(events, Event{Type: EventMessage, Message: &Message{Seq: seq, Role: RoleAssistant, Content: encoded, Model: session.Settings.ModelName, Usage: usageJSON, CreatedAt: time.Now()}})
}
