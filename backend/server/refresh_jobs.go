package server

import (
	"context"
	"fmt"
	"time"

	"github.com/elysia-api/backend/storage"
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

// launchSourceRefresh 为源启动后台拉取任务；已在进行中返回 false（去重）。
// 任务使用独立 context（页面跳开/断开不中断）+ 总预算，结束后记录状态快照、
// 写系统日志并失效路由缓存。内存态在 mutex 保护下访问；信号量与映射懒初始化，
// 兼容直接构造 &Server{} 的测试。
func (s *Server) launchSourceRefresh(source storage.ModelSource) bool {
	s.sourceRefreshMu.Lock()
	if s.sourceRefreshing[source.ID] {
		s.sourceRefreshMu.Unlock()
		return false
	}
	if s.sourceRefreshing == nil {
		s.sourceRefreshing = make(map[string]bool)
	}
	if s.sourceLastFetch == nil {
		s.sourceLastFetch = make(map[string]sourceRefreshState)
	}
	if s.refreshSem == nil {
		s.refreshSem = make(chan struct{}, sourceRefreshConcurrency)
	}
	s.sourceRefreshing[source.ID] = true
	sem := s.refreshSem
	s.sourceRefreshMu.Unlock()

	go s.runSourceRefresh(source, sem)
	return true
}

// startSourceRefreshByID 按源 ID 启动后台拉取。返回 (是否启动, 是否因已在
// 进行中而未启动, 错误)。
func (s *Server) startSourceRefreshByID(ctx context.Context, sourceID string) (bool, bool, error) {
	if s.store == nil {
		return false, false, fmt.Errorf("sqlite store is unavailable")
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return false, false, err
	}
	for _, source := range sources {
		if source.ID == sourceID {
			if s.launchSourceRefresh(source) {
				return true, false, nil
			}
			return false, true, nil
		}
	}
	return false, false, fmt.Errorf("model source %q not found", sourceID)
}

// runSourceRefresh 执行单个源的后台拉取任务体。
func (s *Server) runSourceRefresh(source storage.ModelSource, sem chan struct{}) {
	defer func() {
		s.sourceRefreshMu.Lock()
		delete(s.sourceRefreshing, source.ID)
		s.sourceRefreshMu.Unlock()
	}()

	// 源间并发上限：拿不到槽位就排队等待（任务仍处于 refreshing 状态）。
	sem <- struct{}{}
	defer func() { <-sem }()

	ctx, cancel := context.WithTimeout(context.Background(), sourceRefreshBudget)
	defer cancel()

	summary, err := s.refreshSourceByValue(ctx, source)

	state := sourceRefreshState{
		LastCount:    summary.Count,
		LastAdded:    len(summary.Added),
		LastRemoved:  len(summary.Removed),
		LastKeys:     summary.Keys,
		LastFinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err != nil {
		state.LastError = err.Error()
		_ = s.store.InsertSystemLog(ctx, "warn", "model source refresh failed", map[string]any{
			"sourceId": source.ID, "sourceName": source.Name, "error": err.Error(),
		})
	} else {
		_ = s.store.InsertSystemLog(ctx, "info", "model source refreshed", map[string]any{
			"sourceId": source.ID, "sourceName": source.Name, "count": summary.Count,
		})
	}
	s.sourceRefreshMu.Lock()
	s.sourceLastFetch[source.ID] = state
	s.sourceRefreshMu.Unlock()
	// 无论成败都失效路由缓存：成功的合并改写了模型表；失败的路径也可能有
	// per-key 权限字段落库。
	s.invalidateRouteCache()
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

// anySourceRefreshing 报告是否有任一源的后台拉取进行中。
