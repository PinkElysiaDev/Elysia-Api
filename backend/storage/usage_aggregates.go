// usage 聚合查询：日/模型/脉冲等桶状统计，raw 行与 rollup 增量合并扫描。
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	randv2 "math/rand/v2"
	"sort"
	"strings"
	"time"
)

// UsageDaily 按固定 UTC offset 的本地日聚合请求数、细分 tokens 以及各模型消耗。
// rollup 就绪时中段（完整小时）走预聚合表、两侧不足一小时的边缘走 raw 单次
// (日, 模型) 扫描，在一个读事务（WAL 快照）内精确合并；否则整体走 raw 路径
// （单次 (日, 模型) 粒度 GROUP BY 扫描 + Go 内按日归并，IO 相比旧版两次全窗
// 口扫描减半）。输出结构（含日期格式、未知模型归并）与旧版一致。
func (s *Store) UsageDaily(ctx context.Context, q UsageQuery, utcOffsetMinutes int) ([]UsageDailyBucket, error) {
	offsetMs := int64(utcOffsetMinutes) * msPerMinute
	rows, err := s.usageDailyRows(ctx, q, offsetMs)
	if err != nil {
		return nil, err
	}
	buckets := []UsageDailyBucket{}
	bucketIndex := make(map[string]int)
	for i := range rows {
		r := &rows[i]
		date := usageDayKeyDate(r.dayKey)
		idx, ok := bucketIndex[date]
		if !ok {
			buckets = append(buckets, UsageDailyBucket{Date: date, ModelTokens: make(map[string]int)})
			idx = len(buckets) - 1
			bucketIndex[date] = idx
		}
		b := &buckets[idx]
		b.Requests += r.requests
		b.SuccessRequests += r.success
		b.InputTokens += r.inputTokens
		b.OutputTokens += r.outputTokens
		b.CacheHitTokens += r.cacheHitTokens
		b.Tokens += r.totalTokens
		model := r.model
		if model == "" {
			model = "未知模型"
		}
		b.ModelTokens[model] += r.totalTokens
	}
	for i := range buckets {
		buckets[i].FailedRequests = buckets[i].Requests - buckets[i].SuccessRequests
	}
	return buckets, nil
}

// mergeRawRollupSegments 执行「头 raw → 中 rollup → 尾 raw」三段合并扫描：
// 窗口不跨 rollup 就绪边界时整体走 raw（ok=false）。三个聚合入口
// （日表/模型分布/总计）共用此骨架，只有 scan 回调不同。
func mergeRawRollupSegments[T any](ctx context.Context, s *Store, q UsageQuery,
	rawScan func(ctx context.Context, qe sqlQueryer, q UsageQuery) ([]T, error),
	rollupScan func(ctx context.Context, tx *sql.Tx, q UsageQuery, fromHour, toHour int64) ([]T, error),
	hourAligned bool,
) ([]T, error) {
	fromHour, toHour, ok := s.rollupSplit(q, hourAligned)
	if !ok {
		return rawScan(ctx, s.db, q)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() // 只读事务，结束即弃
	headFrom, headTo, tailFrom, tailTo, hasHead, hasTail := rollupEdgeBounds(q, fromHour, toHour)
	var rows []T
	if hasHead {
		head, err := rawScan(ctx, tx, q.withBounds(headFrom, headTo))
		if err != nil {
			return nil, err
		}
		rows = append(rows, head...)
	}
	middle, err := rollupScan(ctx, tx, q, fromHour, toHour)
	if err != nil {
		return nil, err
	}
	rows = append(rows, middle...)
	if hasTail {
		tail, err := rawScan(ctx, tx, q.withBounds(tailFrom, tailTo))
		if err != nil {
			return nil, err
		}
		rows = append(rows, tail...)
	}
	return rows, nil
}

func (s *Store) usageDailyRows(ctx context.Context, q UsageQuery, offsetMs int64) ([]usageDayRow, error) {
	// 日桶扫描多一个 offset 参数，闭包适配后共用三段合并骨架。
	return mergeRawRollupSegments(ctx, s, q,
		func(ctx context.Context, qe sqlQueryer, query UsageQuery) ([]usageDayRow, error) {
			return scanUsageDailyRows(ctx, qe, query, offsetMs)
		},
		func(ctx context.Context, tx *sql.Tx, query UsageQuery, fromHour, toHour int64) ([]usageDayRow, error) {
			return scanUsageDailyRollupRows(ctx, tx, query, fromHour, toHour, offsetMs)
		}, offsetMs%msPerHour == 0)
}

// scanUsageDailyRows 对 usage_records 做单次 (日, 模型) 粒度聚合扫描。
// offsetMs 为固定 UTC offset（毫秒），日桶 = (started_ms+offsetMs)/86400000。
// qe 允许跑在连接池或读事务上（rollup 边缘补扫复用）。
func scanUsageDailyRows(ctx context.Context, qe sqlQueryer, q UsageQuery, offsetMs int64) ([]usageDayRow, error) {
	where, args := usageWhere(q)
	if !q.orphanTimestamps {
		// 日桶无法安置 started_ms<=0 的坏时间戳，否则会落成 1970-01-01。
		// rollup 未就绪 / keyHash / sourceId / 非整小时 offset 都会走这条 raw 路径。
		where += " AND started_ms > 0"
	}
	// 模型维度统计排除未路由记录（model_name 为空）：组不存在 / 组内无可用模型
	// 等前置失败没有产生模型调用，属网关级错误——审计走调用日志，不进模型统计。
	where += " AND model_name != ''"
	fullArgs := append([]any{offsetMs}, args...)
	// token 列只累计成功记录（口径与 UsageTotals 一致，失败调用不计成本）。
	succOnly := "CASE WHEN " + usageSuccessPredicate + " THEN "
	rows, err := qe.QueryContext(ctx,
		`SELECT (started_ms + ?) / 86400000, model_name, COUNT(*), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN 1 ELSE 0 END),0), COALESCE(SUM(`+succOnly+`input_tokens ELSE 0 END),0), COALESCE(SUM(`+succOnly+`output_tokens ELSE 0 END),0), COALESCE(SUM(`+succOnly+`cache_hit_tokens ELSE 0 END),0), COALESCE(SUM(`+succOnly+`total_tokens ELSE 0 END),0) FROM usage_records `+where+` GROUP BY 1, 2 ORDER BY 1`, fullArgs...)
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

// usageDayKeyDate 把整数日桶格式化为 YYYY-MM-DD，与 SQLite date(...,'unixepoch') 输出一致。
func usageDayKeyDate(dayKey int64) string {
	return time.Unix(dayKey*86400, 0).UTC().Format("2006-01-02")
}

// UsageByModel 按模型聚合（请求数 / 失败数 / tokens），按请求数降序、模型名升序。
// rollup 就绪时中段走预聚合表、边缘小时走 raw，合并后统一排序。
func (s *Store) UsageByModel(ctx context.Context, q UsageQuery) ([]UsageModelBucket, error) {
	// 无界窗口在 rollup 段后补扫孤儿时间戳（rollup 无法安置的坏行）。
	orphanFollowUp := q.From.IsZero()
	rows, err := mergeRawRollupSegments(ctx, s, q,
		func(ctx context.Context, qe sqlQueryer, query UsageQuery) ([]usageModelRow, error) {
			return scanUsageByModelRawRows(ctx, qe, query)
		},
		func(ctx context.Context, tx *sql.Tx, query UsageQuery, fromHour, toHour int64) ([]usageModelRow, error) {
			rows, err := scanUsageByModelRollupRows(ctx, tx, query, fromHour, toHour)
			if err == nil && orphanFollowUp {
				orphans, orphanErr := scanUsageByModelRawRows(ctx, tx, query.orphansOnly())
				if orphanErr != nil {
					return nil, orphanErr
				}
				rows = append(rows, orphans...)
			}
			return rows, err
		}, true)
	if err != nil {
		return nil, err
	}
	byModel := make(map[string]*UsageModelBucket, len(rows))
	for i := range rows {
		r := &rows[i]
		b, ok := byModel[r.model]
		if !ok {
			b = &UsageModelBucket{Model: r.model}
			byModel[r.model] = b
		}
		b.Requests += r.requests
		b.Failed += r.failed
		b.Tokens += r.tokens
	}
	buckets := make([]UsageModelBucket, 0, len(byModel))
	for _, b := range byModel {
		buckets = append(buckets, *b)
	}
	sortUsageModelBuckets(buckets)
	return buckets, nil
}

// ValidatePulseQuery 要求 from，且 [from, to) 不超过 MaxPulseSpan；to 为空时按 now 计。
// from > to 视为参数错误，避免调用方把空结果当成「确实没有数据」。
func ValidatePulseQuery(q UsageQuery) error {
	if q.From.IsZero() {
		return ErrPulseFromRequired
	}
	end := q.To
	if end.IsZero() {
		end = time.Now()
	}
	if end.Before(q.From) {
		return ErrPulseInvertedRange
	}
	if end.Sub(q.From) > MaxPulseSpan {
		return ErrPulseWindowTooLong
	}
	return nil
}

// UsagePulse 按固定分钟桶聚合请求数、平均耗时与 P95 耗时，并同时给出整窗 P95。
// utcOffsetMinutes 与 UsageDaily 相同，用来把桶边界对齐到调用方本地时区。
// 按桶排序后流式计算桶级指标。P95 在样本数 ≤ pulseP95Reservoir 时精确，超出为估算。
func (s *Store) UsagePulse(ctx context.Context, q UsageQuery, utcOffsetMinutes, bucketMinutes int) (UsagePulseResult, error) {
	if bucketMinutes <= 0 {
		return UsagePulseResult{}, fmt.Errorf("bucketMinutes must be positive")
	}
	if err := ValidatePulseQuery(q); err != nil {
		return UsagePulseResult{}, err
	}
	where, args := usageWhere(q)
	offsetMs := int64(utcOffsetMinutes) * msPerMinute
	bucketMs := int64(bucketMinutes) * msPerMinute
	args = append([]any{offsetMs, bucketMs}, args...)
	// 口径：Requests（RPM）计全部记录；时延与 token 只累计成功记录——
	// 失败调用的时延无性能意义，token 存在断流部分估算等非 0 例外。
	rows, err := s.db.QueryContext(ctx,
		`SELECT ((started_ms + ?) / ?) AS bucket, duration_ms, total_tokens, CASE WHEN `+usageSuccessPredicate+` THEN 1 ELSE 0 END FROM usage_records `+where+` ORDER BY 1, 2`, args...)
	if err != nil {
		return UsagePulseResult{}, err
	}
	defer rows.Close()

	out := []UsagePulsePoint{}
	var (
		curBucket    int64
		have         bool
		n            int
		succN        int
		sum          int64
		tokenSum     int64
		bucketSample int64Reservoir
		windowSample int64Reservoir
		windowN      int
		windowSuccN  int
		windowSum    int64
		windowTok    int64
	)
	bucketSample.samples = make([]int64, 0, pulseP95Reservoir)
	windowSample.samples = make([]int64, 0, pulseP95Reservoir)
	flush := func() {
		if !have || n == 0 {
			return
		}
		avg := 0.0
		if succN > 0 {
			avg = float64(sum) / float64(succN)
		}
		out = append(out, UsagePulsePoint{
			T:             curBucket*bucketMs - offsetMs,
			Requests:      n,
			AvgDurationMs: avg,
			P95DurationMs: percentileInt64(append([]int64(nil), bucketSample.samples...), 0.95),
			TotalTokens:   tokenSum,
		})
		windowN += n
		windowSuccN += succN
		windowSum += sum
		windowTok += tokenSum
	}
	for rows.Next() {
		var bucket, durationMs, tokens, success int64
		if err := rows.Scan(&bucket, &durationMs, &tokens, &success); err != nil {
			return UsagePulseResult{}, err
		}
		if !have || bucket != curBucket {
			flush()
			curBucket = bucket
			have = true
			n = 0
			succN = 0
			sum = 0
			tokenSum = 0
			bucketSample.reset()
		}
		n++
		if success == 1 {
			succN++
			sum += durationMs
			tokenSum += tokens
			bucketSample.add(durationMs)
			windowSample.add(durationMs)
		}
	}
	if err := rows.Err(); err != nil {
		return UsagePulseResult{}, err
	}
	flush()
	window := UsagePulseWindow{Requests: windowN, TotalTokens: windowTok}
	if windowSuccN > 0 {
		window.AvgDurationMs = float64(windowSum) / float64(windowSuccN)
		window.P95DurationMs = percentileInt64(windowSample.samples, 0.95)
	}
	return UsagePulseResult{Points: out, Window: window}, nil
}

func (r *int64Reservoir) add(v int64) {
	r.seen++
	if len(r.samples) < cap(r.samples) {
		r.samples = append(r.samples, v)
		return
	}
	if cap(r.samples) == 0 {
		return
	}
	j := randv2.IntN(r.seen)
	if j < len(r.samples) {
		r.samples[j] = v
	}
}

func (r *int64Reservoir) reset() {
	r.samples = r.samples[:0]
	r.seen = 0
}

func percentileInt64(values []int64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	idx := int(math.Round(p * float64(len(values)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return float64(values[idx])
}

// UsageByModelDaily 按本地日 × 模型聚合请求数。total 请求最高的 top 个模型保留原名，其余合并为 isOther。
// 走与 UsageDaily 相同的 rollup 中段 + raw 边缘路径，避免每次刷新都扫完整 raw 表。
func (s *Store) UsageByModelDaily(ctx context.Context, q UsageQuery, utcOffsetMinutes, top int) ([]UsageModelDailyBucket, error) {
	if top <= 0 {
		top = 8
	}
	offsetMs := int64(utcOffsetMinutes) * msPerMinute
	dayRows, err := s.usageDailyRows(ctx, q, offsetMs)
	if err != nil {
		return nil, err
	}

	type row struct {
		date, model string
		requests    int
	}
	var raw []row
	totals := map[string]int{}
	for _, r := range dayRows {
		item := row{date: usageDayKeyDate(r.dayKey), model: r.model, requests: r.requests}
		raw = append(raw, item)
		totals[item.model] += item.requests
	}

	type ranked struct {
		model string
		n     int
	}
	rank := make([]ranked, 0, len(totals))
	for model, n := range totals {
		rank = append(rank, ranked{model, n})
	}
	sort.Slice(rank, func(i, j int) bool {
		if rank[i].n != rank[j].n {
			return rank[i].n > rank[j].n
		}
		return rank[i].model < rank[j].model
	})
	keep := map[string]bool{}
	limit := top
	if limit > len(rank) {
		limit = len(rank)
	}
	for i := 0; i < limit; i++ {
		keep[rank[i].model] = true
	}

	type mergeKey struct {
		model string
		other bool
	}
	merged := map[string]map[mergeKey]int{}
	dates := []string{}
	seenDate := map[string]bool{}
	for _, r := range raw {
		k := mergeKey{model: r.model}
		if !keep[r.model] {
			k = mergeKey{other: true}
		}
		if !seenDate[r.date] {
			seenDate[r.date] = true
			dates = append(dates, r.date)
		}
		byModel := merged[r.date]
		if byModel == nil {
			byModel = map[mergeKey]int{}
			merged[r.date] = byModel
		}
		byModel[k] += r.requests
	}

	out := []UsageModelDailyBucket{}
	for _, date := range dates {
		cells := merged[date]
		keys := make([]mergeKey, 0, len(cells))
		for k := range cells {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].other != keys[j].other {
				return !keys[i].other
			}
			return keys[i].model < keys[j].model
		})
		for _, k := range keys {
			out = append(out, UsageModelDailyBucket{
				Date:     date,
				Model:    k.model,
				Requests: cells[k],
				Other:    k.other,
			})
		}
	}
	return out, nil
}

// usageInClause 生成 `col IN (?, ?, ...)`，n 为占位符个数。
func usageInClause(col string, n int) string {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
	return col + " IN (" + placeholders + ")"
}

func (s *Store) UsageTotals(ctx context.Context, q UsageQuery) (map[string]any, error) {
	acc, err := s.computeUsageTotals(ctx, q)
	if err != nil {
		return nil, err
	}
	// avg_first_byte 仅对 first_byte_ms > 0 的记录求平均（未记录首字的请求为 0）。
	// firstUsedAt / lastUsedAt 仍返回，给旧 /__usage 面板算跨度（毫秒精度）。
	// durationSum 是成功口径（见 usageTotalsRawInto），分母用 acc.success。
	var avgDuration, avgFirstByte float64
	if acc.success > 0 {
		avgDuration = float64(acc.durationSum) / float64(acc.success)
	}
	if acc.firstByteCnt > 0 {
		avgFirstByte = float64(acc.firstByteSum) / float64(acc.firstByteCnt)
	}
	firstUsedAt, lastUsedAt := "", ""
	if acc.firstMs > 0 {
		firstUsedAt = time.UnixMilli(acc.firstMs).UTC().Format(time.RFC3339)
	}
	if acc.lastMs > 0 {
		lastUsedAt = time.UnixMilli(acc.lastMs).UTC().Format(time.RFC3339)
	}
	cacheHitRate := 0.0
	if acc.input > 0 {
		cacheHitRate = float64(acc.cacheHit) / float64(acc.input)
		// 缓存命中 token 是 input 的子集，比率应落在 [0,1]；个别上游语义差异或
		// 迁移前未记录 input 的行可能令分子虚高，钳制避免出现 >100% 的命中率。
		if cacheHitRate > 1 {
			cacheHitRate = 1
		}
	}
	return map[string]any{
		"requests":       acc.requests,
		"success":        acc.success,
		"failed":         acc.requests - acc.success,
		"inputTokens":    acc.input,
		"outputTokens":   acc.output,
		"totalTokens":    acc.total,
		"cacheHitTokens": acc.cacheHit,
		"cacheHitRate":   cacheHitRate,
		"avgDurationMs":  avgDuration,
		"avgFirstByteMs": avgFirstByte,
		"firstUsedAt":    firstUsedAt,
		"lastUsedAt":     lastUsedAt,
	}, nil
}

// usageTotalsAcc 计算 totals 的通用 accumulator：rollup 就绪时中段走预聚合
// 表（单行聚合）、两侧边缘小时走 raw 单行聚合，在一个读事务内精确合并；
// 否则整体 raw（阶段一的覆盖索引单行聚合路径）。
// computeUsageTotals 在同读事务内聚合 totals(方法名曾与返回类型同名遮蔽)。
func (s *Store) computeUsageTotals(ctx context.Context, q UsageQuery) (*usageTotalsAcc, error) {
	// 总计是累加语义：骨架产出行后并入同一累加器。
	acc := &usageTotalsAcc{}
	into := func(scan func(ctx context.Context, qe sqlQueryer, query UsageQuery) error) func(context.Context, sqlQueryer, UsageQuery) ([]*usageTotalsAcc, error) {
		return func(ctx context.Context, qe sqlQueryer, query UsageQuery) ([]*usageTotalsAcc, error) {
			if err := scan(ctx, qe, query); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
	_, err := mergeRawRollupSegments(ctx, s, q,
		into(func(ctx context.Context, qe sqlQueryer, query UsageQuery) error {
			return usageTotalsRawInto(ctx, qe, query, acc)
		}),
		func(ctx context.Context, tx *sql.Tx, query UsageQuery, fromHour, toHour int64) ([]*usageTotalsAcc, error) {
			if err := usageTotalsRollupInto(ctx, tx, query, fromHour, toHour, acc); err != nil {
				return nil, err
			}
			if query.From.IsZero() {
				if err := usageTotalsRawInto(ctx, tx, query.orphansOnly(), acc); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}, true)
	if err != nil {
		return nil, err
	}
	return acc, nil
}

// usageDayRow 是 (本地日, 模型) 粒度的聚合行，由 raw 扫描与 rollup 扫描共同产出，
// 供上层（UsageDaily 及阶段二统一聚合入口）合并。
type usageDayRow struct {
	dayKey         int64
	model          string
	requests       int
	success        int
	inputTokens    int
	outputTokens   int
	cacheHitTokens int
	totalTokens    int
}

// MaxPulseSpan 是 UsagePulse 允许的最大 [from, to) 跨度。短窗接口会把时延读入
// 内存算 P95，不限制窗口会退化成全表扫描。
const MaxPulseSpan = 48 * time.Hour

// pulseP95Reservoir 是桶级 / 窗口级 P95 的最大样本数。48h 窗口只限制时长、
// 不限制 QPS；超过此容量改用 Algorithm R 蓄水池，P95 为估算值。
const pulseP95Reservoir = 16384

type int64Reservoir struct {
	samples []int64
	seen    int
}
