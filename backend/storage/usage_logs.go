// usage 原始行的写入/查询与保留期清理（TTL、条数、容量）及库文件统计。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) SaveUsageRecordJSON(ctx context.Context, payload []byte, summary UsageLogItem, endedAt time.Time, assets ...UsageAsset) error {
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	if summary.StartedAt.IsZero() {
		summary.StartedAt = endedAt
	}
	// 原始行与 rollup 增量同事务：任一失败整体回滚，两表保持一致。
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return saveUsageRecordTx(ctx, tx, payload, summary, endedAt, assets)
	})
}

func saveUsageRecordTx(ctx context.Context, tx *sql.Tx, payload []byte, summary UsageLogItem, endedAt time.Time, assets []UsageAsset) error {
	res, err := tx.ExecContext(ctx, `INSERT INTO usage_records(request_id, started_at, started_ms, ended_at, key_name, key_hash, requested_model_group, group_name, model_name, source_id, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, cache_hit_tokens, cache_creation_tokens, usage_report_mask, request_truncated, response_truncated, record_json, content_bytes) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(request_id) DO NOTHING`, summary.RequestID, summary.StartedAt.UTC().Format(time.RFC3339Nano), summary.StartedAt.UnixMilli(), endedAt.UTC().Format(time.RFC3339Nano), summary.KeyName, summary.KeyHash, summary.RequestedModelGroup, summary.GroupName, summary.ModelName, summary.SourceID, summary.Platform, summary.SourceFormat, summary.TargetFormat, summary.RelayMode, summary.ResponsesMode, summary.UsageSource, sqlBoolToInt(summary.Stream), summary.StatusCode, summary.Error, summary.FirstByteMs, summary.DurationMs, summary.InputTokens, summary.OutputTokens, summary.TotalTokens, summary.CacheHitTokens, summary.CacheCreationTokens, summary.UsageReportMask, sqlBoolToInt(summary.RequestTruncated), sqlBoolToInt(summary.ResponseTruncated), string(payload), len(payload))
	if err != nil {
		return err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		// 同 request_id 已落库：禁止覆盖。覆盖会让 rollup 再 +1 而旧桶不回退。
		return nil
	}
	for _, asset := range assets {
		if err := saveUsageAssetTx(ctx, tx, summary.RequestID, asset); err != nil {
			return err
		}
	}
	return upsertUsageRollupTx(ctx, tx, summary)
}

func (s *Store) QueryUsageLogs(ctx context.Context, q UsageQuery) (int, []UsageLogItem, error) {
	// logs 的 total 必须与 items 同口径(raw 行):rollup 计数包含已被 retention
	// 清理的历史行,分页数会永久大于实际可翻页数(筛选旧窗口时 total>0 页空)。
	// stats/trend 等聚合端点维持 rollup 口径不变。
	total, err := usageCountRaw(ctx, s.db, q)
	if err != nil {
		return 0, nil, err
	}
	limit, offset := clampPage(q.Limit, q.Offset, usageLogPageDefault, usageLogPageMax)
	where, args := usageWhere(q)
	args = append(args, limit, offset)
	// 排序只用 started_ms：索引可直接反向游走取前 offset+limit 条窄索引项、
	// 仅对页内行回表。若追加 started_at 次级排序，任何索引都无法满足复合顺序，
	// SQLite 会退化为全窗口临时 B-tree 排序并逐行回表读取 record_json 胖行。
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, started_at, key_name, key_hash, requested_model_group, group_name, model_name, source_id, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, cache_hit_tokens, cache_creation_tokens, usage_report_mask, request_truncated, response_truncated FROM usage_records `+where+` ORDER BY started_ms DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []UsageLogItem{}
	for rows.Next() {
		var item UsageLogItem
		var started string
		var stream, reqTrunc, respTrunc int
		if err := rows.Scan(&item.RequestID, &started, &item.KeyName, &item.KeyHash, &item.RequestedModelGroup, &item.GroupName, &item.ModelName, &item.SourceID, &item.Platform, &item.SourceFormat, &item.TargetFormat, &item.RelayMode, &item.ResponsesMode, &item.UsageSource, &stream, &item.StatusCode, &item.Error, &item.FirstByteMs, &item.DurationMs, &item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CacheHitTokens, &item.CacheCreationTokens, &item.UsageReportMask, &reqTrunc, &respTrunc); err != nil {
			return 0, nil, err
		}
		item.StartedAt = parseTime(started)
		item.Stream = sqlIntToBool(stream)
		item.RequestTruncated = sqlIntToBool(reqTrunc)
		item.ResponseTruncated = sqlIntToBool(respTrunc)
		items = append(items, item)
	}
	return total, items, rows.Err()
}

// usageFilterClauses 构造维度筛选链（key/group/model 多选 IN、状态/状态码），
// raw 与 rollup 两张表共享：includeTime/includeKeyHash 控制时间与 key_hash
// 两个仅 raw 表适用的谓词。
func usageFilterClauses(q UsageQuery, includeTime, includeKeyHash bool) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	if includeTime {
		// 时间过滤用整型毫秒列：RFC3339Nano 字符串的字典序在整秒/带毫秒混合时
		// 不可靠（'.' < 'Z'），会把边界上的记录漏掉。
		if q.orphanTimestamps {
			parts = append(parts, "started_ms <= 0")
		} else {
			if !q.From.IsZero() {
				parts = append(parts, "started_ms >= ?")
				args = append(args, q.From.UnixMilli())
			}
			if !q.To.IsZero() {
				parts = append(parts, "started_ms < ?")
				args = append(args, q.To.UnixMilli())
			}
		}
	}
	if includeKeyHash && q.KeyHash != "" {
		parts = append(parts, "key_hash = ?")
		args = append(args, q.KeyHash)
	}
	appendInClause := func(column string, values []string, fallback string) {
		if len(values) > 0 {
			parts = append(parts, usageInClause(column, len(values)))
			for _, v := range values {
				args = append(args, v)
			}
		} else if fallback != "" {
			parts = append(parts, column+" = ?")
			args = append(args, fallback)
		}
	}
	appendInClause("key_name", q.KeyNames, q.KeyName)
	appendInClause("group_name", q.GroupNames, q.GroupName)
	appendInClause("model_name", q.ModelNames, q.ModelName)
	if q.StatusCode > 0 {
		parts = append(parts, "status_code = ?")
		args = append(args, q.StatusCode)
	} else if q.Status == "success" {
		parts = append(parts, usageSuccessPredicate)
	} else if q.Status == "failed" {
		parts = append(parts, "("+usageFailedPredicate+")")
	}
	return strings.Join(parts, " AND "), args
}

// usageWhere 生成 raw 表（usage_records）的完整筛选条件。
// source_id 只加在 raw 路径：usage_rollup_hour 没有该列，带 source 过滤时
// rollupSplit 必须返回 ok=false，避免 rollup SQL 引用不存在的列。
func usageWhere(q UsageQuery) (string, []any) {
	clauses, args := usageFilterClauses(q, true, true)
	if len(q.SourceIDs) > 0 {
		clauses += " AND " + usageInClause("source_id", len(q.SourceIDs))
		for _, v := range q.SourceIDs {
			args = append(args, v)
		}
	} else if q.SourceID != "" {
		clauses += " AND source_id = ?"
		args = append(args, q.SourceID)
	}
	return "WHERE " + clauses, args
}

func (s *Store) GetUsageRecordJSON(ctx context.Context, id string) ([]byte, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT record_json FROM usage_records WHERE request_id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []byte(payload), true, nil
}

// ClearUsage 清空全部 usage 数据。rollup 表与状态一并重置（through=until=now、
// ready 保持），后续记录继续由写入侧增量累积，无需重跑回填。
func (s *Store) ClearUsage(ctx context.Context) error {
	// 与后台回填互斥：ClearUsage 重置水位期间若回填循环在跑，其随后的
	// setRollupStateInt 会把水位写回旧值（状态卫生问题，数据本身无损）。
	// 抢不到锁立即失败（见 ErrRollupBackfillInProgress），绝不排队阻塞。
	if !s.rollupMu.TryLock() {
		return ErrRollupBackfillInProgress
	}
	defer s.rollupMu.Unlock()
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_records`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_rollup_hour`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_asset_refs`); err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_rollup_state(key, int_value) VALUES(?, ?), (?, ?), (?, 1)
			ON CONFLICT(key) DO UPDATE SET int_value = excluded.int_value`,
			rollupStateUntil, now, rollupStateThrough, now, rollupStateReady)
		return err
	}); err != nil {
		return err
	}
	s.rollupReady.Store(true)
	return nil
}

// CountUsageRecords 返回当前日志记录总数。
func (s *Store) CountUsageRecords(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records`).Scan(&count)
	return count, err
}

func (st UsageDBStats) LogicalBytes() int64 {
	return (st.PageCount - st.FreePages) * st.PageSize
}

// UsageDBPageStats 单条语句原子读取 page_count/page_size/freelist_count：
// 并发写入时保持一致快照；只用于物理占用和回收，不参与内容配额判断。
func (s *Store) UsageDBPageStats(ctx context.Context) (UsageDBStats, error) {
	var st UsageDBStats
	err := s.db.QueryRowContext(ctx,
		`SELECT (SELECT page_count FROM pragma_page_count),
			(SELECT page_size FROM pragma_page_size),
			(SELECT freelist_count FROM pragma_freelist_count)`).
		Scan(&st.PageCount, &st.PageSize, &st.FreePages)
	return st, err
}

// UsageDBStats 是数据库页面统计：LogicalBytes = (PageCount-FreePages)*PageSize，
// 近似「扣掉空闲页后的实际占用」。删除释放整页进空闲链表，维护任务增量归还磁盘。
type UsageDBStats struct {
	PageCount int64 `json:"pageCount"`
	PageSize  int64 `json:"pageSize"`
	FreePages int64 `json:"freePages"`
}
