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

// withTx 是写事务的统一脚手架：fn 返回 nil 即提交、返回错误即回滚。
// 取代此前并存的三种手写 Begin/defer-Rollback/Commit 风格。
func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ExecRaw 执行裸 SQL（仅限测试与一次性迁移使用）。
func (s *Store) ExecRaw(ctx context.Context, query string) error {
	_, err := s.db.ExecContext(ctx, query)
	return err
}
