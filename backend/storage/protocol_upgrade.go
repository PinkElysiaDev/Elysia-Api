package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elysia-api/backend/protocol"
)

const protocolUpgradeSetting = "protocol_engine_v2_migration"

// ProtocolUpgradeRevision is a verified replacement prepared without writes.
// The original legacy row remains available for inspection and backup recovery.
type ProtocolUpgradeRevision struct {
	Revision protocol.Revision           `json:"revision"`
	Report   protocol.VerificationReport `json:"report"`
	Draft    protocol.Draft              `json:"draft"`
}

// ProtocolUpgrade applies the whole validated graph, never individual rows.
// Baseline binds the preview to configuration, drafts, activations and bindings.
type ProtocolUpgrade struct {
	Baseline    string                    `json:"baseline"`
	RequestHash string                    `json:"requestHash,omitempty"`
	Revisions   []ProtocolUpgradeRevision `json:"revisions"`
	Bindings    []ProtocolBinding         `json:"bindings"`
}

// ProtocolUpgradeReceipt identifies the committed plan and its database backup.
type ProtocolUpgradeReceipt struct {
	PlanHash        string    `json:"planHash"`
	Baseline        string    `json:"baseline"`
	RequestHash     string    `json:"requestHash,omitempty"`
	CompilerVersion string    `json:"compilerVersion"`
	Backup          string    `json:"backup"`
	CompletedAt     time.Time `json:"completedAt"`
}

type protocolUpgradeReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// The fingerprint includes credential ciphertext, never plaintext or a public
// snapshot. Usage/log writes deliberately do not invalidate a configuration plan.
var protocolUpgradeQueries = []string{
	`SELECT * FROM custom_protocols ORDER BY id`,
	`SELECT * FROM model_sources ORDER BY id`,
	`SELECT * FROM models ORDER BY source_id,id`,
	`SELECT * FROM model_groups ORDER BY id`,
	`SELECT * FROM model_group_models ORDER BY group_id,position,source_id,model_id`,
	`SELECT * FROM protocol_drafts ORDER BY protocol_id`,
	`SELECT * FROM protocol_activations ORDER BY protocol_id`,
	`SELECT * FROM protocol_bindings ORDER BY binding_key`,
}

// ProtocolUpgradeBaseline returns an opaque hash for a migration preview.
func (store *Store) ProtocolUpgradeBaseline(ctx context.Context) (string, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	return readProtocolUpgradeBaseline(ctx, tx)
}

func readProtocolUpgradeBaseline(ctx context.Context, reader protocolUpgradeReader) (string, error) {
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for _, query := range protocolUpgradeQueries {
		rows, err := reader.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		err = hashProtocolUpgradeRows(rows, encoder, query)
		closeErr := rows.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hashProtocolUpgradeRows(rows *sql.Rows, encoder *json.Encoder, query string) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	if err := encoder.Encode([]any{query, columns}); err != nil {
		return err
	}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		if err := encoder.Encode(values); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ProtocolUpgradeStatus reports only a committed migration, never a preview.
func (store *Store) ProtocolUpgradeStatus(ctx context.Context) (*ProtocolUpgradeReceipt, error) {
	var raw string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, protocolUpgradeSetting).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipt ProtocolUpgradeReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// ApplyProtocolUpgrade commits the prepared revisions and routing graph in one
// transaction. Stale previews and partial/failed evidence cannot change live state.
func (store *Store) ApplyProtocolUpgrade(ctx context.Context, plan ProtocolUpgrade) (*ProtocolUpgradeReceipt, error) {
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	planHash := hex.EncodeToString(digest[:])
	previous, err := store.ProtocolUpgradeStatus(ctx)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		isSameRequest := plan.RequestHash != "" && previous.RequestHash == plan.RequestHash && previous.Baseline == plan.Baseline
		if previous.PlanHash != planHash && !isSameRequest {
			return nil, protocol.ErrRevisionConflict
		}
		return previous, nil
	}
	if len(plan.Revisions) == 0 || len(plan.Baseline) != hex.EncodedLen(sha256.Size) {
		return nil, fmt.Errorf("protocol upgrade requires a complete verified plan and baseline")
	}
	if err := validateProtocolUpgrade(plan); err != nil {
		return nil, err
	}
	baseline, err := store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return nil, err
	}
	if baseline != plan.Baseline {
		return nil, protocol.ErrRevisionConflict
	}
	backup, err := store.selectProtocolUpgradeBackup(ctx, baseline)
	if err != nil {
		return nil, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	baseline, err = readProtocolUpgradeBaseline(ctx, tx)
	if err != nil {
		return nil, err
	}
	if baseline != plan.Baseline {
		return nil, protocol.ErrRevisionConflict
	}
	for _, entry := range plan.Revisions {
		if err := writeProtocolUpgradeRevision(ctx, tx, entry); err != nil {
			return nil, err
		}
	}
	for _, binding := range plan.Bindings {
		if err := saveProtocolBinding(ctx, tx, binding); err != nil {
			return nil, err
		}
	}
	receipt := &ProtocolUpgradeReceipt{PlanHash: planHash, Baseline: plan.Baseline, RequestHash: plan.RequestHash, CompilerVersion: protocol.CompilerVersion, Backup: backup, CompletedAt: time.Now().UTC()}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)`, protocolUpgradeSetting, string(encoded), nowString()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return receipt, nil
}

func validateProtocolUpgrade(plan ProtocolUpgrade) error {
	revisions := map[string]string{}
	for _, entry := range plan.Revisions {
		revision, report := entry.Revision, entry.Report
		if revision.ProtocolID == "" || revision.Hash == "" || revisions[revision.ProtocolID] != "" || entry.Draft.ProtocolID != revision.ProtocolID || entry.Draft.Hash == "" {
			return fmt.Errorf("invalid or duplicated protocol upgrade identity")
		}
		if !report.Passed || report.Kind != protocol.OfflineVerification || report.CompilerVersion != protocol.CompilerVersion || report.DefinitionHash != revision.Hash || report.SamplesHash == "" || len(report.Checks) == 0 || protocol.IssuesError(report.Issues) != nil {
			return fmt.Errorf("protocol %s lacks current passing offline evidence", revision.ProtocolID)
		}
		revisions[revision.ProtocolID] = revision.Hash
	}
	for _, entry := range plan.Bindings {
		if revisions[entry.Binding.ProtocolID] != entry.Binding.RevisionHash || entry.Binding.RevisionHash == "" {
			return fmt.Errorf("binding references a protocol outside the verified upgrade")
		}
	}
	return nil
}

func writeProtocolUpgradeRevision(ctx context.Context, tx *sql.Tx, entry ProtocolUpgradeRevision) error {
	revision, draft := entry.Revision, entry.Draft
	if _, err := tx.ExecContext(ctx, `INSERT INTO protocol_drafts(protocol_id,content_hash,definition,updated_at) VALUES(?,?,?,?) ON CONFLICT(protocol_id) DO UPDATE SET content_hash=excluded.content_hash,definition=excluded.definition,updated_at=excluded.updated_at`, draft.ProtocolID, draft.Hash, string(draft.Definition.Bytes()), draft.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := saveProtocolRevision(ctx, tx, revision); err != nil {
		return err
	}
	if err := saveProtocolReport(ctx, tx, revision.ProtocolID, revision.Hash, entry.Report); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO protocol_activations(protocol_id,revision_hash,generation,activated_at) VALUES(?,?,1,?) ON CONFLICT(protocol_id) DO UPDATE SET revision_hash=excluded.revision_hash,generation=protocol_activations.generation+1,activated_at=excluded.activated_at`, revision.ProtocolID, revision.Hash, nowString())
	return err
}

func (store *Store) backupProtocolUpgrade(ctx context.Context, baseline string) (string, error) {
	path := store.path + ".pre-protocol-v2-" + baseline
	if _, err := os.Stat(path); err == nil {
		return path, checkProtocolUpgradeBackup(ctx, path, baseline)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(store.path), ".protocol-v2-backup-*.sqlite")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	defer os.Remove(temporary)
	if _, err := store.db.ExecContext(ctx, `VACUUM INTO ?`, temporary); err != nil {
		return "", err
	}
	if err := checkProtocolUpgradeBackup(ctx, temporary, baseline); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, path); err != nil {
		return "", err
	}
	return path, nil
}

func checkProtocolUpgradeBackup(ctx context.Context, path, baseline string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer database.Close()
	actual, err := readProtocolUpgradeBaseline(ctx, database)
	if err != nil {
		return err
	}
	if actual != baseline {
		return protocol.ErrRevisionConflict
	}
	return nil
}
