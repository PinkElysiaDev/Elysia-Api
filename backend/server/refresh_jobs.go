package server

import (
	"context"
	"slices"
	"sync"
	"time"
)

const (
	// sourceRefreshConcurrency 限制同时进行的源拉取任务数（源间并发上限；
	// 单源内部的 per-key 拉取本就并行）。
	sourceRefreshConcurrency = 4
	// sourceRefreshBudget 是单个后台拉取任务的总预算：上游再慢也必然结束，
	// 任务状态不会永久停留在「进行中」。
	sourceRefreshBudget = 10 * time.Minute
)

// sourceRefreshState 是源的运行时拉取状态（不落库，叠加在 GET /model-sources
// 的列表项上供前端轮询）：refreshing 表示后台任务进行中；last* 为最近一次
// 任务的结果快照。
type sourceRefreshState struct {
	Refreshing     bool              `json:"refreshing"`
	LastCount      int               `json:"lastCount,omitempty"`
	LastAdded      int               `json:"lastAdded,omitempty"`
	LastRemoved    int               `json:"lastRemoved,omitempty"`
	LastError      string            `json:"lastError,omitempty"`
	LastFinishedAt string            `json:"lastFinishedAt,omitempty"`
	LastKeys       []keyFetchOutcome `json:"lastKeys,omitempty"`
}

// sourceRefreshJobs owns registration, coalescing and completion. Its callbacks
// adapt source persistence and protocol discovery without exposing Server.
type sourceRefreshJobs struct {
	once       sync.Once
	mu         sync.Mutex
	wg         sync.WaitGroup
	ctx        context.Context
	refresh    func(context.Context, string) (refreshSummary, error)
	exists     func(context.Context, string) (bool, error)
	invalidate func()
	log        func(string, string, map[string]any)
	running    map[string]bool
	pending    map[string]bool
	last       map[string]sourceRefreshState
	sem        chan struct{}
}

func (j *sourceRefreshJobs) begin(sourceID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ctx.Err() != nil || j.running[sourceID] {
		return false
	}
	if j.running == nil {
		j.running = make(map[string]bool)
	}
	if j.pending == nil {
		j.pending = make(map[string]bool)
	}
	if j.last == nil {
		j.last = make(map[string]sourceRefreshState)
	}
	if j.sem == nil {
		j.sem = make(chan struct{}, sourceRefreshConcurrency)
	}
	j.running[sourceID] = true
	j.wg.Add(1)
	return true
}

// Saves coalesce into one follow-up; ordinary refresh clicks only deduplicate.
func (j *sourceRefreshJobs) noteSaved(sourceID string, autoFetch bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running[sourceID] {
		j.pending[sourceID] = autoFetch
	}
}

func (j *sourceRefreshJobs) run(parent context.Context, sourceID string) (refreshSummary, error) {
	defer j.wg.Done()
	defer func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if j.pending[sourceID] && j.ctx.Err() == nil {
			// A saved configuration outlives a canceled synchronous caller.
			j.wg.Add(1)
			go j.run(j.ctx, sourceID)
		} else {
			delete(j.running, sourceID)
			delete(j.pending, sourceID)
		}
	}()
	ctx, cancel := context.WithTimeout(parent, sourceRefreshBudget)
	defer cancel()
	stop := context.AfterFunc(j.ctx, cancel)
	defer stop()
	select {
	case j.sem <- struct{}{}:
	case <-ctx.Done():
		return refreshSummary{}, ctx.Err()
	}
	defer func() { <-j.sem }()

	for {
		j.mu.Lock()
		delete(j.pending, sourceID)
		j.mu.Unlock()
		summary, err := j.refresh(ctx, sourceID)
		if err == nil {
			j.invalidate()
		}
		j.mu.Lock()
		if j.pending[sourceID] && ctx.Err() == nil {
			j.mu.Unlock()
			continue
		}
		logCtx, logCancel := context.WithTimeout(context.Background(), 5*time.Second)
		found, lookupErr := j.exists(logCtx, sourceID)
		if found && lookupErr == nil {
			state := sourceRefreshState{
				LastCount: summary.Count, LastAdded: len(summary.Added), LastRemoved: len(summary.Removed),
				LastKeys: summary.Keys, LastFinishedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}
			level, message := "info", "model source refreshed"
			if err != nil {
				state.LastError = err.Error()
				level, message = "warn", "model source refresh failed"
			}
			j.last[sourceID] = state
			j.log(level, message, map[string]any{
				"sourceId": sourceID, "count": summary.Count, "error": state.LastError,
			})
		}
		logCancel()
		j.mu.Unlock()
		return summary, err
	}
}

func (j *sourceRefreshJobs) state(sourceID string) sourceRefreshState {
	j.mu.Lock()
	defer j.mu.Unlock()
	state := j.last[sourceID]
	state.Refreshing = j.running[sourceID]
	state.LastKeys = slices.Clone(state.LastKeys)
	return state
}

func (j *sourceRefreshJobs) forget(sourceID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.pending, sourceID)
	delete(j.last, sourceID)
}

// Cancellation must precede wait: synchronize with the last registration before
// joining workers. Follow-ups register before their predecessor releases ownership.
func (j *sourceRefreshJobs) wait() {
	j.mu.Lock()
	//lint:ignore SA2001 Synchronize registration before joining workers.
	j.mu.Unlock()
	j.wg.Wait()
}
