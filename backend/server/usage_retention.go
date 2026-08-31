package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// usageRetention 是后台日志清理器：按配置周期巡检 usage_records，执行
//
//  1. 过期清理（retentionDays）：删 started_at 早于 N 天的记录；
//  2. 条数清理（maxRecords）：只保留最新 N 条；
//  3. 超量清理（maxStorageMB）：按 DB 逻辑占用（(page_count-freelist)*page_size）
//     收敛，超限删最旧，收敛后限频 VACUUM 回收磁盘；
//  4. 孤儿资产清扫（常开卫生活）：usage-assets/ 下已无对应记录且超过宽限期
//     的目录整目录删除。
//
// 删除只动原始行，不触 usage_rollup_hour——历史统计不受影响（见 queries.go
// 留存清理一节的注释）。三项上限默认全 0（关闭），开箱行为与历史版本一致。
// 所有参数每 tick 从配置热读取，管理端改配置下一轮即生效。
type usageRetention struct {
	server *Server

	stop chan struct{}
	done chan struct{}

	// mu 保护 running：周期巡检与「立即清理」手动触发不得并发重入。
	mu      sync.Mutex
	running bool

	statsMu sync.Mutex
	stats   retentionStats

	vacuumMu     sync.Mutex
	lastVacuumAt time.Time
}

// retentionStats 是最近一轮清理的结果快照（/api/admin/usage/storage 展示）。
type retentionStats struct {
	LastRunAt        time.Time `json:"lastRunAt"`
	DeletedByTTL     int       `json:"deletedByTTL"`
	DeletedByRecords int       `json:"deletedByRecords"`
	DeletedBySize    int       `json:"deletedBySize"`
	AssetsRemoved    int       `json:"assetsRemoved"`
	Vacuumed         bool      `json:"vacuumed"`
	LastError        string    `json:"lastError,omitempty"`
}

const (
	// retentionBatchSize 是清理循环单批删除的行数（与 storage 层默认一致）。
	retentionBatchSize = 500
	// retentionMaxSizePasses 是单轮超量清理的最大删批数：防「其他表占大头、
	// 日志删光仍超限」时无限循环。每批 500 条，单轮最多删 2.5 万条。
	retentionMaxSizePasses = 50
	// retentionVacuumMinInterval 是两次 VACUUM 的最短间隔：VACUUM 需要短暂
	// 独占写锁与约双倍磁盘空间，频繁执行会拖垮写入。
	retentionVacuumMinInterval = 6 * time.Hour
	// retentionOrphanGrace 是孤儿资产目录的宽限期：在途请求先写资产、记录
	// 在请求结束后才落库，宽限期内不视为孤儿。
	retentionOrphanGrace = 24 * time.Hour
	// retentionOrphanProbeBatch 是孤儿清扫批量存在性探测的单批数量。
	retentionOrphanProbeBatch = 200
)

func newUsageRetention(s *Server) *usageRetention {
	return &usageRetention{
		server: s,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// start 启动后台巡检循环（store 模式下由 ListenAndServe 调用一次）。
func (r *usageRetention) start() {
	if r.server.store == nil {
		close(r.done)
		return
	}
	go func() {
		defer close(r.done)
		// 启动先跑一轮，不必等第一个周期。
		r.runOnce()
		for {
			interval := r.server.usageLogConfig().CleanupInterval
			timer := time.NewTimer(interval)
			select {
			case <-r.stop:
				timer.Stop()
				return
			case <-timer.C:
				r.runOnce()
			}
		}
	}()
}

// shutdown 停止巡检并等待在途一轮结束（幂等）。
func (r *usageRetention) shutdown() {
	select {
	case <-r.stop:
		// already closed
	default:
		close(r.stop)
	}
	<-r.done
}

// triggerAsync 手动触发一轮清理（管理端「立即清理」）；已有轮次在跑时返回
// false。异步执行，调用方立即返回。
func (r *usageRetention) triggerAsync() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	go func() {
		defer func() {
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
		}()
		r.runOnce()
	}()
	return true
}

// runOnce 执行一轮完整清理。周期巡检与手动触发共用；单轮内部串行。
func (r *usageRetention) runOnce() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	s := r.server
	if s.store == nil {
		return
	}
	cfg := s.usageLogConfig()
	ctx := context.Background()
	stats := retentionStats{LastRunAt: time.Now()}
	assetsRoot := s.usageAssetsRoot()
	totalDeleted := 0

	// 1. 过期清理。
	if cfg.RetentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays).UnixMilli()
		n, ids, err := r.deleteInBatches(ctx, func() ([]string, error) {
			return s.store.DeleteUsageOlderThan(ctx, cutoff, retentionBatchSize)
		})
		if err != nil {
			stats.LastError = "ttl: " + err.Error()
		}
		stats.DeletedByTTL = n
		stats.AssetsRemoved += removeUsageAssetDirs(assetsRoot, ids)
		totalDeleted += n
	}

	// 2. 条数清理。
	if cfg.MaxRecords > 0 {
		count, err := s.store.CountUsageRecords(ctx)
		if err != nil {
			if stats.LastError == "" {
				stats.LastError = "count: " + err.Error()
			}
		} else if count > int64(cfg.MaxRecords) {
			ids, err := s.store.DeleteUsageBeyondCount(ctx, int64(cfg.MaxRecords))
			if err != nil {
				if stats.LastError == "" {
					stats.LastError = "records: " + err.Error()
				}
			} else {
				stats.DeletedByRecords = len(ids)
				stats.AssetsRemoved += removeUsageAssetDirs(assetsRoot, ids)
				totalDeleted += len(ids)
			}
		}
	}

	// 3. 超量清理（按逻辑占用收敛，删过才限频 VACUUM）。
	if cfg.MaxStorageBytes > 0 {
		n, err := r.enforceStorageCap(ctx, cfg.MaxStorageBytes, &stats)
		if err != nil && stats.LastError == "" {
			stats.LastError = "size: " + err.Error()
		}
		totalDeleted += n
	}

	// 4. 孤儿资产清扫（常开：与日志清理开关联动的内部卫生活）。
	if removed, err := r.sweepOrphanAssets(ctx, assetsRoot); err != nil {
		if stats.LastError == "" {
			stats.LastError = "sweep: " + err.Error()
		}
	} else {
		stats.AssetsRemoved += removed
	}

	r.statsMu.Lock()
	r.stats = stats
	r.statsMu.Unlock()

	if totalDeleted > 0 {
		// 删除会改变列表/统计结果，失效只读缓存（与 persistUsageRecord 同一语义）。
		s.usageCache.flush()
		s.usageSeq.Add(1)
	}
	if stats.LastError != "" {
		log.Printf("usage retention: completed with error: %s", stats.LastError)
	}
}

// deleteInBatches 循环调用批次删除直至无可删行，返回（总删除数, 全部被删 id）。
func (r *usageRetention) deleteInBatches(ctx context.Context, batch func() ([]string, error)) (int, []string, error) {
	total := 0
	var all []string
	for {
		ids, err := batch()
		if err != nil {
			return total, all, err
		}
		if len(ids) == 0 {
			return total, all, nil
		}
		total += len(ids)
		all = append(all, ids...)
	}
}

// enforceStorageCap 按 (page_count-freelist)*page_size 收敛到 limitBytes 内：
// 每批删最旧 500 条后复查（删除释放的整页进空闲链表，逻辑占用随之下降；
// 页总数与磁盘占用要等 VACUUM 才真实回落）。
func (r *usageRetention) enforceStorageCap(ctx context.Context, limitBytes int64, stats *retentionStats) (int, error) {
	s := r.server
	deleted := 0
	vacuumNeeded := false
	for pass := 0; pass < retentionMaxSizePasses; pass++ {
		st, err := s.store.UsageDBPageStats(ctx)
		if err != nil {
			return deleted, err
		}
		if st.LogicalBytes() <= limitBytes {
			break
		}
		ids, err := s.store.DeleteUsageOldest(ctx, 0)
		if err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			// 日志删光仍超限：超限部分来自其他表/索引，不再挣扎。
			vacuumNeeded = deleted > 0
			break
		}
		deleted += len(ids)
		vacuumNeeded = true
		stats.DeletedBySize += len(ids)
		stats.AssetsRemoved += removeUsageAssetDirs(s.usageAssetsRoot(), ids)
	}
	if vacuumNeeded {
		stats.Vacuumed = r.maybeVacuum(ctx)
	}
	return deleted, nil
}

// maybeVacuum 限频执行 VACUUM + WAL 截断；距上次不足最短间隔或执行失败时
// 返回 false（失败仅告警，下一轮巡检会再尝试）。
func (r *usageRetention) maybeVacuum(ctx context.Context) bool {
	r.vacuumMu.Lock()
	defer r.vacuumMu.Unlock()
	if !r.lastVacuumAt.IsZero() && time.Since(r.lastVacuumAt) < retentionVacuumMinInterval {
		return false
	}
	r.lastVacuumAt = time.Now()
	if err := r.server.store.VacuumUsageDB(ctx); err != nil {
		log.Printf("usage retention: vacuum failed: %v", err)
		return false
	}
	return true
}

// sweepOrphanAssets 删除「request_id 已不在日志表且目录 mtime 超过宽限期」的
// 资产目录。宽限期保护在途请求（资产先写、记录后落库）。
func (r *usageRetention) sweepOrphanAssets(ctx context.Context, root string) (int, error) {
	if root == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 目录名即 request_id（req_<nano>_<hex>）：拒绝任何含路径分隔符/
		// 点号的异常名，防目录穿越。
		if name != filepath.Base(name) || strings.ContainsAny(name, `/\.`) {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return 0, nil
	}
	removed := 0
	for start := 0; start < len(names); start += retentionOrphanProbeBatch {
		end := start + retentionOrphanProbeBatch
		if end > len(names) {
			end = len(names)
		}
		exist, err := r.server.store.UsageRecordIDsExist(ctx, names[start:end])
		if err != nil {
			return removed, err
		}
		for _, name := range names[start:end] {
			if exist[name] {
				continue
			}
			dir := filepath.Join(root, name)
			info, err := os.Stat(dir)
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) < retentionOrphanGrace {
				continue
			}
			if err := os.RemoveAll(dir); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// removeUsageAssetDirs 删除一组 request_id 对应的资产目录，返回成功删除数。
// id 全部来自数据库查询结果，但仍做基本清洗防穿越。
func removeUsageAssetDirs(root string, ids []string) int {
	if root == "" || len(ids) == 0 {
		return 0
	}
	removed := 0
	for _, id := range ids {
		if id == "" || strings.ContainsAny(id, `/\.`) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, id)); err == nil {
			removed++
		}
	}
	return removed
}

// snapshotStats 返回最近一轮清理结果（管理端展示）。
func (r *usageRetention) snapshotStats() retentionStats {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	return r.stats
}
