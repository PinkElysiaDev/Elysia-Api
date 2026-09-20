package storage

import (
	"context"
	"errors"
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
//（大小写不敏感）。新旧 ID 并存（用户自建了同名协议）时跳过该对——自定义
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
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_protocols WHERE id = ? COLLATE NOCASE`, oldID).Scan(&exists); err != nil {
			return renamed, err
		}
		if exists == 0 {
			continue
		}
		var conflict int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_protocols WHERE id = ? COLLATE NOCASE AND id <> ? COLLATE NOCASE`, newID, oldID).Scan(&conflict); err != nil {
			return renamed, err
		}
		if conflict > 0 {
			log.Printf("[preset-rename] skip %q -> %q: target id already exists (user-defined row wins)", oldID, newID)
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE custom_protocols SET id = ?, updated_at = ? WHERE id = ? COLLATE NOCASE`, newID, nowString(), oldID); err != nil {
			return renamed, err
		}
		oldPlatform := "custom:" + oldID
		newPlatform := "custom:" + newID
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
