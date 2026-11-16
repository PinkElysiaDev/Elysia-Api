package storage

import (
	"context"
	"database/sql"
)

// commitConversion publishes the trigger-owned generation only after commit
// succeeds. Reading inside the transaction also covers no-ops, bulk writes and
// statement/savepoint rollbacks without duplicating SQLite trigger semantics.
func (s *Store) commitConversion(ctx context.Context, tx *sql.Tx) error {
	var generation int64
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM conversion_generation WHERE id=1").Scan(&generation); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishConversionGeneration(generation)
	return nil
}

func (s *Store) withConversion(ctx context.Context, write func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := write(tx); err != nil {
		return err
	}
	return s.commitConversion(ctx, tx)
}

func (s *Store) execConversion(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := s.withConversion(ctx, func(tx *sql.Tx) (err error) {
		result, err = tx.ExecContext(ctx, query, args...)
		return err
	})
	return result, err
}

// Concurrent commits or snapshot readers must never publish an older counter.
func (s *Store) publishConversionGeneration(generation int64) {
	for {
		current := s.generation.Load()
		if generation <= current || s.generation.CompareAndSwap(current, generation) {
			return
		}
	}
}

// CachedConversionGeneration reads the last committed conversion generation
// without SQL. All writes to trigger-covered tables use commitConversion.
func (s *Store) CachedConversionGeneration() int64 {
	return s.generation.Load()
}
