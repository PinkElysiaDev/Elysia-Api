package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.source_id, m.name, m.source_name, m.base_url, m.api_key, m.platform, m.type, m.max_tokens, m.vision_capable, m.tools_capable, m.structured_output, m.thinking_mode, m.available, m.last_checked_at FROM models m LEFT JOIN model_sources ms ON m.source_id = ms.id WHERE (m.source_id = '' OR ms.enabled = 1 OR ms.id IS NULL) ORDER BY m.source_name, m.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Model{}
	for rows.Next() {
		var item Model
		var vision, tools, structured, available int
		var checked string
		if err := rows.Scan(&item.ID, &item.SourceID, &item.Name, &item.SourceName, &item.BaseURL, &item.APIKey, &item.Platform, &item.Type, &item.MaxTokens, &vision, &tools, &structured, &item.ThinkingMode, &available, &checked); err != nil {
			return nil, err
		}
		item.VisionCapable = intBool(vision)
		item.ToolsCapable = intBool(tools)
		item.StructuredOutput = intBool(structured)
		item.Available = intBool(available)
		item.LastCheckedAt = parseTime(checked)
		if plain, err := s.codec.decrypt(item.APIKey); err == nil {
			item.APIKey = plain
		} else {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) findModel(ctx context.Context, tx *sql.Tx, id string) (Model, bool, error) {
	query := `SELECT id, source_id, name, source_name, base_url, api_key, platform, type, max_tokens, vision_capable, tools_capable, structured_output, thinking_mode, available, last_checked_at FROM models WHERE id = ? ORDER BY source_name LIMIT 1`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, query, id)
	} else {
		row = s.db.QueryRowContext(ctx, query, id)
	}
	var item Model
	var vision, tools, structured, available int
	var checked string
	err := row.Scan(&item.ID, &item.SourceID, &item.Name, &item.SourceName, &item.BaseURL, &item.APIKey, &item.Platform, &item.Type, &item.MaxTokens, &vision, &tools, &structured, &item.ThinkingMode, &available, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, false, nil
	}
	if err != nil {
		return Model{}, false, err
	}
	item.VisionCapable = intBool(vision)
	item.ToolsCapable = intBool(tools)
	item.StructuredOutput = intBool(structured)
	item.Available = intBool(available)
	item.LastCheckedAt = parseTime(checked)
	if plain, err := s.codec.decrypt(item.APIKey); err == nil {
		item.APIKey = plain
	} else {
		return Model{}, false, err
	}
	return item, true, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]ModelGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, enabled, strategy, max_retries, retry_interval, max_concurrency, daily_limit_max_requests, daily_limit_max_tokens, type, max_tokens, vision_capable, tools_capable FROM model_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	items := []ModelGroup{}
	for rows.Next() {
		var item ModelGroup
		var enabled, vision, tools int
		if err := rows.Scan(&item.ID, &item.Name, &enabled, &item.Strategy, &item.MaxRetries, &item.RetryInterval, &item.MaxConcurrency, &item.DailyLimitMaxRequests, &item.DailyLimitMaxTokens, &item.Type, &item.MaxTokens, &vision, &tools); err != nil {
			rows.Close()
			return nil, err
		}
		item.Enabled = intBool(enabled)
		item.VisionCapable = intBool(vision)
		item.ToolsCapable = intBool(tools)
		// Models 必须以空数组而非 nil 序列化：前端 groups 列表直接调用
		// group.models.slice(...)，nil 会被 JSON 编码为 null 并导致整页崩溃。
		item.Models = []string{}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range items {
		modelRows, err := s.db.QueryContext(ctx, `SELECT mgm.model_id, mgm.source_id FROM model_group_models mgm LEFT JOIN model_sources ms ON ms.id = mgm.source_id WHERE mgm.group_id = ? AND (mgm.source_id = '' OR (ms.id IS NOT NULL AND ms.enabled = 1)) ORDER BY mgm.position`, items[i].ID)
		if err != nil {
			return nil, err
		}
		for modelRows.Next() {
			var id, sourceID string
			if err := modelRows.Scan(&id, &sourceID); err != nil {
				modelRows.Close()
				return nil, err
			}
			// 有 source_id 则返回复合键 sourceId:modelId（精确身份）；
			// 旧数据 source_id 为空时返回裸 id（装配端会按 id 回退匹配）。
			if sourceID != "" {
				items[i].Models = append(items[i].Models, sourceID+":"+id)
			} else {
				items[i].Models = append(items[i].Models, id)
			}
		}
		if err := modelRows.Close(); err != nil {
			return nil, err
		}
		if err := modelRows.Err(); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) UpsertGroup(ctx context.Context, item ModelGroup) error {
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("group id is required")
	}
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("group name is required")
	}
	if item.Strategy == "" {
		item.Strategy = "round-robin"
	}
	if item.MaxRetries == 0 {
		item.MaxRetries = 3
	}
	if item.RetryInterval == 0 {
		item.RetryInterval = 1000
	}
	if item.Type == "" {
		item.Type = "llm"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 取改名前的旧组名：token 用组名（而非 id）引用可访问组，改名后需把所有
	// token 的 allowed_groups_json 里的旧名同步成新名，否则旧名会残留成悬空引用。
	var oldName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM model_groups WHERE id = ?`, item.ID).Scan(&oldName); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	now := nowString()
	_, err = tx.ExecContext(ctx, `INSERT INTO model_groups(id, name, enabled, strategy, max_retries, retry_interval, max_concurrency, daily_limit_max_requests, daily_limit_max_tokens, type, max_tokens, vision_capable, tools_capable, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, enabled=excluded.enabled, strategy=excluded.strategy, max_retries=excluded.max_retries, retry_interval=excluded.retry_interval, max_concurrency=excluded.max_concurrency, daily_limit_max_requests=excluded.daily_limit_max_requests, daily_limit_max_tokens=excluded.daily_limit_max_tokens, type=excluded.type, max_tokens=excluded.max_tokens, vision_capable=excluded.vision_capable, tools_capable=excluded.tools_capable, updated_at=excluded.updated_at`, item.ID, item.Name, boolInt(item.Enabled), item.Strategy, item.MaxRetries, item.RetryInterval, item.MaxConcurrency, item.DailyLimitMaxRequests, item.DailyLimitMaxTokens, item.Type, item.MaxTokens, boolInt(item.VisionCapable), boolInt(item.ToolsCapable), now, now)
	if err != nil {
		return err
	}
	if oldName != "" && oldName != item.Name {
		if err := renameGroupInTokens(ctx, tx, oldName, item.Name); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE group_id = ?`, item.ID); err != nil {
		return err
	}
	for i, ref := range item.Models {
		// ref 形如 "sourceId:modelId"（新）或裸 "modelId"（旧/兼容）。
		// 复合键直接拆出 source_id + model_id，精确定位同名不同源的模型；
		// 裸 id 回退到 findModel 猜一个源（保持旧行为）。
		var modelID, sourceID string
		if idx := strings.Index(ref, ":"); idx >= 0 {
			sourceID = ref[:idx]
			modelID = ref[idx+1:]
		} else {
			modelID = ref
			if model, ok, err := s.findModel(ctx, tx, modelID); err != nil {
				return err
			} else if ok {
				sourceID = model.SourceID
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_group_models(group_id, model_id, source_id, position) VALUES(?, ?, ?, ?)`, item.ID, modelID, sourceID, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// renameGroupInTokens 在组改名后，把所有 token 的 allowed_groups_json 里的旧组名
// 替换为新名。逐行 JSON 解析后精确替换（不用 SQL REPLACE，避免误伤子串，
// 如 "gpt" 误伤 "gpt-4"）；替换时去重，防止新名已存在导致重复项。
// 仅对实际包含旧名的 token 执行 UPDATE。必须在改名同一事务内调用以保证原子性。
func renameGroupInTokens(ctx context.Context, tx *sql.Tx, oldName, newName string) error {
	type pendingToken struct {
		name   string
		groups []string
	}
	var pending []pendingToken
	rows, err := tx.QueryContext(ctx, `SELECT name, allowed_groups_json FROM api_tokens`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			rows.Close()
			return err
		}
		groups := decodeStringSlice(raw)
		updated, changed := replaceGroupName(groups, oldName, newName)
		if changed {
			pending = append(pending, pendingToken{name: name, groups: updated})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	now := nowString()
	for _, t := range pending {
		payload, err := json.Marshal(t.groups)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET allowed_groups_json = ?, updated_at = ? WHERE name = ?`, string(payload), now, t.name); err != nil {
			return err
		}
	}
	return nil
}

// replaceGroupName 把切片里的 oldName 替换为 newName 并去重，返回新切片与是否发生变更。
// 仅当 oldName 实际存在时才视为变更（避免对未引用该组的 token 产生无谓 UPDATE）；
// 替换后去重，防止 newName 与列表中已有项重复。
func replaceGroupName(groups []string, oldName, newName string) ([]string, bool) {
	found := false
	for _, g := range groups {
		if g == oldName {
			found = true
			break
		}
	}
	if !found {
		return groups, false
	}
	seen := make(map[string]struct{}, len(groups))
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if g == oldName {
			g = newName
		}
		if _, dup := seen[g]; dup {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	return out, true
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("group id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除前读取组名，用于级联清理 token 的 allowed_groups_json 悬空引用。
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM model_groups WHERE id = ?`, id).Scan(&name); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_groups WHERE id = ?`, id); err != nil {
		return err
	}
	if name != "" {
		if err := removeGroupFromTokens(ctx, tx, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// removeGroupFromTokens 在删除模型组后，把所有 token 的 allowed_groups_json 里的
// 该组名移除，避免残留成悬空引用。仅在 JSON 实际包含该组名时写回；与
// renameGroupInTokens 一样，必须在删除组的事务内调用以保证原子性。
func removeGroupFromTokens(ctx context.Context, tx *sql.Tx, groupName string) error {
	type pendingToken struct {
		name   string
		groups []string
	}
	var pending []pendingToken
	rows, err := tx.QueryContext(ctx, `SELECT name, allowed_groups_json FROM api_tokens`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			rows.Close()
			return err
		}
		groups := decodeStringSlice(raw)
		updated, changed := removeGroupName(groups, groupName)
		if changed {
			pending = append(pending, pendingToken{name: name, groups: updated})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	now := nowString()
	for _, t := range pending {
		payload, err := json.Marshal(t.groups)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET allowed_groups_json = ?, updated_at = ? WHERE name = ?`, string(payload), now, t.name); err != nil {
			return err
		}
	}
	return nil
}

// removeGroupName 从切片中移除指定组名并保持原有顺序，返回新切片与是否发生变更。
func removeGroupName(groups []string, groupName string) ([]string, bool) {
	found := false
	for _, g := range groups {
		if g == groupName {
			found = true
			break
		}
	}
	if !found {
		return groups, false
	}
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if g != groupName {
			out = append(out, g)
		}
	}
	return out, true
}

// SetModelAvailability 更新某个模型（按 id+source_id 唯一）的可用状态，
// 供后台健康检测自动禁用/恢复使用。返回受影响行数。
func (s *Store) SetModelAvailability(ctx context.Context, modelID, sourceID string, available bool) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE models SET available = ? WHERE id = ? AND source_id = ?`, boolInt(available), modelID, sourceID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListAllModelsForProbe 返回所有模型（含不可用的），供健康检测遍历。
// 与 ListModels 不同，这里不过滤 available，以便对已禁用模型做恢复探测。
func (s *Store) ListAllModelsForProbe(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_id, name, source_name, base_url, api_key, platform, type, max_tokens, vision_capable, tools_capable, structured_output, thinking_mode, available, last_checked_at FROM models ORDER BY source_name, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Model{}
	for rows.Next() {
		var item Model
		var vision, tools, structured, available int
		var checked string
		if err := rows.Scan(&item.ID, &item.SourceID, &item.Name, &item.SourceName, &item.BaseURL, &item.APIKey, &item.Platform, &item.Type, &item.MaxTokens, &vision, &tools, &structured, &item.ThinkingMode, &available, &checked); err != nil {
			return nil, err
		}
		item.VisionCapable = intBool(vision)
		item.ToolsCapable = intBool(tools)
		item.StructuredOutput = intBool(structured)
		item.Available = intBool(available)
		item.LastCheckedAt = parseTime(checked)
		if plain, err := s.codec.decrypt(item.APIKey); err == nil {
			item.APIKey = plain
		} else {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SaveUsageRecordJSON(ctx context.Context, payload []byte, summary UsageLogItem, endedAt time.Time) error {
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	if summary.StartedAt.IsZero() {
		summary.StartedAt = endedAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO usage_records(request_id, started_at, started_ms, ended_at, key_name, key_hash, group_name, model_name, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, cache_hit_tokens, request_truncated, response_truncated, record_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, summary.RequestID, summary.StartedAt.UTC().Format(time.RFC3339Nano), summary.StartedAt.UnixMilli(), endedAt.UTC().Format(time.RFC3339Nano), summary.KeyName, summary.KeyHash, summary.GroupName, summary.ModelName, summary.Platform, summary.SourceFormat, summary.TargetFormat, summary.RelayMode, summary.ResponsesMode, summary.UsageSource, boolInt(summary.Stream), summary.StatusCode, summary.Error, summary.FirstByteMs, summary.DurationMs, summary.InputTokens, summary.OutputTokens, summary.TotalTokens, summary.CacheHitTokens, boolInt(summary.RequestTruncated), boolInt(summary.ResponseTruncated), string(payload))
	return err
}

func (s *Store) QueryUsageLogs(ctx context.Context, q UsageQuery) (int, []UsageLogItem, error) {
	where, args := usageWhere(q)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records `+where, args...).Scan(&total); err != nil {
		return 0, nil, err
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, started_at, key_name, key_hash, group_name, model_name, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, cache_hit_tokens, request_truncated, response_truncated FROM usage_records `+where+` ORDER BY started_ms DESC, started_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []UsageLogItem{}
	for rows.Next() {
		var item UsageLogItem
		var started string
		var stream, reqTrunc, respTrunc int
		if err := rows.Scan(&item.RequestID, &started, &item.KeyName, &item.KeyHash, &item.GroupName, &item.ModelName, &item.Platform, &item.SourceFormat, &item.TargetFormat, &item.RelayMode, &item.ResponsesMode, &item.UsageSource, &stream, &item.StatusCode, &item.Error, &item.FirstByteMs, &item.DurationMs, &item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CacheHitTokens, &reqTrunc, &respTrunc); err != nil {
			return 0, nil, err
		}
		item.StartedAt = parseTime(started)
		item.Stream = intBool(stream)
		item.RequestTruncated = intBool(reqTrunc)
		item.ResponseTruncated = intBool(respTrunc)
		items = append(items, item)
	}
	return total, items, rows.Err()
}

func usageWhere(q UsageQuery) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	// 时间过滤用整型毫秒列：RFC3339Nano 字符串的字典序在整秒/带毫秒混合时
	// 不可靠（'.' < 'Z'），会把边界上的记录漏掉。
	if !q.From.IsZero() {
		parts = append(parts, "started_ms >= ?")
		args = append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		parts = append(parts, "started_ms <= ?")
		args = append(args, q.To.UnixMilli())
	}
	// 多选优先于单值：非空时生成 IN (...)，否则回退到单值等值条件。
	if len(q.KeyNames) > 0 {
		parts = append(parts, usageInClause("key_name", len(q.KeyNames)))
		for _, v := range q.KeyNames {
			args = append(args, v)
		}
	} else if q.KeyName != "" {
		parts = append(parts, "key_name = ?")
		args = append(args, q.KeyName)
	}
	if q.KeyHash != "" {
		parts = append(parts, "key_hash = ?")
		args = append(args, q.KeyHash)
	}
	if len(q.GroupNames) > 0 {
		parts = append(parts, usageInClause("group_name", len(q.GroupNames)))
		for _, v := range q.GroupNames {
			args = append(args, v)
		}
	} else if q.GroupName != "" {
		parts = append(parts, "group_name = ?")
		args = append(args, q.GroupName)
	}
	if len(q.ModelNames) > 0 {
		parts = append(parts, usageInClause("model_name", len(q.ModelNames)))
		for _, v := range q.ModelNames {
			args = append(args, v)
		}
	} else if q.ModelName != "" {
		parts = append(parts, "model_name = ?")
		args = append(args, q.ModelName)
	}
	if q.StatusCode > 0 {
		parts = append(parts, "status_code = ?")
		args = append(args, q.StatusCode)
	}
	return "WHERE " + strings.Join(parts, " AND "), args
}

// usageInClause 生成 `col IN (?, ?, ...)`，n 为占位符个数。
func usageInClause(col string, n int) string {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
	return col + " IN (" + placeholders + ")"
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

func (s *Store) ClearUsage(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM usage_records`)
	return err
}

func (s *Store) UsageTotals(ctx context.Context, q UsageQuery) (map[string]any, error) {
	where, args := usageWhere(q)
	// avg_first_byte 仅对 first_byte_ms > 0 的记录求平均（非流式/未记录首字的请求为 0，
	// 计入会拉低均值）；first/last_used_at 提供时间跨度，供前端计算 RPM/TPM。
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(cache_hit_tokens),0), COALESCE(AVG(duration_ms),0), COALESCE(AVG(CASE WHEN first_byte_ms > 0 THEN first_byte_ms END),0), COALESCE(MIN(started_at),''), COALESCE(MAX(started_at),'') FROM usage_records `+where, args...)
	var requests, success, input, output, total, cacheHit int
	var avgDuration, avgFirstByte float64
	var firstUsedAt, lastUsedAt string
	if err := row.Scan(&requests, &success, &input, &output, &total, &cacheHit, &avgDuration, &avgFirstByte, &firstUsedAt, &lastUsedAt); err != nil {
		return nil, err
	}
	cacheHitRate := 0.0
	if input > 0 {
		cacheHitRate = float64(cacheHit) / float64(input)
		// 缓存命中 token 是 input 的子集，比率应落在 [0,1]；个别上游语义差异或
		// 迁移前未记录 input 的行可能令分子虚高，钳制避免出现 >100% 的命中率。
		if cacheHitRate > 1 {
			cacheHitRate = 1
		}
	}
	return map[string]any{
		"requests":       requests,
		"success":        success,
		"failed":         requests - success,
		"inputTokens":    input,
		"outputTokens":   output,
		"totalTokens":    total,
		"cacheHitTokens": cacheHit,
		"cacheHitRate":   cacheHitRate,
		"avgDurationMs":  avgDuration,
		"avgFirstByteMs": avgFirstByte,
		"firstUsedAt":    firstUsedAt,
		"lastUsedAt":     lastUsedAt,
	}, nil
}

func (s *Store) InsertSystemLog(ctx context.Context, level, message string, fields any) error {
	payload, _ := json.Marshal(fields)
	_, err := s.db.ExecContext(ctx, `INSERT INTO system_logs(created_at, level, message, fields_json) VALUES(?, ?, ?, ?)`, nowString(), level, message, string(payload))
	return err
}

func (s *Store) QuerySystemLogs(ctx context.Context, limit, offset int, level string) (int, []SystemLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	where := "WHERE 1=1"
	args := []any{}
	if level != "" {
		where += " AND level = ?"
		args = append(args, level)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_logs `+where, args...).Scan(&total); err != nil {
		return 0, nil, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, level, message, fields_json FROM system_logs `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []SystemLog{}
	for rows.Next() {
		var item SystemLog
		var created string
		if err := rows.Scan(&item.ID, &created, &item.Level, &item.Message, &item.Fields); err != nil {
			return 0, nil, err
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return total, items, rows.Err()
}

func (s *Store) ImportLegacyConfig(ctx context.Context, tokens []APIToken, groups []ModelGroup, models []Model) error {
	for _, token := range tokens {
		if err := s.UpsertAPIToken(ctx, token); err != nil {
			return fmt.Errorf("token %s: %w", token.Name, err)
		}
	}
	if len(models) > 0 {
		source := ModelSource{ID: "legacy-config", Name: "Legacy Config", BaseURL: "", Platform: "openai", Enabled: true, AutoFetchModels: false}
		if err := s.UpsertSource(ctx, source); err != nil {
			return err
		}
		if err := s.ReplaceSourceModels(ctx, source, models); err != nil {
			return err
		}
	}
	for _, group := range groups {
		if err := s.UpsertGroup(ctx, group); err != nil {
			return fmt.Errorf("group %s: %w", group.Name, err)
		}
	}
	return nil
}
