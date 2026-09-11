package storage

import (
	"context"
	"errors"
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
