package storage

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// 小时级预聚合表（usage_rollup_hour）：把 usage_records 按 (小时, 模型, 组,
// key, 状态码) 预聚合，仪表盘的 stats/trend/by-model/logs-count 从这张窄表
// 供数，成本与原始表大小解耦——「全部时间」在百万行级大库上也是毫秒级。
//
// 一致性设计（关键不变量：rollup 永远可以从 raw 表重建，且重建幂等）：
//   - 写入侧：SaveUsageRecordJSON 在同一事务里写原始行 + 对应 rollup 桶做
//     增量 UPSERT（cnt=cnt+1 ...）；
//   - 回填侧：每块先 DELETE 该小时区间的 rollup 行、再 INSERT...SELECT 从
//     raw 重建（raw 是唯一事实来源）。写入侧在块事务提交前后到达的记录，
//     要么已被重建包含、要么其 UPSERT 落在块之后叠加，均恰好计数一次；
//   - 回填期间（ready=false）所有聚合查询自动走 raw 路径（阶段一的覆盖
//     索引优化），完成后切到 rollup 路径；
//   - 降级自愈：曾在旧版二进制下运行过（该期间记录只进 raw 不进 rollup）
//     的库，启动回填时按小时对比 raw 与 rollup 的计数，发现缺口的区间
//     自动重建补齐。
const (
	rollupChunkMs        = 6 * 3_600_000         // 回填分块：6 小时/事务
	rollupChunkPauseMs   = 10                    // 块间让出单连接给实时写入
	rollupLogEveryChunks = 60                    // 进度日志频率（约 15 天数据/次）
	rollupStateThrough   = "backfill_through_ms" // 已回填到的水位（不含）
	rollupStateUntil     = "backfill_until_ms"   // 回填上界（首次初始化时刻）
	rollupStateReady     = "backfill_ready"      // 1 = 回填完成，查询可走 rollup
)

// StartRollupBackfill 在后台 goroutine 里执行/续跑回填；失败仅记日志，
// 查询继续走 raw 路径（下次启动重试）。
func (s *Store) StartRollupBackfill() {
	s.rollupWG.Add(1)
	go func() {
		defer s.rollupWG.Done()
		ctx := s.rollupCtx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := s.RunRollupBackfill(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[usage-rollup] backfill failed (queries stay on raw path): %v", err)
		}
	}()
}

// WaitRollupBackfill 等待后台回填结束（测试用）。
func (s *Store) WaitRollupBackfill() {
	s.rollupWG.Wait()
}

// initRollupState 初始化状态行（幂等）并把 ready 载入内存。首次运行把回填
// 上界固定在当前时刻：此前的记录归回填，此后由写入侧增量维护，两者不重叠。
func (s *Store) initRollupState(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO usage_rollup_state(key, int_value) VALUES(?, ?), (?, 0), (?, 0)`,
		rollupStateUntil, time.Now().UnixMilli(), rollupStateThrough, rollupStateReady); err != nil {
		return err
	}
	ready, err := s.rollupStateInt(ctx, rollupStateReady)
	if err != nil {
		return err
	}
	s.rollupReady.Store(ready != 0)
	return nil
}

func (s *Store) rollupStateInt(ctx context.Context, key string) (int64, error) {
	var value int64
	err := s.db.QueryRowContext(ctx, `SELECT int_value FROM usage_rollup_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return value, err
}

func (s *Store) setRollupStateInt(ctx context.Context, key string, value int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO usage_rollup_state(key, int_value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET int_value = excluded.int_value`, key, value)
	return err
}

// RunRollupBackfill 执行（或续跑）回填，并在 ready 状态下先做小时级对账。
func (s *Store) RunRollupBackfill(ctx context.Context) error {
	s.rollupMu.Lock()
	defer s.rollupMu.Unlock()

	if err := s.initRollupState(ctx); err != nil {
		return err
	}
	through, err := s.rollupStateInt(ctx, rollupStateThrough)
	if err != nil {
		return err
	}
	until, err := s.rollupStateInt(ctx, rollupStateUntil)
	if err != nil {
		return err
	}
	ready, err := s.rollupStateInt(ctx, rollupStateReady)
	if err != nil {
		return err
	}

	startedAt := time.Now()
	if ready != 0 {
		// 对账：按小时比较 raw 与 rollup 计数，重建不一致的区间
		// （覆盖中途降级运行旧版二进制留下的缺口）。
		rebuilt, err := s.auditRollupHours(ctx)
		if err != nil {
			return err
		}
		if rebuilt == 0 {
			s.rollupReady.Store(true)
			return nil
		}
		log.Printf("[usage-rollup] audit found stale hour ranges, rebuilt %d chunks", rebuilt)
		s.rollupReady.Store(true)
		log.Printf("[usage-rollup] audit + rebuild done in %s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}

	total := until - through
	// 首次回填时把起点钳到最早数据所在小时：水位默认从 0（Unix 纪元）开始。
	// 空库 MIN(started_ms) 为 NULL/0，若不短路会从 1970 空跑到现在
	// （约 2 万天、数万个 6 小时空块，启动日志会卡在 0% 刷很久）。
	if through == 0 && total > 0 {
		var minMs sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT MIN(started_ms) FROM usage_records WHERE started_ms > 0`).Scan(&minMs); err != nil {
			return err
		}
		if minMs.Valid && minMs.Int64 > 0 {
			through = minMs.Int64 / 3_600_000 * 3_600_000
			total = until - through
		} else {
			// 空库：无任何记录可回填，直接就绪——否则会从纪元起空跑约 9 万个
			// 6 小时空块（持续占用单连接 + 进度日志刷屏）。
			through = until
			total = 0
		}
	}
	if total <= 0 {
		if err := s.setRollupStateInt(ctx, rollupStateReady, 1); err != nil {
			return err
		}
		s.rollupReady.Store(true)
		return nil
	}
	if through > 0 {
		if err := s.setRollupStateInt(ctx, rollupStateThrough, through); err != nil {
			return err
		}
	}
	log.Printf("[usage-rollup] backfilling %.1f days of history (one-time, queries stay on raw path until done)", float64(total)/86_400_000.0)

	chunks := 0
	throughInitial := through
	for through < until {
		chunkEnd := through + rollupChunkMs
		if chunkEnd > until {
			chunkEnd = until
		}
		if err := s.rebuildRollupRange(ctx, through, chunkEnd); err != nil {
			return err
		}
		if err := s.setRollupStateInt(ctx, rollupStateThrough, chunkEnd); err != nil {
			return err
		}
		through = chunkEnd
		chunks++
		if chunks%rollupLogEveryChunks == 0 {
			percent := (through - throughInitial) * 100 / total
			log.Printf("[usage-rollup] %s %d%%", backfillProgressBar(int(percent), 100), percent)
		}
		timer := time.NewTimer(rollupChunkPauseMs * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := s.setRollupStateInt(ctx, rollupStateReady, 1); err != nil {
		return err
	}
	s.rollupReady.Store(true)
	log.Printf("[usage-rollup] backfill complete in %s — aggregates now served from rollup", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

// rebuildRollupRange 以 raw 表为事实来源，重建 [fromMs, toMs) 的 rollup 行。
// 与水位更新同事务（调用方自行决定是否推进水位），重跑幂等。
func (s *Store) rebuildRollupRange(ctx context.Context, fromMs, toMs int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_rollup_hour WHERE hour_ms >= ? AND hour_ms < ?`, fromMs, toMs); err != nil {
		return err
	}
	// SELECT 的全部列都在 idx_usage_agg_cover 内：历史回填同样不回表读胖行。
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_rollup_hour(hour_ms, model_name, group_name, key_name, status_code, cnt, in_tok, out_tok, total_tok, cache_tok, dur_ms_sum, fb_ms_sum, fb_cnt, min_started_ms, max_started_ms)
SELECT (started_ms / 3600000) * 3600000, model_name, group_name, key_name, status_code, COUNT(*),
       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(cache_hit_tokens),0),
       COALESCE(SUM(duration_ms),0), COALESCE(SUM(CASE WHEN first_byte_ms > 0 THEN first_byte_ms END),0), COALESCE(COUNT(CASE WHEN first_byte_ms > 0 THEN 1 END),0),
       COALESCE(MIN(started_ms),0), COALESCE(MAX(started_ms),0)
FROM usage_records WHERE started_ms > 0 AND started_ms >= ? AND started_ms < ? GROUP BY 1, model_name, group_name, key_name, status_code`, fromMs, toMs); err != nil {
		return err
	}
	committed = true
	return tx.Commit()
}

// auditRollupHours 按小时对比 raw 与 rollup 的记录数，重建不一致的区间，
// 返回重建的块数。0 表示无需修复。两次聚合在同一读事务（WAL 快照）内执行：
// 分开跑会在活写入下产生假阳性"不一致"（两查询之间落库的记录只计入一侧），
// 触发无意义的重建。
func (s *Store) auditRollupHours(ctx context.Context) (int, error) {
	type hourCount struct {
		hourMs int64
		count  int64
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }() // 只读事务，结束即弃

	rawByHour := make(map[int64]int64)
	rows, err := tx.QueryContext(ctx, `SELECT (started_ms / 3600000) * 3600000, COUNT(*) FROM usage_records WHERE started_ms > 0 GROUP BY 1`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var h hourCount
		if err := rows.Scan(&h.hourMs, &h.count); err != nil {
			rows.Close()
			return 0, err
		}
		rawByHour[h.hourMs] = h.count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	rollupByHour := make(map[int64]int64)
	rows, err = tx.QueryContext(ctx, `SELECT hour_ms, SUM(cnt) FROM usage_rollup_hour GROUP BY 1`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var h hourCount
		if err := rows.Scan(&h.hourMs, &h.count); err != nil {
			rows.Close()
			return 0, err
		}
		rollupByHour[h.hourMs] = h.count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	// 读快照用完立即释放连接：单连接约束下，下面的重建（写事务）必须能拿到
	// 连接——审计事务若持有连接直到函数返回，重建会自锁死。
	if err := tx.Rollback(); err != nil {
		return 0, err
	}

	// 找出计数不一致的小时，合并成连续区间后按块重建。
	// 只修「漏计」方向（rollup < raw：写入侧丢增量）；rollup > raw 说明该小时
	// 的 raw 行被留存清理（usage_retention）或手工删除过——raw 已不是完整
	// 事实，从 raw 重建只会把统计削掉，跳过即可（rollup 侧保留原计数）。
	var mismatches []int64
	retentionDrift := 0
	for hourMs, count := range rawByHour {
		rollupCount := rollupByHour[hourMs]
		if rollupCount < count {
			mismatches = append(mismatches, hourMs)
		} else if rollupCount > count {
			retentionDrift++
		}
	}
	if retentionDrift > 0 {
		log.Printf("rollup audit: skipped %d hour(s) where rollup count exceeds raw (retention-cleaned), keeping rollup counts", retentionDrift)
	}
	if len(mismatches) == 0 {
		return 0, nil
	}
	sortInt64(mismatches)
	rebuilt := 0
	for i := 0; i < len(mismatches); {
		start := mismatches[i]
		end := start + 3_600_000
		for i+1 < len(mismatches) && mismatches[i+1] == end {
			i++
			end += 3_600_000
		}
		i++
		if err := s.rebuildRollupRange(ctx, start, end); err != nil {
			return rebuilt, err
		}
		rebuilt++
	}
	return rebuilt, nil
}

func sortInt64(values []int64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// upsertUsageRollupTx 把一条记录增量并入其 (小时, 维度) 桶。与原始行同事务。
func upsertUsageRollupTx(ctx context.Context, tx *sql.Tx, summary UsageLogItem) error {
	startedMs := summary.StartedAt.UnixMilli()
	hourMs := startedMs / 3_600_000 * 3_600_000
	var fbSum, fbCnt int64
	if summary.FirstByteMs > 0 {
		fbSum, fbCnt = summary.FirstByteMs, 1
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_rollup_hour(hour_ms, model_name, group_name, key_name, status_code, cnt, in_tok, out_tok, total_tok, cache_tok, dur_ms_sum, fb_ms_sum, fb_cnt, min_started_ms, max_started_ms)
VALUES(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(hour_ms, model_name, group_name, key_name, status_code) DO UPDATE SET
  cnt = cnt + 1,
  in_tok = in_tok + excluded.in_tok,
  out_tok = out_tok + excluded.out_tok,
  total_tok = total_tok + excluded.total_tok,
  cache_tok = cache_tok + excluded.cache_tok,
  dur_ms_sum = dur_ms_sum + excluded.dur_ms_sum,
  fb_ms_sum = fb_ms_sum + excluded.fb_ms_sum,
  fb_cnt = fb_cnt + excluded.fb_cnt,
  min_started_ms = MIN(min_started_ms, excluded.min_started_ms),
  max_started_ms = MAX(max_started_ms, excluded.max_started_ms)`,
		hourMs, summary.ModelName, summary.GroupName, summary.KeyName, summary.StatusCode,
		summary.InputTokens, summary.OutputTokens, summary.TotalTokens, summary.CacheHitTokens,
		summary.DurationMs, fbSum, fbCnt, startedMs, startedMs)
	return err
}
