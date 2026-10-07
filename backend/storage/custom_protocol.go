package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// CustomProtocol 是 Maheshvara 自定义协议的持久化行。Config 保留用户提交的
// 原始 JSON（未知字段不丢失）；其余列来自协议结构，用于列表展示与检索。
type CustomProtocol struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Version   string    `json:"version,omitempty"`
	Type      string    `json:"type"`
	Config    string    `json:"config"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ListCustomProtocols 按协议 ID 排序返回全部自定义协议。
func (s *Store) ListCustomProtocols(ctx context.Context) ([]CustomProtocol, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, version, type, config, created_at, updated_at FROM custom_protocols ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CustomProtocol{}
	for rows.Next() {
		var item CustomProtocol
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.Version, &item.Type, &item.Config, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		item.UpdatedAt = parseTime(updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

// UpsertCustomProtocol 按 ID 插入或更新一条协议（覆盖式，更新时间刷新）。
func (s *Store) UpsertCustomProtocol(ctx context.Context, item CustomProtocol) error {
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("custom protocol id is required")
	}
	if item.Type == "" {
		item.Type = "llm"
	}
	now := nowString()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO custom_protocols(id, name, version, type, config, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name, version=excluded.version, type=excluded.type, config=excluded.config, updated_at=excluded.updated_at`,
		item.ID, item.Name, item.Version, item.Type, item.Config, now, now)
	return err
}

// ProtocolRenamePair 是一次性 ID 迁移对（预置协议去厂商化重命名用）。
type ProtocolRenamePair struct {
	OldID string
	NewID string
}

// MigratePresetProtocolRenames 在单事务里完成协议 ID 改名，并把
// model_sources / models 两表 platform 列里的 custom:<旧> 引用同步重写
// （大小写不敏感）。新旧 ID 并存（用户自建了同名协议）时跳过该对——自定义
// 行优先，平台引用保持原样。幂等：旧 ID 不存在即无事发生。
func (s *Store) MigratePresetProtocolRenames(ctx context.Context, pairs []ProtocolRenamePair) (renamed int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, pair := range pairs {
		oldID := strings.TrimSpace(pair.OldID)
		newID := strings.TrimSpace(pair.NewID)
		if oldID == "" || newID == "" || oldID == newID {
			continue
		}
		// platform 引用的重写不依赖协议行是否存在：用户删过旧预置行的话，
		// 源上仍留着 custom:<old>——不重写它们会在播种新预置后变成永久悬空
		// 引用（源无法编辑、调度失效）。
		oldPlatform := "custom:" + oldID
		newPlatform := "custom:" + newID
		var conflict int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_protocols WHERE id = ? COLLATE NOCASE AND id <> ? COLLATE NOCASE`, newID, oldID).Scan(&conflict); err != nil {
			return renamed, err
		}
		if conflict > 0 {
			log.Printf("[preset-rename] skip %q -> %q: target id already exists (user-defined row wins)", oldID, newID)
			continue
		}
		// 平台引用与 v2 注册表的重写不依赖 v1 行是否存在：v2 升级过的老库
		// custom_protocols 可能已无预置行，注册表行与 custom:<旧> 引用仍在。
		// 重放安全：旧值零行即静默无事发生。
		if _, err := tx.ExecContext(ctx, `UPDATE model_sources SET platform = ? WHERE LOWER(platform) = ?`, newPlatform, strings.ToLower(oldPlatform)); err != nil {
			return renamed, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE models SET platform = ? WHERE LOWER(platform) = ?`, newPlatform, strings.ToLower(oldPlatform)); err != nil {
			return renamed, err
		}
		if err := renameProtocolRegistryIDs(ctx, tx, oldID, newID); err != nil {
			return renamed, err
		}
		// 运行时注册表按 config JSON 内部 id 建键,只改行 id 列会让注册键与
		// 改写后的 custom:<新> 平台引用脱节(保存源/请求时 not registered)。
		if err := rewriteProtocolConfigIDs(ctx, tx, oldID, newID); err != nil {
			return renamed, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE custom_protocols SET id = ?, updated_at = ? WHERE id = ? COLLATE NOCASE`, newID, nowString(), oldID)
		if err != nil {
			return renamed, err
		}
		// 旧 ID 行不存在(全新库/已改过名)时不算改名:否则每次启动都会打
		// 误导性的 "renamed" 日志。
		affected, err := result.RowsAffected()
		if err != nil {
			return renamed, err
		}
		if affected == 0 {
			continue
		}
		renamed++
		log.Printf("[preset-rename] protocol %q renamed to %q (registry rows and platform references rewritten)", oldID, newID)
	}
	return renamed, tx.Commit()
}

// renameProtocolRegistryIDs 把 v2 协议注册表里 oldID 的行（revisions/drafts/
// activations/reports/history、bindings JSON 内的 protocolId、agent 会话引用）
// 改名到 newID。预置行由引擎随版本强制重发：与新 ID 并存的行删旧拷贝消解主键
// 冲突；旧 ID 无行时整段为幂等空操作（重放不破坏已迁移的新行）。
func renameProtocolRegistryIDs(ctx context.Context, tx *sql.Tx, oldID, newID string) error {
	// 注册表带 FOREIGN KEY（activations/reports → revisions），UPDATE 改主键会
	// 触发即时约束。改为两阶段：先按父表在前的次序把旧行「复制为新键」
	// （INSERT OR IGNORE：新旧并存的行以新行——引擎当前内容——胜出），再按
	// 子表在前的次序删除旧行。全程序等幂等，重放零副作用。
	// 复制语句按表显式给出：protocol_history 的 id 是内容寻址主键
	// （<protocol_id>~<hash>），照抄会与源行主键冲突被 OR IGNORE 吞掉，
	// 须按新 ID 重新生成；revisions 的新内容哈希由引擎随版本重发。
	copyTables := []struct {
		table string
		sql   string
		args  []any
	}{
		{"protocol_revisions", `INSERT OR IGNORE INTO protocol_revisions(protocol_id, content_hash, definition, created_at) SELECT ?, content_hash, definition, created_at FROM protocol_revisions WHERE protocol_id = ?`, nil},
		{"protocol_drafts", `INSERT OR IGNORE INTO protocol_drafts(protocol_id, content_hash, definition, updated_at) SELECT ?, content_hash, definition, updated_at FROM protocol_drafts WHERE protocol_id = ?`, nil},
		{"protocol_history", `INSERT OR IGNORE INTO protocol_history(id, protocol_id, content_hash, definition, reason, is_draft, created_at, archived_at) SELECT ? || '~' || content_hash, ?, content_hash, definition, reason, is_draft, created_at, archived_at FROM protocol_history WHERE protocol_id = ?`, nil},
		{"protocol_activations", `INSERT OR IGNORE INTO protocol_activations(protocol_id, revision_hash, generation, activated_at) SELECT ?, revision_hash, generation, activated_at FROM protocol_activations WHERE protocol_id = ?`, nil},
		{"protocol_verification_reports", `INSERT OR IGNORE INTO protocol_verification_reports(id, protocol_id, revision_hash, compiler_version, samples_hash, kind, report, verified_at) SELECT id, ?, revision_hash, compiler_version, samples_hash, kind, report, verified_at FROM protocol_verification_reports WHERE protocol_id = ?`, nil},
	}
	deleteTables := []string{"protocol_activations", "protocol_verification_reports", "protocol_revisions", "protocol_drafts", "protocol_history"}
	for _, copy := range copyTables {
		args := copy.args
		if args == nil {
			if copy.table == "protocol_history" {
				args = []any{newID, newID, oldID}
			} else {
				args = []any{newID, oldID}
			}
		}
		if _, err := tx.ExecContext(ctx, copy.sql, args...); err != nil {
			return fmt.Errorf("copy %s: %w", copy.table, err)
		}
	}
	for _, table := range deleteTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE protocol_id = ?`, oldID); err != nil {
			return fmt.Errorf("delete legacy %s: %w", table, err)
		}
	}
	// bindings 的行键不含协议 ID，只需改写 JSON 内容；单连接 SQLite 下先收集
	// 再写回，避免游标未关时同事务写入。
	rows, err := tx.QueryContext(ctx, `SELECT binding_key, binding FROM protocol_bindings`)
	if err != nil {
		return err
	}
	type bindingPatch struct{ key, binding string }
	patches := []bindingPatch{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var binding ProtocolBinding
		if err := json.Unmarshal([]byte(raw), &binding); err != nil || binding.Binding.ProtocolID != oldID {
			continue
		}
		binding.Binding.ProtocolID = newID
		updated, err := json.Marshal(binding)
		if err != nil {
			rows.Close()
			return err
		}
		patches = append(patches, bindingPatch{key: key, binding: string(updated)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, patch := range patches {
		if _, err := tx.ExecContext(ctx, `UPDATE protocol_bindings SET binding = ? WHERE binding_key = ?`, patch.binding, patch.key); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_sessions SET protocol_id = ? WHERE protocol_id = ?`, newID, oldID)
	return err
}

// rewriteProtocolConfigID 把存储行 config JSON 内部的 "id" 字段改写为 wantID
// (以 map 形态改写,保留未知字段与其余键值原文;缺失则补上)。changed 表示
// 是否实际改写;JSON 无效时报错并由调用方决定跳过。
func rewriteProtocolConfigID(config, wantID string) (rewritten string, changed bool, err error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(config), &obj); err != nil {
		return config, false, fmt.Errorf("invalid JSON: %w", err)
	}
	current := ""
	if raw, ok := obj["id"]; ok {
		_ = json.Unmarshal(raw, &current)
	}
	if strings.ToLower(strings.TrimSpace(current)) == strings.ToLower(strings.TrimSpace(wantID)) {
		return config, false, nil
	}
	encoded, err := json.Marshal(wantID)
	if err != nil {
		return config, false, err
	}
	obj["id"] = encoded
	out, err := json.Marshal(obj)
	if err != nil {
		return config, false, err
	}
	return string(out), true, nil
}

// rewriteProtocolConfigIDs 在事务内把匹配 oldID 的行的 config 内部 id 改写为
// newID;无效 JSON 的行跳过(记日志,由注册表装配的容错路径另行暴露)。
func rewriteProtocolConfigIDs(ctx context.Context, tx *sql.Tx, oldID, newID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT config FROM custom_protocols WHERE id = ? COLLATE NOCASE`, oldID)
	if err != nil {
		return err
	}
	var configs []string
	for rows.Next() {
		var config string
		if err := rows.Scan(&config); err != nil {
			rows.Close()
			return err
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, config := range configs {
		rewritten, changed, err := rewriteProtocolConfigID(config, newID)
		if err != nil {
			log.Printf("[preset-rename] skip config id rewrite for %q: %v", oldID, err)
			continue
		}
		if !changed {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE custom_protocols SET config = ? WHERE id = ? COLLATE NOCASE`, rewritten, oldID); err != nil {
			return err
		}
		log.Printf("[preset-rename] protocol %q config id rewritten to %q", oldID, newID)
	}
	return nil
}

// ReconcileCustomProtocolConfigIDs 对账行 id 列与 config JSON 内部 "id":不一致
// (大小写不敏感)时以行 id 为准改写 JSON 并落库。注册表按 config 内部 id 建
// 键,而 platform 引用跟随行 id 列——旧版改名迁移只改列不改 JSON 的库正是
// 靠此修复(否则 custom:<行id> 引用解析报 not registered)。幂等;无效 JSON
// 的行跳过。
func (s *Store) ReconcileCustomProtocolConfigIDs(ctx context.Context) (fixed int, err error) {
	rows, err := s.ListCustomProtocols(ctx)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		rewritten, changed, err := rewriteProtocolConfigID(row.Config, row.ID)
		if err != nil {
			log.Printf("[protocol-reconcile] skip %q: %v", row.ID, err)
			continue
		}
		if !changed {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE custom_protocols SET config = ?, updated_at = ? WHERE id = ? COLLATE NOCASE`, rewritten, nowString(), row.ID); err != nil {
			return fixed, err
		}
		fixed++
		log.Printf("[protocol-reconcile] protocol %q config id rewritten to match row id", row.ID)
	}
	return fixed, nil
}
