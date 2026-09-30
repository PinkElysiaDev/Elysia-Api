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

// DeleteCustomProtocol 删除一条协议，返回是否确实删除了记录。
func (s *Store) DeleteCustomProtocol(ctx context.Context, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM custom_protocols WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
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
		// 误导性的 "renamed" 日志;platform 引用的悬空重写照常执行(见上)。
		affected, err := result.RowsAffected()
		if err != nil {
			return renamed, err
		}
		if affected == 0 {
			if srcResult, err := tx.ExecContext(ctx, `UPDATE model_sources SET platform = ? WHERE LOWER(platform) = ?`, newPlatform, strings.ToLower(oldPlatform)); err == nil {
				if rows, rowsErr := srcResult.RowsAffected(); err == nil && rowsErr == nil && rows > 0 {
					log.Printf("[preset-rename] rewrote %d dangling custom:%s platform reference(s) to custom:%s", rows, oldID, newID)
				}
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE model_sources SET platform = ? WHERE LOWER(platform) = ?`, newPlatform, strings.ToLower(oldPlatform)); err != nil {
			return renamed, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE models SET platform = ? WHERE LOWER(platform) = ?`, newPlatform, strings.ToLower(oldPlatform)); err != nil {
			return renamed, err
		}
		renamed++
		log.Printf("[preset-rename] protocol %q renamed to %q (platform references rewritten)", oldID, newID)
	}
	return renamed, tx.Commit()
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
