package server

import (
	"context"
	"fmt"
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

func (s *Server) beginSourceRefresh(sourceID string) bool {
	s.initLifecycle()
	s.sourceRefreshMu.Lock()
	defer s.sourceRefreshMu.Unlock()
	if s.lifecycleCtx.Err() != nil || s.sourceRefreshing[sourceID] {
		return false
	}
	if s.sourceRefreshing == nil {
		s.sourceRefreshing = make(map[string]bool)
	}
	if s.sourceRefreshPending == nil {
		s.sourceRefreshPending = make(map[string]bool)
	}
	if s.sourceLastFetch == nil {
		s.sourceLastFetch = make(map[string]sourceRefreshState)
	}
	if s.refreshSem == nil {
		s.refreshSem = make(chan struct{}, sourceRefreshConcurrency)
	}
	s.sourceRefreshing[sourceID] = true
	s.sourceRefreshWG.Add(1)
	return true
}

func (s *Server) launchSourceRefresh(sourceID string) bool {
	if !s.beginSourceRefresh(sourceID) {
		return false
	}
	go s.runSourceRefresh(s.lifecycleCtx, sourceID)
	return true
}

// Saves coalesce into one follow-up; ordinary refresh clicks only deduplicate.
func (s *Server) noteSourceSaved(sourceID string, autoFetch bool) {
	s.sourceRefreshMu.Lock()
	defer s.sourceRefreshMu.Unlock()
	if s.sourceRefreshing[sourceID] {
		s.sourceRefreshPending[sourceID] = autoFetch
	}
}

func (s *Server) startSourceRefreshByID(ctx context.Context, sourceID string) (bool, bool, error) {
	_, found, err := s.findSourceByID(ctx, sourceID)
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, fmt.Errorf("model source %q not found", sourceID)
	}
	started := s.launchSourceRefresh(sourceID)
	return started, !started, nil
}

func (s *Server) refreshSourceSync(ctx context.Context, sourceID string) (refreshSummary, error) {
	if !s.beginSourceRefresh(sourceID) {
		return refreshSummary{Added: []string{}, Removed: []string{}}, fmt.Errorf("model source %q is already refreshing or server is stopping", sourceID)
	}
	return s.runSourceRefresh(ctx, sourceID)
}

func (s *Server) runSourceRefresh(parent context.Context, sourceID string) (refreshSummary, error) {
	defer s.sourceRefreshWG.Done()
	defer func() {
		s.sourceRefreshMu.Lock()
		defer s.sourceRefreshMu.Unlock()
		if s.sourceRefreshPending[sourceID] && s.lifecycleCtx.Err() == nil {
			// A saved configuration outlives a canceled synchronous caller.
			s.sourceRefreshWG.Add(1)
			go s.runSourceRefresh(s.lifecycleCtx, sourceID)
		} else {
			delete(s.sourceRefreshing, sourceID)
			delete(s.sourceRefreshPending, sourceID)
		}
	}()
	ctx, cancel := context.WithTimeout(parent, sourceRefreshBudget)
	defer cancel()
	stop := context.AfterFunc(s.lifecycleCtx, cancel)
	defer stop()
	select {
	case s.refreshSem <- struct{}{}:
	case <-ctx.Done():
		return refreshSummary{}, ctx.Err()
	}
	defer func() { <-s.refreshSem }()

	for {
		s.sourceRefreshMu.Lock()
		delete(s.sourceRefreshPending, sourceID)
		s.sourceRefreshMu.Unlock()
		summary, err := s.refreshSourceByID(ctx, sourceID)
		if err == nil {
			s.invalidateRouteCache()
		}
		s.sourceRefreshMu.Lock()
		if s.sourceRefreshPending[sourceID] && ctx.Err() == nil {
			s.sourceRefreshMu.Unlock()
			continue
		}
		logCtx, logCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, found, lookupErr := s.findSourceByID(logCtx, sourceID)
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
			s.sourceLastFetch[sourceID] = state
			_ = s.store.InsertSystemLog(logCtx, level, message, map[string]any{
				"sourceId": sourceID, "count": summary.Count, "error": state.LastError,
			})
		}
		logCancel()
		s.sourceRefreshMu.Unlock()
		return summary, err
	}
}

// sourceRefreshStateOf 返回指定源的当前拉取状态快照（含进行中标志）。
func (s *Server) sourceRefreshStateOf(sourceID string) sourceRefreshState {
	s.sourceRefreshMu.Lock()
	defer s.sourceRefreshMu.Unlock()
	state := sourceRefreshState{Refreshing: s.sourceRefreshing[sourceID]}
	if last, ok := s.sourceLastFetch[sourceID]; ok {
		state.LastCount = last.LastCount
		state.LastAdded = last.LastAdded
		state.LastRemoved = last.LastRemoved
		state.LastError = last.LastError
		state.LastFinishedAt = last.LastFinishedAt
		state.LastKeys = last.LastKeys
	}
	return state
}
