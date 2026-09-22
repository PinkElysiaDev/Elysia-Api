// 模型与分组的查询/写入：模型列清单、分组 CRUD 与 token-分组引用同步。
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// modelColumns 是 models 行读取的统一列清单（含 enabled/origin/capability_source）。
const modelColumns = `m.id, m.source_id, m.name, m.source_name, m.base_url, m.api_key, m.platform, m.type, m.max_tokens, m.vision_capable, m.tools_capable, m.structured_output, m.thinking_mode, m.available, m.enabled, m.origin, m.capability_source, m.last_checked_at`

// scanModel 把一行 models 数据扫进 Model（解密 api_key）。
func (s *Store) scanModel(scanner interface{ Scan(dest ...any) error }) (Model, error) {
	var item Model
	var vision, tools, structured, available, enabled int
	var checked string
	if err := scanner.Scan(&item.ID, &item.SourceID, &item.Name, &item.SourceName, &item.BaseURL, &item.APIKey, &item.Platform, &item.Type, &item.MaxTokens, &vision, &tools, &structured, &item.ThinkingMode, &available, &enabled, &item.Origin, &item.CapabilitySource, &checked); err != nil {
		return Model{}, err
	}
	item.VisionCapable = sqlIntToBool(vision)
	item.ToolsCapable = sqlIntToBool(tools)
	item.StructuredOutput = sqlIntToBool(structured)
	item.Available = sqlIntToBool(available)
	item.Enabled = sqlIntToBool(enabled)
	item.LastCheckedAt = parseTime(checked)
	item.APIKey = s.decryptOrClear("model api_key", item.ID+"/"+item.SourceID, item.APIKey)
	return item, nil
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
		item.Enabled = sqlIntToBool(enabled)
		item.VisionCapable = sqlIntToBool(vision)
		item.ToolsCapable = sqlIntToBool(tools)
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
		// 组成员引用完整返回，即使所属模型源已停用。编辑页打开后无修改保存
		// 会走 UpsertGroup 的「先删后写」；若此处按源 enabled 过滤，停用源下的
		// 成员会从 payload 消失并被永久删除。调度热路径仍通过 ListModels 过滤
		// 停用源，不会把请求打到已停用源。
		modelRows, err := s.db.QueryContext(ctx, `SELECT mgm.model_id, mgm.source_id FROM model_group_models mgm WHERE mgm.group_id = ? ORDER BY mgm.position`, items[i].ID)
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
	// 组名唯一:findGroupByName 按名路由永远命中首个,同名第二组的配置
	// (限流/候选)全部静默失效。
	var conflictingID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM model_groups WHERE name = ? AND id <> ? LIMIT 1`, item.Name, item.ID).Scan(&conflictingID)
	if err == nil {
		return fmt.Errorf("group name %q already used by group %q", item.Name, conflictingID)
	}
	if err != sql.ErrNoRows {
		return err
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
	_, err = tx.ExecContext(ctx, `INSERT INTO model_groups(id, name, enabled, strategy, max_retries, retry_interval, max_concurrency, daily_limit_max_requests, daily_limit_max_tokens, type, max_tokens, vision_capable, tools_capable, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, enabled=excluded.enabled, strategy=excluded.strategy, max_retries=excluded.max_retries, retry_interval=excluded.retry_interval, max_concurrency=excluded.max_concurrency, daily_limit_max_requests=excluded.daily_limit_max_requests, daily_limit_max_tokens=excluded.daily_limit_max_tokens, type=excluded.type, max_tokens=excluded.max_tokens, vision_capable=excluded.vision_capable, tools_capable=excluded.tools_capable, updated_at=excluded.updated_at`, item.ID, item.Name, sqlBoolToInt(item.Enabled), item.Strategy, item.MaxRetries, item.RetryInterval, item.MaxConcurrency, item.DailyLimitMaxRequests, item.DailyLimitMaxTokens, item.Type, item.MaxTokens, sqlBoolToInt(item.VisionCapable), sqlBoolToInt(item.ToolsCapable), now, now)
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
		// "sourceId:modelId"（复合键）或裸 "modelId"（旧/兼容），统一走
		// resolveModelRef 解析（裸 id 回退 findModel 猜源，保持旧行为）。
		modelID, sourceID, err := s.resolveModelRef(ctx, tx, ref)
		if err != nil {
			return err
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
// updateTokenGroupsTx 遍历全部 API token 的组授权，对每个 token 应用 transform
// 并在变更时落库。重命名/移除组共用同一骨架，只差变换函数。
func updateTokenGroupsTx(ctx context.Context, tx *sql.Tx, transform func(groups []string) (updated []string, changed bool)) error {
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
		updated, changed := transform(decodeStringSlice(raw))
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

func renameGroupInTokens(ctx context.Context, tx *sql.Tx, oldName, newName string) error {
	return updateTokenGroupsTx(ctx, tx, func(groups []string) ([]string, bool) {
		return replaceGroupName(groups, oldName, newName)
	})
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

// DeleteGroup 删除模型组并级联清理 token 的组授权。返回因授权列表被清空而
// 一并禁用的 token 名单（见 removeGroupFromTokens）。
func (s *Store) DeleteGroup(ctx context.Context, id string) ([]string, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("group id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 删除前读取组名，用于级联清理 token 的 allowed_groups_json 悬空引用。
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM model_groups WHERE id = ?`, id).Scan(&name); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_groups WHERE id = ?`, id); err != nil {
		return nil, err
	}
	disabled := []string{}
	if name != "" {
		disabled, err = removeGroupFromTokens(ctx, tx, name)
		if err != nil {
			return nil, err
		}
	}
	return disabled, tx.Commit()
}

// removeGroupFromTokens 在删除模型组后，把所有 token 的 allowed_groups_json 里的
// 该组名移除，避免残留成悬空引用。仅在 JSON 实际包含该组名时写回；与
// renameGroupInTokens 一样，必须在删除组的事务内调用以保证原子性。
//
// 授权列表因此被清空的 token 一并禁用：空列表在鉴权语义中表示「不限制」，
// 静默保留会让受限 token 因删除组而扩权为全部组可用。返回被禁用的 token
// 名单，由调用方透出给管理员。
func removeGroupFromTokens(ctx context.Context, tx *sql.Tx, groupName string) ([]string, error) {
	type pendingToken struct {
		name    string
		groups  []string
		emptied bool
	}
	var pending []pendingToken
	rows, err := tx.QueryContext(ctx, `SELECT name, allowed_groups_json FROM api_tokens`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		updated, changed := removeGroupName(decodeStringSlice(raw), groupName)
		if changed {
			pending = append(pending, pendingToken{name: name, groups: updated, emptied: len(updated) == 0})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	now := nowString()
	disabled := []string{}
	for _, t := range pending {
		payload, err := json.Marshal(t.groups)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET allowed_groups_json = ?, updated_at = ? WHERE name = ?`, string(payload), now, t.name); err != nil {
			return nil, err
		}
		if t.emptied {
			if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET enabled = 0, updated_at = ? WHERE name = ?`, now, t.name); err != nil {
				return nil, err
			}
			disabled = append(disabled, t.name)
		}
	}
	return disabled, nil
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
	res, err := s.db.ExecContext(ctx, `UPDATE models SET available = ? WHERE id = ? AND source_id = ?`, sqlBoolToInt(available), modelID, sourceID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListAllModelsForProbe 返回适合健康检测的模型：不过滤 available（已自动
// 禁用的模型要持续探测以便恢复），但排除用户手动停用的模型（enabled=0）
// 与所属源已停用的模型——探测是真实的计费请求，打向管理员明确关掉的
// 模型既浪费钱也毫无意义（路由装配本就不会把流量派过去）。
func (s *Store) ListAllModelsForProbe(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+modelColumns+` FROM models m
		LEFT JOIN model_sources ms ON m.source_id = ms.id
		WHERE m.enabled = 1 AND (m.source_id = '' OR ms.enabled = 1 OR ms.id IS NULL)
		ORDER BY m.source_name, m.name`)
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
		args = append(args, sqlBoolToInt(*patch.VisionCapable))
		capabilityTouched = true
	}
	if patch.ToolsCapable != nil {
		sets = append(sets, "tools_capable = ?")
		args = append(args, sqlBoolToInt(*patch.ToolsCapable))
		capabilityTouched = true
	}
	if patch.StructuredOutput != nil {
		sets = append(sets, "structured_output = ?")
		args = append(args, sqlBoolToInt(*patch.StructuredOutput))
		capabilityTouched = true
	}
	if patch.ThinkingMode != nil {
		sets = append(sets, "thinking_mode = ?")
		args = append(args, *patch.ThinkingMode)
		capabilityTouched = true
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, sqlBoolToInt(*patch.Enabled))
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

// ModelListFilter 提供管理面的模型列表过滤（方向4）：SourceID 为空查全部；
// Search 对 id/name/source_name 做 SQL LIKE 模糊匹配（已 escape 通配符）。
type ModelListFilter struct {
	SourceID string
	Search   string
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
