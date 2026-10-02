package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/elysia-api/backend/protocol"
)

const protocolUpgradeBackupSetting = "protocol_engine_v2_backup"

type protocolUpgradeBackup struct {
	Path     string `json:"path"`
	Baseline string `json:"baseline"`
}

// PrepareProtocolUpgradeBackup captures configuration before historical startup
// imports or preset upgrades. Repeated starts retain the first valid backup.
func (store *Store) PrepareProtocolUpgradeBackup(ctx context.Context) error {
	if receipt, err := store.ProtocolUpgradeStatus(ctx); err != nil || receipt != nil {
		return err
	}
	if backup, err := store.readProtocolUpgradeBackup(ctx); err != nil || backup != nil {
		return err
	}
	baseline, err := store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return err
	}
	path, err := store.backupProtocolUpgrade(ctx, baseline)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(protocolUpgradeBackup{Path: path, Baseline: baseline})
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, protocolUpgradeBackupSetting, string(encoded), nowString())
	return err
}

func (store *Store) readProtocolUpgradeBackup(ctx context.Context) (*protocolUpgradeBackup, error) {
	var raw string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, protocolUpgradeBackupSetting).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var backup protocolUpgradeBackup
	if err := json.Unmarshal([]byte(raw), &backup); err != nil {
		return nil, err
	}
	if backup.Path == "" || backup.Baseline == "" {
		return nil, protocol.ErrRevisionConflict
	}
	if err := checkProtocolUpgradeBackup(ctx, backup.Path, backup.Baseline); err != nil {
		return nil, err
	}
	return &backup, nil
}

func (store *Store) selectProtocolUpgradeBackup(ctx context.Context, baseline string) (string, error) {
	backup, err := store.readProtocolUpgradeBackup(ctx)
	if err != nil {
		return "", err
	}
	if backup != nil {
		return backup.Path, nil
	}
	return store.backupProtocolUpgrade(ctx, baseline)
}
