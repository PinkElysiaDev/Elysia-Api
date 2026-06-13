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
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range items {
		modelRows, err := s.db.QueryContext(ctx, `SELECT model_id FROM model_group_models WHERE group_id = ? ORDER BY position`, items[i].ID)
		if err != nil {
			return nil, err
		}
		for modelRows.Next() {
			var id string
			if err := modelRows.Scan(&id); err != nil {
				modelRows.Close()
				return nil, err
			}
			items[i].Models = append(items[i].Models, id)
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
	now := nowString()
	_, err = tx.ExecContext(ctx, `INSERT INTO model_groups(id, name, enabled, strategy, max_retries, retry_interval, max_concurrency, daily_limit_max_requests, daily_limit_max_tokens, type, max_tokens, vision_capable, tools_capable, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, enabled=excluded.enabled, strategy=excluded.strategy, max_retries=excluded.max_retries, retry_interval=excluded.retry_interval, max_concurrency=excluded.max_concurrency, daily_limit_max_requests=excluded.daily_limit_max_requests, daily_limit_max_tokens=excluded.daily_limit_max_tokens, type=excluded.type, max_tokens=excluded.max_tokens, vision_capable=excluded.vision_capable, tools_capable=excluded.tools_capable, updated_at=excluded.updated_at`, item.ID, item.Name, boolInt(item.Enabled), item.Strategy, item.MaxRetries, item.RetryInterval, item.MaxConcurrency, item.DailyLimitMaxRequests, item.DailyLimitMaxTokens, item.Type, item.MaxTokens, boolInt(item.VisionCapable), boolInt(item.ToolsCapable), now, now)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE group_id = ?`, item.ID); err != nil {
		return err
	}
	for i, modelID := range item.Models {
		model, ok, err := s.findModel(ctx, tx, modelID)
		if err != nil {
			return err
		}
		sourceID := ""
		if ok {
			sourceID = model.SourceID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_group_models(group_id, model_id, source_id, position) VALUES(?, ?, ?, ?)`, item.ID, modelID, sourceID, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM model_groups WHERE id = ?`, id)
	return err
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
	return s.ListModels(ctx)
}

func (s *Store) SaveUsageRecordJSON(ctx context.Context, payload []byte, summary UsageLogItem, endedAt time.Time) error {
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	if summary.StartedAt.IsZero() {
		summary.StartedAt = endedAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO usage_records(request_id, started_at, ended_at, key_name, key_hash, group_name, model_name, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, request_truncated, response_truncated, record_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, summary.RequestID, summary.StartedAt.UTC().Format(time.RFC3339Nano), endedAt.UTC().Format(time.RFC3339Nano), summary.KeyName, summary.KeyHash, summary.GroupName, summary.ModelName, summary.Platform, summary.SourceFormat, summary.TargetFormat, summary.RelayMode, summary.ResponsesMode, summary.UsageSource, boolInt(summary.Stream), summary.StatusCode, summary.Error, summary.FirstByteMs, summary.DurationMs, summary.InputTokens, summary.OutputTokens, summary.TotalTokens, boolInt(summary.RequestTruncated), boolInt(summary.ResponseTruncated), string(payload))
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
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, started_at, key_name, key_hash, group_name, model_name, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, request_truncated, response_truncated FROM usage_records `+where+` ORDER BY started_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []UsageLogItem{}
	for rows.Next() {
		var item UsageLogItem
		var started string
		var stream, reqTrunc, respTrunc int
		if err := rows.Scan(&item.RequestID, &started, &item.KeyName, &item.KeyHash, &item.GroupName, &item.ModelName, &item.Platform, &item.SourceFormat, &item.TargetFormat, &item.RelayMode, &item.ResponsesMode, &item.UsageSource, &stream, &item.StatusCode, &item.Error, &item.FirstByteMs, &item.DurationMs, &item.InputTokens, &item.OutputTokens, &item.TotalTokens, &reqTrunc, &respTrunc); err != nil {
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
	if !q.From.IsZero() {
		parts = append(parts, "started_at >= ?")
		args = append(args, q.From.UTC().Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		parts = append(parts, "started_at <= ?")
		args = append(args, q.To.UTC().Format(time.RFC3339Nano))
	}
	if q.KeyName != "" {
		parts = append(parts, "key_name = ?")
		args = append(args, q.KeyName)
	}
	if q.KeyHash != "" {
		parts = append(parts, "key_hash = ?")
		args = append(args, q.KeyHash)
	}
	if q.GroupName != "" {
		parts = append(parts, "group_name = ?")
		args = append(args, q.GroupName)
	}
	if q.ModelName != "" {
		parts = append(parts, "model_name = ?")
		args = append(args, q.ModelName)
	}
	if q.StatusCode > 0 {
		parts = append(parts, "status_code = ?")
		args = append(args, q.StatusCode)
	}
	return "WHERE " + strings.Join(parts, " AND "), args
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
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*), SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(AVG(duration_ms),0) FROM usage_records `+where, args...)
	var requests, success, input, output, total int
	var avgDuration float64
	if err := row.Scan(&requests, &success, &input, &output, &total, &avgDuration); err != nil {
		return nil, err
	}
	return map[string]any{"requests": requests, "success": success, "failed": requests - success, "inputTokens": input, "outputTokens": output, "totalTokens": total, "avgDurationMs": avgDuration}, nil
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
