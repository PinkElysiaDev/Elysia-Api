// usage 资产引用：请求与资产文件的关联登记、释放与全量引用扫描。
package storage

import (
	"context"
	"database/sql"
	"strings"
)

// InsertUsageAssetRef 登记一条「记录 → 资产文件」引用（幂等）。文件按内容
// 哈希全局去重，多个记录可引用同一文件；能否删文件由引用计数决定。
func (s *Store) InsertUsageAssetRef(ctx context.Context, requestID, assetFile string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO usage_asset_refs(asset_file, request_id) VALUES(?, ?)`, assetFile, requestID)
	return err
}

// DeleteUsageAssetRefs 删除一组记录的资产引用，返回因此不再被任何记录
// 引用（可安全删除文件）的资产文件名。仍在被其他记录引用的不返回。
func (s *Store) DeleteUsageAssetRefs(ctx context.Context, requestIDs []string) ([]string, error) {
	if len(requestIDs) == 0 {
		return nil, nil
	}
	var orphans []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		for start := 0; start < len(requestIDs); start += retentionDeleteBatchLimit {
			end := start + 500
			if end > len(requestIDs) {
				end = len(requestIDs)
			}
			batch := requestIDs[start:end]
			placeholders := make([]string, len(batch))
			args := make([]any, len(batch))
			for i, id := range batch {
				placeholders[i] = "?"
				args[i] = id
			}
			in := strings.Join(placeholders, ",")
			// 先收集这批请求涉及的文件，删引用后再筛出零引用的。
			rows, err := tx.QueryContext(ctx,
				`SELECT DISTINCT asset_file FROM usage_asset_refs WHERE request_id IN (`+in+`)`, args...)
			if err != nil {
				return err
			}
			var touched []string
			for rows.Next() {
				var file string
				if err := rows.Scan(&file); err != nil {
					rows.Close()
					return err
				}
				touched = append(touched, file)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM usage_asset_refs WHERE request_id IN (`+in+`)`, args...); err != nil {
				return err
			}
			for _, file := range touched {
				var remaining int
				if err := tx.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM usage_asset_refs WHERE asset_file = ?`, file).Scan(&remaining); err != nil {
					return err
				}
				if remaining == 0 {
					orphans = append(orphans, file)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return orphans, nil
}

// ReferencedAssetFiles 返回当前仍被引用的全部资产文件名（孤儿清扫用）。
func (s *Store) ReferencedAssetFiles(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT asset_file FROM usage_asset_refs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			return nil, err
		}
		result[file] = true
	}
	return result, rows.Err()
}

// QueryRecordBodiesWithAssets 流式返回 (request_id, record_json)，仅取
// 含资产占位符的记录（LIKE 预过滤）。资产布局迁移重建引用表用。
func (s *Store) QueryRecordBodiesWithAssets(ctx context.Context) (*sql.Rows, error) {
	return s.db.QueryContext(ctx,
		`SELECT request_id, record_json FROM usage_records WHERE record_json LIKE '%__ELYSIA_ASSET__%'`)
}
