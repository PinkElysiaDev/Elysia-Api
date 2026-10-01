package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const logLifecycleVersion = 2026092901

// migrationProgressInterval 是迁移进度日志的最小间隔(时间驱动,比行数
// 驱动在大正文行与快段之间输出更均匀)。
const migrationProgressInterval = 10 * time.Second

// vacuumHeartbeatInterval 是无进度回调的长耗时语句(一次性 VACUUM)的心跳
// 间隔——没有总量可算百分比,心跳只证明仍在推进。
const vacuumHeartbeatInterval = 30 * time.Second

func (s *Store) logLifecycleReady(ctx context.Context) (bool, error) {
	var tables, version int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='schema_migrations'`).Scan(&tables); err != nil {
		return false, err
	}
	if tables == 0 {
		return false, nil
	}
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=?`, logLifecycleVersion).Scan(&version)
	return version != 0, err
}

// Take a consistent compact backup before any schema edits, including old migrations.
func (s *Store) prepareLogLifecycle(ctx context.Context) error {
	var tables int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table'`).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		_, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`)
		return err
	}
	ready, err := s.logLifecycleReady(ctx)
	if err != nil || ready {
		return err
	}
	backup := s.path + ".pre-log-lifecycle"
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp := backup + ".tmp"
	// This filename is owned solely by this migration; an interrupted VACUUM INTO
	// leaves an incomplete file which must never be mistaken for a committed backup.
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	// 备份是整库压实复制,耗时与库大小成正比且无进度回调——先把规模与
	// 「可安全中断」说清楚,避免大库升级时被误判卡死(此时尚未做任何
	// schema 修改,中断只留下 tmp 残留,下次启动自动清理重试)。
	var dbSize, walSize int64
	if info, err := os.Stat(s.path); err == nil {
		dbSize = info.Size()
	}
	if info, err := os.Stat(s.path + "-wal"); err == nil {
		walSize = info.Size()
	}
	log.Printf("[migration] log lifecycle upgrade: backing up database to %s (db %.1f MiB, wal %.1f MiB; duration scales with size — interrupting here is safe and retried)", backup, float64(dbSize)/(1<<20), float64(walSize)/(1<<20))
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("backup log database: %w", err)
	}
	if info, err := os.Stat(tmp); err == nil {
		log.Printf("[migration] backup complete (%.1f MiB)", float64(info.Size())/(1<<20))
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, backup)
}

var assetNamePattern = regexp.MustCompile(`^[0-9a-f]{16}\.[a-z0-9]{1,5}$`)
var assetRefPattern = regexp.MustCompile(`__ELYSIA_ASSET__:[\w-]+/([0-9a-f]{16}\.[a-z0-9]{1,5})`)

// runWithHeartbeat 在无进度回调的长耗时 SQL(一次性 VACUUM)执行期间按
// vacuumHeartbeatInterval 打心跳(带累计时长)——这类语句既无总量可算百分比
// 也无逐行回调,心跳至少让"还在推进"可观测,不再被误判卡死。
func (s *Store) runWithHeartbeat(ctx context.Context, label string, run func() error) error {
	done := make(chan struct{})
	go func() {
		started := time.Now()
		timer := time.NewTimer(vacuumHeartbeatInterval)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-timer.C:
				elapsed := time.Since(started).Round(time.Second)
				log.Printf("[migration] %s still running (%s elapsed)", label, elapsed)
				timer.Reset(vacuumHeartbeatInterval)
			}
		}
	}()
	err := run()
	close(done)
	return err
}

// Schema, content accounting, and attachment references commit together. The final
// marker is written only after the native SQLite format conversion succeeds.
func (s *Store) migrateLogLifecycle(ctx context.Context) error {
	ready, err := s.logLifecycleReady(ctx)
	if err != nil || ready {
		return err
	}
	root := filepath.Join(filepath.Dir(s.path), "usage-assets")
	if err := migrateAssetDirectories(root); err != nil {
		return err
	}
	// 全表回填 + 附件引用重建在同一事务里,大库上耗时且无中间输出——先声明
	// 阶段与代价,并明确「中途停止会整体回滚、下次重来」,防止被误判卡死后
	// 反复中断(每次回滚本身也要付出同等代价)。
	log.Printf("[migration] rebuilding content accounting and asset references in one transaction (full table scan; may take a long while on large databases — stopping midway rolls everything back and restarts from scratch next boot)")
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		for _, col := range []struct{ table, name, definition string }{
			{"usage_records", "content_bytes", "INTEGER NOT NULL DEFAULT 0"},
			{"system_logs", "content_bytes", "INTEGER NOT NULL DEFAULT 0"},
			{"system_logs", "created_ms", "INTEGER NOT NULL DEFAULT 0"},
		} {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				if _, err := tx.ExecContext(ctx, `ALTER TABLE `+col.table+` ADD COLUMN `+col.name+` `+col.definition); err != nil {
					return err
				}
			}
		}
		// usage_records 的 content_bytes 回填挪进下方 keyset 扫描逐批执行——
		// 单条全表 UPDATE 在大库上以小时计且无行级进度;system_logs 通常很小,
		// 保留单条语句。
		for _, stmt := range []string{
			`UPDATE system_logs SET content_bytes=length(CAST(created_at||level||message||fields_json AS BLOB)), created_ms=CAST(round((julianday(created_at)-2440587.5)*86400000) AS INTEGER)`,
			`CREATE INDEX IF NOT EXISTS idx_usage_content ON usage_records(content_bytes)`,
			`CREATE INDEX IF NOT EXISTS idx_system_content ON system_logs(content_bytes)`,
			`CREATE INDEX IF NOT EXISTS idx_system_time ON system_logs(created_ms, id)`,
			`CREATE TABLE IF NOT EXISTS usage_assets (asset_file TEXT PRIMARY KEY, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0))`,
			`DROP INDEX IF EXISTS idx_usage_asset_refs_request`,
			`ALTER TABLE usage_asset_refs RENAME TO log_migration_refs`,
			`CREATE TABLE usage_asset_refs (asset_file TEXT NOT NULL REFERENCES usage_assets(asset_file), request_id TEXT NOT NULL REFERENCES usage_records(request_id) ON DELETE CASCADE, PRIMARY KEY(asset_file, request_id))`,
			`CREATE INDEX idx_usage_asset_refs_request ON usage_asset_refs(request_id)`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		// Keyset batches bound migration memory even with multi-megabyte bodies.
		// 批大小 32:查询往返比 16 减半;批内存上限 = 32 行正文之和(极端多兆
		// 正文时峰值约百余 MB,常规 KB 级记录远低于此)。
		var totalRecords int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM usage_records`).Scan(&totalRecords); err != nil {
			return err
		}
		log.Printf("[migration] content accounting: %d record(s) to scan", totalRecords)
		var last int64
		scanned := 0
		lastProgressAt := time.Time{}
		for {
			rows, err := tx.QueryContext(ctx, `SELECT rowid, request_id, record_json FROM usage_records WHERE rowid>? ORDER BY rowid LIMIT 32`, last)
			if err != nil {
				return err
			}
			type ref struct{ id, file string }
			var refs []ref
			first, batchEnd := int64(0), last
			n := 0
			for rows.Next() {
				var id, body string
				if err := rows.Scan(&last, &id, &body); err != nil {
					rows.Close()
					return err
				}
				if n == 0 {
					first = last
				}
				batchEnd = last
				n++
				for _, match := range assetRefPattern.FindAllStringSubmatch(body, -1) {
					refs = append(refs, ref{id, match[1]})
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			// 批内回填 content_bytes(原单条全表 UPDATE 的分批等价物)。
			if _, err := tx.ExecContext(ctx, `UPDATE usage_records SET content_bytes=length(CAST(record_json AS BLOB)) WHERE rowid>=? AND rowid<=?`, first, batchEnd); err != nil {
				return err
			}
			scanned += n
			if time.Since(lastProgressAt) >= migrationProgressInterval {
				log.Printf("[migration] content accounting: %d/%d (%.1f%%)", scanned, totalRecords, float64(scanned)/float64(totalRecords)*100)
				lastProgressAt = time.Now()
			}
			for _, ref := range refs {
				if err := migrateAssetRef(ctx, tx, root, ref.id, ref.file); err != nil {
					return err
				}
			}
		}
		log.Printf("[migration] content accounting finished (%d record(s))", scanned)
		last = 0
		migratedRefs := 0
		var totalRefs int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM log_migration_refs`).Scan(&totalRefs); err != nil {
			return err
		}
		if totalRefs > 0 {
			log.Printf("[migration] asset reference rebuild: %d reference(s) to process", totalRefs)
		}
		lastProgressAt = time.Time{}
		for {
			rows, err := tx.QueryContext(ctx, `SELECT r.rowid,r.request_id,r.asset_file FROM log_migration_refs r JOIN usage_records u ON u.request_id=r.request_id WHERE r.rowid>? ORDER BY r.rowid LIMIT 500`, last)
			if err != nil {
				return err
			}
			type ref struct{ id, file string }
			var refs []ref
			for rows.Next() {
				var v ref
				if err := rows.Scan(&last, &v.id, &v.file); err != nil {
					rows.Close()
					return err
				}
				refs = append(refs, v)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(refs) == 0 {
				break
			}
			migratedRefs += len(refs)
			if time.Since(lastProgressAt) >= migrationProgressInterval {
				log.Printf("[migration] asset reference rebuild: %d/%d", migratedRefs, totalRefs)
				lastProgressAt = time.Now()
			}
			for _, v := range refs {
				if err := migrateAssetRef(ctx, tx, root, v.id, v.file); err != nil {
					return err
				}
			}
		}
		log.Printf("[migration] asset reference rebuild finished (%d reference(s))", migratedRefs)
		if _, err := tx.ExecContext(ctx, `DROP TABLE log_migration_refs`); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("migrate log content: %w", err)
	}
	var mode int
	if err := s.db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return err
	}
	if mode != 2 {
		log.Printf("[migration] enabling incremental database reclamation (one-time VACUUM, duration scales with database size)")
		if _, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
			return err
		}
		if err := s.runWithHeartbeat(ctx, "database vacuum", func() error {
			_, err := s.db.ExecContext(ctx, `VACUUM`)
			return err
		}); err != nil {
			return err
		}
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return err
	}
	if mode != 2 {
		return fmt.Errorf("incremental reclamation unavailable: auto_vacuum=%d", mode)
	}
	cp, err := s.Checkpoint(ctx, true)
	if err != nil {
		return err
	}
	if cp.Busy {
		return fmt.Errorf("log migration checkpoint blocked by another database user")
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(?,?)`, logLifecycleVersion, nowString())
	if err == nil {
		log.Printf("[migration] log lifecycle upgrade complete")
	}
	return err
}

func migrateAssetDirectories(root string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.IsDir() || !assetNamePattern.MatchString(file.Name()) {
				continue
			}
			src, dst := filepath.Join(dir, file.Name()), filepath.Join(root, file.Name())
			if _, err := os.Lstat(dst); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := copyAssetAtomic(src, dst); err != nil {
				return err
			}
		}
		// Keep original files outside the live asset root as the migration backup.
		backup := root + ".pre-log-lifecycle"
		if err := os.MkdirAll(backup, 0o700); err != nil {
			return err
		}
		if err := os.Rename(dir, filepath.Join(backup, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyAssetAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".asset-migration-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(out.Name(), dst)
}

func migrateAssetRef(ctx context.Context, tx *sql.Tx, root, id, file string) error {
	if !assetNamePattern.MatchString(file) {
		return fmt.Errorf("invalid existing attachment name %q", file)
	}
	info, err := os.Lstat(filepath.Join(root, file))
	if os.IsNotExist(err) {
		log.Printf("[migration] missing usage attachment %s for %s", file, id)
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("attachment is not a regular file: %s", file)
	}
	return saveUsageAssetTx(ctx, tx, id, UsageAsset{File: file, SizeBytes: info.Size()})
}
