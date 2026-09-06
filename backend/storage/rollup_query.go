package storage

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// sqlQueryer 抽象 *sql.DB 与 *sql.Tx：rollup 路径的中段 + 两侧边缘必须在
// 同一个读事务（WAL 快照）里执行，否则两条语句之间落库的记录会被漏计。
type sqlQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rollupSplit 计算 rollup 表可精确供数的完整小时区间 [fromHour, toHour)。
// 返回 ok=false 时调用方必须整体走 raw 路径（优雅降级，行为与阶段一一致）：
//   - rollup 未就绪（回填未完成或失败）；
//   - 带 keyHash 筛选（rollup 不含该维度，仅遗留面板使用）；
//   - 带来源筛选（rollup 不含 source_id，历史行也无法回填该维度）；
//   - 做本地日分桶且 offset 不是整小时倍数（+5:30 等时区的日边界切在小时桶中间）；
//   - 窗口本身不足一个完整小时。
func (s *Store) rollupSplit(q UsageQuery, offsetAligned bool) (fromHour, toHour int64, ok bool) {
	if !s.rollupReady.Load() || q.KeyHash != "" || q.SourceID != "" || len(q.SourceIDs) > 0 || !offsetAligned {
		return 0, 0, false
	}
	var fromMs int64
	if !q.From.IsZero() {
		fromMs = q.From.UnixMilli()
	}
	fromHour = (fromMs + msPerHour - 1) / msPerHour * msPerHour // ceil 到小时
	toMs := time.Now().UnixMilli()
	if !q.To.IsZero() {
		toMs = q.To.UnixMilli()
	}
	toHour = toMs / msPerHour * msPerHour // floor 到小时
	if toHour <= fromHour {
		return 0, 0, false
	}
	return fromHour, toHour, true
}

// withBounds 返回仅替换时间窗的查询副本（fromMs/toMs 为 0 表示该侧无界），
// 多选/状态等筛选保持不变——供边缘小时的 raw 补扫复用。
func (q UsageQuery) withBounds(fromMs, toMs int64) UsageQuery {
	qq := q
	qq.From = time.Time{}
	if fromMs > 0 {
		qq.From = time.UnixMilli(fromMs)
	}
	qq.To = time.Time{}
	if toMs > 0 {
		qq.To = time.UnixMilli(toMs)
	}
	return qq
}

// orphansOnly 只扫描 started_ms<=0 的坏时间戳行（rollup 不收录它们）。
// 仅在查询没有下界（From 为零，即「全部时间」）时并入 totals/by-model。
func (q UsageQuery) orphansOnly() UsageQuery {
	qq := q
	qq.From = time.Time{}
	qq.To = time.Time{}
	qq.orphanTimestamps = true
	return qq
}

// edgeBounds 计算 rollup 中段 [fromHour, toHour) 之外的两侧 raw 补扫参数。
// head 为 [q.From, fromHour)，tail 为 [toHour, q.To)；窗口某侧无界时对应
// 边界为 0。
func rollupEdgeBounds(q UsageQuery, fromHour, toHour int64) (headFrom, headTo, tailFrom, tailTo int64, hasHead, hasTail bool) {
	if !q.From.IsZero() && q.From.UnixMilli() < fromHour {
		headFrom, headTo, hasHead = q.From.UnixMilli(), fromHour, true
	}
	if q.To.IsZero() {
		// to 缺省 = 当前时刻：当前不完整小时永远走 raw。
		tailFrom, tailTo, hasTail = toHour, 0, true
	} else if q.To.UnixMilli() > toHour {
		tailFrom, tailTo, hasTail = toHour, q.To.UnixMilli(), true
	}
	return
}

// usageRollupWhere 生成 rollup 表的筛选条件（与 usageWhere 的维度语义一致，
// 但不含时间与 key_hash：时间由小时区间显式给出，keyHash 走 raw 回退）。
// usageRollupWhere 生成 rollup 表的筛选条件（与 usageWhere 的维度语义一致，
// 但不含时间与 key_hash：时间由小时区间显式给出，keyHash 走 raw 回退）。
// 筛选链构造复用 usageFilterClauses（与 raw 表共享同一份判定逻辑）。
func usageRollupWhere(q UsageQuery) (string, []any) {
	clauses, args := usageFilterClauses(q, false, false)
	return " AND " + clauses, args
}

// ---------- UsageTotals ----------

// usageTotalsAcc 是 totals 的可加 accumulator：raw 单行聚合与 rollup 单行
// 聚合各自扫出一份后 merge，AVG 由 sum/count 重构。
type usageTotalsAcc struct {
	requests     int
	success      int
	input        int
	output       int
	total        int
	cacheHit     int
	durationSum  int64
	firstByteSum int64
	firstByteCnt int
	firstMs      int64
	lastMs       int64
}

func (a *usageTotalsAcc) merge(other usageTotalsAcc) {
	a.requests += other.requests
	a.success += other.success
	a.input += other.input
	a.output += other.output
	a.total += other.total
	a.cacheHit += other.cacheHit
	a.durationSum += other.durationSum
	a.firstByteSum += other.firstByteSum
	a.firstByteCnt += other.firstByteCnt
	if other.firstMs > 0 && (a.firstMs == 0 || other.firstMs < a.firstMs) {
		a.firstMs = other.firstMs
	}
	if other.lastMs > a.lastMs {
		a.lastMs = other.lastMs
	}
}

// usageTotalsRawInto 对 raw 表做单行全聚合（阶段一的覆盖索引路径），结果并入 acc。
// 口径：requests/success 计全部记录；token 与时延类只累计成功记录——失败调用的
// token 多为 0 但存在断流部分估算等非 0 例外，计入会虚增成本类指标并拉偏时延。
func usageTotalsRawInto(ctx context.Context, qe sqlQueryer, q UsageQuery, acc *usageTotalsAcc) error {
	where, args := usageWhere(q)
	succOnly := "CASE WHEN " + usageSuccessPredicate + " THEN "
	succFb := "CASE WHEN " + usageSuccessPredicate + " AND first_byte_ms > 0 THEN "
	row := qe.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN 1 ELSE 0 END),0), COALESCE(SUM(`+succOnly+`input_tokens ELSE 0 END),0), COALESCE(SUM(`+succOnly+`output_tokens ELSE 0 END),0), COALESCE(SUM(`+succOnly+`total_tokens ELSE 0 END),0), COALESCE(SUM(`+succOnly+`cache_hit_tokens ELSE 0 END),0), COALESCE(SUM(`+succOnly+`duration_ms ELSE 0 END),0), COALESCE(SUM(`+succFb+`first_byte_ms END),0), COALESCE(COUNT(`+succFb+`1 END),0), COALESCE(MIN(started_ms),0), COALESCE(MAX(started_ms),0) FROM usage_records `+where, args...)
	var part usageTotalsAcc
	if err := row.Scan(&part.requests, &part.success, &part.input, &part.output, &part.total, &part.cacheHit, &part.durationSum, &part.firstByteSum, &part.firstByteCnt, &part.firstMs, &part.lastMs); err != nil {
		return err
	}
	acc.merge(part)
	return nil
}

// usageTotalsRollupInto 对 rollup 表做单行全聚合，结果并入 acc。
// 口径与 usageTotalsRawInto 一致：token/时延列只累计成功记录（rollup 行按
// status_code 分桶，谓词可直接用）。
func usageTotalsRollupInto(ctx context.Context, qe sqlQueryer, q UsageQuery, fromHour, toHour int64, acc *usageTotalsAcc) error {
	filters, filterArgs := usageRollupWhere(q)
	args := append([]any{fromHour, toHour}, filterArgs...)
	succOnly := "CASE WHEN " + usageSuccessPredicate + " THEN "
	succFb := "CASE WHEN " + usageSuccessPredicate + " AND fb_cnt > 0 THEN "
	row := qe.QueryRowContext(ctx, `SELECT COALESCE(SUM(cnt),0), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN cnt ELSE 0 END),0), COALESCE(SUM(`+succOnly+`in_tok ELSE 0 END),0), COALESCE(SUM(`+succOnly+`out_tok ELSE 0 END),0), COALESCE(SUM(`+succOnly+`total_tok ELSE 0 END),0), COALESCE(SUM(`+succOnly+`cache_tok ELSE 0 END),0), COALESCE(SUM(`+succOnly+`dur_ms_sum ELSE 0 END),0), COALESCE(SUM(`+succFb+`fb_ms_sum END),0), COALESCE(SUM(`+succFb+`fb_cnt ELSE 0 END),0), COALESCE(MIN(min_started_ms),0), COALESCE(MAX(max_started_ms),0) FROM usage_rollup_hour WHERE hour_ms >= ? AND hour_ms < ?`+filters, args...)
	var part usageTotalsAcc
	if err := row.Scan(&part.requests, &part.success, &part.input, &part.output, &part.total, &part.cacheHit, &part.durationSum, &part.firstByteSum, &part.firstByteCnt, &part.firstMs, &part.lastMs); err != nil {
		return err
	}
	acc.merge(part)
	return nil
}

// ---------- UsageDaily ----------

// scanUsageDailyRollupRows 对 rollup 表做 (本地日, 模型) 粒度聚合，
// 行结构与 raw 扫描（scanUsageDailyRows）一致，可直接拼接后按日归并。
func scanUsageDailyRollupRows(ctx context.Context, qe sqlQueryer, q UsageQuery, fromHour, toHour, offsetMs int64) ([]usageDayRow, error) {
	filters, filterArgs := usageRollupWhere(q)
	// 模型维度统计排除未路由记录（model_name 为空的前置失败），口径见 scanUsageDailyRows。
	filters += " AND model_name != ''"
	args := append([]any{offsetMs, fromHour, toHour}, filterArgs...)
	// token 列只累计成功记录（口径与 UsageTotals 一致）。
	succOnly := "CASE WHEN " + usageSuccessPredicate + " THEN "
	rows, err := qe.QueryContext(ctx,
		`SELECT (hour_ms + ?) / 86400000, model_name, COALESCE(SUM(cnt),0), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN cnt ELSE 0 END),0), COALESCE(SUM(`+succOnly+`in_tok ELSE 0 END),0), COALESCE(SUM(`+succOnly+`out_tok ELSE 0 END),0), COALESCE(SUM(`+succOnly+`cache_tok ELSE 0 END),0), COALESCE(SUM(`+succOnly+`total_tok ELSE 0 END),0) FROM usage_rollup_hour WHERE hour_ms >= ? AND hour_ms < ?`+filters+` GROUP BY 1, 2 ORDER BY 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []usageDayRow{}
	for rows.Next() {
		var r usageDayRow
		if err := rows.Scan(&r.dayKey, &r.model, &r.requests, &r.success, &r.inputTokens, &r.outputTokens, &r.cacheHitTokens, &r.totalTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- UsageByModel ----------

type usageModelRow struct {
	model    string
	requests int
	failed   int
	tokens   int
}

// scanUsageByModelRawRows 对 raw 表按模型聚合（不含排序：rollup+边缘合并后统一排）。
// requests/failed 计全部记录，tokens 只累计成功记录（成本口径）。
func scanUsageByModelRawRows(ctx context.Context, qe sqlQueryer, q UsageQuery) ([]usageModelRow, error) {
	where, args := usageWhere(q)
	// 模型维度统计排除未路由记录（model_name 为空的前置失败），口径见 scanUsageDailyRows。
	where += " AND model_name != ''"
	rows, err := qe.QueryContext(ctx,
		`SELECT model_name, COUNT(*), COALESCE(SUM(CASE WHEN `+usageFailedPredicate+` THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN total_tokens ELSE 0 END),0) FROM usage_records `+where+` GROUP BY model_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []usageModelRow{}
	for rows.Next() {
		var r usageModelRow
		if err := rows.Scan(&r.model, &r.requests, &r.failed, &r.tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanUsageByModelRollupRows 对 rollup 表按模型聚合。
func scanUsageByModelRollupRows(ctx context.Context, qe sqlQueryer, q UsageQuery, fromHour, toHour int64) ([]usageModelRow, error) {
	filters, filterArgs := usageRollupWhere(q)
	// 模型维度统计排除未路由记录（model_name 为空的前置失败），口径见 scanUsageDailyRows。
	filters += " AND model_name != ''"
	args := append([]any{fromHour, toHour}, filterArgs...)
	rows, err := qe.QueryContext(ctx,
		`SELECT model_name, COALESCE(SUM(cnt),0), COALESCE(SUM(CASE WHEN `+usageFailedPredicate+` THEN cnt ELSE 0 END),0), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN total_tok ELSE 0 END),0) FROM usage_rollup_hour WHERE hour_ms >= ? AND hour_ms < ?`+filters+` GROUP BY model_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []usageModelRow{}
	for rows.Next() {
		var r usageModelRow
		if err := rows.Scan(&r.model, &r.requests, &r.failed, &r.tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- 计数（logs 分页 total） ----------

func usageCountRaw(ctx context.Context, qe sqlQueryer, q UsageQuery) (int, error) {
	where, args := usageWhere(q)
	var total int
	if err := qe.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records `+where, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func usageCountRollup(ctx context.Context, qe sqlQueryer, q UsageQuery, fromHour, toHour int64) (int, error) {
	filters, filterArgs := usageRollupWhere(q)
	args := append([]any{fromHour, toHour}, filterArgs...)
	var total int
	if err := qe.QueryRowContext(ctx, `SELECT COALESCE(SUM(cnt),0) FROM usage_rollup_hour WHERE hour_ms >= ? AND hour_ms < ?`+filters, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// usageCount 是 logs 分页 total 的统一入口：rollup 就绪时中段走窄表、
// 两侧边缘小时走 raw，否则整体 raw COUNT。
func (s *Store) usageCount(ctx context.Context, q UsageQuery) (int, error) {
	fromHour, toHour, ok := s.rollupSplit(q, true)
	if !ok {
		return usageCountRaw(ctx, s.db, q)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }() // 只读事务，结束即弃
	total := 0
	headFrom, headTo, tailFrom, tailTo, hasHead, hasTail := rollupEdgeBounds(q, fromHour, toHour)
	if hasHead {
		n, err := usageCountRaw(ctx, tx, q.withBounds(headFrom, headTo))
		if err != nil {
			return 0, err
		}
		total += n
	}
	n, err := usageCountRollup(ctx, tx, q, fromHour, toHour)
	if err != nil {
		return 0, err
	}
	total += n
	if hasTail {
		n, err := usageCountRaw(ctx, tx, q.withBounds(tailFrom, tailTo))
		if err != nil {
			return 0, err
		}
		total += n
	}
	if q.From.IsZero() {
		n, err := usageCountRaw(ctx, tx, q.orphansOnly())
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// sortUsageModelBuckets 按请求数降序、模型名升序（与旧版 SQL ORDER BY 一致）。
func sortUsageModelBuckets(buckets []UsageModelBucket) {
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Requests != buckets[j].Requests {
			return buckets[i].Requests > buckets[j].Requests
		}
		return buckets[i].Model < buckets[j].Model
	})
}
