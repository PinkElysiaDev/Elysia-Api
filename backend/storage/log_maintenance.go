package storage

import (
	"context"
	"database/sql"
	"os"
)

type CheckpointResult struct {
	Busy               bool `json:"busy"`
	LogFrames          int  `json:"logFrames"`
	CheckpointedFrames int  `json:"checkpointedFrames"`
}

// Checkpoint must inspect SQLite's result row: SQLITE_OK can still mean BUSY.
// Do not wait five seconds behind a long reader on the shared single connection.
func (s *Store) Checkpoint(ctx context.Context, truncate bool) (CheckpointResult, error) {
	var result CheckpointResult
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		return result, err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA busy_timeout=5000`)
	mode := "PASSIVE"
	if truncate {
		mode = "TRUNCATE"
	}
	var busy int
	err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(`+mode+`)`).Scan(&busy, &result.LogFrames, &result.CheckpointedFrames)
	result.Busy = busy != 0 || (result.LogFrames >= 0 && result.CheckpointedFrames < result.LogFrames)
	return result, err
}

func (s *Store) ReclaimLogPages(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA incremental_vacuum(256)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	// incremental_vacuum yields once per page. Exec or a single Next may only
	// reclaim one page with drivers which stop at the first SQLITE_ROW.
	for rows.Next() {
	}
	return rows.Err()
}

type LogContentStats struct {
	UsageBytes        int64 `json:"usageBytes"`
	UsageRecords      int64 `json:"usageRecords"`
	SystemBytes       int64 `json:"systemBytes"`
	SystemRecords     int64 `json:"systemRecords"`
	MediaBytes        int64 `json:"mediaBytes"`
	PendingMediaBytes int64 `json:"pendingMediaBytes"`
	MediaFiles        int64 `json:"mediaFiles"`
}

func (s *Store) LogContentStats(ctx context.Context) (LogContentStats, error) {
	var st LogContentStats
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT coalesce(sum(content_bytes),0) FROM usage_records), (SELECT count(*) FROM usage_records),
		(SELECT coalesce(sum(content_bytes),0) FROM system_logs), (SELECT count(*) FROM system_logs),
		(SELECT coalesce(sum(size_bytes),0) FROM usage_assets a WHERE EXISTS(SELECT 1 FROM usage_asset_refs r WHERE r.asset_file=a.asset_file)),
		(SELECT coalesce(sum(size_bytes),0) FROM usage_assets a WHERE NOT EXISTS(SELECT 1 FROM usage_asset_refs r WHERE r.asset_file=a.asset_file)),
		(SELECT count(*) FROM usage_assets)`).Scan(&st.UsageBytes, &st.UsageRecords, &st.SystemBytes, &st.SystemRecords, &st.MediaBytes, &st.PendingMediaBytes, &st.MediaFiles)
	return st, err
}

type LogStorageStats struct {
	FileBytes         int64 `json:"fileBytes"`
	UsedBytes         int64 `json:"usedBytes"`
	FreeBytes         int64 `json:"freeBytes"`
	WALBytes          int64 `json:"walBytes"`
	RollupBytes       int64 `json:"rollupBytes"`
	IndexBytes        int64 `json:"indexBytes"`
	PageOverheadBytes int64 `json:"pageOverheadBytes"`
	FreePages         int64 `json:"freePages"`
}

func (s *Store) LogStorageStats(ctx context.Context) (LogStorageStats, error) {
	var st LogStorageStats
	pages, err := s.UsageDBPageStats(ctx)
	if err != nil {
		return st, err
	}
	st.UsedBytes, st.FreePages, st.FreeBytes = pages.LogicalBytes(), pages.FreePages, pages.FreePages*pages.PageSize
	info, err := os.Stat(s.path)
	if err != nil {
		return st, err
	}
	st.FileBytes = info.Size()
	info, err = os.Stat(s.path + "-wal")
	if err == nil {
		st.WALBytes = info.Size()
	} else if !os.IsNotExist(err) {
		return st, err
	}
	// Aggregate dbstat reports storage without reading JSON into Go. This is a
	// status query, never a quota decision or a per-request query.
	err = s.db.QueryRowContext(ctx, `SELECT
		coalesce(sum(CASE WHEN name IN ('usage_rollup_hour','usage_rollup_state') THEN pgsize ELSE 0 END),0),
		coalesce(sum(CASE WHEN name IN (SELECT name FROM sqlite_schema WHERE type='index') THEN pgsize ELSE 0 END),0),
		coalesce(sum(pgsize-payload),0) FROM dbstat WHERE aggregate=TRUE`).Scan(&st.RollupBytes, &st.IndexBytes, &st.PageOverheadBytes)
	return st, err
}

type LogPrunePolicy struct {
	CutoffMS        int64
	MaxRecords      int64
	MaxContentBytes int64
}
type LogDeleted struct {
	ByTTL     int `json:"byTTL"`
	ByRecords int `json:"byRecords"`
	ByContent int `json:"byContent"`
}

func (d LogDeleted) Total() int { return d.ByTTL + d.ByRecords + d.ByContent }

// PruneLogBatch makes decisions from exact content bytes, not database pages or
// an estimated average. Each transaction deletes at most 500 oldest records.
func (s *Store) PruneLogBatch(ctx context.Context, system bool, policy LogPrunePolicy) (LogDeleted, error) {
	var deleted LogDeleted
	if policy.CutoffMS == 0 && policy.MaxRecords == 0 && policy.MaxContentBytes == 0 {
		return deleted, nil
	}
	table, key, stamp := "usage_records", "request_id", "started_ms"
	if system {
		table, key, stamp = "system_logs", "id", "created_ms"
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var count, bytes int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(content_bytes),0) FROM `+table).Scan(&count, &bytes); err != nil {
			return err
		}
		if !system {
			var media int64
			if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(size_bytes),0) FROM usage_assets a WHERE EXISTS(SELECT 1 FROM usage_asset_refs r WHERE r.asset_file=a.asset_file)`).Scan(&media); err != nil {
				return err
			}
			bytes += media
		}
		rows, err := tx.QueryContext(ctx, `SELECT CAST(`+key+` AS TEXT),content_bytes,`+stamp+` FROM `+table+` ORDER BY `+stamp+`,`+key+` LIMIT 500`)
		if err != nil {
			return err
		}
		type candidate struct {
			id           string
			bytes, stamp int64
		}
		var batch []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.bytes, &c.stamp); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, c := range batch {
			switch {
			case policy.CutoffMS > 0 && c.stamp < policy.CutoffMS:
				deleted.ByTTL++
			case policy.MaxRecords > 0 && count > policy.MaxRecords:
				deleted.ByRecords++
			case policy.MaxContentBytes > 0 && bytes > policy.MaxContentBytes:
				deleted.ByContent++
			default:
				return nil
			}
			if !system {
				var freed int64
				if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(a.size_bytes),0) FROM usage_assets a JOIN usage_asset_refs r ON r.asset_file=a.asset_file WHERE r.request_id=? AND NOT EXISTS(SELECT 1 FROM usage_asset_refs other WHERE other.asset_file=a.asset_file AND other.request_id<>?)`, c.id, c.id).Scan(&freed); err != nil {
					return err
				}
				bytes -= freed
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+key+`=?`, c.id); err != nil {
				return err
			}
			count--
			bytes -= c.bytes
		}
		return nil
	})
	if err != nil {
		return LogDeleted{}, err
	}
	return deleted, nil
}
