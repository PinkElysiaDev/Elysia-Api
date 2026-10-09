package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// RefreshProtocolRuntime atomically refreshes active definitions and bindings
// after an engine upgrade. It preserves the original migration receipt and
// writes a new database backup before changing the executable graph.
func (store *Store) RefreshProtocolRuntime(ctx context.Context, plan ProtocolUpgrade) error {
	if err := validateProtocolUpgrade(plan); err != nil {
		return err
	}
	if len(plan.Revisions) == 0 {
		return fmt.Errorf("runtime refresh requires active revisions")
	}
	backup, err := store.backupProtocolUpgrade(ctx, plan.Baseline)
	if err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	baseline, err := readProtocolUpgradeBaseline(ctx, tx)
	if err != nil {
		return err
	}
	if baseline != plan.Baseline {
		return protocol.ErrRevisionConflict
	}
	for _, entry := range plan.Revisions {
		if err := writeProtocolUpgradeRevision(ctx, tx, entry); err != nil {
			return err
		}
	}
	for _, binding := range plan.Bindings {
		if err := saveProtocolBinding(ctx, tx, binding); err != nil {
			return err
		}
	}
	for _, rejection := range plan.Rejections {
		report := rejection.Report
		if protocol.IsPresetProtocolID(rejection.ProtocolID) || report.Passed || report.Kind != protocol.OfflineVerification || report.CompilerVersion != protocol.CompilerVersion || protocol.IssuesError(report.Issues) == nil {
			return fmt.Errorf("invalid isolated custom verification report")
		}
		var active string
		if err := tx.QueryRowContext(ctx, `SELECT revision_hash FROM protocol_activations WHERE protocol_id=?`, rejection.ProtocolID).Scan(&active); err != nil {
			return err
		}
		if active != report.DefinitionHash {
			return protocol.ErrRevisionConflict
		}
		if err := saveProtocolReport(ctx, tx, rejection.ProtocolID, active, report); err != nil {
			return err
		}
	}
	receipt, err := json.Marshal(map[string]string{"baseline": plan.Baseline, "backup": backup, "compilerVersion": protocol.CompilerVersion})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, "protocol_runtime_refresh", string(receipt), nowString()); err != nil {
		return err
	}
	return tx.Commit()
}
