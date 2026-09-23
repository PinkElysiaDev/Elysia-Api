package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/agent"

	"github.com/gin-gonic/gin"
)

// A2A 服务端（POST /a2a，JSON-RPC 2.0 + SSE）。双线格式按 A2A-Version 头
// 分派（缺省 v0.3；v1.0 用 PascalCase 方法名与大写蛇形枚举）。
//
//   - v0.3：message/send、message/stream、tasks/get、tasks/cancel、
//     tasks/resubscribe；push 配置四方法 → 不支持错误。
//   - v1.0：SendMessage、SendStreamingMessage、GetTask、ListTasks、
//     CancelTask、SubscribeToTask。
//
// 任务模型：contextId=会话 id，task=一轮（taskId=会话id:用户消息seq）。
// waiting_approval → input-required；恢复约定见 a2aResumeDecision。

const (
	a2aErrTaskNotFound      = -32001
	a2aErrTaskNotCancelable = -32002
	a2aErrUnsupported       = -32003
	a2aErrPushUnsupported   = -32006
	a2aRegistryLimit        = 512 // 任务登记上限（含已完结，超限淘汰最旧）
)

// a2aTaskEntry 是一轮的登记条目：最新任务快照 + 已产生的流帧（resubscribe
// 回放用）。帧由轮次消费路径（send 的后台 watcher 或 stream 的写循环）追加。
type a2aTaskEntry struct {
	mu     sync.Mutex
	task   a2aTask
	done   bool
	frames []a2aStreamFrame
	woke   chan struct{} // 容量 1：新帧信号（非阻塞投递）
}

func (e *a2aTaskEntry) snapshot() a2aTask {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.task
}

func (e *a2aTaskEntry) isDone() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.done
}

// append 追加一帧并更新任务状态快照。
func (e *a2aTaskEntry) append(frame a2aStreamFrame) {
	e.mu.Lock()
	e.frames = append(e.frames, frame)
	if frame.Kind == a2aFrameStatus && frame.State != "" {
		e.task.Status.State = frame.State
		if frame.Message != "" {
			e.task.Status.Message = &a2aMessage{MessageID: "status-" + e.task.ID, Role: "agent",
				Parts: []a2aPart{{Kind: "text", Text: frame.Message}}, TaskID: e.task.ID, ContextID: e.task.ContextID}
		}
		e.task.Status.Timestamp = a2aNowTime()
	}
	if frame.Kind == a2aFrameArtifact {
		e.task.Artifacts = append(e.task.Artifacts, frame.Artifact)
	}
	terminal := frame.Final && (frame.Kind != a2aFrameStatus || frame.State != a2aStateWorking)
	if frame.State == a2aStateCompleted || frame.State == a2aStateFailed ||
		frame.State == a2aStateCanceled || frame.State == a2aStateInputRequired {
		terminal = true
	}
	if terminal {
		e.done = true
	}
	e.mu.Unlock()
	// 唤醒监听者（无监听时丢弃信号）。
	select {
	case e.woke <- struct{}{}:
	default:
	}
}

// handleA2A 是 /a2a 统一入口：版本头校验 + 方法分派。
func (s *Server) handleA2A(c *gin.Context) {
	version := strings.TrimSpace(c.Request.Header.Get("A2A-Version"))
	if version != "" && version != a2aVersionV03 && version != a2aVersionV10 {
		c.JSON(http.StatusBadRequest, jsonrpcFail(nil, jsonrpcInvalidRequest,
			fmt.Sprintf("unsupported A2A-Version %q (supported: %s, %s)", version, a2aVersionV03, a2aVersionV10)))
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, agentMaxBodyBytes))
	if err != nil {
		c.JSON(http.StatusBadRequest, jsonrpcFail(nil, jsonrpcParseError, err.Error()))
		return
	}
	var req jsonrpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, jsonrpcFail(nil, jsonrpcParseError, err.Error()))
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		c.JSON(http.StatusBadRequest, jsonrpcFail(req.ID, jsonrpcInvalidRequest, `jsonrpc must be "2.0"`))
		return
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		// A2A 全部方法都是请求；无 id 一律按 invalid request 拒绝。
		c.JSON(http.StatusBadRequest, jsonrpcFail(nil, jsonrpcInvalidRequest, "A2A requires request id"))
		return
	}
	wire := a2aWire{v1: version == a2aVersionV10}
	// v1.0 与 v0.3 方法名对照分派。
	switch a2aMethodName(req.Method, wire.v1) {
	case "send":
		s.a2aHandleSend(c, req, wire, false)
	case "stream":
		s.a2aHandleSend(c, req, wire, true)
	case "get":
		s.a2aHandleGetTask(c, req, wire)
	case "cancel":
		s.a2aHandleCancel(c, req, wire)
	case "resubscribe":
		s.a2aHandleResubscribe(c, req, wire)
	case "list":
		s.a2aHandleListTasks(c, req, wire)
	case "pushConfig":
		s.a2aUnsupported(c, req, a2aErrPushUnsupported, "push notifications are not supported")
	case "extendedCard":
		s.a2aUnsupported(c, req, a2aErrUnsupported, "extended agent card is not supported")
	default:
		c.JSON(http.StatusNotFound, jsonrpcFail(req.ID, jsonrpcMethodNotFound, "method not found: "+req.Method))
	}
}

// a2aMethodName 把两代方法名归一到内部动词。
func a2aMethodName(method string, v1 bool) string {
	pairs := map[string]string{
		"message/send":                        "send",
		"message/stream":                      "stream",
		"tasks/get":                           "get",
		"tasks/cancel":                        "cancel",
		"tasks/resubscribe":                   "resubscribe",
		"SendMessage":                         "send",
		"SendStreamingMessage":                "stream",
		"GetTask":                             "get",
		"CancelTask":                          "cancel",
		"SubscribeToTask":                     "resubscribe",
		"ListTasks":                           "list",
		"tasks/pushNotificationConfig/set":    "pushConfig",
		"tasks/pushNotificationConfig/get":    "pushConfig",
		"tasks/pushNotificationConfig/list":   "pushConfig",
		"tasks/pushNotificationConfig/delete": "pushConfig",
		"CreateTaskPushNotificationConfig":    "pushConfig",
		"GetTaskPushNotificationConfig":       "pushConfig",
		"ListTaskPushNotificationConfigs":     "pushConfig",
		"DeleteTaskPushNotificationConfig":    "pushConfig",
		"agent/getAuthenticatedExtendedCard":  "extendedCard",
		"GetExtendedAgentCard":                "extendedCard",
	}
	return pairs[method]
}

func (s *Server) a2aUnsupported(c *gin.Context, req jsonrpcRequest, code int, message string) {
	c.JSON(http.StatusOK, jsonrpcFailData(req.ID, code, message,
		map[string]any{"reason": "UNSUPPORTED_OPERATION", "domain": "a2a-protocol.org"}))
}

// ---- 登记 ----

func (s *Server) a2aRegistry() (map[string]*a2aTaskEntry, map[string]string) {
	s.a2aMu.Lock()
	defer s.a2aMu.Unlock()
	if s.a2aTasks == nil {
		s.a2aTasks = map[string]*a2aTaskEntry{}
	}
	if s.a2aMessageIDs == nil {
		s.a2aMessageIDs = map[string]string{}
	}
	return s.a2aTasks, s.a2aMessageIDs
}

func (s *Server) a2aRegisterEntry(taskID, contextID string) *a2aTaskEntry {
	tasks, messages := s.a2aRegistry()
	s.a2aMu.Lock()
	defer s.a2aMu.Unlock()
	if len(tasks) >= a2aRegistryLimit {
		// 容量有界：淘汰若干最旧条目（map 无序，按到达粗略淘汰即可，
		// 老任务早可由 tasks/get 之外重建——实际上仅影响回放）。
		dropped := 0
		for id := range tasks {
			if dropped >= 64 {
				break
			}
			delete(tasks, id)
			dropped++
		}
	}
	entry := &a2aTaskEntry{woke: make(chan struct{}, 1), task: a2aTask{
		ID: taskID, ContextID: contextID,
		Status: a2aTaskStatus{State: a2aStateWorking, Timestamp: a2aNowTime()},
	}}
	tasks[taskID] = entry
	_ = messages
	return entry
}

func (s *Server) a2aFindEntry(taskID string) *a2aTaskEntry {
	tasks, _ := s.a2aRegistry()
	s.a2aMu.Lock()
	defer s.a2aMu.Unlock()
	return tasks[taskID]
}

// a2aRememberMessage 幂等登记：messageId 已见过返回对应 taskID，否则记录
// 并返回空串。
func (s *Server) a2aRememberMessage(messageID, taskID string) string {
	if strings.TrimSpace(messageID) == "" {
		return ""
	}
	_, messages := s.a2aRegistry()
	s.a2aMu.Lock()
	defer s.a2aMu.Unlock()
	if existing, ok := messages[messageID]; ok {
		return existing
	}
	messages[messageID] = taskID
	return ""
}

// ---- 任务 ID 与会话定位 ----

// a2aTaskID 组 taskId。
func a2aTaskID(sessionID string, userSeq int) string {
	return fmt.Sprintf("%s:%d", sessionID, userSeq)
}

// a2aParseTaskID 拆 taskId（会话 id 不含冒号）。
func a2aParseTaskID(taskID string) (sessionID string, ok bool) {
	at := strings.LastIndex(taskID, ":")
	if at <= 0 || at == len(taskID)-1 {
		return "", false
	}
	return taskID[:at], true
}

// ---- message/send 与 message/stream ----

func (s *Server) a2aHandleSend(c *gin.Context, req jsonrpcRequest, wire a2aWire, stream bool) {
	var params struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params.Message) == 0 {
		c.JSON(http.StatusOK, jsonrpcFail(req.ID, jsonrpcInvalidParams, "params.message is required"))
		return
	}
	message, err := parseA2AMessage(params.Message)
	if err != nil {
		c.JSON(http.StatusOK, jsonrpcFail(req.ID, jsonrpcInvalidParams, "invalid message: "+err.Error()))
		return
	}

	// 幂等：同 messageId 的重发返回已建任务，不重复执行副作用。
	if taskID := s.a2aRememberMessageProbe(message.MessageID); taskID != "" {
		if entry := s.a2aFindEntry(taskID); entry != nil {
			snapshot := entry.snapshot()
			if stream {
				s.a2aStreamReplay(c, req, wire, entry)
				return
			}
			c.JSON(http.StatusOK, jsonrpcOK(req.ID, wire.task(snapshot)))
			return
		}
	}

	if message.TaskID != "" {
		s.a2aResumeTurn(c, req, wire, message, stream)
		return
	}
	s.a2aStartTurn(c, req, wire, message, stream)
}

// a2aRememberMessageProbe 只查询不登记。
func (s *Server) a2aRememberMessageProbe(messageID string) string {
	if strings.TrimSpace(messageID) == "" {
		return ""
	}
	_, messages := s.a2aRegistry()
	s.a2aMu.Lock()
	defer s.a2aMu.Unlock()
	return messages[messageID]
}

// a2aStartTurn 开新一轮：定位（或创建）会话 → 启动轮次 → 注册任务。
func (s *Server) a2aStartTurn(c *gin.Context, req jsonrpcRequest, wire a2aWire, message a2aIncomingMessage, stream bool) {
	sessionID := strings.TrimSpace(message.ContextID)
	if sessionID != "" {
		if _, _, err := s.getRemoteAgentSession(c.Request.Context(), sessionID); err != nil {
			// 未知 contextId：按新线程处理（服务端是 contextId 的权威）。
			sessionID = ""
		}
	}
	if sessionID == "" {
		session, err := s.createRemoteAgentSession(c.Request.Context(), strings.TrimSpace(firstLine(message.Text)), agent.ModeCreate, "", nil)
		if err != nil {
			c.JSON(http.StatusOK, jsonrpcFail(req.ID, jsonrpcInternalError, "create session failed: "+err.Error()))
			return
		}
		sessionID = session.ID
	}
	events, err := s.startRemoteTurn(c.Request.Context(), sessionID, message.Text, nil)
	if err != nil {
		code, text := remoteTurnErrorInfo(err)
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, jsonrpcInternalError, text, map[string]any{"reason": strings.ToUpper(code)}))
		return
	}
	// 等首条 user 消息事件确定 taskId（引擎在轮次开始即落库用户消息）。
	var entry *a2aTaskEntry
	for event := range events {
		if event.Type == agent.EventMessage && event.Message != nil && event.Message.Role == agent.RoleUser {
			entry = s.a2aRegisterEntry(a2aTaskID(sessionID, event.Message.Seq), sessionID)
			break
		}
	}
	if entry == nil {
		c.JSON(http.StatusOK, jsonrpcFail(req.ID, jsonrpcInternalError, "turn ended before user message persisted"))
		return
	}
	s.a2aRememberMessage(message.MessageID, entry.task.ID)
	if stream {
		s.a2aStreamLive(c, req, wire, entry, events)
		return
	}
	// 非流式：立即返回 working 快照，后台 watcher 继续聚合。
	go s.a2aWatchTurn(entry, events)
	snapshot := entry.snapshot()
	snapshot.Status.State = a2aStateWorking
	c.JSON(http.StatusOK, jsonrpcOK(req.ID, wire.task(snapshot)))
}

// a2aResumeTurn 恢复等待中的任务（input-required 后带 taskId 重发）。
func (s *Server) a2aResumeTurn(c *gin.Context, req jsonrpcRequest, wire a2aWire, message a2aIncomingMessage, stream bool) {
	sessionID, ok := a2aParseTaskID(message.TaskID)
	if !ok {
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, jsonrpcInvalidParams, "malformed taskId",
			map[string]any{"reason": "TASK_NOT_FOUND", "domain": "a2a-protocol.org"}))
		return
	}
	entry := s.a2aFindEntry(message.TaskID)
	if entry == nil {
		// 兼容重启后的恢复：会话仍处 waiting_approval 时接受（登记表丢失）。
		if _, _, err := s.getRemoteAgentSession(c.Request.Context(), sessionID); err != nil {
			c.JSON(http.StatusOK, jsonrpcFailData(req.ID, a2aErrTaskNotFound, "task not found",
				map[string]any{"reason": "TASK_NOT_FOUND", "domain": "a2a-protocol.org"}))
			return
		}
		entry = s.a2aRegisterEntry(message.TaskID, sessionID)
	}
	session, _, err := s.getRemoteAgentSession(c.Request.Context(), sessionID)
	if err != nil || session.Status != agent.StatusWaitingApproval {
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, jsonrpcInvalidParams,
			"task is not waiting for input (only input-required tasks accept taskId)",
			map[string]any{"reason": "TASK_NOT_CANCELABLE", "domain": "a2a-protocol.org"}))
		return
	}
	decision, err := a2aResumeDecision(session, message)
	if err != nil {
		c.JSON(http.StatusOK, jsonrpcFail(req.ID, jsonrpcInvalidParams, err.Error()))
		return
	}
	events, err := s.respondRemoteApproval(c.Request.Context(), sessionID, decision)
	if err != nil {
		code, text := remoteTurnErrorInfo(err)
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, jsonrpcInternalError, text, map[string]any{"reason": strings.ToUpper(code)}))
		return
	}
	entry.append(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateWorking, Message: "已收到决策，继续执行"})
	s.a2aRememberMessage(message.MessageID, message.TaskID)
	if stream {
		s.a2aStreamLive(c, req, wire, entry, events)
		return
	}
	go s.a2aWatchTurn(entry, events)
	snapshot := entry.snapshot()
	c.JSON(http.StatusOK, jsonrpcOK(req.ID, wire.task(snapshot)))
}

// a2aResumeDecision 把恢复消息折算为审批决策：
//   - 审批/方案型：data part 必须显式给 approved（true/false），文本可作
//     修改意见（方案型拒绝时随 note 回给模型）；
//   - 提问型：文本即作答（data part 的 answer 优先）。
func a2aResumeDecision(session *agent.Session, message a2aIncomingMessage) (agent.ApprovalDecision, error) {
	decision := agent.ApprovalDecision{Note: strings.TrimSpace(message.Text)}
	kind := ""
	if session.PendingAction != nil {
		kind = session.PendingAction.Kind
	}
	if message.Decision != nil {
		if message.Decision.Approved != nil {
			decision.Approved = *message.Decision.Approved
		}
		if message.Decision.Answer != "" {
			decision.Answer = message.Decision.Answer
		}
		if message.Decision.APIKey != "" {
			decision.APIKey = message.Decision.APIKey
		}
		if message.Decision.BaseURL != "" {
			decision.BaseURL = message.Decision.BaseURL
		}
		if message.Decision.Note != "" {
			decision.Note = message.Decision.Note
		}
	}
	if kind == "question" {
		if decision.Answer == "" {
			decision.Answer = strings.TrimSpace(message.Text)
		}
		if decision.Answer == "" {
			return decision, fmt.Errorf("提问型任务需要文本作答（或 data part 的 answer 字段）")
		}
		return decision, nil
	}
	if message.Decision == nil || message.Decision.Approved == nil {
		return decision, fmt.Errorf("审批/方案确认必须在 data part 显式携带 {\"approved\": true|false}")
	}
	return decision, nil
}

// a2aWatchTurn 后台聚合轮次事件到登记条目（非流式 send / 恢复路径）。
func (s *Server) a2aWatchTurn(entry *a2aTaskEntry, events <-chan agent.Event) {
	sink := func(frame a2aStreamFrame) { entry.append(frame) }
	outcome := collectRemoteTurn(entry.task.ID, events, func(event agent.Event) {
		a2aEventFrame(event, sink)
	})
	a2aFinalizeEntry(entry, outcome, sink)
}

// a2aEventFrame 把引擎事件映射为 A2A 流帧（跳过不关心的事件）。
func a2aEventFrame(event agent.Event, sink func(a2aStreamFrame)) {
	switch event.Type {
	case agent.EventStatus:
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateWorking, Message: event.Text})
	case agent.EventToolCall:
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateWorking, Message: fmt.Sprintf("调用工具 %s", event.Name)})
	case agent.EventToolResult:
		state := "完成"
		if event.Result != nil && !event.Result.OK {
			state = "失败"
		}
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateWorking, Message: fmt.Sprintf("工具 %s %s", event.Name, state)})
	case agent.EventPlanUpdated:
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateWorking, Message: "工作方案已更新"})
	case agent.EventApprovalPending:
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateInputRequired, Message: a2aPendingSummary(event.Approval), Final: true})
	case agent.EventError:
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateFailed, Message: event.Text, Final: true})
	}
}

// a2aFinalizeEntry 轮次终稿：完成态产物帧 + 状态收尾。
func a2aFinalizeEntry(entry *a2aTaskEntry, outcome remoteTurnOutcome, sink func(a2aStreamFrame)) {
	switch {
	case outcome.Error != "":
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateFailed, Message: outcome.Error, Final: true})
	case outcome.Pending != nil:
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateInputRequired, Message: a2aPendingSummary(outcome.Pending), Final: true})
	default:
		if outcome.Reply != "" {
			sink(a2aStreamFrame{Kind: a2aFrameArtifact, Artifact: a2aArtifact{
				ArtifactID: "reply", Name: "assistant-reply",
				Parts: []a2aPart{
					{Kind: "text", Text: outcome.Reply},
					{Kind: "data", Data: map[string]any{"rounds": outcome.Rounds, "model": outcome.Model, "durationMs": outcome.DurationMs}},
				},
			}})
		}
		sink(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateCompleted, Final: true})
	}
}

// a2aPendingSummary 待批动作的人话摘要（事件与轮次聚合共用）。
func a2aPendingSummary(pending *agent.PendingAction) string {
	if pending == nil {
		return "等待输入"
	}
	switch pending.Kind {
	case "question":
		summary := "等待回答：" + pending.Question.Question
		if len(pending.Question.Options) > 0 {
			labels := make([]string, 0, len(pending.Question.Options))
			for _, option := range pending.Question.Options {
				labels = append(labels, option.Label)
			}
			summary += "（选项：" + strings.Join(labels, " / ") + "）"
		}
		return summary
	case "plan":
		return fmt.Sprintf("等待方案确认（%d 步）：用 data part {\"approved\": true|false} 回复；拒绝可在文本里附修改意见", len(pending.Plan))
	default:
		names := make([]string, 0, len(pending.Calls))
		for _, call := range pending.Calls {
			names = append(names, call.Name)
		}
		summary := "等待审批"
		if pending.Reason != "" {
			summary += "：" + pending.Reason
		}
		if len(names) > 0 {
			summary += "（工具：" + strings.Join(names, "、") + "）"
		}
		summary += "；用 data part {\"approved\": true|false} 回复"
		return summary
	}
}

// ---- 流式输出 ----

// a2aStreamLive 实时流：先发任务快照帧，随后逐帧转发至终态/中断态关流。
func (s *Server) a2aStreamLive(c *gin.Context, req jsonrpcRequest, wire a2aWire, entry *a2aTaskEntry, events <-chan agent.Event) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := c.Writer.(http.Flusher)
	writeResult := func(result gin.H) bool {
		encoded, err := json.Marshal(jsonrpcOK(req.ID, result))
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", encoded); err != nil {
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}
	writeResult(wire.task(entry.snapshot()))
	entry.append(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateWorking})
	// 流式路径的 sink 双写：帧既出站也入登记表（tasks/get 与重订阅才有
	// 完整现场）。
	sink := func(frame a2aStreamFrame) {
		entry.append(frame)
		writeResult(wire.frame(entry.task.ID, entry.task.ContextID, frame))
	}
	outcome := collectRemoteTurn(entry.task.ID, events, func(event agent.Event) {
		a2aEventFrame(event, sink)
	})
	a2aFinalizeEntry(entry, outcome, sink)
}

// a2aStreamReplay 重订阅：回放登记帧，未完结则跟随新帧。
func (s *Server) a2aStreamReplay(c *gin.Context, req jsonrpcRequest, wire a2aWire, entry *a2aTaskEntry) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := c.Writer.(http.Flusher)
	writeResult := func(result gin.H) bool {
		encoded, err := json.Marshal(jsonrpcOK(req.ID, result))
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", encoded); err != nil {
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}
	cursor := 0
	for {
		entry.mu.Lock()
		frames := append([]a2aStreamFrame(nil), entry.frames...)
		done := entry.done
		entry.mu.Unlock()
		for ; cursor < len(frames); cursor++ {
			if !writeResult(wire.frame(entry.task.ID, entry.task.ContextID, frames[cursor])) {
				return
			}
		}
		if done {
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-entry.woke:
		case <-time.After(2 * time.Second):
			// 保活兜底：无新帧时写 SSE 注释行。
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// ---- tasks/get | tasks/cancel | resubscribe | ListTasks ----

func (s *Server) a2aHandleGetTask(c *gin.Context, req jsonrpcRequest, wire a2aWire) {
	var params struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(req.Params, &params)
	entry := s.a2aFindEntry(strings.TrimSpace(params.ID))
	if entry == nil {
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, a2aErrTaskNotFound, "task not found",
			map[string]any{"reason": "TASK_NOT_FOUND", "domain": "a2a-protocol.org"}))
		return
	}
	c.JSON(http.StatusOK, jsonrpcOK(req.ID, wire.task(entry.snapshot())))
}

func (s *Server) a2aHandleCancel(c *gin.Context, req jsonrpcRequest, wire a2aWire) {
	var params struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(req.Params, &params)
	taskID := strings.TrimSpace(params.ID)
	entry := s.a2aFindEntry(taskID)
	if entry == nil {
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, a2aErrTaskNotFound, "task not found",
			map[string]any{"reason": "TASK_NOT_FOUND", "domain": "a2a-protocol.org"}))
		return
	}
	if entry.isDone() && entry.snapshot().Status.State != a2aStateInputRequired {
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, a2aErrTaskNotCancelable, "task already in terminal state",
			map[string]any{"reason": "TASK_NOT_CANCELABLE", "domain": "a2a-protocol.org"}))
		return
	}
	sessionID, _ := a2aParseTaskID(taskID)
	if session, _, err := s.getRemoteAgentSession(c.Request.Context(), sessionID); err == nil &&
		session.Status == agent.StatusWaitingApproval && !s.isSessionRunning(sessionID) {
		// 暂停中的任务：以拒绝收尾（释放会话，模型收到拒绝结果）。续跑的
		// 事件流由后台丢弃消费（任务已终态，不再更新登记表）。
		if events, err := s.respondRemoteApproval(c.Request.Context(), sessionID, agent.ApprovalDecision{Approved: false, Note: "A2A 客户端取消"}); err == nil {
			go func() {
				for range events {
				}
			}()
		}
	} else {
		go s.stopRemoteTurn(sessionID)
	}
	entry.append(a2aStreamFrame{Kind: a2aFrameStatus, State: a2aStateCanceled, Message: "已取消", Final: true})
	c.JSON(http.StatusOK, jsonrpcOK(req.ID, wire.task(entry.snapshot())))
}

func (s *Server) a2aHandleResubscribe(c *gin.Context, req jsonrpcRequest, wire a2aWire) {
	var params struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(req.Params, &params)
	entry := s.a2aFindEntry(strings.TrimSpace(params.ID))
	if entry == nil {
		c.JSON(http.StatusOK, jsonrpcFailData(req.ID, a2aErrTaskNotFound, "task not found",
			map[string]any{"reason": "TASK_NOT_FOUND", "domain": "a2a-protocol.org"}))
		return
	}
	s.a2aStreamReplay(c, req, wire, entry)
}

func (s *Server) a2aHandleListTasks(c *gin.Context, req jsonrpcRequest, wire a2aWire) {
	var params struct {
		Cursor   string `json:"cursor"`
		PageSize int    `json:"pageSize"`
	}
	_ = json.Unmarshal(req.Params, &params)
	tasks, _ := s.a2aRegistry()
	s.a2aMu.Lock()
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	s.a2aMu.Unlock()
	sortStrings(ids)
	offset := 0
	if params.Cursor != "" {
		for i, id := range ids {
			if id == params.Cursor {
				offset = i + 1
				break
			}
		}
	}
	size := defaultInt(params.PageSize, 50, 200)
	end := offset + size
	if end > len(ids) {
		end = len(ids)
	}
	items := make([]gin.H, 0, end-offset)
	for _, id := range ids[offset:end] {
		if entry := s.a2aFindEntry(id); entry != nil {
			items = append(items, wire.task(entry.snapshot()))
		}
	}
	result := gin.H{"tasks": items}
	if end < len(ids) && end > offset {
		result["nextCursor"] = ids[end-1]
	}
	c.JSON(http.StatusOK, jsonrpcOK(req.ID, result))
}

// isSessionRunning 引擎运行态口径。
func (s *Server) isSessionRunning(sessionID string) bool {
	engine := s.protocolAgentEngine()
	return engine != nil && engine.IsRunning(sessionID)
}

// firstLine 取首行作会话标题（引擎对首条消息另有截断兜底）。
func firstLine(text string) string {
	if at := strings.IndexAny(text, "\r\n"); at > 0 {
		return text[:at]
	}
	return text
}

// sortStrings 简单字典序（避免引入 sort 之外的依赖顾虑——sort 是标准库，
// 这里直接内联小工具保持文件自包含）。
func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
