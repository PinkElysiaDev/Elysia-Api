package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// RefreshProtocolRuntime atomically refreshes active definitions and bindings
// after an engine upgrade. It preserves the original migration receipt and
// snapshots only changes that overwrite existing data.
func (store *Store) RefreshProtocolRuntime(ctx context.Context, plan ProtocolUpgrade) error {
	if err := validateProtocolUpgrade(plan); err != nil {
		return err
	}
	if len(plan.Revisions) == 0 {
		return fmt.Errorf("runtime refresh requires active revisions")
	}
	prepared, snapshot, err := store.prepareProtocolCommit(ctx, plan)
	if err != nil {
		return err
	}
	changed := len(prepared.plan.Bindings) > 0 || len(prepared.plan.Rejections) > 0
	for _, entry := range prepared.plan.Revisions {
		changed = changed || entry.Refresh.Changed()
	}
	if !changed {
		return nil
	}
	tx, err := store.beginProtocolCommit(ctx, prepared)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeProtocolCommit(ctx, tx, prepared); err != nil {
		return err
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
	mode, path := "not_required", ""
	if snapshot != nil {
		mode, path = "current_snapshot", snapshot.Path
	}
	receipt, err := json.Marshal(map[string]any{"baseline": plan.Baseline, "backup": path, "backupMode": mode, "snapshot": snapshot, "compilerVersion": protocol.CompilerVersion})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, "protocol_runtime_refresh", string(receipt), nowString()); err != nil {
		return err
	}
	return tx.Commit()
}
