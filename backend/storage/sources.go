// 模型源与模型行存取：源 CRUD、多 key 更新与拉取/手动模型的合并。
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Store) ListSources(ctx context.Context) ([]ModelSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, base_url, api_key, platform, enabled, auto_fetch_models, manual_models_json, fetch_base_url, api_keys, key_strategy, created_at, updated_at FROM model_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelSource{}
	for rows.Next() {
		var item ModelSource
		var enabled, autoFetch int
		var manual, fetchBase, storedKeys, strategy, created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &item.APIKey, &item.Platform, &enabled, &autoFetch, &manual, &fetchBase, &storedKeys, &strategy, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = sqlIntToBool(enabled)
		item.AutoFetchModels = sqlIntToBool(autoFetch)
		item.FetchBaseURL = fetchBase
		item.KeyStrategy = SourceKeyStrategy(strategy)
		item.CreatedAt = parseTime(created)
		item.UpdatedAt = parseTime(updated)
		item.APIKey = s.decryptOrClear("source api_key", item.ID, item.APIKey)
		_ = json.Unmarshal([]byte(manual), &item.ManualModels)
		if storedKeys != "" {
			if plain := s.decryptOrClear("source api_keys", item.ID, storedKeys); plain != "" {
				_ = json.Unmarshal([]byte(plain), &item.APIKeys)
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertSource(ctx context.Context, item ModelSource) error {
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("source id is required")
	}
	manual, err := json.Marshal(item.ManualModels)
	if err != nil {
		return err
	}
	storedKey, err := s.codec.encrypt(item.APIKey)
	if err != nil {
		return err
	}
	// api_keys 为 JSON 数组整体加密存储；空列表落空串（单 key 模式）。
	storedKeys := ""
	if len(item.APIKeys) > 0 {
		payload, err := json.Marshal(item.APIKeys)
		if err != nil {
			return err
		}
		if storedKeys, err = s.codec.encrypt(string(payload)); err != nil {
			return err
		}
	}
	strategy := string(item.KeyStrategy)
	if strategy == "" {
		strategy = string(KeyStrategySingle)
	}
	now := nowString()
	_, err = s.db.ExecContext(ctx, `INSERT INTO model_sources(id, name, base_url, api_key, platform, enabled, auto_fetch_models, manual_models_json, fetch_base_url, api_keys, key_strategy, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, base_url=excluded.base_url, api_key=excluded.api_key, platform=excluded.platform, enabled=excluded.enabled, auto_fetch_models=excluded.auto_fetch_models, manual_models_json=excluded.manual_models_json, fetch_base_url=excluded.fetch_base_url, api_keys=excluded.api_keys, key_strategy=excluded.key_strategy, updated_at=excluded.updated_at`, item.ID, item.Name, item.BaseURL, storedKey, item.Platform, sqlBoolToInt(item.Enabled), sqlBoolToInt(item.AutoFetchModels), string(manual), item.FetchBaseURL, storedKeys, strategy, now, now)
	return err
}

// UpdateSourceAPIKeys 定向更新某个源的 key 列表（加密整列重写，不碰其他字段）。
// 供逐 key 拉取后回写 fetchedModels/allowedModels 使用——避免为了改 key 元数据
// 而走整源 Upsert 与用户编辑产生竞争。
func (s *Store) UpdateSourceAPIKeys(ctx context.Context, sourceID string, keys []SourceAPIKey) error {
	payload, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	stored, err := s.codec.encrypt(string(payload))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE model_sources SET api_keys = ?, updated_at = ? WHERE id = ?`, stored, nowString(), sourceID)
	return err
}

// UpdateSourceEnabled 仅更新源的启停开关（方向：源级轻量 PATCH）。
// 与整源 Upsert 不同：不触发「保存后自动同步模型」之类的副作用，也不触碰
// key/模型数据——启停与模型列表无关，重拉上游纯属多余（可能触发限流）。
func (s *Store) UpdateSourceEnabled(ctx context.Context, id string, enabled bool) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE model_sources SET enabled = ?, updated_at = ? WHERE id = ?`, sqlBoolToInt(enabled), nowString(), id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) DeleteSource(ctx context.Context, id string) error {
	// 事务化：删除模型源、其下模型以及组内对该源模型的引用必须原子完成，
	// 避免第二步失败留下孤儿模型或组内残留旧模型引用。
	// （models / model_group_models 表对 model_sources 无 FK 级联）。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_sources WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM models WHERE source_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE source_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceSourceModels(ctx context.Context, source ModelSource, models []Model) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM models WHERE source_id = ?`, source.ID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, insertModelSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	checked := nowString()
	// models.api_key 冗余列写入首个有效 key：热路径已改用源级 key 集合（方向6），
	// 该列仅供健康检测等遗留消费方回退，避免空 key 探测必败。
	storedKey, err := s.codec.encrypt(firstEffectiveKey(source))
	if err != nil {
		return err
	}
	for _, model := range models {
		model = normalizeModelDefaults(model)
		// platform 优先取模型自带值（legacy 配置导入的模型可逐模型声明平台，
		// 如 responses/gemini），为空才回落源级平台（自动拉取的模型两者一致）。
		platform := model.Platform
		if strings.TrimSpace(platform) == "" {
			platform = source.Platform
		}
		if _, err := stmt.ExecContext(ctx, model.ID, source.ID, model.Name, source.Name, model.BaseURL, storedKey, NormalizePlatform(platform), model.Type, model.MaxTokens, sqlBoolToInt(model.VisionCapable), sqlBoolToInt(model.ToolsCapable), sqlBoolToInt(model.StructuredOutput), model.ThinkingMode, sqlBoolToInt(true), sqlBoolToInt(model.Enabled), model.Origin, model.CapabilitySource, checked); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// firstEffectiveKey 返回源的首个有效 key（多 key 时取第一个启用项，单 key 即 APIKey），
// 供 models.api_key 冗余列的向后兼容写入。
func firstEffectiveKey(source ModelSource) string {
	for _, key := range source.EffectiveKeys() {
		return key.Value
	}
	return ""
}

// insertModelSQL 是 models 表 18 列的统一 INSERT(ReplaceSourceModels 与
// MergeSourceModels 共用;列清单与 modelColumns 常量保持同步)。
const insertModelSQL = `INSERT INTO models(id, source_id, name, source_name, base_url, api_key, platform, type, max_tokens, vision_capable, tools_capable, structured_output, thinking_mode, available, enabled, origin, capability_source, last_checked_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// NormalizePlatform 把历史别名 openai-compatible 归一为 openai（server 侧
// 同名逻辑已删除，统一从这里调用）。
func NormalizePlatform(platform string) string {
	if platform == "openai-compatible" {
		return "openai"
	}
	return platform
}

// ModelMergeResult 汇总一次刷新合并的变更，供前端展示「新增 N / 移除 M」。
type ModelMergeResult struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// MergeSourceModels 用新拉取（或手动同步）的模型列表合并进该源的模型表，
// 替代旧的全删全插（ReplaceSourceModels）：
//   - manual 行（origin='manual'）永不触碰：既不更新也不删除——手动模型与
//     用户显式保留的模型不随上游变动丢失（借鉴 axonhub manual∪fetched 合并策略）；
//   - fetched 行更新源身份（base_url/api_key/platform/name）与上游返回的元数据，
//     但保留用户手动启停（enabled）与健康位（available）；
//   - 能力字段（vision/tools/structured/thinking/maxTokens/type）：incoming 携带
//     capability_source='manual'（用户在 UI 编辑过）的行保留现有值，否则用
//     incoming 值（目录回填或上游解析）覆盖；
//   - 上游消失的 fetched 行删除并同步清理组内引用；manual 行即使上游消失也保留
//     （手动同步路径用 SyncManualSourceModels，其 manual 语义不同）。
func (s *Store) MergeSourceModels(ctx context.Context, source ModelSource, incoming []Model) (ModelMergeResult, error) {
	return s.mergeSourceModels(ctx, source, incoming, false)
}

// SyncManualSourceModels 以源配置里的手动模型集为权威同步 models 表：
// 除 MergeSourceModels 的合并语义外，缺席于 manual 集的 manual 行删除并清理
// 组内引用——用户在源编辑里删除的手动模型必须从表中消失，外层页面读的正是
// 这张表。空集合法（清空全部手动模型）。
func (s *Store) SyncManualSourceModels(ctx context.Context, source ModelSource, manual []Model) (ModelMergeResult, error) {
	return s.mergeSourceModels(ctx, source, manual, true)
}

// existingModelRow 是合并时读入的既有模型行（仅参与合并判定的列）。
type existingModelRow struct {
	id, name, thinking, origin, capabilitySource string
	maxTokens                                    int
	vision, tools, structured                    bool
}

// loadExistingModelRows 读入某源的全量既有模型行（单连接库：先全部读进内存
// 再写，避免游标占用连接死锁）。
func loadExistingModelRows(ctx context.Context, tx *sql.Tx, sourceID string) (map[string]existingModelRow, error) {
	existing := map[string]existingModelRow{}
	rows, err := tx.QueryContext(ctx, `SELECT id, name, thinking_mode, origin, capability_source, max_tokens, vision_capable, tools_capable, structured_output FROM models WHERE source_id = ?`, sourceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r existingModelRow
		var vision, tools, structured int
		if err := rows.Scan(&r.id, &r.name, &r.thinking, &r.origin, &r.capabilitySource, &r.maxTokens, &vision, &tools, &structured); err != nil {
			rows.Close()
			return nil, err
		}
		r.vision, r.tools, r.structured = sqlIntToBool(vision), sqlIntToBool(tools), sqlIntToBool(structured)
		existing[r.id] = r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return existing, nil
}

// modelMerge 封装一次模型合并事务的共享材料：事务、预编译语句、加密 key、
// 时间戳与源身份，供逐模型处理与缺席清扫共用。
type modelMerge struct {
	tx         *sql.Tx
	source     ModelSource
	storedKey  string
	checked    string
	insertStmt *sql.Stmt
	updateStmt *sql.Stmt
}

func (s *Store) newModelMerge(ctx context.Context, tx *sql.Tx, source ModelSource) (*modelMerge, error) {
	storedKey, err := s.codec.encrypt(firstEffectiveKey(source))
	if err != nil {
		return nil, err
	}
	insertStmt, err := tx.PrepareContext(ctx, insertModelSQL)
	if err != nil {
		return nil, err
	}
	updateStmt, err := tx.PrepareContext(ctx, `UPDATE models SET name = ?, source_name = ?, base_url = ?, api_key = ?, platform = ?, type = ?, max_tokens = ?, vision_capable = ?, tools_capable = ?, structured_output = ?, thinking_mode = ?, capability_source = ?, last_checked_at = ? WHERE id = ? AND source_id = ?`)
	if err != nil {
		insertStmt.Close()
		return nil, err
	}
	return &modelMerge{tx: tx, source: source, storedKey: storedKey, checked: nowString(), insertStmt: insertStmt, updateStmt: updateStmt}, nil
}

func (m *modelMerge) close() {
	m.insertStmt.Close()
	m.updateStmt.Close()
}

// mergeOne 处理 incoming 中的一个模型，返回是否为新插入行：
//   - manual 行保留能力与启停，只刷新源身份快照（base_url/api_key/platform
//     随源保存刷新——产品面不存在逐模型覆盖地址/密钥的语义；源 BaseURL 为空
//     的 legacy 导入源跳过，其 models 行携带逐模型地址，见 ImportLegacyConfig）；
//   - 既有行按能力来源保留用户编辑（capability_source='manual' 的能力值不动），
//     其余能力值随 incoming（目录回填/上游解析）更新；
//   - 新行插入（新模型默认启用，available 初始为 true 由健康检测接管）。
func (m *modelMerge) mergeOne(ctx context.Context, existing map[string]existingModelRow, model Model) (bool, error) {
	prev, existed := existing[model.ID]
	if existed && prev.origin == "manual" {
		if m.source.BaseURL != "" {
			if _, err := m.tx.ExecContext(ctx, `UPDATE models SET base_url = ?, api_key = ?, platform = ?, last_checked_at = ? WHERE id = ? AND source_id = ?`,
				m.source.BaseURL, m.storedKey, NormalizePlatform(m.source.Platform), m.checked, model.ID, m.source.ID); err != nil {
				return false, err
			}
		} else if _, err := m.tx.ExecContext(ctx, `UPDATE models SET last_checked_at = ? WHERE id = ? AND source_id = ?`, m.checked, model.ID, m.source.ID); err != nil {
			return false, err
		}
		return false, nil
	}
	if existed {
		// 用户编辑过的能力字段（capability_source='manual'）在刷新时保留，
		// 其余能力值随 incoming（目录回填/上游解析）更新。
		vision, tools, structured, thinking, maxTokens, modelType, capabilitySource :=
			model.VisionCapable, model.ToolsCapable, model.StructuredOutput, model.ThinkingMode, model.MaxTokens, model.Type, model.CapabilitySource
		if prev.capabilitySource == "manual" {
			vision, tools, structured = prev.vision, prev.tools, prev.structured
			thinking, maxTokens = prev.thinking, prev.maxTokens
			capabilitySource = "manual"
		}
		if _, err := m.updateStmt.ExecContext(ctx, model.Name, m.source.Name, m.source.BaseURL, m.storedKey, NormalizePlatform(m.source.Platform), modelType, maxTokens, sqlBoolToInt(vision), sqlBoolToInt(tools), sqlBoolToInt(structured), thinking, capabilitySource, m.checked, model.ID, m.source.ID); err != nil {
			return false, err
		}
		return false, nil
	}
	if _, err := m.insertStmt.ExecContext(ctx, model.ID, m.source.ID, model.Name, m.source.Name, m.source.BaseURL, m.storedKey, NormalizePlatform(m.source.Platform), model.Type, model.MaxTokens, sqlBoolToInt(model.VisionCapable), sqlBoolToInt(model.ToolsCapable), sqlBoolToInt(model.StructuredOutput), model.ThinkingMode, sqlBoolToInt(true), sqlBoolToInt(model.Enabled), model.Origin, model.CapabilitySource, m.checked); err != nil {
		return false, err
	}
	return true, nil
}

// sweepMissing 清扫缺席行：上游消失的 fetched 行删除；手动同步路径
// （deleteMissingManual）下，缺席于权威集的 manual 行同样删除——fetch 路径
// 维持 manual 行保留语义。删除同步清理组内引用防悬空。
func (m *modelMerge) sweepMissing(ctx context.Context, existing map[string]existingModelRow, incomingIDs map[string]struct{}, deleteMissingManual bool) ([]string, error) {
	var removed []string
	for id, prev := range existing {
		_, stillPresent := incomingIDs[id]
		isManual := prev.origin == "manual"
		if stillPresent || (isManual && !deleteMissingManual) {
			continue
		}
		removed = append(removed, id)
		if _, err := m.tx.ExecContext(ctx, `DELETE FROM models WHERE id = ? AND source_id = ?`, id, m.source.ID); err != nil {
			return removed, err
		}
		if _, err := m.tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE model_id = ? AND source_id = ?`, id, m.source.ID); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// mergeSourceModels 把一份权威模型清单合并进库：新行插入、既有行按能力
// 来源刷新、缺席行清扫，全部在同一事务内。
func (s *Store) mergeSourceModels(ctx context.Context, source ModelSource, incoming []Model, deleteMissingManual bool) (ModelMergeResult, error) {
	result := ModelMergeResult{Added: []string{}, Removed: []string{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	existing, err := loadExistingModelRows(ctx, tx, source.ID)
	if err != nil {
		return result, err
	}
	merge, err := s.newModelMerge(ctx, tx, source)
	if err != nil {
		return result, err
	}
	defer merge.close()

	incomingIDs := make(map[string]struct{}, len(incoming))
	for i := range incoming {
		model := normalizeModelDefaults(incoming[i])
		incomingIDs[model.ID] = struct{}{}
		added, err := merge.mergeOne(ctx, existing, model)
		if err != nil {
			return result, err
		}
		if added {
			result.Added = append(result.Added, model.ID)
		}
	}
	result.Removed, err = merge.sweepMissing(ctx, existing, incomingIDs, deleteMissingManual)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// normalizeModelDefaults 补齐模型字段的落库默认值。
// Enabled 恒置 true：该函数只服务于「新插入行」（合并的新模型默认启用——已确认
// 的产品决策；已有行的启停由 UPDATE 语句不涉及该列而天然保留，manual 行整体跳过）。
func normalizeModelDefaults(model Model) Model {
	if model.Type == "" {
		model.Type = "llm"
	}
	if model.ThinkingMode == "" {
		model.ThinkingMode = "both"
	}
	if model.Name == "" {
		model.Name = model.ID
	}
	if model.Origin == "" {
		model.Origin = "fetched"
	}
	model.Enabled = true
	return model
}
