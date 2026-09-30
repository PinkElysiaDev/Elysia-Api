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
)

const logLifecycleVersion = 2026092901

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
	log.Printf("[migration] backing up log database to %s", backup)
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("backup log database: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, backup)
}

var assetNamePattern = regexp.MustCompile(`^[0-9a-f]{16}\.[a-z0-9]{1,5}$`)
var assetRefPattern = regexp.MustCompile(`__ELYSIA_ASSET__:[\w-]+/([0-9a-f]{16}\.[a-z0-9]{1,5})`)

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
		for _, stmt := range []string{
			`UPDATE usage_records SET content_bytes=length(CAST(record_json AS BLOB))`,
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
		var last int64
		for {
			rows, err := tx.QueryContext(ctx, `SELECT rowid, request_id, record_json FROM usage_records WHERE rowid>? ORDER BY rowid LIMIT 16`, last)
			if err != nil {
				return err
			}
			type ref struct{ id, file string }
			var refs []ref
			n := 0
			for rows.Next() {
				var id, body string
				if err := rows.Scan(&last, &id, &body); err != nil {
					rows.Close()
					return err
				}
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
			for _, ref := range refs {
				if err := migrateAssetRef(ctx, tx, root, ref.id, ref.file); err != nil {
					return err
				}
			}
		}
		last = 0
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
			for _, v := range refs {
				if err := migrateAssetRef(ctx, tx, root, v.id, v.file); err != nil {
					return err
				}
			}
		}
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
		log.Printf("[migration] enabling incremental database reclamation")
		if _, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
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
