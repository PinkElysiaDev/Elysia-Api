package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/elysia-api/backend/agent"

	"golang.org/x/sync/singleflight"
)

// SDK wire models and business interfaces are shared by the native Hertz
// transport. This service owns session/turn/task and approval policy.
type a2aService struct {
	server        *Server
	mu            sync.Mutex
	nextTaskOrder uint64
	tasks         map[a2a.TaskID]*a2aTaskEntry
	messages      map[string]a2aRun
	flights       singleflight.Group
}

var _ a2asrv.RequestHandler = (*a2aService)(nil)

func (s *Server) a2aService() *a2aService {
	s.a2aOnce.Do(func() {
		h := &a2aService{server: s, tasks: make(map[a2a.TaskID]*a2aTaskEntry), messages: make(map[string]a2aRun)}
		s.a2aRuntime = h
	})
	return s.a2aRuntime
}

// a2aRun identifies one execution within a resumable task. Its starting cursor
// prevents a resumed send from replaying the previous input-required boundary.
type a2aRun struct {
	entry   *a2aTaskEntry
	initial *a2a.Task
	cursor  int
}

func (h *a2aService) begin(ctx context.Context, req *a2a.SendMessageRequest) (a2aRun, error) {
	if req == nil || req.Message == nil || req.Message.ID == "" || req.Message.Role != a2a.MessageRoleUser || len(req.Message.Parts) == 0 {
		return a2aRun{}, a2a.ErrInvalidParams
	}
	if req.Config != nil {
		if req.Config.PushConfig != nil {
			return a2aRun{}, a2a.ErrPushNotificationNotSupported
		}
		if req.Config.HistoryLength != nil && *req.Config.HistoryLength < 0 {
			return a2aRun{}, a2a.ErrInvalidParams
		}
	}
	message, err := parseA2AMessage(req.Message)
	if err != nil {
		return a2aRun{}, err
	}
	if message.TaskID == "" && strings.TrimSpace(message.Text) == "" {
		return a2aRun{}, a2a.NewError(a2a.ErrInvalidParams, "a new task requires text")
	}
	value, err, _ := h.flights.Do(message.MessageID, func() (any, error) {
		h.mu.Lock()
		previous, found := h.messages[message.MessageID]
		h.mu.Unlock()
		if found {
			return previous, nil
		}
		var run a2aRun
		var err error
		if message.TaskID != "" {
			run, err = h.resume(ctx, message)
		} else {
			run, err = h.start(ctx, message)
		}
		if err == nil {
			h.mu.Lock()
			if h.tasks[run.entry.id] == run.entry {
				h.messages[message.MessageID] = run
			}
			h.mu.Unlock()
		}
		return run, err
	})
	if err != nil {
		return a2aRun{}, err
	}
	return value.(a2aRun), nil
}

func (h *a2aService) start(ctx context.Context, message a2aIncomingMessage) (a2aRun, error) {
	s := h.server
	sessionID := strings.TrimSpace(message.ContextID)
	if sessionID != "" {
		if _, _, err := s.getRemoteAgentSession(ctx, sessionID); err != nil {
			sessionID = ""
		}
	}
	if sessionID == "" {
		title, _, _ := strings.Cut(strings.TrimSpace(message.Text), "\n")
		session, err := s.createRemoteAgentSession(ctx, title, agent.ModeCreate, "", nil)
		if err != nil {
			return a2aRun{}, a2a.NewError(a2a.ErrInternalError, err.Error())
		}
		sessionID = session.ID
	}
	events, err := s.startRemoteTurn(ctx, sessionID, message.Text, nil)
	if err != nil {
		return a2aRun{}, a2aTurnError(err)
	}
	for event := range events {
		if event.Type == agent.EventMessage && event.Message != nil && event.Message.Role == agent.RoleUser {
			entry := h.register(a2a.TaskID(fmt.Sprintf("%s:%d", sessionID, event.Message.Seq)), sessionID, true)
			h.supersede(sessionID, entry.id)
			run := a2aRun{entry: entry, initial: entry.snapshot()}
			go h.watch(entry, events)
			return run, nil
		}
	}
	return a2aRun{}, a2a.NewError(a2a.ErrInternalError, "turn ended before user message persisted")
}

func (h *a2aService) resume(ctx context.Context, message a2aIncomingMessage) (a2aRun, error) {
	entry := h.find(message.TaskID)
	if entry == nil {
		// The durable Agent session can still be awaiting approval after an
		// application restart, even though the bounded task registry was lost.
		sessionID, _, ok := strings.Cut(string(message.TaskID), ":")
		if !ok || sessionID == "" {
			return a2aRun{}, a2a.ErrTaskNotFound
		}
		session, _, err := h.server.getRemoteAgentSession(ctx, sessionID)
		if err != nil || session.Status != agent.StatusWaitingApproval {
			return a2aRun{}, a2a.ErrTaskNotFound
		}
		entry = h.register(message.TaskID, sessionID, false)
	}
	entry.execution.Lock()
	defer entry.execution.Unlock()
	if entry.snapshot().Status.State.Terminal() {
		return a2aRun{}, a2a.ErrInvalidParams
	}
	session, _, err := h.server.getRemoteAgentSession(ctx, entry.contextID)
	if err != nil || session.Status != agent.StatusWaitingApproval {
		return a2aRun{}, a2a.NewError(a2a.ErrInvalidParams, "task is not waiting for input")
	}
	// Finish aggregation of the previous turn before publishing a new working
	// state; its late input-required event must not overwrite the resumed turn.
	select {
	case <-entry.roundDone:
	case <-ctx.Done():
		return a2aRun{}, ctx.Err()
	}
	if entry.snapshot().Status.State.Terminal() {
		return a2aRun{}, a2a.ErrInvalidParams
	}
	decision, err := a2aResumeDecision(session, message)
	if err != nil {
		return a2aRun{}, a2a.NewError(a2a.ErrInvalidParams, err.Error())
	}
	events, err := h.server.respondRemoteApproval(ctx, entry.contextID, decision)
	if err != nil {
		return a2aRun{}, a2aTurnError(err)
	}
	entry.mu.Lock()
	cursor := len(entry.events)
	entry.mu.Unlock()
	entry.setStatus(a2a.TaskStateWorking, "已收到决策，继续执行")
	run := a2aRun{entry: entry, initial: entry.snapshot(), cursor: cursor}
	entry.roundDone = make(chan struct{})
	go h.watch(entry, events)
	return run, nil
}

func (h *a2aService) SendMessage(ctx context.Context, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	run, err := h.begin(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.Config == nil || !req.Config.ReturnImmediately {
		for _, err := range run.entry.follow(ctx, run.cursor, true) {
			if err != nil {
				return nil, err
			}
		}
	}
	task := run.entry.snapshot()
	if req.Config != nil {
		trimA2AHistory(task, req.Config.HistoryLength)
	}
	return task, nil
}

func (h *a2aService) SendStreamingMessage(ctx context.Context, req *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		run, err := h.begin(ctx, req)
		if err != nil {
			yield(nil, err)
			return
		}
		if !yield(run.initial, nil) {
			return
		}
		for event, err := range run.entry.follow(ctx, run.cursor, true) {
			if !yield(event, err) {
				return
			}
		}
	}
}

func (h *a2aService) GetTask(_ context.Context, req *a2a.GetTaskRequest) (*a2a.Task, error) {
	if req == nil || req.ID == "" || (req.HistoryLength != nil && *req.HistoryLength < 0) {
		return nil, a2a.ErrInvalidParams
	}
	entry := h.find(req.ID)
	if entry == nil {
		return nil, a2a.ErrTaskNotFound
	}
	task := entry.snapshot()
	trimA2AHistory(task, req.HistoryLength)
	return task, nil
}

func (h *a2aService) SubscribeToTask(ctx context.Context, req *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if req == nil || req.ID == "" {
			yield(nil, a2a.ErrInvalidParams)
			return
		}
		entry := h.find(req.ID)
		if entry == nil {
			yield(nil, a2a.ErrTaskNotFound)
			return
		}
		for event, err := range entry.follow(ctx, 0, false) {
			if !yield(event, err) {
				return
			}
		}
	}
}

func (h *a2aService) CancelTask(ctx context.Context, req *a2a.CancelTaskRequest) (*a2a.Task, error) {
	if req == nil || req.ID == "" {
		return nil, a2a.ErrInvalidParams
	}
	entry := h.find(req.ID)
	if entry == nil {
		return nil, a2a.ErrTaskNotFound
	}
	entry.execution.Lock()
	defer entry.execution.Unlock()
	if !entry.setStatus(a2a.TaskStateCanceled, "已取消") {
		return nil, a2a.ErrTaskNotCancelable
	}
	s := h.server
	if session, _, err := s.getRemoteAgentSession(ctx, entry.contextID); err == nil && session.Status == agent.StatusWaitingApproval {
		if events, err := s.respondRemoteApproval(ctx, entry.contextID, agent.ApprovalDecision{Approved: false, Note: "A2A 客户端取消"}); err == nil {
			go func() {
				for range events {
				}
			}()
		}
	} else {
		s.stopRemoteTurn(entry.contextID)
	}
	return entry.snapshot(), nil
}

func (h *a2aService) ListTasks(_ context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	if req == nil || req.PageSize < 0 || req.PageSize > 100 || (req.HistoryLength != nil && *req.HistoryLength < 0) {
		return nil, a2a.ErrInvalidParams
	}
	size := req.PageSize
	if size == 0 {
		size = 50
	}
	h.mu.Lock()
	entries := make([]*a2aTaskEntry, 0, len(h.tasks))
	for _, entry := range h.tasks {
		entries = append(entries, entry)
	}
	h.mu.Unlock()
	tasks := make([]*a2a.Task, 0, len(entries))
	for _, entry := range entries {
		task := entry.snapshot()
		if req.ContextID != "" && task.ContextID != req.ContextID {
			continue
		}
		if req.Status != "" && task.Status.State != req.Status {
			continue
		}
		if req.StatusTimestampAfter != nil && !task.Status.Timestamp.After(*req.StatusTimestampAfter) {
			continue
		}
		if !req.IncludeArtifacts {
			task.Artifacts = nil
		}
		trimA2AHistory(task, req.HistoryLength)
		tasks = append(tasks, task)
	}
	slices.SortFunc(tasks, func(a, b *a2a.Task) int {
		if order := b.Status.Timestamp.Compare(*a.Status.Timestamp); order != 0 {
			return order
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	offset := 0
	if req.PageToken != "" {
		id, err := base64.RawURLEncoding.DecodeString(req.PageToken)
		if err != nil {
			return nil, a2a.ErrInvalidParams
		}
		index := slices.IndexFunc(tasks, func(task *a2a.Task) bool { return task.ID == a2a.TaskID(id) })
		if index < 0 {
			return nil, a2a.ErrInvalidParams
		}
		offset = index + 1
	}
	end := min(offset+size, len(tasks))
	result := &a2a.ListTasksResponse{Tasks: tasks[offset:end], TotalSize: len(tasks), PageSize: size}
	if end < len(tasks) {
		result.NextPageToken = base64.RawURLEncoding.EncodeToString([]byte(tasks[end-1].ID))
	}
	return result, nil
}

func trimA2AHistory(task *a2a.Task, length *int) {
	if length != nil && *length >= 0 && *length < len(task.History) {
		task.History = task.History[len(task.History)-*length:]
	}
}

func (*a2aService) GetTaskPushConfig(context.Context, *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}
func (*a2aService) ListTaskPushConfigs(context.Context, *a2a.ListTaskPushConfigRequest) (*a2a.ListTaskPushConfigResponse, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}
func (*a2aService) CreateTaskPushConfig(context.Context, *a2a.PushConfig) (*a2a.PushConfig, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}
func (*a2aService) DeleteTaskPushConfig(context.Context, *a2a.DeleteTaskPushConfigRequest) error {
	return a2a.ErrPushNotificationNotSupported
}
func (*a2aService) GetExtendedAgentCard(context.Context, *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return nil, a2a.ErrExtendedCardNotConfigured
}

func a2aTurnError(err error) error {
	code, message := remoteTurnErrorInfo(err)
	return a2a.NewError(a2a.ErrInternalError, message).WithDetails(map[string]any{"reason": strings.ToUpper(code)})
}
