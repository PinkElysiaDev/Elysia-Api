package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/bytedance/sonic"
	log "github.com/elysia-api/backend/logging"
	"golang.org/x/sync/errgroup"
)

const (
	toolParallelLimit       = 4
	toolProgressInterval    = 5 * time.Second
	toolResultRecordTimeout = 5 * time.Second
	// DefaultToolTimeoutMs bounds tools without an explicit execution timeout.
	DefaultToolTimeoutMs = 120_000
)

// denyKind 区分拒绝语义：unknown 保留参数与专用错误码（模型才能发现拼错
// 的名字），denied 走通用拒绝结果。
type denyKind int

const (
	denyDenied denyKind = iota
	denyUnknown
)

// 暂停型工具名。引擎按名字分发（ask_user 暂停提问、update_plan 触发方案
// 定稿检查），常量定义在本包供宿主注册时复用，避免两侧字面量漂移。
const (
	ToolNameAskUser    = "ask_user"
	ToolNameUpdatePlan = "update_plan"
)

// executeCalls 执行一批工具调用。门控与非并行工具保持原顺序：遇到首个需
// 审批动作即暂停（剩余调用连同当前调用存入 PendingAction）。连续的非门控
// ConcurrentSafe 工具合成一个并行组（上限 toolParallelLimit），结果按完成
// 序回传、按原调用序追加进对话。approvedIDs 命中只豁免 ask 级暂停；非 nil
// 时必覆盖 calls 全体 ID（恢复路径的既定语义），故 gatePause 分支只会在
// 新轮次（approvedIDs=nil）到达。
func (e *Engine) executeCalls(ctx context.Context, sessionID string, session *Session, conversation *[]conversationTurn, calls []FunctionCall, reason string, approvedIDs map[string]bool, events chan Event) (bool, error) {
	index := 0
	for index < len(calls) {
		call := calls[index]
		if call.Name == ToolNameAskUser {
			if isPaused, err := e.pauseForQuestion(ctx, sessionID, calls[index:], reason, events); isPaused || err != nil {
				return isPaused, err
			}
		}
		tool := e.tools.Get(call.Name)
		if tool == nil {
			e.denyCall(ctx, sessionID, conversation, call, fmt.Sprintf("未知工具 %q", call.Name), denyUnknown, events)
			index++
			continue
		}
		gate, denial, pauseNotes := e.gateCall(session, call, tool, approvedIDs)
		if gate == gateDeny {
			e.denyCall(ctx, sessionID, conversation, call, denial, denyDenied, events)
			index++
			continue
		}
		if gate == gatePause {
			return e.pauseForApproval(ctx, sessionID, session, calls, index, reason, pauseNotes, events)
		}
		if !canRunParallel(tool) {
			info := e.runOneTool(ctx, sessionID, session, tool, call, events)
			appendToolOutput(conversation, info, modelResultLimit(tool, e.opts.ToolResultModelLimit))
			index++
			continue
		}
		end := index + 1
		for end < len(calls) && e.canRunParallelCall(session, calls[end], approvedIDs) {
			end++
		}
		infos := e.runParallel(ctx, sessionID, session, calls[index:end], events)
		for _, info := range infos {
			appendToolOutput(conversation, info, e.modelLimitFor(info.Name))
		}
		index = end
	}
	return false, nil
}

// canRunParallelCall 报告调用能否并入当前并行组：必须存在、非门控且声明
// ConcurrentSafe。门控调用留给顺序路径处理暂停。
func (e *Engine) canRunParallelCall(session *Session, call FunctionCall, approvedIDs map[string]bool) bool {
	tool := e.tools.Get(call.Name)
	if tool == nil || !canRunParallel(tool) {
		return false
	}
	gate, _, _ := e.gateCall(session, call, tool, approvedIDs)
	return gate == gateAllow
}

func canRunParallel(tool Tool) bool {
	return !tool.Gated() && MetaOf(tool).ConcurrentSafe
}

func modelResultLimit(tool Tool, fallback int) int {
	if limit := MetaOf(tool).MaxModelBytes; limit > 0 {
		return limit
	}
	return fallback
}

// modelLimitFor 按工具名取模型回传上限（并行路径用——结果只带名字）。
func (e *Engine) modelLimitFor(name string) int {
	return modelResultLimit(e.tools.Get(name), e.opts.ToolResultModelLimit)
}

// runParallel 并行执行一组只读工具。每个调用拿到会话快照：ConcurrentSafe
// 是硬契约——并行工具不得修改共享会话态（SetDraft/SetPlan 的写入只会落在
// 快照上静默丢失）；需要写会话的工具必须保持不可并行。
func (e *Engine) runParallel(ctx context.Context, sessionID string, session *Session, calls []FunctionCall, events chan Event) []ToolResultInfo {
	infos := make([]ToolResultInfo, len(calls))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(toolParallelLimit)
	for index, call := range calls {
		tool := e.tools.Get(call.Name)
		group.Go(func() (err error) {
			// errgroup 不 recover——runOneTool 外壳(进度/落库/截断)一旦 panic
			// 会直接崩进程;这里降级为该工具的失败结果(与 runOneTool 内对
			// tool.Execute 的防护同语义)。
			defer func() {
				if r := recover(); r != nil {
					log.FromContext(ctx).Error().Str("tool", call.Name).Interface("panic", r).Bytes("stack", debug.Stack()).Msg("parallel tool panic")
					infos[index] = ToolResultInfo{
						CallID: call.ID, Name: call.Name, OK: false,
						Summary: fmt.Sprintf("工具执行异常(并行): %v", r),
						Data:    json.RawMessage(`{"error":"tool_panic"}`),
					}
				}
			}()
			snapshot := *session
			infos[index] = e.runOneTool(groupCtx, sessionID, &snapshot, tool, call, events)
			return nil
		})
	}
	_ = group.Wait()
	return infos
}

// denyCall 合成一次被拒/未知的工具结果：落库 + 回传事件 + 追加到对话，
// 三类拒绝（计划模式 / never 权限 / 未知工具）共用同一收尾。
func (e *Engine) denyCall(ctx context.Context, sessionID string, conversation *[]conversationTurn, call FunctionCall, message string, kind denyKind, events chan Event) {
	info := deniedToolResult(call, message)
	if kind == denyUnknown {
		info = ToolResultInfo{
			CallID: call.ID, Name: call.Name, Input: call.Arguments,
			OK: false, Summary: message,
			Data: json.RawMessage(`{"error":"unknown_tool"}`),
		}
	}
	info = e.persistToolResult(ctx, sessionID, info, events)
	appendToolOutput(conversation, info, e.opts.ToolResultModelLimit)
}

// runOneTool 执行单个工具（带 panic 防护），落库并发出事件。
func (e *Engine) runOneTool(ctx context.Context, sessionID string, session *Session, tool Tool, call FunctionCall, events chan Event) (info ToolResultInfo) {
	// 事件出口脱敏：模型回路与 PendingAction 落库保留原文（执行需要），
	// 只有发往 SSE 的副本遮盖密钥类字段。
	emitEvent(events, Event{Type: EventToolCall, CallID: call.ID, Name: call.Name, Input: MaskSecretInputs(call.Arguments)})
	started := time.Now()
	draftBefore := append(json.RawMessage(nil), session.DraftConfig...)
	planBefore := append([]PlanStep(nil), session.Plan...)
	planSummaryBefore := session.PlanSummary

	execCtx, stopProgress := e.watchToolProgress(ctx, call, MetaOf(tool), events)
	// 批处理型工具（elysia_cli/CLI）经 ProgressReporter 逐子步骤上报进度，
	// 复用 tool_progress 事件并携带 Text（心跳只有 ElapsedMs）。
	progressStarted := time.Now()
	tctx := (&engineToolContext{store: e.store, ctx: execCtx, session: session}).WithProgress(func(text string) {
		emitEvent(events, Event{Type: EventToolProgress, CallID: call.ID, Name: call.Name,
			Text: text, ElapsedMs: time.Since(progressStarted).Milliseconds()})
	})
	var result ToolResult
	func() {
		defer stopProgress()
		defer func() {
			if r := recover(); r != nil {
				result = ToolResult{OK: false, Summary: fmt.Sprintf("工具执行异常: %v", r), Data: map[string]any{"error": "tool_panic"}}
			}
		}()
		result = tool.Execute(execCtx, tctx, call.Arguments)
	}()

	encoded, encodeErr := result.MarshalData()
	info = ToolResultInfo{
		CallID:       call.ID,
		Name:         call.Name,
		Input:        call.Arguments,
		OK:           result.OK,
		Summary:      result.Summary,
		Data:         encoded,
		DurationMs:   time.Since(started).Milliseconds(),
		SecretValues: result.SecretValues,
		RedactedData: result.RedactedData,
	}
	if encodeErr != nil {
		info = rejectToolResult(info)
	}
	// 调用取消后仍记录实际结果，避免会话回放只剩一条未结束的工具调用。
	recordCtx, stopRecord := context.WithTimeout(context.WithoutCancel(ctx), toolResultRecordTimeout)
	info = e.persistToolResult(recordCtx, sessionID, info, events)
	stopRecord()

	if len(session.DraftConfig) > 0 && !bytes.Equal(draftBefore, session.DraftConfig) {
		emitEvent(events, Event{Type: EventDraftUpdated, Draft: session.DraftConfig})
	}
	if !planStepsEqual(planBefore, session.Plan) || planSummaryBefore != session.PlanSummary {
		emitEvent(events, Event{Type: EventPlanUpdated, Plan: session.Plan, PlanSummary: session.PlanSummary})
		session.PlanStaleRounds = 0
	}
	// ready 标志的判定不依赖「方案有变化」：模型原样重发步骤并声明定稿时，
	// 方案内容不变但确认流程仍然要触发。
	if call.Name == ToolNameUpdatePlan {
		e.notePlanReady(session, call, result)
	}
	return info
}

// notePlanReady 记住本批 update_plan 是否声明方案定稿。真正暂停放在整批
// 工具结束之后，避免打断同批后续只读调用。
func (e *Engine) notePlanReady(session *Session, call FunctionCall, result ToolResult) {
	if !result.OK || !session.Settings.PlanMode {
		return
	}
	var payload struct {
		Ready bool `json:"ready_for_approval"`
	}
	if sonic.ConfigStd.Unmarshal(call.Arguments, &payload) == nil && payload.Ready {
		session.PlanReady = true
	}
}

// watchToolProgress 给长工具发耗时心跳，并设置声明或默认的 context 超时。
// 工具必须配合 context 取消；它不能强制中断忽略 context 的 Execute。
// 返回的停止函数必须在执行结束后调用，以免心跳访问已关闭的事件通道。
func (e *Engine) watchToolProgress(ctx context.Context, call FunctionCall, meta ToolMeta, events chan Event) (context.Context, func()) {
	timeoutMs := meta.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultToolTimeoutMs
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	started := time.Now()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(toolProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-execCtx.Done():
				return
			case <-ticker.C:
				emitEvent(events, Event{Type: EventToolProgress, CallID: call.ID, Name: call.Name, ElapsedMs: time.Since(started).Milliseconds()})
			}
		}
	}()
	return execCtx, func() {
		close(stop)
		cancel()
		<-done // 确保调用方关闭 events 前心跳已经退出。
	}
}
