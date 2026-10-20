package server

import (
	"context"
	"iter"
	"slices"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const a2aRegistryLimit = 512

// Bounded, process-local mapping of gateway turns to SDK tasks. Published SDK
// events and their nested values are immutable; snapshots copy mutable slices.
type a2aTaskEntry struct {
	id        a2a.TaskID
	contextID string
	createdAt time.Time
	execution sync.Mutex    // serializes approval resumption and cancellation
	roundDone chan struct{} // guarded by execution once the entry is published
	mu        sync.Mutex
	task      a2a.Task
	events    []a2a.Event
	changed   chan struct{} // broadcast to every subscriber, not just one waiter
}

func (e *a2aTaskEntry) snapshot() *a2a.Task {
	e.mu.Lock()
	defer e.mu.Unlock()
	copy := e.task
	copy.Artifacts = slices.Clone(e.task.Artifacts)
	copy.History = slices.Clone(e.task.History)
	return &copy
}

func (e *a2aTaskEntry) append(event a2a.Event) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.task.Status.State.Terminal() {
		return false
	}
	switch event := event.(type) {
	case *a2a.TaskStatusUpdateEvent:
		e.task.Status = event.Status
	case *a2a.TaskArtifactUpdateEvent:
		e.task.Artifacts = append(e.task.Artifacts, event.Artifact)
	}
	e.events = append(e.events, event)
	close(e.changed)
	e.changed = make(chan struct{})
	return true
}

func (e *a2aTaskEntry) setStatus(state a2a.TaskState, text string) bool {
	info := a2a.TaskInfo{TaskID: e.id, ContextID: e.contextID}
	var message *a2a.Message
	if text != "" {
		message = a2a.NewMessageForTask(a2a.MessageRoleAgent, info, a2a.NewTextPart(text))
	}
	return e.append(a2a.NewStatusUpdateEvent(info, state, message))
}

func (e *a2aTaskEntry) follow(ctx context.Context, cursor int, stopOnInput bool) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		for {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			e.mu.Lock()
			events := slices.Clone(e.events[cursor:])
			state, changed := e.task.Status.State, e.changed
			e.mu.Unlock()
			for _, event := range events {
				cursor++
				if !yield(event, nil) {
					return
				}
				if update, ok := event.(*a2a.TaskStatusUpdateEvent); ok && stopOnInput && update.Status.State == a2a.TaskStateInputRequired {
					return
				}
			}
			if state.Terminal() {
				return
			}
			select {
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			case <-changed:
			}
		}
	}
}

func (h *a2aService) find(id a2a.TaskID) *a2aTaskEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tasks[id]
}

func (h *a2aService) register(id a2a.TaskID, contextID string, running bool) *a2aTaskEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	if entry := h.tasks[id]; entry != nil {
		return entry
	}
	if len(h.tasks) >= a2aRegistryLimit {
		var oldest *a2aTaskEntry
		for _, entry := range h.tasks {
			terminal := entry.snapshot().Status.State.Terminal()
			if oldest == nil || (terminal && !oldest.snapshot().Status.State.Terminal()) ||
				(terminal == oldest.snapshot().Status.State.Terminal() && entry.createdAt.Before(oldest.createdAt)) {
				oldest = entry
			}
		}
		delete(h.tasks, oldest.id)
		for id, run := range h.messages {
			if run.entry == oldest {
				delete(h.messages, id)
			}
		}
	}
	now := time.Now().UTC()
	entry := &a2aTaskEntry{id: id, contextID: contextID, createdAt: now, changed: make(chan struct{}), roundDone: make(chan struct{}),
		task: a2a.Task{ID: id, ContextID: contextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Timestamp: &now}}}
	if !running {
		close(entry.roundDone)
	}
	h.tasks[id] = entry
	return entry
}

func (h *a2aService) supersede(contextID string, keep a2a.TaskID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, entry := range h.tasks {
		if id != keep && entry.contextID == contextID {
			entry.setStatus(a2a.TaskStateFailed, "已被同会话的新任务取代")
		}
	}
}
