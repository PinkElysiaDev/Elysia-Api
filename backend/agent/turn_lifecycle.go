package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/elysia-api/backend/logging"
)

const (
	stopDrainWait          = 15 * time.Second
	panicStackLineBytes    = 160
	panicStackFrames       = 3
	turnPanicRecordTimeout = 5 * time.Second
)

type turnHandle struct {
	cancel    context.CancelFunc
	done      chan struct{}
	isStopped atomic.Bool
}

func (e *Engine) beginTurn(sessionID string, timeout time.Duration) (context.Context, *turnHandle, context.CancelFunc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.isClosed {
		return nil, nil, nil, ErrEngineClosed
	}
	if _, exists := e.running[sessionID]; exists {
		return nil, nil, nil, ErrSessionRunning
	}
	// Publish the cancellation handle while holding the session guard.
	turnCtx, cancel := context.WithTimeout(context.Background(), timeout)
	handle := &turnHandle{done: make(chan struct{}), cancel: cancel}
	e.running[sessionID] = handle
	return turnCtx, handle, cancel, nil
}

func (e *Engine) endTurn(sessionID string, handle *turnHandle) {
	e.mu.Lock()
	delete(e.running, sessionID)
	e.mu.Unlock()
	close(handle.done)
}

// Stop 取消会话的进行中轮次（等待收尾）。返回是否有轮次被取消。
func (e *Engine) Stop(sessionID string) bool {
	e.mu.Lock()
	handle := e.running[sessionID]
	e.mu.Unlock()
	if handle == nil {
		return false
	}
	handle.isStopped.Store(true)
	handle.cancel()
	select {
	case <-handle.done:
	case <-time.After(stopDrainWait):
	}
	return true
}

// Shutdown prevents new turns and cancels all active turns before the host closes storage.
func (e *Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.isClosed = true
	handles := make([]*turnHandle, 0, len(e.running))
	for _, handle := range e.running {
		handle.isStopped.Store(true)
		handle.cancel()
		handles = append(handles, handle)
	}
	e.mu.Unlock()
	for _, handle := range handles {
		select {
		case <-handle.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// RunTurn 开始一个新轮次。input 非 nil 时先追加用户消息；input 为 nil 表示
// 从既有历史继续（重新生成）。返回的事件 channel 在轮次结束后关闭。
func (e *Engine) RunTurn(ctx context.Context, sessionID string, input *UserContent) (<-chan Event, error) {
	turnCtx, handle, cancel, err := e.beginTurn(sessionID, e.opts.TurnTimeout)
	if err != nil {
		return nil, err
	}
	logger := log.FromContext(ctx).With().Str("session_id", sessionID).Logger()
	return e.spawnTurn(logger.WithContext(turnCtx), sessionID, handle, cancel, input, nil, ApprovalDecision{}), nil
}

// spawnTurn 在后台 goroutine 里跑 startTurn 并负责收尾（释放会话锁、
// 停表、关事件流）——RunTurn 与 ResumeApproval 的同型启动块。
func (e *Engine) spawnTurn(turnCtx context.Context, sessionID string, handle *turnHandle, cancel context.CancelFunc, input *UserContent, resume *PendingAction, decision ApprovalDecision) <-chan Event {
	events := make(chan Event, e.opts.EventBuffer)
	go func() {
		// EOF permits the caller to start another turn, so release this turn's
		// resources and session guard before making completion observable.
		defer close(events)
		defer e.endTurn(sessionID, handle)
		defer cancel()
		e.startTurn(turnCtx, sessionID, handle, input, resume, decision, events)
	}()
	return events
}

// startTurn 是轮次统一入口：新消息或审批恢复 → 模型循环 → 收尾。
func (e *Engine) startTurn(ctx context.Context, sessionID string, handle *turnHandle, input *UserContent, resume *PendingAction, decision ApprovalDecision, events chan Event) {
	// 整个轮次主体的 panic 防护（modelLoop 内已有 recover，这里覆盖其余
	// 部分——如历史渲染/审批记录写入）：置回 idle 并发终态事件，避免进程
	// 崩溃、以及会话在 DB 里永卡 running。
	defer func() {
		if r := recover(); r != nil {
			message := e.reportTurnPanic(context.Background(), sessionID, r, "startTurn")
			// panic 路径不用轮次 ctx：ctx 可能正是 panic 诱因（已取消），
			// Background + emitTerminal 的 5s 上限保证错误事件尽力送达。
			emitTerminal(context.Background(), events, Event{Type: EventError, Text: message, Retryable: true})
			e.setStatus(context.Background(), sessionID, StatusIdle, true, events)
			emitTerminal(ctx, events, Event{Type: EventTurnDone})
		}
	}()
	started := time.Now()
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		emitTerminal(ctx, events, Event{Type: EventError, Text: fmt.Sprintf("读取会话失败: %v", err)})
		return
	}
	if session == nil {
		e.failTurn(ctx, sessionID, events, "读取会话失败: 存储返回空会话")
		return
	}
	// Only a waiting snapshot owns unexecuted calls. An idle session may retain
	// stale pending data after a failed clear of an already executed approval.
	var pending *PendingAction
	if resume == nil && session.Status == StatusWaitingApproval {
		pending = session.PendingAction
	}
	e.setStatus(ctx, sessionID, StatusRunning, resume == nil && pending == nil, events)

	// 新轮次开始前快照当前草稿：还原点单槽覆盖，用户可回滚到上一轮修改前。
	if resume == nil && len(session.DraftConfig) > 0 {
		snapshot := append(json.RawMessage(nil), session.DraftConfig...)
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{DraftRestore: snapshot}); err == nil {
			session.DraftRestore = snapshot
		}
	}

	conversation, err := e.prepareTurnHistory(ctx, session, input, pending, events)
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
		// 待批动作已消费完毕：清掉 pending，避免恢复后残留的旧动作被二次
		// 批准（paused 时不清——那时的 pending 是本次新写入的）。
		if err := e.store.UpdateSessionState(ctx, sessionID, SessionStateUpdate{ClearPending: true}); err != nil {
			e.failTurn(ctx, sessionID, events, fmt.Sprintf("清除已处理审批失败: %v", err))
			return
		}
	}

	e.modelLoop(ctx, sessionID, session, handle, conversation, started, events)
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

// reportTurnPanic 记录轮次 panic 的完整栈(控制台)并落库一条含栈顶摘录的
// 错误消息,返回带同样摘录的用户可见文本——此前两处 recover 只回传 panic
// 值,栈完全丢失且不落库,偶发 panic(SSE 断开时)事后零痕迹、永远无法定位。
func (e *Engine) reportTurnPanic(ctx context.Context, sessionID string, r any, stage string) string {
	stack := debug.Stack()
	log.Error().Str("component", "agent").Str("session_id", sessionID).Str("stage", stage).Interface("panic", r).Bytes("stack", stack).Msg("turn panic")
	message := fmt.Sprintf("引擎异常(%s): %v", stage, r)
	if excerpt := panicStackExcerpt(stack); excerpt != "" {
		message += "\n" + excerpt
	}
	finCtx := context.WithoutCancel(ctx)
	finCtx, cancel := context.WithTimeout(finCtx, turnPanicRecordTimeout)
	defer cancel()
	_, _ = e.store.AppendMessage(finCtx, sessionID, RoleSystem, SystemContent{Kind: "error", Text: message}, "", nil)
	return message
}

// panicStackExcerpt 从 panic 栈里摘出最顶部的 3 帧用户代码行(过滤 runtime
// 帧与过长的参数),供错误文本携带——完整栈在控制台日志。
func panicStackExcerpt(stack []byte) string {
	lines := strings.Split(string(stack), "\n")
	var picked []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "github.com/elysia-api/") {
			continue
		}
		if len(trimmed) > panicStackLineBytes {
			trimmed = trimmed[:panicStackLineBytes] + "..."
		}
		picked = append(picked, trimmed)
		if len(picked) >= panicStackFrames {
			break
		}
	}
	return strings.Join(picked, "\n")
}

func (e *Engine) failTurn(ctx context.Context, sessionID string, events chan Event, message string) {
	// 收尾写库不随轮次取消（failTurn 也会在取消路径上被调到）。
	finCtx := context.WithoutCancel(ctx)
	_, _ = e.store.AppendMessage(finCtx, sessionID, RoleSystem, SystemContent{Kind: "error", Text: message}, "", nil)
	e.setStatus(finCtx, sessionID, StatusIdle, false, events)
	emitTerminal(ctx, events, Event{Type: EventError, Text: message, Retryable: true})
}

// ReconcileInterruptedSessions 进程启动对账：崩溃/被杀遗留的 running 会话
// 复位为 idle（轮次已随进程消失，不复位会让 UI 永远挡在假轮次上）。
func (e *Engine) ReconcileInterruptedSessions(ctx context.Context) {
	if err := e.store.ResetRunningSessions(ctx); err != nil {
		log.Error().Str("component", "agent").Err(err).Msg("reconcile interrupted sessions")
	}
}
