package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
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
	// retentionBatchSize 是 TTL/条数清理的分批行数（只影响分块不影响总量），
	// 同时是单事务内 IN 删除的分块粒度（写锁时长上限由此控制）。
	retentionBatchSize = 500
	// 超量清理的自适应批量区间与估算余量。设计依据：稳态下删除速率恒等于
	// 增长速率（上限守恒，不可优化），算法只消除可避免损失——
	//   - 批下限 1：末批精确到条，轻微超限不多删；
	//   - 批上限 1000：大额超限的单批上限（单条 IN 语句直删，1000 个绑定
	//     参数远低于 modernc SQLite 的 32766 上限；500 分块仅用于条数清理）；
	//   - slack 10%：估算偏差由逐批实测修正（见 enforceStorageCap），无需大余量；
	//   - 只清到迟滞带边缘，绝不清到上限以下（不浪费余量删本放得下的记录）。
	retentionCapBatchMin      = 1
	retentionCapBatchMax      = 1000
	retentionCapEstimateSlack = 1.1
	// retentionCapHysteresisPercent 是超量清理的迟滞带（百分比）：逻辑占用
	// 未超过上限的 101% 时不清理，避免在边界附近删删停停。清理频率由此带
	// 加巡检周期（≥5min）与 VACUUM 冷却共同限频。
	retentionCapHysteresisPercent = 101
	// retentionMaxSizePasses 是单轮超量清理的最大删批数：防「其他表占大头、
	// 日志删光仍超限」时无限循环。批上限 1000 条，单轮最多删 5 万条。
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
	if !r.acquireRunSlot() {
		return false
	}
	go func() {
		defer r.releaseRunSlot()
		r.runOnceInner()
	}()
	return true
}

// runOnce 获取执行权后执行一轮；已有轮次在跑时直接跳过（周期巡检用：
// 手动触发刚跑完时少跑一次周期轮无害）。
func (r *usageRetention) runOnce() {
	if !r.acquireRunSlot() {
		return
	}
	defer r.releaseRunSlot()
	r.runOnceInner()
}

// acquireRunSlot 原子占用单轮执行权；false 表示已有轮次在跑。
// 注意触发方必须占用后调用无守卫的 runOnceInner——若调用带守卫的
// runOnce 会被自己置上的 running 标志立即挡回（手动清理空转的根因）。
func (r *usageRetention) acquireRunSlot() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

func (r *usageRetention) releaseRunSlot() {
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
}

// runOnceInner 执行一轮完整清理，不做并发守卫——调用方必须已持有执行权
// （acquireRunSlot）。周期巡检与手动触发共用。
func (r *usageRetention) runOnceInner() {
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
		err := r.deleteInBatches(ctx, func() ([]string, error) {
			return s.store.DeleteUsageOlderThan(ctx, cutoff, retentionBatchSize)
		}, func(ids []string) {
			// 每批即清资产目录：不把全量被删 id 累积在内存里——大库过期清理
			// 可达百万行，累积的 id 切片本身就要几十 MB。
			stats.DeletedByTTL += len(ids)
			stats.AssetsRemoved += r.releaseAssets(ctx, assetsRoot, ids)
			totalDeleted += len(ids)
		})
		if err != nil {
			stats.LastError = "ttl: " + err.Error()
		}
	}

	// 2. 条数清理（单批有界，循环驱动直至收敛到上限内）。
	if cfg.MaxRecords > 0 {
		count, err := s.store.CountUsageRecords(ctx)
		if err != nil {
			if stats.LastError == "" {
				stats.LastError = "count: " + err.Error()
			}
		} else if count > int64(cfg.MaxRecords) {
			err := r.deleteInBatches(ctx, func() ([]string, error) {
				return s.store.DeleteUsageBeyondCount(ctx, int64(cfg.MaxRecords))
			}, func(ids []string) {
				stats.DeletedByRecords += len(ids)
				stats.AssetsRemoved += r.releaseAssets(ctx, assetsRoot, ids)
				totalDeleted += len(ids)
			})
			if err != nil && stats.LastError == "" {
				stats.LastError = "records: " + err.Error()
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

// deleteInBatches 循环调用批次删除直至无可删行；每批结果交 onBatch 处理
// （计数/资产目录清理），不累积全量 id——百万行级清理时累积切片本身就是负担。
func (r *usageRetention) deleteInBatches(ctx context.Context, batch func() ([]string, error), onBatch func([]string)) error {
	for {
		ids, err := batch()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		onBatch(ids)
	}
}

// enforceStorageCap 按 (page_count-freelist)*page_size 收敛到迟滞带内。
// 批量在 [1,1000] 全区间自适应滑动：首批按「需释放量 ÷ 全局均摊字节」
// 估算，之后逐批用实测均摊字节（上一批真实释放量 ÷ 删除数）修正——实测
// 值天然涵盖页粒度碎片（单条小于一页的记录删除后可能不释放页）与记录
// 大小不均。收敛形态：首批接近真实需求，末批收敛到个位数；轻微超限只
// 删几条，不再整批超删。删多少总量始终由复测决定，估算只影响批大小。
func (r *usageRetention) enforceStorageCap(ctx context.Context, limitBytes int64, stats *retentionStats) (int, error) {
	s := r.server
	deleted := 0
	vacuumNeeded := false
	// measuredAvg 是最近一批的实测均摊字节；0 表示尚无实测（首轮走全局
	// 均值估算）。实测释放为 0（页面未腾空）时沿用上一有效值。
	measuredAvg := float64(0)
	prevLogical := int64(-1) // 上一批决策时的逻辑占用
	prevBatch := 0
	zeroShrinkPasses := 0 // 连续零收缩批数（停滞检测）
	for pass := 0; pass < retentionMaxSizePasses; pass++ {
		st, err := s.store.UsageDBPageStats(ctx)
		if err != nil {
			return deleted, err
		}
		now := st.LogicalBytes()
		if prevBatch > 0 && prevLogical > now {
			measuredAvg = float64(prevLogical-now) / float64(prevBatch)
		}
		// 迟滞：上限 1% 以内不清理，避免在边界附近删删停停地抖动。
		if now*100 <= limitBytes*retentionCapHysteresisPercent {
			break
		}
		batch, err := r.capBatchSize(ctx, now, limitBytes, measuredAvg)
		if err != nil {
			// COUNT 失败必须显式报错：静默当作「无可删」会让面板显示一轮
			// 干净的空巡检，而配额实际未被强制执行。
			stats.LastError = "size: " + err.Error()
			vacuumNeeded = deleted > 0
			break
		}
		if batch <= 0 {
			// 无记录可删（除零保护）：超限部分来自其他表/索引，不再挣扎。
			vacuumNeeded = deleted > 0
			break
		}
		// 停滞检测：删除未释放任何页（记录共享页面/超限来自其他表）时，
		// 连续多批零收缩即停止本轮——继续空删最旧记录毫无意义。
		if prevBatch > 0 && prevLogical <= now {
			zeroShrinkPasses++
			if zeroShrinkPasses >= 3 {
				stats.LastError = "size: no logical shrink after consecutive batches; overage likely from non-usage tables"
				vacuumNeeded = deleted > 0
				break
			}
		} else {
			zeroShrinkPasses = 0
		}
		ids, err := s.store.DeleteUsageOldest(ctx, batch)
		if err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			vacuumNeeded = deleted > 0
			break
		}
		prevLogical = now
		prevBatch = len(ids)
		deleted += prevBatch
		vacuumNeeded = true
		stats.DeletedBySize += prevBatch
		stats.AssetsRemoved += r.releaseAssets(ctx, s.usageAssetsRoot(), ids)
	}
	if vacuumNeeded {
		stats.Vacuumed = r.maybeVacuum(ctx)
	}
	return deleted, nil
}

// capBatchSize 估算本批删除条数：need := 需释放量 ÷ 均摊字节 × slack + 1，
// 钳到 [retentionCapBatchMin, retentionCapBatchMax]。需释放量的目标与
// enforceStorageCap 的停止条件一致——迟滞带边缘（而非上限本身），否则
// 首批会多删整整一个带宽的记录。measuredAvg>0 时用实测值；否则回落
// 全局均值（logical / 记录数）。已入带或无记录可估返回 0。
func (r *usageRetention) capBatchSize(ctx context.Context, logical, limitBytes int64, measuredAvg float64) (int, error) {
	target := limitBytes * retentionCapHysteresisPercent / 100
	if logical <= target {
		return 0, nil
	}
	avg := measuredAvg
	if avg <= 0 {
		count, err := r.server.store.CountUsageRecords(ctx)
		if err != nil {
			return 0, err
		}
		if count <= 0 || logical <= 0 {
			return 0, nil
		}
		avg = float64(logical) / float64(count)
	}
	need := float64(logical-target)/avg*retentionCapEstimateSlack + 1
	batch := int(need)
	if batch < retentionCapBatchMin {
		batch = retentionCapBatchMin
	}
	if batch > retentionCapBatchMax {
		batch = retentionCapBatchMax
	}
	return batch, nil
}

// maybeVacuum 限频执行 VACUUM + WAL 截断；距上次成功不足最短间隔或执行
// 失败时返回 false。冷却时间只在成功后占用：失败的 VACUUM（BUSY/磁盘满）
// 下一轮巡检即会重试，避免超配额状态下删记录却收不回磁盘的空转。
func (r *usageRetention) maybeVacuum(ctx context.Context) bool {
	r.vacuumMu.Lock()
	defer r.vacuumMu.Unlock()
	if !r.lastVacuumAt.IsZero() && time.Since(r.lastVacuumAt) < retentionVacuumMinInterval {
		return false
	}
	if err := r.server.store.VacuumUsageDB(ctx); err != nil {
		log.Printf("usage retention: vacuum failed: %v", err)
		return false
	}
	r.lastVacuumAt = time.Now()
	return true
}

// sweepOrphanAssets 删除「引用表中无记录且 mtime 超过宽限期」的资产文件。
// 宽限期保护在途请求（文件先写、引用随记录后落库）与迁移遗留（旧布局中
// 记录已删但保守保留的文件）。扁平布局下文件全局去重，孤儿判定依据是
// usage_asset_refs 引用计数而非目录存在性。
func (r *usageRetention) sweepOrphanAssets(ctx context.Context, root string) (int, error) {
	if root == "" {
		return 0, nil
	}
	referenced, err := r.server.store.ReferencedAssetFiles(ctx)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			// 旧布局残留的请求子目录：迁移会搬走文件，这里兜底清空目录。
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err == nil {
				removed++
			}
			continue
		}
		name := entry.Name()
		if referenced[name] {
			continue
		}
		if _, _, ok := parseAssetFileName(name); !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) < retentionOrphanGrace {
			continue
		}
		if err := os.Remove(filepath.Join(root, name)); err == nil {
			removed++
		}
	}
	return removed, nil
}

// releaseAssets 删除一组记录的资产引用并回收因此零引用的文件，返回删除
// 的文件数。仍被其他记录引用的文件保留（扁平去重布局的核心语义）。
func (r *usageRetention) releaseAssets(ctx context.Context, root string, ids []string) int {
	if root == "" || len(ids) == 0 {
		return 0
	}
	orphans, err := r.server.store.DeleteUsageAssetRefs(ctx, ids)
	if err != nil {
		log.Printf("usage retention: delete asset refs failed: %v", err)
		return 0
	}
	removed := 0
	for _, file := range orphans {
		if _, _, ok := parseAssetFileName(file); !ok {
			continue
		}
		if err := os.Remove(filepath.Join(root, file)); err == nil {
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
