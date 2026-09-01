package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	randv2 "math/rand/v2"
	"sort"
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
	return updateTokenGroupsTx(ctx, tx, func(groups []string) ([]string, bool) {
		return removeGroupName(groups, groupName)
	})
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
	// 原始行与 rollup 增量同事务：任一失败整体回滚，两表保持一致。
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
	res, err := tx.ExecContext(ctx, `INSERT INTO usage_records(request_id, started_at, started_ms, ended_at, key_name, key_hash, group_name, model_name, source_id, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, cache_hit_tokens, request_truncated, response_truncated, record_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(request_id) DO NOTHING`, summary.RequestID, summary.StartedAt.UTC().Format(time.RFC3339Nano), summary.StartedAt.UnixMilli(), endedAt.UTC().Format(time.RFC3339Nano), summary.KeyName, summary.KeyHash, summary.GroupName, summary.ModelName, summary.SourceID, summary.Platform, summary.SourceFormat, summary.TargetFormat, summary.RelayMode, summary.ResponsesMode, summary.UsageSource, boolInt(summary.Stream), summary.StatusCode, summary.Error, summary.FirstByteMs, summary.DurationMs, summary.InputTokens, summary.OutputTokens, summary.TotalTokens, summary.CacheHitTokens, boolInt(summary.RequestTruncated), boolInt(summary.ResponseTruncated), string(payload))
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
	if err := upsertUsageRollupTx(ctx, tx, summary); err != nil {
		return err
	}
	committed = true
	return tx.Commit()
}

func (s *Store) QueryUsageLogs(ctx context.Context, q UsageQuery) (int, []UsageLogItem, error) {
	total, err := s.usageCount(ctx, q)
	if err != nil {
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
	where, args := usageWhere(q)
	args = append(args, limit, offset)
	// 排序只用 started_ms：索引可直接反向游走取前 offset+limit 条窄索引项、
	// 仅对页内行回表。若追加 started_at 次级排序，任何索引都无法满足复合顺序，
	// SQLite 会退化为全窗口临时 B-tree 排序并逐行回表读取 record_json 胖行。
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, started_at, key_name, key_hash, group_name, model_name, source_id, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, cache_hit_tokens, request_truncated, response_truncated FROM usage_records `+where+` ORDER BY started_ms DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []UsageLogItem{}
	for rows.Next() {
		var item UsageLogItem
		var started string
		var stream, reqTrunc, respTrunc int
		if err := rows.Scan(&item.RequestID, &started, &item.KeyName, &item.KeyHash, &item.GroupName, &item.ModelName, &item.SourceID, &item.Platform, &item.SourceFormat, &item.TargetFormat, &item.RelayMode, &item.ResponsesMode, &item.UsageSource, &stream, &item.StatusCode, &item.Error, &item.FirstByteMs, &item.DurationMs, &item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CacheHitTokens, &reqTrunc, &respTrunc); err != nil {
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

// UsageDaily 按固定 UTC offset 的本地日聚合请求数、细分 tokens 以及各模型消耗。
// rollup 就绪时中段（完整小时）走预聚合表、两侧不足一小时的边缘走 raw 单次
// (日, 模型) 扫描，在一个读事务（WAL 快照）内精确合并；否则整体走 raw 路径
// （单次 (日, 模型) 粒度 GROUP BY 扫描 + Go 内按日归并，IO 相比旧版两次全窗
// 口扫描减半）。输出结构（含日期格式、未知模型归并）与旧版一致。
func (s *Store) UsageDaily(ctx context.Context, q UsageQuery, utcOffsetMinutes int) ([]UsageDailyBucket, error) {
	offsetMs := int64(utcOffsetMinutes) * 60_000
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

func (s *Store) usageDailyRows(ctx context.Context, q UsageQuery, offsetMs int64) ([]usageDayRow, error) {
	if fromHour, toHour, ok := s.rollupSplit(q, offsetMs%3_600_000 == 0); ok {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }() // 只读事务，结束即弃
		rows := []usageDayRow{}
		headFrom, headTo, tailFrom, tailTo, hasHead, hasTail := rollupEdgeBounds(q, fromHour, toHour)
		if hasHead {
			head, err := scanUsageDailyRows(ctx, tx, q.withBounds(headFrom, headTo), offsetMs)
			if err != nil {
				return nil, err
			}
			rows = append(rows, head...)
		}
		middle, err := scanUsageDailyRollupRows(ctx, tx, q, fromHour, toHour, offsetMs)
		if err != nil {
			return nil, err
		}
		rows = append(rows, middle...)
		if hasTail {
			tail, err := scanUsageDailyRows(ctx, tx, q.withBounds(tailFrom, tailTo), offsetMs)
			if err != nil {
				return nil, err
			}
			rows = append(rows, tail...)
		}
		return rows, nil
	}
	return scanUsageDailyRows(ctx, s.db, q, offsetMs)
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
	var rows []usageModelRow
	if fromHour, toHour, ok := s.rollupSplit(q, true); ok {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }() // 只读事务，结束即弃
		headFrom, headTo, tailFrom, tailTo, hasHead, hasTail := rollupEdgeBounds(q, fromHour, toHour)
		if hasHead {
			head, err := scanUsageByModelRawRows(ctx, tx, q.withBounds(headFrom, headTo))
			if err != nil {
				return nil, err
			}
			rows = append(rows, head...)
		}
		middle, err := scanUsageByModelRollupRows(ctx, tx, q, fromHour, toHour)
		if err != nil {
			return nil, err
		}
		rows = append(rows, middle...)
		if hasTail {
			tail, err := scanUsageByModelRawRows(ctx, tx, q.withBounds(tailFrom, tailTo))
			if err != nil {
				return nil, err
			}
			rows = append(rows, tail...)
		}
		if q.From.IsZero() {
			orphans, err := scanUsageByModelRawRows(ctx, tx, q.orphansOnly())
			if err != nil {
				return nil, err
			}
			rows = append(rows, orphans...)
		}
	} else {
		raw, err := scanUsageByModelRawRows(ctx, s.db, q)
		if err != nil {
			return nil, err
		}
		rows = raw
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

// MaxPulseSpan 是 UsagePulse 允许的最大 [from, to) 跨度。短窗接口会把时延读入
// 内存算 P95，不限制窗口会退化成全表扫描。
const MaxPulseSpan = 48 * time.Hour

var (
	ErrPulseFromRequired  = errors.New("pulse requires from")
	ErrPulseWindowTooLong = errors.New("pulse window exceeds 48 hours")
	ErrPulseInvertedRange = errors.New("pulse to is before from")
)

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

// pulseP95Reservoir 是桶级 / 窗口级 P95 的最大样本数。48h 窗口只限制时长、
// 不限制 QPS；超过此容量改用 Algorithm R 蓄水池，P95 为估算值。
const pulseP95Reservoir = 16384

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
	offsetMs := int64(utcOffsetMinutes) * 60_000
	bucketMs := int64(bucketMinutes) * 60_000
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

type int64Reservoir struct {
	samples []int64
	seen    int
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
	offsetMs := int64(utcOffsetMinutes) * 60_000
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

// ErrRollupBackfillInProgress 表示小时聚合后台回填正在运行，ClearUsage 抢
// 互斥锁失败。调用方（resetUsage）持有 usage writer/persist 锁期间不能排队
// 等待——大库首次回填可能持锁数分钟，排队会把所有请求的 usage 落库一并卡住。
var ErrRollupBackfillInProgress = errors.New("rollup backfill in progress")

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
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_records`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_rollup_hour`); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_rollup_state(key, int_value) VALUES(?, ?), (?, ?), (?, 1)
		ON CONFLICT(key) DO UPDATE SET int_value = excluded.int_value`,
		rollupStateUntil, now, rollupStateThrough, now, rollupStateReady); err != nil {
		return err
	}
	committed = true
	if err := tx.Commit(); err != nil {
		return err
	}
	s.rollupReady.Store(true)
	return nil
}

// ---- 日志留存清理（usage_retention.go 使用）----
//
// 与 ClearUsage 的全清不同：以下删除只动 usage_records 原始行，不触
// usage_rollup_hour/水位表——小时聚合在写入时已累加进 rollup，清理原始
// 记录不影响历史统计口径（仅查询窗口边界小时的 raw 补扫可能少算，可接受）。
// 每批删除返回被删 request_id，供调用方联动删除外置媒体目录。

// retentionDeleteBatchLimit 是单批删除的默认行数上限。
const retentionDeleteBatchLimit = 500

func deleteUsageByIDsTx(ctx context.Context, tx *sql.Tx, ids []string) error {
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM usage_records WHERE request_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	return err
}

func deleteUsageSelectTx(ctx context.Context, tx *sql.Tx, selectSQL string, args ...interface{}) ([]string, error) {
	rows, err := tx.QueryContext(ctx, selectSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteUsageOlderThan 删除 started_ms 早于 cutoffMs 的最旧一批记录（至多
// limit 条），返回被删 request_id；空切片表示已无可删。
func (s *Store) DeleteUsageOlderThan(ctx context.Context, cutoffMs int64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = retentionDeleteBatchLimit
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	ids, err := deleteUsageSelectTx(ctx, tx,
		`SELECT request_id FROM usage_records WHERE started_ms > 0 AND started_ms < ? ORDER BY started_ms ASC LIMIT ?`,
		cutoffMs, limit)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		committed = true
		return nil, tx.Commit()
	}
	if err := deleteUsageByIDsTx(ctx, tx, ids); err != nil {
		return nil, err
	}
	committed = true
	return ids, tx.Commit()
}

// DeleteUsageOldest 删除全局最旧的一批记录（至多 limit 条），超量清理用。
func (s *Store) DeleteUsageOldest(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = retentionDeleteBatchLimit
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	ids, err := deleteUsageSelectTx(ctx, tx,
		`SELECT request_id FROM usage_records ORDER BY started_ms ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		committed = true
		return nil, tx.Commit()
	}
	if err := deleteUsageByIDsTx(ctx, tx, ids); err != nil {
		return nil, err
	}
	committed = true
	return ids, tx.Commit()
}

// retentionBeyondCountBatch 是条数清理单次选择的上限：有界选择避免大表
// 一次性把数百万 id 读进内存、并在单个事务里持长写锁。
const retentionBeyondCountBatch = 2000

// DeleteUsageBeyondCount 只保留最新 keep 条，删除超出部分中最旧的一批
// （单次至多 retentionBeyondCountBatch 条），返回被删 id；空切片表示已收敛。
// 调用方（usage_retention）循环驱动直至无可删。
func (s *Store) DeleteUsageBeyondCount(ctx context.Context, keep int64) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	ids, err := deleteUsageSelectTx(ctx, tx,
		`SELECT request_id FROM usage_records ORDER BY started_ms DESC LIMIT ? OFFSET ?`, retentionBeyondCountBatch, keep)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		committed = true
		return nil, tx.Commit()
	}
	// 事务内按 500 一块删除，避免超长 IN 列表。
	for start := 0; start < len(ids); start += retentionDeleteBatchLimit {
		end := start + retentionDeleteBatchLimit
		if end > len(ids) {
			end = len(ids)
		}
		if err := deleteUsageByIDsTx(ctx, tx, ids[start:end]); err != nil {
			return nil, err
		}
	}
	committed = true
	return ids, tx.Commit()
}

// CountUsageRecords 返回当前日志记录总数。
func (s *Store) CountUsageRecords(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records`).Scan(&count)
	return count, err
}

// UsageDBStats 是数据库页面统计：LogicalBytes = (PageCount-FreePages)*PageSize，
// 近似「扣掉空闲页后的实际占用」，超量清理按它收敛（删除会释放整页进空闲
// 链表，页总数要等 VACUUM 才下降）。
type UsageDBStats struct {
	PageCount int64 `json:"pageCount"`
	PageSize  int64 `json:"pageSize"`
	FreePages int64 `json:"freePages"`
}

func (st UsageDBStats) TotalBytes() int64 {
	return st.PageCount * st.PageSize
}

func (st UsageDBStats) LogicalBytes() int64 {
	return (st.PageCount - st.FreePages) * st.PageSize
}

// UsageDBPageStats 读取 page_count/page_size/freelist_count。
func (s *Store) UsageDBPageStats(ctx context.Context) (UsageDBStats, error) {
	var st UsageDBStats
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&st.PageCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&st.PageSize); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&st.FreePages); err != nil {
		return st, err
	}
	return st, nil
}

// UsageRecordIDsExist 批量判断 request_id 是否仍存在于日志表（孤儿资产清扫用）。
func (s *Store) UsageRecordIDsExist(ctx context.Context, ids []string) (map[string]bool, error) {
	result := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT request_id FROM usage_records WHERE request_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

// VacuumUsageDB 执行 VACUUM 回收空闲页并截断 WAL。需要短暂独占写锁、
// 约双倍磁盘空间，调用方必须自行限频（见 usageRetention.maybeVacuum）。
func (s *Store) VacuumUsageDB(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (s *Store) UsageTotals(ctx context.Context, q UsageQuery) (map[string]any, error) {
	acc, err := s.usageTotalsAcc(ctx, q)
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
func (s *Store) usageTotalsAcc(ctx context.Context, q UsageQuery) (*usageTotalsAcc, error) {
	acc := &usageTotalsAcc{}
	fromHour, toHour, ok := s.rollupSplit(q, true)
	if !ok {
		if err := usageTotalsRawInto(ctx, s.db, q, acc); err != nil {
			return nil, err
		}
		return acc, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() // 只读事务，结束即弃
	headFrom, headTo, tailFrom, tailTo, hasHead, hasTail := rollupEdgeBounds(q, fromHour, toHour)
	if hasHead {
		if err := usageTotalsRawInto(ctx, tx, q.withBounds(headFrom, headTo), acc); err != nil {
			return nil, err
		}
	}
	if err := usageTotalsRollupInto(ctx, tx, q, fromHour, toHour, acc); err != nil {
		return nil, err
	}
	if hasTail {
		if err := usageTotalsRawInto(ctx, tx, q.withBounds(tailFrom, tailTo), acc); err != nil {
			return nil, err
		}
	}
	if q.From.IsZero() {
		if err := usageTotalsRawInto(ctx, tx, q.orphansOnly(), acc); err != nil {
			return nil, err
		}
	}
	return acc, nil
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
