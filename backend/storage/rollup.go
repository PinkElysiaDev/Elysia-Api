package storage

import (
	"context"
	"database/sql"
	"time"

	log "github.com/elysia-api/backend/logging"
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
const (
	// 时间换算基元（毫秒）。SQL 字符串内的 3600000/86400000 保持字面量
	//（便于与 PRAGMA/执行计划对照），Go 侧一律用这里的常量。
	msPerMinute = 60_000
	msPerHour   = 3_600_000
	msPerDay    = 86_400_000

	rollupChunkMs        = 6 * msPerHour         // 回填分块：6 小时/事务
	rollupChunkPauseMs   = 10                    // 块间让出单连接给实时写入
	rollupLogEveryChunks = 60                    // 进度日志频率（约 15 天数据/次）
	rollupStateThrough   = "backfill_through_ms" // 已回填到的水位（不含）
	rollupStateUntil     = "backfill_until_ms"   // 回填上界（首次初始化时刻）
	rollupStateReady     = "backfill_ready"      // 1 = 回填完成，查询可走 rollup
)

// StartRollupBackfill 在后台 goroutine 里执行/续跑回填；失败仅记日志，
// 查询继续走 raw 路径（下次启动重试）。
func (s *LogStore) StartRollupBackfill() {
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
			log.Error().Str("component", "usage_rollup").Err(err).Msg("backfill failed; queries stay on raw path")
		}
	}()
}

// initRollupState 初始化状态行（幂等）并把 ready 载入内存。首次运行把回填
// 上界固定在当前时刻：此前的记录归回填，此后由写入侧增量维护，两者不重叠。
func (s *LogStore) initRollupState(ctx context.Context) error {
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

func (s *LogStore) rollupStateInt(ctx context.Context, key string) (int64, error) {
	var value int64
	err := s.db.QueryRowContext(ctx, `SELECT int_value FROM usage_rollup_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return value, err
}

func (s *LogStore) setRollupStateInt(ctx context.Context, key string, value int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO usage_rollup_state(key, int_value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET int_value = excluded.int_value`, key, value)
	return err
}

// RunRollupBackfill rebuilds an unfinished derived aggregate, not historical schemas.
func (s *LogStore) RunRollupBackfill(ctx context.Context) error {
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
		s.rollupReady.Store(true)
		return nil
	}
	return s.backfillRollup(ctx, through, until, startedAt)
}

// backfillRollup 是未就绪态的相位：钳起点 → 分块重建 → 推进水位 → 置 ready。
func (s *LogStore) backfillRollup(ctx context.Context, through, until int64, startedAt time.Time) error {
	total := until - through
	// 首次回填时把起点钳到最早数据所在小时：水位默认从 0（Unix 纪元）开始。
	// 空库 MIN(started_ms) 为 NULL/0，若不短路会从 1970 空跑到现在
	// （约 2 万天、数万个 6 小时空块，启动日志会卡在 0% 刷很久）。
	if through == 0 && total > 0 {
		clamped, empty, err := s.earliestRollupStart(ctx)
		if err != nil {
			return err
		}
		if empty {
			through, total = until, 0
		} else {
			through = clamped
			total = until - through
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
	log.Info().Str("component", "usage_rollup").Float64("days", float64(total)/float64(msPerDay)).Msg("backfilling history")
	if err := s.backfillRollupChunks(ctx, through, until, total); err != nil {
		return err
	}
	if err := s.setRollupStateInt(ctx, rollupStateReady, 1); err != nil {
		return err
	}
	s.rollupReady.Store(true)
	log.Info().Str("component", "usage_rollup").Int64("duration_ms", time.Since(startedAt).Milliseconds()).Msg("backfill complete")
	return nil
}

// earliestRollupStart 返回最早记录所在小时的下界；空库时 empty=true。
func (s *LogStore) earliestRollupStart(ctx context.Context) (startMs int64, empty bool, err error) {
	var minMs sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(started_ms) FROM usage_records WHERE started_ms > 0`).Scan(&minMs); err != nil {
		return 0, false, err
	}
	if !minMs.Valid || minMs.Int64 <= 0 {
		// 空库：无任何记录可回填，直接就绪——否则会从纪元起空跑约 9 万个
		// 6 小时空块（持续占用单连接 + 进度日志刷屏）。
		return 0, true, nil
	}
	return minMs.Int64 / msPerHour * msPerHour, false, nil
}

// backfillRollupChunks 按 rollupChunkMs 分块重建并推进水位，块间让出连接。
func (s *LogStore) backfillRollupChunks(ctx context.Context, through, until, total int64) error {
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
			log.Info().Str("component", "usage_rollup").Int("percent", int(percent)).Msg("backfill progress")
		}
		timer := time.NewTimer(rollupChunkPauseMs * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

// rebuildRollupRange 以 raw 表为事实来源，重建 [fromMs, toMs) 的 rollup 行。
// 与水位更新同事务（调用方自行决定是否推进水位），重跑幂等。
func (s *LogStore) rebuildRollupRange(ctx context.Context, fromMs, toMs int64) error {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_rollup_hour(hour_ms, model_name, group_name, key_name, status_code, cnt, in_tok, out_tok, total_tok, cache_tok, cc_tok, cc_rows, dur_ms_sum, fb_ms_sum, fb_cnt, min_started_ms, max_started_ms)
SELECT (started_ms / 3600000) * 3600000, model_name, group_name, key_name, status_code, COUNT(*),
       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(cache_hit_tokens),0),
       COALESCE(SUM(cache_creation_tokens),0), COALESCE(COUNT(CASE WHEN usage_report_mask & 4 THEN 1 END),0),
       COALESCE(SUM(duration_ms),0), COALESCE(SUM(CASE WHEN first_byte_ms > 0 THEN first_byte_ms END),0), COALESCE(COUNT(CASE WHEN first_byte_ms > 0 THEN 1 END),0),
       COALESCE(MIN(started_ms),0), COALESCE(MAX(started_ms),0)
FROM usage_records WHERE started_ms > 0 AND started_ms >= ? AND started_ms < ? GROUP BY 1, model_name, group_name, key_name, status_code`, fromMs, toMs); err != nil {
		return err
	}
	committed = true
	return tx.Commit()
}

// upsertUsageRollup uses a prepared statement bound to the raw record's transaction.
func upsertUsageRollup(ctx context.Context, statement *sql.Stmt, summary UsageLogItem) error {
	startedMs := summary.StartedAt.UnixMilli()
	hourMs := startedMs / msPerHour * msPerHour
	var fbSum, fbCnt int64
	if summary.FirstByteMs > 0 {
		fbSum, fbCnt = summary.FirstByteMs, 1
	}
	ccRows := 0
	if summary.UsageReportMask&UsageReportCreation != 0 {
		ccRows = 1
	}
	_, err := statement.ExecContext(ctx,
		hourMs, summary.ModelName, summary.GroupName, summary.KeyName, summary.StatusCode,
		summary.InputTokens, summary.OutputTokens, summary.TotalTokens, summary.CacheHitTokens, summary.CacheCreationTokens, ccRows,
		summary.DurationMs, fbSum, fbCnt, startedMs, startedMs)
	return err
}

const upsertUsageRollupSQL = `INSERT INTO usage_rollup_hour(hour_ms, model_name, group_name, key_name, status_code, cnt, in_tok, out_tok, total_tok, cache_tok, cc_tok, cc_rows, dur_ms_sum, fb_ms_sum, fb_cnt, min_started_ms, max_started_ms)
VALUES(?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(hour_ms, model_name, group_name, key_name, status_code) DO UPDATE SET
  cnt = cnt + 1,
  in_tok = in_tok + excluded.in_tok,
  out_tok = out_tok + excluded.out_tok,
  total_tok = total_tok + excluded.total_tok,
  cache_tok = cache_tok + excluded.cache_tok,
  cc_tok = cc_tok + excluded.cc_tok,
  cc_rows = cc_rows + excluded.cc_rows,
  dur_ms_sum = dur_ms_sum + excluded.dur_ms_sum,
  fb_ms_sum = fb_ms_sum + excluded.fb_ms_sum,
  fb_cnt = fb_cnt + excluded.fb_cnt,
  min_started_ms = MIN(min_started_ms, excluded.min_started_ms),
  max_started_ms = MAX(max_started_ms, excluded.max_started_ms)`
