package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/elysia-api/backend/protocol"
)

var _ protocol.JobRepository = (*Store)(nil)

func (store *Store) migrateGenerationJobs(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS generation_jobs (
		 id TEXT PRIMARY KEY,owner_hash TEXT NOT NULL,deduplication_hash TEXT NOT NULL,
		 phase TEXT NOT NULL,revision INTEGER NOT NULL,next_attempt INTEGER NOT NULL,
		 lease_until INTEGER NOT NULL,job TEXT NOT NULL,result TEXT NOT NULL,
		 UNIQUE(owner_hash,deduplication_hash))`,
		`CREATE INDEX IF NOT EXISTS idx_generation_jobs_due ON generation_jobs(phase,next_attempt,lease_until)`,
		`CREATE TABLE IF NOT EXISTS generation_job_settlements (
		 job_id TEXT PRIMARY KEY,settlement_id TEXT NOT NULL UNIQUE,is_delivered INTEGER NOT NULL DEFAULT 0,
		 FOREIGN KEY(job_id) REFERENCES generation_jobs(id))`,
	} {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) encodeGenerationJob(job protocol.GenerationJob) (string, string, error) {
	result := ""
	if job.Task.Result != nil {
		payload, err := json.Marshal(job.Task.Result)
		if err != nil {
			return "", "", err
		}
		result, err = store.codec.encrypt(string(payload))
		if err != nil {
			return "", "", err
		}
	}
	job.Task.Result = nil
	payload, err := json.Marshal(job)
	if err != nil {
		return "", "", err
	}
	encrypted, err := store.codec.encrypt(string(payload))
	return encrypted, result, err
}

func (store *Store) scanGenerationJob(row interface{ Scan(...any) error }) (protocol.GenerationJob, error) {
	var job protocol.GenerationJob
	var payload, result string
	if err := row.Scan(&payload, &result); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return job, protocol.ErrJobNotFound
		}
		return job, err
	}
	plainJob, err := store.codec.decrypt(payload)
	if err != nil {
		return job, err
	}
	if err := json.Unmarshal([]byte(plainJob), &job); err != nil {
		return job, err
	}
	if result != "" {
		plain, err := store.codec.decrypt(result)
		if err != nil {
			return job, fmt.Errorf("cannot decrypt generation result: %w", err)
		}
		if err := json.Unmarshal([]byte(plain), &job.Task.Result); err != nil {
			return job, err
		}
	}
	return job, nil
}

// CreateGenerationJob reserves the idempotency identity before external I/O.
// Duplicate submissions return the durable existing identity without replacing it.
func (store *Store) CreateGenerationJob(ctx context.Context, job protocol.GenerationJob) (protocol.GenerationJob, bool, error) {
	if err := protocol.CheckNewJob(job); err != nil {
		return job, false, err
	}
	payload, result, err := store.encodeGenerationJob(job)
	if err != nil {
		return job, false, err
	}
	insert, err := store.db.ExecContext(ctx, `INSERT INTO generation_jobs(id,owner_hash,deduplication_hash,phase,revision,next_attempt,lease_until,job,result)
	 VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(owner_hash,deduplication_hash) DO NOTHING`, job.Task.ID, job.OwnerHash, job.DeduplicationHash, job.Phase, job.Revision, job.NextAttempt.UnixMilli(), job.LeaseUntil.UnixMilli(), payload, result)
	if err != nil {
		return job, false, err
	}
	count, err := insert.RowsAffected()
	if err != nil {
		return job, false, err
	}
	if count == 1 {
		return job, true, nil
	}
	existing, err := store.scanGenerationJob(store.db.QueryRowContext(ctx, `SELECT job,result FROM generation_jobs WHERE owner_hash=? AND deduplication_hash=?`, job.OwnerHash, job.DeduplicationHash))
	return existing, false, err
}

// ReadGenerationJob retrieves operational state; authorization is applied by the
// shared coordinator before exposing it through the gateway or management API.
func (store *Store) ReadGenerationJob(ctx context.Context, id string) (protocol.GenerationJob, error) {
	return store.scanGenerationJob(store.db.QueryRowContext(ctx, `SELECT job,result FROM generation_jobs WHERE id=?`, id))
}

func writeGenerationJob(ctx context.Context, tx *sql.Tx, job protocol.GenerationJob, expected int64, payload, result string) error {
	updated, err := tx.ExecContext(ctx, `UPDATE generation_jobs SET phase=?,revision=?,next_attempt=?,lease_until=?,job=?,result=? WHERE id=? AND revision=?`, job.Phase, job.Revision, job.NextAttempt.UnixMilli(), job.LeaseUntil.UnixMilli(), payload, result, job.Task.ID, expected)
	if err != nil {
		return err
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return protocol.ErrJobConflict
	}
	if job.Phase == protocol.JobTerminal {
		_, err = tx.ExecContext(ctx, `INSERT INTO generation_job_settlements(job_id,settlement_id) VALUES(?,?) ON CONFLICT(job_id) DO NOTHING`, job.Task.ID, job.Task.SettlementID)
	}
	return err
}

// UpdateGenerationJob atomically changes the lease/state and appends terminal
// accounting. Delivery uses an outbox because providers cannot join SQLite's tx.
func (store *Store) UpdateGenerationJob(ctx context.Context, job protocol.GenerationJob, expected int64) (protocol.GenerationJob, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
	previous, err := store.scanGenerationJob(tx.QueryRowContext(ctx, `SELECT job,result FROM generation_jobs WHERE id=?`, job.Task.ID))
	if err != nil {
		return job, err
	}
	if previous.Revision != expected {
		return job, protocol.ErrJobConflict
	}
	if err := protocol.CheckJobTransition(previous, job); err != nil {
		return job, err
	}
	job.Revision = expected + 1
	payload, result, err := store.encodeGenerationJob(job)
	if err != nil {
		return job, err
	}
	if err := writeGenerationJob(ctx, tx, job, expected, payload, result); err != nil {
		return job, err
	}
	return job, tx.Commit()
}

// ClaimGenerationJobs leases due work in a transaction. Expired submitting work
// is returned for uncertain-state recovery; it is never considered resubmittable.
func (store *Store) ClaimGenerationJobs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]protocol.GenerationJob, error) {
	if lease <= 0 || limit <= 0 || limit > protocol.DefaultJobBatchSize {
		return nil, fmt.Errorf("invalid generation job claim bounds")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT job,result FROM generation_jobs
	 WHERE phase IN (?,?,?) AND lease_until<=? AND (phase=? OR next_attempt<=?)
	 ORDER BY next_attempt,id LIMIT ?`, protocol.JobSubmitting, protocol.JobPolling, protocol.JobFetchingResult, now.UnixMilli(), protocol.JobSubmitting, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	jobs := []protocol.GenerationJob{}
	for rows.Next() {
		job, err := store.scanGenerationJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		jobs = append(jobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for index := range jobs {
		expected := jobs[index].Revision
		jobs[index].LeaseUntil, jobs[index].Revision = now.Add(lease), expected+1
		payload, result, err := store.encodeGenerationJob(jobs[index])
		if err != nil {
			return nil, err
		}
		if err := writeGenerationJob(ctx, tx, jobs[index], expected, payload, result); err != nil {
			return nil, err
		}
	}
	return jobs, tx.Commit()
}

// PendingJobSettlements returns durable undelivered terminal snapshots.
func (store *Store) PendingJobSettlements(ctx context.Context, limit int) ([]protocol.GenerationJob, error) {
	if limit <= 0 || limit > protocol.DefaultJobBatchSize {
		return nil, fmt.Errorf("invalid settlement batch size")
	}
	rows, err := store.db.QueryContext(ctx, `SELECT j.job,j.result FROM generation_job_settlements s JOIN generation_jobs j ON j.id=s.job_id WHERE s.is_delivered=0 ORDER BY s.job_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []protocol.GenerationJob{}
	for rows.Next() {
		job, err := store.scanGenerationJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// AcknowledgeJobSettlement marks delivery after an idempotent usage write.
func (store *Store) AcknowledgeJobSettlement(ctx context.Context, id string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE generation_job_settlements SET is_delivered=1 WHERE job_id=?`, id)
	return err
}
