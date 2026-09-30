package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elysia-api/backend/storage"
)

// One worker owns retention, attachment GC, and database reclamation. Wakeups
// coalesce, but a wakeup received during maintenance always schedules another run.
type usageRetention struct {
	server    *Server
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	done      chan struct{}
	startOnce sync.Once
	runMu     sync.Mutex
	statsMu   sync.Mutex
	stats     retentionStats
}

type retentionStats struct {
	State              string             `json:"state"`
	Phase              string             `json:"phase"`
	Pending            bool               `json:"pending"`
	LastRunAt          time.Time          `json:"lastRunAt"`
	FinishedAt         *time.Time         `json:"finishedAt,omitempty"`
	UsageDeleted       storage.LogDeleted `json:"usageDeleted"`
	SystemDeleted      storage.LogDeleted `json:"systemDeleted"`
	AssetsRemoved      int                `json:"assetsRemoved"`
	RemainingFreePages int64              `json:"remainingFreePages"`
	CheckpointBlocked  bool               `json:"checkpointBlocked"`
	LastError          string             `json:"lastError,omitempty"`
}

const retentionOrphanGrace = 24 * time.Hour

// 巡检退避与让步节奏:waiting(如 VACUUM checkpoint 忙)短退避重试,failed
// 长退避;prune 批间让步给 usage 落库。
const (
	retentionWaitingRetryDelay = 5 * time.Second
	retentionFailedRetryDelay  = 30 * time.Second
	retentionYieldPause        = 10 * time.Millisecond
)

func newUsageRetention(s *Server) *usageRetention {
	ctx, cancel := context.WithCancel(context.Background())
	return &usageRetention{server: s, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{}), stats: retentionStats{State: "idle", Phase: "idle"}}
}

func (r *usageRetention) start() {
	r.startOnce.Do(func() {
		go func() {
			defer close(r.done)
			if r.server.store == nil {
				return
			}
			r.runOnce()
			for {
				delay := r.server.usageLogConfig().CleanupInterval
				state := r.snapshotStats().State
				if state == "waiting" {
					delay = retentionWaitingRetryDelay
				} else if state == "failed" {
					delay = retentionFailedRetryDelay
				}
				timer := time.NewTimer(delay)
				select {
				case <-r.ctx.Done():
					timer.Stop()
					return
				case <-r.wake:
					timer.Stop()
				case <-timer.C:
				}
				if r.ctx.Err() != nil {
					return
				}
				r.runOnce()
			}
		}()
	})
}

func (r *usageRetention) shutdown() { r.cancel(); r.start(); <-r.done }

func (r *usageRetention) triggerAsync() bool {
	if r.ctx.Err() != nil {
		return false
	}
	r.statsMu.Lock()
	r.stats.Pending = true
	if r.stats.State != "running" {
		r.stats.State = "queued"
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	r.statsMu.Unlock()
	r.start()
	return true
}

func (r *usageRetention) snapshotStats() retentionStats {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	return r.stats
}

func (r *usageRetention) publish(st retentionStats) {
	r.statsMu.Lock()
	// A trigger arriving during the run must remain visible until consumed.
	st.Pending = len(r.wake) > 0
	r.stats = st
	r.statsMu.Unlock()
}

func (r *usageRetention) runOnce() {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if r.ctx.Err() != nil || r.server.store == nil {
		return
	}
	st := retentionStats{State: "running", Phase: "retention", LastRunAt: time.Now()}
	r.publish(st)
	err := r.maintain(&st)
	now := time.Now()
	st.FinishedAt = &now
	switch {
	case err != nil:
		st.State = "failed"
		st.LastError = err.Error()
	case st.CheckpointBlocked:
		st.State = "waiting"
	default:
		st.State = "completed"
		st.Phase = "idle"
	}
	r.publish(st)
}

func (r *usageRetention) maintain(st *retentionStats) error {
	var cleanupErr error
	if err := r.pruneLogType(st, false, &cleanupErr); err != nil {
		return err
	}
	if err := r.pruneLogType(st, true, &cleanupErr); err != nil {
		return err
	}

	st.Phase = "assets"
	r.publish(*st)
	removed, err := r.sweepAssets(r.ctx)
	st.AssetsRemoved = removed
	cleanupErr = errors.Join(cleanupErr, err)

	return errors.Join(cleanupErr, r.reclaimFreePages(st))
}

// pruneLogType 按策略分批修剪 usage 调用日志(system=false)或系统日志
// (system=true),批间让步给 usage 落库;结果累进 st 的对应计数并即时发布。
// 错误并入 cleanupErr(不中断另一类型的修剪);返回非 nil 仅代表巡检被
// 取消(调用方应终止整个维护流程)。
func (r *usageRetention) pruneLogType(st *retentionStats, system bool, cleanupErr *error) error {
	s, ctx := r.server, r.ctx
	policy := storage.LogPrunePolicy{}
	if system {
		if s.config != nil {
			sys := s.config.GetSystemLogConfig()
			policy.MaxRecords = int64(sys.MaxRecords)
			policy.MaxContentBytes = int64(sys.MaxContentMB) * 1024 * 1024
			if sys.RetentionDays > 0 {
				policy.CutoffMS = time.Now().AddDate(0, 0, -sys.RetentionDays).UnixMilli()
			}
		}
	} else {
		cfg := s.usageLogConfig()
		policy = storage.LogPrunePolicy{MaxRecords: int64(cfg.MaxRecords), MaxContentBytes: cfg.MaxContentBytes}
		if cfg.RetentionDays > 0 {
			policy.CutoffMS = time.Now().AddDate(0, 0, -cfg.RetentionDays).UnixMilli()
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Short transactions share the attachment lifecycle lock with persistence
		// and reset. No file can acquire a new reference between GC's check/unlink.
		s.usagePersistMu.Lock()
		deleted, err := s.store.PruneLogBatch(ctx, system, policy)
		s.usagePersistMu.Unlock()
		if err != nil {
			*cleanupErr = errors.Join(*cleanupErr, err)
			return nil
		}
		target := &st.UsageDeleted
		if system {
			target = &st.SystemDeleted
		}
		target.ByTTL += deleted.ByTTL
		target.ByRecords += deleted.ByRecords
		target.ByContent += deleted.ByContent
		r.publish(*st)
		if deleted.Total() == 0 {
			return nil
		}
		if !system {
			s.usageCache.flush()
			s.usageSeq.Add(1)
		}
		if err := r.yield(); err != nil {
			return err
		}
	}
}

// reclaimFreePages 回收 auto_vacuum 空闲页直至清零,最后做一次 TRUNCATE
// checkpoint;checkpoint 被并发读者占用(Busy)时标记受阻并留待下轮。
func (r *usageRetention) reclaimFreePages(st *retentionStats) error {
	s, ctx := r.server, r.ctx
	st.Phase = "reclaim"
	r.publish(*st)
	for {
		pages, err := s.store.UsageDBPageStats(ctx)
		if err != nil {
			return err
		}
		st.RemainingFreePages = pages.FreePages
		r.publish(*st)
		if pages.FreePages == 0 {
			break
		}
		if err := s.store.ReclaimLogPages(ctx); err != nil {
			return err
		}
		cp, err := s.store.Checkpoint(ctx, false)
		if err != nil {
			return err
		}
		if cp.Busy {
			// Stop generating WAL while a reader pins the old snapshot. Retry later.
			st.CheckpointBlocked = true
			pages, err = s.store.UsageDBPageStats(ctx)
			if err != nil {
				return err
			}
			st.RemainingFreePages = pages.FreePages
			return nil
		}
		if err := r.yield(); err != nil {
			return err
		}
	}
	st.Phase = "checkpoint"
	r.publish(*st)
	cp, err := s.store.Checkpoint(ctx, true)
	st.CheckpointBlocked = cp.Busy
	return err
}

func (r *usageRetention) yield() error {
	timer := time.NewTimer(retentionYieldPause)
	defer timer.Stop()
	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *usageRetention) sweepAssets(ctx context.Context) (int, error) {
	s := r.server
	s.usagePersistMu.Lock()
	defer s.usagePersistMu.Unlock()
	assets, err := s.store.UsageAssets(ctx)
	if err != nil {
		return 0, err
	}
	root := s.usageAssetsRoot()
	if root == "" {
		if len(assets) > 0 {
			return 0, fmt.Errorf("attachment directory is unavailable")
		}
		return 0, nil
	}
	known := make(map[string]bool, len(assets))
	removed := 0
	var failures error
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			return removed, errors.Join(failures, err)
		}
		known[asset.File] = true
		if asset.Referenced {
			continue
		}
		if _, _, ok := parseAssetFileName(asset.File); !ok {
			failures = errors.Join(failures, fmt.Errorf("invalid asset name: %q", asset.File))
			continue
		}
		err := os.Remove(filepath.Join(root, asset.File))
		if err != nil && !os.IsNotExist(err) {
			failures = errors.Join(failures, err)
			continue
		}
		if err := s.store.ForgetUnusedAsset(ctx, asset.File); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		removed++
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return removed, failures
	}
	if err != nil {
		return removed, errors.Join(failures, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, errors.Join(failures, err)
		}
		if entry.IsDir() || known[entry.Name()] {
			continue
		}
		_, _, valid := parseAssetFileName(entry.Name())
		// Also collect interrupted atomic writes after the grace period.
		if !valid && !strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if time.Since(info.ModTime()) < retentionOrphanGrace {
			continue
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
			failures = errors.Join(failures, err)
		} else {
			removed++
		}
	}
	return removed, failures
}
