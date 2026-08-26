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

// modelColumns 是 models 行读取的统一列清单（含 enabled/origin/capability_source）。
const modelColumns = `m.id, m.source_id, m.name, m.source_name, m.base_url, m.api_key, m.platform, m.type, m.max_tokens, m.vision_capable, m.tools_capable, m.structured_output, m.thinking_mode, m.available, m.enabled, m.origin, m.capability_source, m.last_checked_at`

const (
	usageSuccessPredicate = "status_code >= 200 AND status_code < 400"
	usageFailedPredicate  = "status_code < 200 OR status_code >= 400"
)

// scanModel 把一行 models 数据扫进 Model（解密 api_key）。
func (s *Store) scanModel(scanner interface{ Scan(dest ...any) error }) (Model, error) {
	var item Model
	var vision, tools, structured, available, enabled int
	var checked string
	if err := scanner.Scan(&item.ID, &item.SourceID, &item.Name, &item.SourceName, &item.BaseURL, &item.APIKey, &item.Platform, &item.Type, &item.MaxTokens, &vision, &tools, &structured, &item.ThinkingMode, &available, &enabled, &item.Origin, &item.CapabilitySource, &checked); err != nil {
		return Model{}, err
	}
	item.VisionCapable = intBool(vision)
	item.ToolsCapable = intBool(tools)
	item.StructuredOutput = intBool(structured)
	item.Available = intBool(available)
	item.Enabled = intBool(enabled)
	item.LastCheckedAt = parseTime(checked)
	if plain, err := s.codec.decrypt(item.APIKey); err == nil {
		item.APIKey = plain
	} else {
		return Model{}, err
	}
	return item, nil
}

// ModelListFilter 提供管理面的模型列表过滤（方向4）：SourceID 为空查全部；
// Search 对 id/name/source_name 做 SQL LIKE 模糊匹配（已 escape 通配符）。
type ModelListFilter struct {
	SourceID string
	Search   string
}

func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	return s.listModelsFiltered(ctx, ModelListFilter{})
}

// ListModelsFiltered 按过滤条件列出模型（管理面检索，方向4）。
func (s *Store) ListModelsFiltered(ctx context.Context, filter ModelListFilter) ([]Model, error) {
	return s.listModelsFiltered(ctx, filter)
}

func (s *Store) listModelsFiltered(ctx context.Context, filter ModelListFilter) ([]Model, error) {
	where := "WHERE (m.source_id = '' OR ms.enabled = 1 OR ms.id IS NULL)"
	args := []any{}
	if filter.SourceID != "" {
		where += " AND m.source_id = ?"
		args = append(args, filter.SourceID)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		where += " AND (m.id LIKE ? ESCAPE '\\' OR m.name LIKE ? ESCAPE '\\' OR m.source_name LIKE ? ESCAPE '\\')"
		like := "%" + escapeLike(search) + "%"
		args = append(args, like, like, like)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+modelColumns+` FROM models m LEFT JOIN model_sources ms ON m.source_id = ms.id `+where+` ORDER BY m.source_name, m.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Model{}
	for rows.Next() {
		item, err := s.scanModel(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// escapeLike 转义 LIKE 通配符，保证搜索词按字面匹配。
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func (s *Store) findModel(ctx context.Context, tx *sql.Tx, id string) (Model, bool, error) {
	query := `SELECT ` + modelColumns + ` FROM models m WHERE m.id = ? ORDER BY m.source_name LIMIT 1`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, query, id)
	} else {
		row = s.db.QueryRowContext(ctx, query, id)
	}
	item, err := s.scanModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, false, nil
	}
	if err != nil {
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

// AddGroupMembers 向现有模型组追加成员（方向3：批量「添加到已有组」的原子端点，
// 避免整组 PUT 的读改写竞争）。refs 元素为 "sourceId:modelId" 复合键或裸 id；
// 已存在的引用跳过，新引用的 position 排在现有成员之后。返回实际新增数。
func (s *Store) AddGroupMembers(ctx context.Context, groupID string, refs []string) (int, error) {
	if strings.TrimSpace(groupID) == "" {
		return 0, errors.New("group id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM model_groups WHERE id = ?`, groupID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("model group %q not found", groupID)
		}
		return 0, err
	}
	var maxPosition int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) FROM model_group_models WHERE group_id = ?`, groupID).Scan(&maxPosition); err != nil {
		return 0, err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO model_group_models(group_id, model_id, source_id, position) VALUES(?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer insert.Close()
	added := 0
	for _, ref := range refs {
		modelID, sourceID, err := s.resolveModelRef(ctx, tx, ref)
		if err != nil {
			return 0, err
		}
		if modelID == "" {
			continue // 解析不到对应模型的引用跳过（不阻断整批）
		}
		res, err := insert.ExecContext(ctx, groupID, modelID, sourceID, maxPosition+1+added)
		if err != nil {
			return 0, err
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			added++
		}
	}
	return added, tx.Commit()
}

// RemoveGroupMembers 从模型组移除成员。refs 元素为复合键或裸 id；裸 id 会删除
// 该组内所有同名引用（跨源同名场景需用复合键精确指定）。返回实际移除数。
func (s *Store) RemoveGroupMembers(ctx context.Context, groupID string, refs []string) (int, error) {
	if strings.TrimSpace(groupID) == "" {
		return 0, errors.New("group id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	removed := 0
	for _, ref := range refs {
		modelID, sourceID, err := s.resolveModelRef(ctx, tx, ref)
		if err != nil {
			return 0, err
		}
		if modelID == "" {
			continue
		}
		var res sql.Result
		if sourceID != "" {
			res, err = tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE group_id = ? AND model_id = ? AND source_id = ?`, groupID, modelID, sourceID)
		} else {
			res, err = tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE group_id = ? AND model_id = ?`, groupID, modelID)
		}
		if err != nil {
			return 0, err
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			removed++
		}
	}
	return removed, tx.Commit()
}

// resolveModelRef 把 "sourceId:modelId" 复合键或裸 id 解析为 (modelID, sourceID)。
// 裸 id 回退 findModel 猜源（与 UpsertGroup 的兼容行为一致）；解析不到返回空串。
func (s *Store) resolveModelRef(ctx context.Context, tx *sql.Tx, ref string) (modelID, sourceID string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", nil
	}
	if idx := strings.Index(ref, ":"); idx >= 0 {
		return ref[idx+1:], ref[:idx], nil
	}
	model, ok, err := s.findModel(ctx, tx, ref)
	if err != nil || !ok {
		return ref, "", err
	}
	return model.ID, model.SourceID, nil
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
	rows, err := s.db.QueryContext(ctx, `SELECT `+modelColumns+` FROM models m ORDER BY m.source_name, m.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Model{}
	for rows.Next() {
		item, err := s.scanModel(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ModelPatch 是单个模型的部分更新（方向4）：nil 字段表示不修改。
type ModelPatch struct {
	Name             *string
	Type             *string
	MaxTokens        *int
	VisionCapable    *bool
	ToolsCapable     *bool
	StructuredOutput *bool
	ThinkingMode     *string
	Enabled          *bool
}

// UpdateModel 按 (id, source_id) 更新单个模型的可编辑字段，返回是否找到该行。
// 任一能力字段（vision/tools/structured/thinking/maxTokens/type）被修改时，
// capability_source 置为 'manual'——后续刷新保留这些用户修改值。
func (s *Store) UpdateModel(ctx context.Context, modelID, sourceID string, patch ModelPatch) (bool, error) {
	sets := []string{}
	args := []any{}
	capabilityTouched := false
	if patch.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *patch.Name)
	}
	if patch.Type != nil {
		sets = append(sets, "type = ?")
		args = append(args, *patch.Type)
		capabilityTouched = true
	}
	if patch.MaxTokens != nil {
		sets = append(sets, "max_tokens = ?")
		args = append(args, *patch.MaxTokens)
		capabilityTouched = true
	}
	if patch.VisionCapable != nil {
		sets = append(sets, "vision_capable = ?")
		args = append(args, boolInt(*patch.VisionCapable))
		capabilityTouched = true
	}
	if patch.ToolsCapable != nil {
		sets = append(sets, "tools_capable = ?")
		args = append(args, boolInt(*patch.ToolsCapable))
		capabilityTouched = true
	}
	if patch.StructuredOutput != nil {
		sets = append(sets, "structured_output = ?")
		args = append(args, boolInt(*patch.StructuredOutput))
		capabilityTouched = true
	}
	if patch.ThinkingMode != nil {
		sets = append(sets, "thinking_mode = ?")
		args = append(args, *patch.ThinkingMode)
		capabilityTouched = true
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolInt(*patch.Enabled))
	}
	if len(sets) == 0 {
		// 无字段更新：探测行是否存在即可。
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM models WHERE id = ? AND source_id = ?`, modelID, sourceID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	if capabilityTouched {
		sets = append(sets, "capability_source = 'manual'")
	}
	args = append(args, modelID, sourceID)
	res, err := s.db.ExecContext(ctx, `UPDATE models SET `+strings.Join(sets, ", ")+` WHERE id = ? AND source_id = ?`, args...)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// DeleteModel 删除单个模型并清理组内引用（事务化防悬空）。返回是否删除了行。
func (s *Store) DeleteModel(ctx context.Context, modelID, sourceID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM models WHERE id = ? AND source_id = ?`, modelID, sourceID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE model_id = ? AND source_id = ?`, modelID, sourceID); err != nil {
		return false, err
	}
	return true, tx.Commit()
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
		parts = append(parts, "started_ms < ?")
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
	} else if q.Status == "success" {
		parts = append(parts, usageSuccessPredicate)
	} else if q.Status == "failed" {
		parts = append(parts, "("+usageFailedPredicate+")")
	}
	return "WHERE " + strings.Join(parts, " AND "), args
}

// UsageDaily 按固定 UTC offset 的本地日聚合请求数、细分 tokens 以及各模型消耗。
func (s *Store) UsageDaily(ctx context.Context, q UsageQuery, utcOffsetMinutes int) ([]UsageDailyBucket, error) {
	where, args := usageWhere(q)
	offsetMs := int64(utcOffsetMinutes) * 60_000
	fullArgs := append([]any{offsetMs}, args...)
	rows, err := s.db.QueryContext(ctx,
		`SELECT date((started_ms + ?) / 1000, 'unixepoch'), COUNT(*), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN 1 ELSE 0 END),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_hit_tokens),0), COALESCE(SUM(total_tokens),0) FROM usage_records `+where+` GROUP BY 1 ORDER BY 1`, fullArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := []UsageDailyBucket{}
	bucketIndex := make(map[string]int)
	for rows.Next() {
		var b UsageDailyBucket
		if err := rows.Scan(&b.Date, &b.Requests, &b.SuccessRequests, &b.InputTokens, &b.OutputTokens, &b.CacheHitTokens, &b.Tokens); err != nil {
			return nil, err
		}
		b.FailedRequests = b.Requests - b.SuccessRequests
		b.ModelTokens = make(map[string]int)
		bucketIndex[b.Date] = len(buckets)
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 聚合该时间窗口内各日期下每个模型的 token 消耗明细
	if len(buckets) > 0 {
		mRows, mErr := s.db.QueryContext(ctx,
			`SELECT date((started_ms + ?) / 1000, 'unixepoch'), model_name, COALESCE(SUM(total_tokens),0) FROM usage_records `+where+` GROUP BY 1, 2`, fullArgs...)
		if mErr == nil {
			defer mRows.Close()
			for mRows.Next() {
				var date, model string
				var modelTokens int
				if err := mRows.Scan(&date, &model, &modelTokens); err == nil {
					if idx, ok := bucketIndex[date]; ok {
						if model == "" {
							model = "未知模型"
						}
						buckets[idx].ModelTokens[model] += modelTokens
					}
				}
			}
		}
	}

	return buckets, nil
}

// UsageByModel 按模型聚合（请求数 / 失败数 / tokens），按请求数降序、模型名升序。
func (s *Store) UsageByModel(ctx context.Context, q UsageQuery) ([]UsageModelBucket, error) {
	where, args := usageWhere(q)
	rows, err := s.db.QueryContext(ctx,
		`SELECT model_name, COUNT(*), COALESCE(SUM(CASE WHEN `+usageFailedPredicate+` THEN 1 ELSE 0 END),0), COALESCE(SUM(total_tokens),0) FROM usage_records `+where+` GROUP BY model_name ORDER BY COUNT(*) DESC, model_name ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := []UsageModelBucket{}
	for rows.Next() {
		var b UsageModelBucket
		if err := rows.Scan(&b.Model, &b.Requests, &b.Failed, &b.Tokens); err != nil {
			return nil, err
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
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
	// avg_first_byte 仅对 first_byte_ms > 0 的记录求平均（未记录首字的请求为 0）。
	// firstUsedAt / lastUsedAt 仍返回，给旧 /__usage 面板算跨度。
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN `+usageSuccessPredicate+` THEN 1 ELSE 0 END),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(cache_hit_tokens),0), COALESCE(AVG(duration_ms),0), COALESCE(AVG(CASE WHEN first_byte_ms > 0 THEN first_byte_ms END),0), COALESCE(MIN(started_at),''), COALESCE(MAX(started_at),'') FROM usage_records `+where, args...)
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
