package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/elysia-api/backend/relay"
)

// Engine 是领域无关的 Agent 执行引擎：模型流式调用 → 工具分发 → 门控暂停/
// 审批恢复，全部状态经 Store 持久化，事件经 channel 流出（SSE 编码在宿主）。
//
// 生命周期：一个会话同一时刻至多一个活动轮次（内存锁强制）；轮次脱离 HTTP
// 请求上下文运行——浏览器断开后轮次继续，结果落库，回来即可见；Stop 是唯一
// 的中途取消途径（另有 TurnTimeout 兜底）。
type Engine struct {
	caller StreamCaller
	store  Store
	tools  *Registry
	render UserContentRenderer
	prompt func(*Session) string
	opts   Options

	mu      sync.Mutex
	running map[string]*turnHandle
}

// Options 引擎行为参数（零值字段取默认）。
type Options struct {
	MaxModelCalls int           // 单轮最大模型调用次数（工具循环上限）
	TurnTimeout   time.Duration // 单轮总超时
	EventBuffer   int           // 事件 channel 缓冲
	// ToolResultModelLimit 回传模型的工具结果字节上限（超出截断）。
	ToolResultModelLimit int
	// ToolResultStoreLimit 持久化的工具结果字节上限（超出截断）。
	ToolResultStoreLimit int
}

func (o Options) withDefaults() Options {
	if o.MaxModelCalls <= 0 {
		o.MaxModelCalls = 12
	}
	if o.TurnTimeout <= 0 {
		o.TurnTimeout = 5 * time.Minute
	}
	if o.EventBuffer <= 0 {
		o.EventBuffer = 256
	}
	if o.ToolResultModelLimit <= 0 {
		o.ToolResultModelLimit = 16 << 10
	}
	if o.ToolResultStoreLimit <= 0 {
		o.ToolResultStoreLimit = 64 << 10
	}
	return o
}

// UserContentRenderer 把用户消息（含附件）渲染为 Maheshvara 内容块。
// 平台差异（如文档 part 的 data URL vs 裸 base64）由宿主实现处理——引擎
// 不知道目标平台。
type UserContentRenderer interface {
	RenderUserContent(meta SessionMeta, content *UserContent) ([]relay.MaheshvaraContentPart, error)
}

// TextUserContentRenderer 是最简渲染器：纯文本，不处理附件（兜底用）。
type TextUserContentRenderer struct{}

func (TextUserContentRenderer) RenderUserContent(meta SessionMeta, content *UserContent) ([]relay.MaheshvaraContentPart, error) {
	if content.Text == "" {
		return nil, nil
	}
	return []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: content.Text}}, nil
}

// ApprovalDecision 是用户对门控动作的裁决。
type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	BaseURL  string `json:"baseUrl,omitempty"` // 允许时可补充/更新测试目标
	APIKey   string `json:"apiKey,omitempty"`
	Note     string `json:"note,omitempty"`
}

type turnHandle struct {
	cancel  context.CancelFunc
	done    chan struct{}
	stopped atomic.Bool
}

// NewEngine 装配引擎。systemPrompt 由宿主提供（领域知识），可为 nil
// （无系统提示词）。
func NewEngine(caller StreamCaller, store Store, tools *Registry, render UserContentRenderer, systemPrompt func(*Session) string, opts Options) *Engine {
	if render == nil {
		render = TextUserContentRenderer{}
	}
	return &Engine{
		caller:  caller,
		store:   store,
		tools:   tools,
		render:  render,
		prompt:  systemPrompt,
		opts:    opts.withDefaults(),
		running: make(map[string]*turnHandle),
	}
}

// IsRunning 报告会话是否有活动轮次。
func (e *Engine) IsRunning(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, running := e.running[sessionID]
	return running
}

func (e *Engine) begin(sessionID string, timeout time.Duration) (context.Context, *turnHandle, context.CancelFunc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.running[sessionID]; exists {
		return nil, nil, nil, ErrSessionRunning
	}
	// turnCtx/cancel 在持锁段内构造并挂到 handle 再发布：旧实现先发布后无锁写
	// handle.cancel，Stop 无锁读——数据竞争之外，Stop 在赋值前读到 nil 会
	// 「假成功」，轮次照常跑完。
	turnCtx, cancel := context.WithTimeout(context.Background(), timeout)
	handle := &turnHandle{done: make(chan struct{}), cancel: cancel}
	e.running[sessionID] = handle
	return turnCtx, handle, cancel, nil
}

func (e *Engine) end(sessionID string, handle *turnHandle) {
	e.mu.Lock()
	if e.running[sessionID] == handle {
		delete(e.running, sessionID)
	}
	e.mu.Unlock()
	select {
	case <-handle.done:
	default:
		close(handle.done)
	}
}

// Stop 取消会话的进行中轮次（等待收尾）。返回是否有轮次被取消。
func (e *Engine) Stop(sessionID string) bool {
	e.mu.Lock()
	handle := e.running[sessionID]
	e.mu.Unlock()
	if handle == nil {
		return false
	}
	handle.stopped.Store(true)
	if handle.cancel != nil {
		handle.cancel()
	}
	select {
	case <-handle.done:
	case <-time.After(stopDrainWait):
	}
	return true
}

// RunTurn 开始一个新轮次。input 非 nil 时先追加用户消息；input 为 nil 表示
// 从既有历史继续（重新生成）。返回的事件 channel 在轮次结束后关闭。
func (e *Engine) RunTurn(ctx context.Context, sessionID string, input *UserContent) (<-chan Event, error) {
	_ = ctx // 参数为 API 对称保留；轮次脱离调用方 ctx 运行（SSE 断开不终止，结果照常落库）
	turnCtx, handle, cancel, err := e.begin(sessionID, e.opts.TurnTimeout)
	if err != nil {
		return nil, err
	}
	events := make(chan Event, e.opts.EventBuffer)
	go func() {
		defer e.end(sessionID, handle)
		defer cancel()
		defer close(events)
		e.startTurn(turnCtx, sessionID, handle, input, nil, ApprovalDecision{}, events)
	}()
	return events, nil
}

// ResumeApproval 恢复一个等待审批的轮次：执行或拒绝待定动作，然后继续
// 模型循环。
func (e *Engine) ResumeApproval(ctx context.Context, sessionID string, decision ApprovalDecision) (<-chan Event, error) {
	if ctx == nil {
		ctx = context.Background() // 仅测试直传 nil 时触发；HTTP 路径恒非 nil
	}
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != StatusWaitingApproval || session.PendingAction == nil || len(session.PendingAction.Calls) == 0 {
		return nil, ErrNoPendingApproval
	}
	turnCtx, handle, cancel, err := e.begin(sessionID, e.opts.TurnTimeout)
	if err != nil {
		return nil, err
	}

	pending := session.PendingAction
	events := make(chan Event, e.opts.EventBuffer)
	go func() {
		defer e.end(sessionID, handle)
		defer cancel()
		defer close(events)
		e.startTurn(turnCtx, sessionID, handle, nil, pending, decision, events)
	}()
	return events, nil
}

// emitEvent 非阻塞发送瞬态事件（缓冲满则丢弃——消息本身会持久化，丢增量无损）。
func emitEvent(events chan<- Event, event Event) {
	select {
	case events <- event:
	default:
	}
}

// 引擎内联时限与长度的具名锚点。
const (
	stopDrainWait     = 15 * time.Second // Stop 等待轮次收尾的窗口
	terminalEmitWait  = 5 * time.Second  // 终态事件尽力送达窗口
	titleMaxRunes     = 24               // 会话标题截断长度
	previewHalfFactor = 2                // clampJSON 预览占限额的分之一
)

// emitTerminal 发送终态事件：尽力送达（5s 窗口），随后 channel 将被关闭。
func emitTerminal(events chan<- Event, event Event) {
	select {
	case events <- event:
	case <-time.After(terminalEmitWait):
	}
}

// startTurn 是轮次统一入口：新消息或审批恢复 → 模型循环 → 收尾。
func (e *Engine) startTurn(ctx context.Context, sessionID string, handle *turnHandle, input *UserContent, resume *PendingAction, decision ApprovalDecision, events chan Event) {
	// 整个轮次主体的 panic 防护（modelLoop 内已有 recover，这里覆盖其余
	// 部分——如历史渲染/审批记录写入）：置回 idle 并发终态事件，避免进程
	// 崩溃、以及会话在 DB 里永卡 running。
	defer func() {
		if r := recover(); r != nil {
			emitTerminal(events, Event{Type: EventError, Text: fmt.Sprintf("引擎异常: %v", r), Retryable: true})
			e.setStatus(context.Background(), sessionID, StatusIdle, true, events)
			emitTerminal(events, Event{Type: EventTurnDone})
		}
	}()
	started := time.Now()
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		emitTerminal(events, Event{Type: EventError, Text: fmt.Sprintf("读取会话失败: %v", err)})
		return
	}
	// 新轮次作废旧审批；审批恢复路径随后自行清理。
	e.setStatus(ctx, sessionID, StatusRunning, resume == nil, events)

	// 新轮次开始前快照当前草稿：还原点单槽覆盖，用户可回滚到上一轮修改前。
	if resume == nil && len(session.DraftConfig) > 0 {
		snapshot := append(json.RawMessage(nil), session.DraftConfig...)
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{DraftRestore: snapshot}); err == nil {
			session.DraftRestore = snapshot
		}
	}

	if e.appendUserMessage(ctx, sessionID, session, input, events) {
		return
	}

	conversation, err := e.loadConversation(ctx, session)
	if err != nil {
		e.failTurn(ctx, sessionID, events, err.Error())
		return
	}

	// 审批恢复：记录裁决、（可选）补充测试凭证、执行或拒绝待定动作。
	if resume != nil {
		var paused bool
		conversation, paused, err = e.resumeApprovalPrefix(ctx, sessionID, session, resume, decision, conversation, events)
		if err != nil {
			e.failTurn(ctx, sessionID, events, err.Error())
			return
		}
		if paused {
			return
		}
	}

	e.modelLoop(ctx, sessionID, session, handle, conversation, started, events)
}

// appendUserMessage 把本轮用户输入落库并回传事件；首条文本同时充当会话
// 标题。返回 true 表示写入失败、轮次已按失败收尾。
func (e *Engine) appendUserMessage(ctx context.Context, sessionID string, session *Session, input *UserContent, events chan Event) bool {
	if input == nil || (strings.TrimSpace(input.Text) == "" && len(input.Documents) == 0) {
		return false
	}
	seq, err := e.store.AppendMessage(ctx, sessionID, RoleUser, *input, "", nil)
	if err != nil {
		e.failTurn(ctx, sessionID, events, fmt.Sprintf("写入用户消息失败: %v", err))
		return true
	}
	if encoded, err := json.Marshal(*input); err == nil {
		emitEvent(events, Event{Type: EventMessage, Message: &Message{Seq: seq, Role: RoleUser, Content: encoded, CreatedAt: time.Now()}})
	}
	if strings.TrimSpace(session.Title) == "" && strings.TrimSpace(input.Text) != "" {
		title := truncateRunes(strings.TrimSpace(input.Text), titleMaxRunes)
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{Title: title}); err == nil {
			session.Title = title
		}
	}
	return false
}

// resumeApprovalPrefix 处理审批恢复前缀：写入裁决记录、（可选）补测试
// 凭证，批准则执行待定调用（再次暂停时返回 paused=true），拒绝则为每个
// 待定调用合成拒绝结果。返回的 conversation 已追加相应消息。
func (e *Engine) resumeApprovalPrefix(ctx context.Context, sessionID string, session *Session, resume *PendingAction, decision ApprovalDecision, conversation []relay.MaheshvaraMessage, events chan Event) ([]relay.MaheshvaraMessage, bool, error) {
	names := make([]string, 0, len(resume.Calls))
	for _, call := range resume.Calls {
		names = append(names, call.Name)
	}
	decisionText := "denied"
	if decision.Approved {
		decisionText = "approved"
	}
	if decision.BaseURL != "" || decision.APIKey != "" {
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{TestBaseURL: decision.BaseURL, TestAPIKey: decision.APIKey}); err == nil {
			// 内存副本必须同步两字段：本轮恢复路径马上用这个 session 构造
			// 工具上下文（TestTarget），只同步 BaseURL 会让首次批准的实测
			// 拿旧/空 key 跑（曾有这样的不对称 bug）。
			if decision.BaseURL != "" {
				session.TestBaseURL = decision.BaseURL
			}
			if decision.APIKey != "" {
				session.TestAPIKey = decision.APIKey
			}
		}
	}
	if _, err := e.store.AppendMessage(ctx, sessionID, RoleApproval, ApprovalContent{Decision: decisionText, Names: names, Note: decision.Note}, "", nil); err != nil {
		return nil, false, fmt.Errorf("写入审批记录失败: %v", err)
	}
	if !decision.Approved {
		// 拒绝：为每个待定调用合成拒绝结果，模型据此改道。
		for _, call := range resume.Calls {
			info := deniedToolResult(call, "用户拒绝了该操作"+denialSuffix(decision.Note))
			e.persistToolResult(ctx, sessionID, info, events)
			conversation = append(conversation, toolResultToMaheshvara(info, e.opts.ToolResultModelLimit))
		}
		return conversation, false, nil
	}
	// 已获用户明确批准：批内调用跳过 ask 级暂停，但 PermissionNever 与
	// 计划模式仍在 executeCalls 内逐调用复核（批准后策略可能已收紧，
	// 例如批量 [test_upstream, save_protocol] 里 save 是 never）。
	approved := make(map[string]bool, len(resume.Calls))
	for _, call := range resume.Calls {
		approved[call.ID] = true
	}
	paused, err := e.executeCalls(ctx, sessionID, session, &conversation, resume.Calls, "", approved, events)
	if err != nil {
		return nil, false, err
	}
	return conversation, paused, nil
}

// handleCallFailure 收尾一次失败的模型调用：取消路径下 result 可能带部分
// 聚合文本，照常落库保证可追溯；随后写入 system 错误记录并发出终态错误
// 事件（区分轮次停止/超时/上游失败的可重试性）。
func (e *Engine) handleCallFailure(ctx context.Context, sessionID string, session *Session, handle *turnHandle, result *CallResult, err error, events chan Event) {
	if result != nil && (result.Text != "" || result.Reasoning != "") {
		e.persistAssistant(ctx, sessionID, session, AssistantContent{Text: result.Text, Reasoning: result.Reasoning}, result.Usage, events)
	}
	reason := "模型调用失败"
	retryable := true
	if ctx.Err() != nil {
		if handle.stopped.Load() {
			reason, retryable = "轮次已停止", false
		} else {
			reason = "轮次超时"
		}
	}
	if _, cerr := e.store.AppendMessage(ctx, sessionID, RoleSystem, SystemContent{Kind: "error", Text: fmt.Sprintf("%s: %v", reason, err)}, "", nil); cerr != nil { //nolint:staticcheck // 落库失败无从恢复，继续走错误回报
	}
	e.setStatus(ctx, sessionID, StatusIdle, false, events)
	emitTerminal(events, Event{Type: EventError, Text: fmt.Sprintf("%s: %v", reason, err), Retryable: retryable})
}

// modelLoop 运行「模型调用 → 工具执行」循环直到模型给出终稿正文、暂停审批、
// 出错或到达上限。
func (e *Engine) modelLoop(ctx context.Context, sessionID string, session *Session, handle *turnHandle, conversation []relay.MaheshvaraMessage, started time.Time, events chan Event) {
	var usageTotal relay.MaheshvaraUsage
	sawUsage := false
	rounds := 0
	paused := false

	defer func() {
		if r := recover(); r != nil {
			emitEvent(events, Event{Type: EventError, Text: fmt.Sprintf("引擎异常: %v", r), Retryable: true})
		}
		if !paused {
			e.setStatus(ctx, sessionID, StatusIdle, false, events)
			event := Event{Type: EventTurnDone, DurationMs: time.Since(started).Milliseconds(), Rounds: rounds, Model: session.Settings.ModelName}
			if sawUsage {
				usage := usageTotal
				event.Usage = &usage
			}
			emitTerminal(events, event)
		}
	}()

	for round := 0; round < e.opts.MaxModelCalls; round++ {
		if ctx.Err() != nil {
			return
		}
		rounds = round + 1
		emitEvent(events, Event{Type: EventStatus, Text: "正在调用模型…"})

		req := CallRequest{
			Model:           session.Settings.ModelName,
			ModelSourceID:   session.Settings.ModelSourceID,
			Messages:        conversation,
			Tools:           e.tools.Definitions(),
			Thinking:        thinkingFromSettings(session.Settings),
			Reasoning:       reasoningFromSettings(session.Settings),
			MaxOutputTokens: 0, // 由 caller 层决定默认
		}
		req.Instructions = e.composeInstructions(session)

		result, err := e.caller.Call(ctx, req, StreamCallbacks{
			OnText:      func(delta string) { emitEvent(events, Event{Type: EventTextDelta, Delta: delta}) },
			OnReasoning: func(delta string) { emitEvent(events, Event{Type: EventReasoningDelta, Delta: delta}) },
		})
		if result != nil && result.Usage != nil {
			accumulateUsage(&usageTotal, result.Usage)
			sawUsage = true
		}
		if err != nil {
			e.handleCallFailure(ctx, sessionID, session, handle, result, err, events)
			return
		}

		content := AssistantContent{Text: result.Text, Reasoning: result.Reasoning, ToolCalls: result.ToolCalls}
		e.persistAssistant(ctx, sessionID, session, content, result.Usage, events)

		if len(result.ToolCalls) == 0 {
			return // 终稿
		}
		conversation = append(conversation, assistantToMaheshvara(content))

		paused, err = e.executeCalls(ctx, sessionID, session, &conversation, result.ToolCalls, result.Text, nil, events)
		if err != nil {
			e.failTurn(ctx, sessionID, events, err.Error())
			paused = false
			return
		}
		if paused {
			return
		}
	}

	// 到达循环上限：如实告知，等待用户指示。
	note := fmt.Sprintf("已达到单轮工具循环上限（%d 次模型调用），请检查工具结果或继续对话", e.opts.MaxModelCalls)
	_, _ = e.store.AppendMessage(ctx, sessionID, RoleSystem, SystemContent{Kind: "info", Text: note}, "", nil)
	emitEvent(events, Event{Type: EventStatus, Text: note})
}

// executeCalls 顺序执行一批工具调用。gating 生效时遇到首个需审批动作即暂停
// （剩余调用连同当前调用存入 PendingAction）。approvedIDs 是审批恢复路径传入
// 的「已获用户批准的调用」集合：命中的调用跳过 ask 级暂停，但 PermissionNever
// 与计划模式始终逐调用复核——批准动作只豁免 ask，不豁免显式禁令。
// 每个结果都会持久化并回传事件，同时追加到 conversation。
func (e *Engine) executeCalls(ctx context.Context, sessionID string, session *Session, conversation *[]relay.MaheshvaraMessage, calls []relay.MaheshvaraToolCall, reason string, approvedIDs map[string]bool, events chan Event) (bool, error) {
	for index, call := range calls {
		tool := e.tools.Get(call.Name)
		if tool == nil {
			e.denyCall(ctx, sessionID, conversation, call, fmt.Sprintf("未知工具 %q", call.Name), events)
			continue
		}
		gate, denial := e.gateCall(session, call, tool, approvedIDs)
		if gate == gateDeny {
			e.denyCall(ctx, sessionID, conversation, call, denial, events)
			continue
		}
		if gate == gatePause {
			pending := &PendingAction{Calls: append([]relay.MaheshvaraToolCall(nil), calls[index:]...), Reason: reason}
			waiting := StatusWaitingApproval
			if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{Status: &waiting, PendingAction: pending}); err != nil {
				return false, err
			}
			emitTerminal(events, Event{Type: EventApprovalPending, Approval: pending})
			return true, nil
		}
		info := e.runOneTool(ctx, sessionID, session, tool, call, events)
		*conversation = append(*conversation, toolResultToMaheshvara(info, e.opts.ToolResultModelLimit))
	}
	return false, nil
}

// gateCall 复核单个调用的门禁（计划模式 → 显式禁令 → ask 级审批）。批准集
// 命中只豁免 ask；never 与计划模式始终逐调用复核——批准后策略可能已收紧。
type callGate int

const (
	gateAllow callGate = iota
	gateDeny
	gatePause
)

func (e *Engine) gateCall(session *Session, call relay.MaheshvaraToolCall, tool Tool, approvedIDs map[string]bool) (callGate, string) {
	if !tool.Gated() {
		return gateAllow, ""
	}
	if session.Settings.PlanMode {
		return gateDeny, "计划模式已开启：修改与出站操作暂不执行。请先用 update_plan 给出完整方案，并等待用户确认后再执行"
	}
	switch PermissionFor(session.Settings, tool.PermissionKey()) {
	case PermissionNever:
		return gateDeny, "用户已在会话设置中禁止此操作，请改用其他方式完成任务"
	case PermissionAsk:
		if !approvedIDs[call.ID] {
			return gatePause, ""
		}
	}
	return gateAllow, ""
}

// denyCall 合成一次被拒/未知的工具结果：落库 + 回传事件 + 追加到对话，
// 三类拒绝（计划模式 / never 权限 / 未知工具）共用同一收尾。
func (e *Engine) denyCall(ctx context.Context, sessionID string, conversation *[]relay.MaheshvaraMessage, call relay.MaheshvaraToolCall, message string, events chan Event) {
	info := deniedToolResult(call, message)
	if strings.Contains(message, "未知工具") {
		info = ToolResultInfo{
			CallID: call.ID, Name: call.Name, Input: call.Arguments,
			OK: false, Summary: message,
			Data: json.RawMessage(`{"error":"unknown_tool"}`),
		}
	}
	e.persistToolResult(ctx, sessionID, info, events)
	*conversation = append(*conversation, toolResultToMaheshvara(info, e.opts.ToolResultModelLimit))
}

// runOneTool 执行单个工具（带 panic 防护），落库并发出事件。
func (e *Engine) runOneTool(ctx context.Context, sessionID string, session *Session, tool Tool, call relay.MaheshvaraToolCall, events chan Event) (info ToolResultInfo) {
	emitEvent(events, Event{Type: EventToolCall, CallID: call.ID, Name: call.Name, Input: call.Arguments})
	started := time.Now()
	draftBefore := append(json.RawMessage(nil), session.DraftConfig...)
	planBefore := append([]PlanStep(nil), session.Plan...)

	var result ToolResult
	func() {
		defer func() {
			if r := recover(); r != nil {
				result = ToolResult{OK: false, Summary: fmt.Sprintf("工具执行异常: %v", r), Data: map[string]any{"error": "tool_panic"}}
			}
		}()
		result = tool.Execute(ctx, &engineToolContext{store: e.store, ctx: ctx, session: session}, call.Arguments)
	}()

	info = ToolResultInfo{
		CallID:     call.ID,
		Name:       call.Name,
		Input:      call.Arguments,
		OK:         result.OK,
		Summary:    result.Summary,
		Data:       clampJSON(result.MarshalData(), e.opts.ToolResultStoreLimit),
		DurationMs: time.Since(started).Milliseconds(),
	}
	e.persistToolResult(ctx, sessionID, info, events)

	if len(session.DraftConfig) > 0 && !bytes.Equal(draftBefore, session.DraftConfig) {
		emitEvent(events, Event{Type: EventDraftUpdated, Draft: session.DraftConfig})
	}
	if !planStepsEqual(planBefore, session.Plan) {
		emitEvent(events, Event{Type: EventPlanUpdated, Plan: session.Plan})
	}
	return info
}

func (e *Engine) persistToolResult(ctx context.Context, sessionID string, info ToolResultInfo, events chan Event) {
	// 落库副本脱敏调用参数里的密钥类字段（apiKey/api_key/token/secret）：
	// test_upstream 等工具的 key 以参数传入，原样入库与「凭证加密存储」的
	// 承诺矛盾。现场事件与审批恢复路径（PendingAction.Calls）保留原值——
	// 恢复执行需要真实参数。
	stored := info
	stored.Input = maskSecretInputs(info.Input)
	seq, err := e.store.AppendMessage(ctx, sessionID, RoleToolResult, stored, "", nil)
	if err != nil {
		emitEvent(events, Event{Type: EventStatus, Text: fmt.Sprintf("工具结果落库失败: %v", err)})
		return
	}
	encoded, _ := json.Marshal(stored)
	result := stored
	emitEvent(events, Event{Type: EventToolResult, CallID: info.CallID, Name: info.Name, Result: &result, Message: &Message{Seq: seq, Role: RoleToolResult, Content: encoded, CreatedAt: time.Now()}})
}

// maskSecretInputs 把输入 JSON 中密钥类字符串字段替换为 ***（递归遍历；
// 解析失败则原样返回——脱敏尽力而为，不阻断落库）。
func maskSecretInputs(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	if encoded, err := json.Marshal(maskSecretValue(value)); err == nil {
		return encoded
	}
	return raw
}

func maskSecretValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if text, isString := item.(string); isString && text != "" && isSecretInputKey(key) {
				typed[key] = "***"
				continue
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
		lower == "token" || strings.Contains(lower, "secret") || strings.Contains(lower, "password")
}

func (e *Engine) persistAssistant(ctx context.Context, sessionID string, session *Session, content AssistantContent, usage *relay.MaheshvaraUsage, events chan Event) {
	var usageJSON json.RawMessage
	if usage != nil {
		if encoded, err := json.Marshal(usage); err == nil {
			usageJSON = encoded
		}
	}
	seq, err := e.store.AppendMessage(ctx, sessionID, RoleAssistant, content, session.Settings.ModelName, usageJSON)
	if err != nil {
		emitEvent(events, Event{Type: EventStatus, Text: fmt.Sprintf("助手消息落库失败: %v", err)})
		return
	}
	encoded, _ := json.Marshal(content)
	emitEvent(events, Event{Type: EventMessage, Message: &Message{Seq: seq, Role: RoleAssistant, Content: encoded, Model: session.Settings.ModelName, Usage: usageJSON, CreatedAt: time.Now()}})
}

func (e *Engine) setStatus(ctx context.Context, sessionID, status string, clearPending bool, events chan Event) {
	update := SessionStateUpdate{Status: &status}
	if clearPending {
		update.ClearPending = true
	}
	if err := e.store.UpdateSessionState(ctx, sessionID, update); err != nil {
		emitEvent(events, Event{Type: EventStatus, Text: fmt.Sprintf("会话状态更新失败: %v", err)})
	}
}

func (e *Engine) failTurn(ctx context.Context, sessionID string, events chan Event, message string) {
	_, _ = e.store.AppendMessage(ctx, sessionID, RoleSystem, SystemContent{Kind: "error", Text: message}, "", nil)
	e.setStatus(ctx, sessionID, StatusIdle, false, events)
	emitTerminal(events, Event{Type: EventError, Text: message, Retryable: true})
}

// composeInstructions 组装每次模型调用的系统提示词：宿主领域提示 + 当前草稿
// 状态块（草稿随工具调用演进，模型每轮都看最新状态）。
func (e *Engine) composeInstructions(session *Session) string {
	var b strings.Builder
	if e.prompt != nil {
		b.WriteString(e.prompt(session))
	}
	if len(session.DraftConfig) > 0 {
		b.WriteString("\n\n## 当前工作草稿（工具 update_protocol_draft 的最新产物，后续修改以它为基准）\n```json\n")
		b.Write(session.DraftConfig)
		b.WriteString("\n```")
	} else {
		b.WriteString("\n\n## 当前工作草稿\n（尚无草稿——请先调用 update_protocol_draft 生成第一版。）")
	}
	return b.String()
}

// thinkingFromSettings 把会话思考设置映射为请求级思考配置（同时设置
// Reasoning 以覆盖 OpenAI 线的 reasoning_effort 渲染路径）。
func thinkingFromSettings(s Settings) *relay.MaheshvaraThinking {
	if !s.ThinkingEnabled {
		return nil
	}
	effort := strings.ToLower(strings.TrimSpace(s.ThinkingEffort))
	thinking := &relay.MaheshvaraThinking{Enabled: true, Effort: effort}
	if effort == "adaptive" {
		thinking.Adaptive = true
	}
	return thinking
}

func reasoningFromSettings(s Settings) *relay.MaheshvaraReasoning {
	if !s.ThinkingEnabled {
		return nil
	}
	effort := strings.ToLower(strings.TrimSpace(s.ThinkingEffort))
	if effort == "" || effort == "adaptive" {
		return nil
	}
	return &relay.MaheshvaraReasoning{Effort: effort}
}

// PermissionFor 取权限键的策略（未知键视为 ask）。
func PermissionFor(s Settings, key string) string {
	// 权限值在存储写入与读取（遗留行兼容）时已归一化，此处直接消费。
	switch key {
	case "live_test":
		return s.AllowLiveTest
	case "save":
		return s.AllowSave
	default:
		return PermissionAsk
	}
}

func denialSuffix(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	return "（备注：" + note + "）"
}

func deniedToolResult(call relay.MaheshvaraToolCall, message string) ToolResultInfo {
	// 拒绝理由随 Data 一起回传给模型（ToolOutput 只含 Data），模型才能据此调整行为。
	encoded, err := json.Marshal(map[string]string{"error": "denied", "message": message})
	if err != nil {
		encoded = json.RawMessage(`{"error":"denied"}`)
	}
	return ToolResultInfo{
		CallID:  call.ID,
		Name:    call.Name,
		Input:   call.Arguments,
		OK:      false,
		Summary: message,
		Data:    encoded,
	}
}

// engineToolContext 是引擎内置的 ToolContext 默认实现：直读会话、草稿
// 写穿透到 Store。
type engineToolContext struct {
	store   Store
	ctx     context.Context
	session *Session
}

// metaFromSession 构造工具上下文视角的会话元数据快照。
func metaFromSession(s *Session) SessionMeta {
	return SessionMeta{
		ID: s.ID, Title: s.Title, Mode: s.Mode, ProtocolID: s.ProtocolID,
		SeedConfig: s.SeedConfig, Draft: s.DraftConfig, Settings: s.Settings,
	}
}

func (c *engineToolContext) SessionMeta() SessionMeta {
	return metaFromSession(c.session)
}

func (c *engineToolContext) Draft() json.RawMessage { return c.session.DraftConfig }

func (c *engineToolContext) SetDraft(draft json.RawMessage) error {
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, SessionStateUpdate{DraftConfig: draft}); err != nil {
		return err
	}
	c.session.DraftConfig = append(json.RawMessage(nil), draft...)
	return nil
}

func (c *engineToolContext) TestTarget() (string, string) {
	return c.session.TestBaseURL, c.session.TestAPIKey
}

func (c *engineToolContext) SetTestTarget(baseURL, apiKey string) error {
	update := SessionStateUpdate{}
	if strings.TrimSpace(baseURL) != "" {
		update.TestBaseURL = baseURL
		c.session.TestBaseURL = baseURL
	}
	if strings.TrimSpace(apiKey) != "" {
		update.TestAPIKey = apiKey
		c.session.TestAPIKey = apiKey
	}
	if update.TestBaseURL == "" && update.TestAPIKey == "" {
		return nil
	}
	return c.store.UpdateSessionState(c.ctx, c.session.ID, update)
}

func (c *engineToolContext) SetPlan(steps []PlanStep) error {
	if steps == nil {
		steps = []PlanStep{}
	}
	if planStepsEqual(c.session.Plan, steps) {
		return nil
	}
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, SessionStateUpdate{Plan: steps}); err != nil {
		return err
	}
	c.session.Plan = steps
	return nil
}

func planStepsEqual(a, b []PlanStep) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].Title != b[index].Title || a[index].Status != b[index].Status {
			return false
		}
	}
	return true
}

// ---- 会话历史 → Maheshvara 对话 ----

func (e *Engine) loadConversation(ctx context.Context, session *Session) ([]relay.MaheshvaraMessage, error) {
	messages, err := e.store.ListMessages(ctx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("读取会话消息失败: %w", err)
	}
	conversation := make([]relay.MaheshvaraMessage, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case RoleUser:
			var content UserContent
			if err := json.Unmarshal(message.Content, &content); err != nil {
				return nil, fmt.Errorf("用户消息 #%d 解析失败: %w", message.Seq, err)
			}
			meta := metaFromSession(session)
			parts, err := e.render.RenderUserContent(meta, &content)
			if err != nil {
				return nil, fmt.Errorf("用户消息 #%d 渲染失败: %w", message.Seq, err)
			}
			if len(parts) > 0 {
				conversation = append(conversation, relay.MaheshvaraMessage{Role: "user", Content: parts})
			}
		case RoleAssistant:
			var content AssistantContent
			if err := json.Unmarshal(message.Content, &content); err != nil {
				return nil, fmt.Errorf("助手消息 #%d 解析失败: %w", message.Seq, err)
			}
			conversation = append(conversation, assistantToMaheshvara(content))
		case RoleToolResult:
			var info ToolResultInfo
			if err := json.Unmarshal(message.Content, &info); err != nil {
				return nil, fmt.Errorf("工具结果 #%d 解析失败: %w", message.Seq, err)
			}
			conversation = append(conversation, toolResultToMaheshvara(info, e.opts.ToolResultModelLimit))
		}
	}
	return conversation, nil
}

func assistantToMaheshvara(content AssistantContent) relay.MaheshvaraMessage {
	msg := relay.MaheshvaraMessage{Role: "assistant", ToolCalls: content.ToolCalls}
	if content.Text != "" {
		msg.Content = []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: content.Text}}
	}
	return msg
}

func toolResultToMaheshvara(info ToolResultInfo, limit int) relay.MaheshvaraMessage {
	data := info.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	output := clampJSON(data, limit)
	return relay.MaheshvaraMessage{
		Role:    "tool",
		Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentToolOutput, ToolCallID: info.CallID, ToolOutput: string(output)}},
	}
}

// ---- 工具函数 ----

func accumulateUsage(total *relay.MaheshvaraUsage, u *relay.MaheshvaraUsage) {
	total.InputTokens += u.InputTokens
	total.OutputTokens += u.OutputTokens
	if u.TotalTokens > 0 {
		total.TotalTokens += u.TotalTokens
	} else {
		total.TotalTokens += u.InputTokens + u.OutputTokens
	}
	total.CachedInputTokens += u.CachedInputTokens
	total.CacheCreationInputTokens += u.CacheCreationInputTokens
	total.ReasoningTokens += u.ReasoningTokens
}

// clampJSON 在字节上限内截断超大 JSON 文档。截断产物必须是**合法 JSON**：
// 它会作为 json.RawMessage 嵌进消息持久化与发往模型的对话，两边都要过
// MarshalJSON 校验——旧实现「JSON 前缀 + 文本尾巴」让超限工具结果双双
// 落库失败，历史留下无结果的 tool_calls，会话后续每轮被上游 400 拒绝。
// 现改为 JSON 信封携带原文前缀（preview 取半量，为信封开销与转义膨胀留量）。
func clampJSON(raw json.RawMessage, limit int) json.RawMessage {
	if len(raw) <= limit {
		return raw
	}
	value := string(raw)
	cut := limit / previewHalfFactor
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	envelope := map[string]any{
		"truncated": true,
		"bytes":     len(value),
		"preview":   value[:cut],
	}
	if encoded, err := json.Marshal(envelope); err == nil {
		return encoded
	}
	return json.RawMessage(`{"truncated":true}`)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// ReconcileInterruptedSessions 进程启动对账：崩溃/被杀遗留的 running 会话
// 复位为 idle（轮次已随进程消失，不复位会让 UI 永远挡在假轮次上）。
func (e *Engine) ReconcileInterruptedSessions(ctx context.Context) {
	if err := e.store.ResetRunningSessions(ctx); err != nil {
		log.Printf("[agent] reconcile interrupted sessions: %v", err)
	}
}
