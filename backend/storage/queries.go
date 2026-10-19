package storage

import (
	"context"
	"database/sql"
	"errors"
)

const (
	// 查询分页的默认/上限（管理端列表与系统日志各自独立口径）。
	usageLogPageDefault = 50
	usageLogPageMax     = 500
	systemLogPageMax    = 500

	usageSuccessPredicate = "status_code >= 200 AND status_code < 400"
	usageFailedPredicate  = "status_code < 200 OR status_code >= 400"
)

var (
	ErrPulseFromRequired  = errors.New("pulse requires from")
	ErrPulseWindowTooLong = errors.New("pulse window exceeds 48 hours")
	ErrPulseInvertedRange = errors.New("pulse to is before from")
)

// clampPage 归一化分页参数：非法/超限 limit 回落 def，负 offset 归零。
func clampPage(limit, offset, def, max int) (int, int) {
	if limit <= 0 || limit > max {
		limit = def
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// withTx commits on success and releases the transaction on errors or panics.
func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ExecRaw 执行裸 SQL（仅限测试与一次性迁移使用）。
func (s *Store) ExecRaw(ctx context.Context, query string) error {
	_, err := s.db.ExecContext(ctx, query)
	return err
}
