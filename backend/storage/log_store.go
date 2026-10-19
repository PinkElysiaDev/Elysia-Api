package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const logSchemaVersion = 1

func DefaultLogPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + "-logs" + ext
}

// Resolve symlinked parents as well as existing files, before either database
// is opened. SameFile also catches hard links.
func ValidateDatabasePaths(business, logs string) error {
	canonical := func(path string) (string, error) {
		if strings.TrimSpace(path) == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
			return "", errors.New("database paths must be filesystem paths")
		}
		path, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		var tail []string
		for {
			resolved, err := filepath.EvalSymlinks(path)
			if err == nil {
				for i := len(tail) - 1; i >= 0; i-- {
					resolved = filepath.Join(resolved, tail[i])
				}
				return resolved, nil
			}
			if !os.IsNotExist(err) {
				return "", err
			}
			tail = append(tail, filepath.Base(path))
			path = filepath.Dir(path)
		}
	}
	a, err := canonical(business)
	if err != nil {
		return err
	}
	b, err := canonical(logs)
	if err != nil {
		return err
	}
	x, xe := os.Stat(a)
	y, ye := os.Stat(b)
	if a == b || (xe == nil && ye == nil && os.SameFile(x, y)) {
		return errors.New("business and log database must be different files")
	}
	return nil
}

func OpenLogStore(path string) (*LogStore, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil, errors.New("log database path must be a filesystem path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn, err := logDatabaseDSN(path, false)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	s := &LogStore{db: db, path: path, rollupCtx: ctx, rollupCancel: cancel}
	if err := s.init(ctx); err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	// Driver DSN pragmas run on every newly opened connection, not just the
	// first connection borrowed from database/sql. Readers never own write locks.
	dsn, err = logDatabaseDSN(path, true)
	if err == nil {
		s.readDB, err = sql.Open("sqlite", dsn)
		if err == nil {
			s.readDB.SetMaxOpenConns(4)
			s.readDB.SetMaxIdleConns(4)
			err = s.readDB.PingContext(ctx)
		}
	}
	if err != nil {
		cancel()
		if s.readDB != nil {
			s.readDB.Close()
		}
		db.Close()
		return nil, err
	}
	return s, nil
}

func logDatabaseDSN(path string, readOnly bool) (string, error) {
	q := url.Values{}
	if readOnly {
		q.Set("mode", "ro")
		q.Set("_query_only", "on")
		q.Add("_pragma", "cache_size(-8192)")
	} else {
		q.Set("_synchronous", "FULL")
		q.Add("_pragma", "cache_size(-32768)")
	}
	return sqliteDatabaseDSN(path, q)
}

func (s *LogStore) init(ctx context.Context) error {
	var version, tables int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		return err
	}
	var businessTables int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name IN ('settings','model_sources','api_tokens')").Scan(&businessTables); err != nil {
		return err
	}
	if businessTables > 0 || (version != logSchemaVersion && (version != 0 || tables != 0)) {
		return fmt.Errorf("unsupported log database schema %d; use a new log database", version)
	}
	// Database-wide settings live here; connection-local settings live in the DSN.
	for _, stmt := range []string{"PRAGMA auto_vacuum=INCREMENTAL", "PRAGMA journal_mode=WAL"} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := createDataSchema(ctx, s.db, true); err != nil {
		return err
	}
	if err := s.initRollupState(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", logSchemaVersion))
	return err
}

func (s *LogStore) Path() string                   { return s.path }
func (s *LogStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *LogStore) Close() error {
	s.closeOnce.Do(func() {
		s.StopBackground()
		readErr := s.readDB.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// FULL synchronous commits are durable in WAL even if another reader
		// prevents truncation. Checkpoint is for orderly file reclamation.
		_, err := s.Checkpoint(ctx, true)
		s.closeErr = errors.Join(readErr, err, s.db.Close())
	})
	return s.closeErr
}

func (s *LogStore) StopBackground() {
	s.rollupCancel()
	s.rollupWG.Wait()
}

func (s *LogStore) ExecRaw(ctx context.Context, query string) error {
	_, err := s.db.ExecContext(ctx, query)
	return err
}
