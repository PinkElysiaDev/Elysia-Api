// 系统日志的写入与查询。
package storage

import (
	"context"
	"encoding/json"
)

func (s *Store) InsertSystemLog(ctx context.Context, level, message string, fields any) error {
	payload, _ := json.Marshal(fields)
	_, err := s.db.ExecContext(ctx, `INSERT INTO system_logs(created_at, level, message, fields_json) VALUES(?, ?, ?, ?)`, nowString(), level, message, string(payload))
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
