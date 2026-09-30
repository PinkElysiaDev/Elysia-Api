// 系统日志的写入与查询。
package storage

import (
	"context"
	"encoding/json"
	"time"
)

func (s *Store) InsertSystemLog(ctx context.Context, level, message string, fields any) error {
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	now := time.Now()
	created := now.UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO system_logs(created_at, created_ms, level, message, fields_json, content_bytes) VALUES(?, ?, ?, ?, ?, ?)`, created, now.UnixMilli(), level, message, string(payload), len(created)+len(level)+len(message)+len(payload))
	return err
}

func (s *Store) QuerySystemLogs(ctx context.Context, limit, offset int, level string) (int, []SystemLog, error) {
	limit, offset = clampPage(limit, offset, 100, systemLogPageMax)
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
	// 按 created_ms(毫秒列)排序而非 RFC3339Nano 字符串:整秒时间戳与同秒内
	// 带小数者的字符串序会颠倒,分页顺序不稳定;且毫秒列有 idx_system_time
	// 索引可直接服务排序(usage_records 的 started_ms 修过同款问题)。
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, level, message, fields_json FROM system_logs `+where+` ORDER BY created_ms DESC, id DESC LIMIT ? OFFSET ?`, args...)
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
