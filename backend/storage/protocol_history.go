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

type ProtocolHistory struct {
	ID            string         `json:"id"`
	ProtocolID    string         `json:"protocolId"`
	Hash          string         `json:"hash"`
	Name          string         `json:"name"`
	Version       string         `json:"version"`
	Definition    protocol.Value `json:"definition,omitzero"`
	RawDefinition string         `json:"rawDefinition,omitempty"`
	ReadError     string         `json:"readError,omitempty"`
	Reason        string         `json:"reason"`
	IsDraft       bool           `json:"isDraft"`
	CreatedAt     time.Time      `json:"createdAt"`
	ArchivedAt    time.Time      `json:"archivedAt"`
}

func archiveReplacedPreset(ctx context.Context, tx *sql.Tx, id, nextHash string) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO protocol_history(id,protocol_id,content_hash,definition,reason,created_at,archived_at)
		SELECT r.protocol_id || '~' || r.content_hash,r.protocol_id,r.content_hash,r.definition,'preset_replaced',r.created_at,?
		FROM protocol_revisions r JOIN protocol_activations a ON a.protocol_id=r.protocol_id AND a.revision_hash=r.content_hash
		WHERE r.protocol_id=? AND r.content_hash<>?`, nowString(), id, nextHash)
	return err
}

func (s *Store) backfillProtocolHistory(ctx context.Context) error {
	for _, id := range []string{protocol.PresetAnthropicID, protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetGeminiID} {
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO protocol_history(id,protocol_id,content_hash,definition,reason,created_at,archived_at)
		SELECT r.protocol_id || '~' || r.content_hash,r.protocol_id,r.content_hash,r.definition,'preset_replaced',r.created_at,a.activated_at
		FROM protocol_revisions r JOIN protocol_activations a ON a.protocol_id=r.protocol_id
		WHERE r.protocol_id=? AND r.content_hash<>a.revision_hash`, id)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListProtocolHistory(ctx context.Context) ([]ProtocolHistory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,protocol_id,content_hash,definition,reason,is_draft,created_at,archived_at FROM protocol_history ORDER BY archived_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProtocolHistory{}
	for rows.Next() {
		item, err := scanProtocolHistory(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ReadProtocolHistory(ctx context.Context, id string) (ProtocolHistory, error) {
	return scanProtocolHistory(s.db.QueryRowContext(ctx, `SELECT id,protocol_id,content_hash,definition,reason,is_draft,created_at,archived_at FROM protocol_history WHERE id=?`, id))
}

func scanProtocolHistory(row interface{ Scan(...any) error }) (ProtocolHistory, error) {
	var item ProtocolHistory
	var raw, created, archived string
	if err := row.Scan(&item.ID, &item.ProtocolID, &item.Hash, &raw, &item.Reason, &item.IsDraft, &created, &archived); err != nil {
		return item, protocolRowError(err)
	}
	value, err := protocol.ParseValue([]byte(raw))
	var meta struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &meta)
	}
	if err != nil {
		item.RawDefinition, item.ReadError = raw, err.Error()
	} else {
		item.Definition, item.Name, item.Version = value, meta.Name, meta.Version
	}
	if item.Reason == "preset_repaired" || item.Reason == "preset_draft_replaced" {
		item.RawDefinition = raw
	}
	item.CreatedAt, item.ArchivedAt = parseTime(created), parseTime(archived)
	return item, nil
}

// ArchiveProtocol applies the reviewed reference changes and archives all saved
// revisions plus the draft in one transaction. No credentials leave the store.
func (s *Store) ArchiveProtocol(ctx context.Context, id, baseline, replacement string, bindings []ProtocolBinding) error {
	if protocol.IsPresetProtocolID(id) {
		return fmt.Errorf("preset protocols cannot be archived")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actual, err := readProtocolUpgradeBaseline(ctx, tx)
	if err != nil {
		return err
	}
	if baseline == "" || baseline != actual {
		return protocol.ErrRevisionConflict
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM protocol_drafts WHERE protocol_id=?`, id).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return protocol.ErrNotFound
	}
	for _, binding := range bindings {
		if err := saveProtocolBinding(ctx, tx, binding); err != nil {
			return err
		}
	}
	for _, binding := range bindings {
		platform := ""
		if !binding.Unbound {
			platform = "custom:" + binding.Binding.ProtocolID
		}
		if binding.Kind == "source" {
			if _, err := tx.ExecContext(ctx, `UPDATE model_sources SET platform=? WHERE id=?`, platform, binding.SourceID); err != nil {
				return err
			}
		}
		if binding.Kind == "model" {
			if _, err := tx.ExecContext(ctx, `UPDATE models SET platform=? WHERE source_id=? AND id=?`, platform, binding.SourceID, binding.ModelID); err != nil {
				return err
			}
		}
	}
	// The platform column is a compatibility reference; unbound records are
	// explicitly empty and cannot be reinterpreted as a default protocol.
	for _, table := range []string{"model_sources", "models"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET platform=? WHERE platform=? COLLATE NOCASE`, replacement, "custom:"+id); err != nil {
			return err
		}
	}
	if replacement == "" {
		for _, b := range bindings {
			if b.Kind == "source" && b.Unbound {
				if _, err := tx.ExecContext(ctx, `UPDATE model_sources SET auto_fetch_models=0 WHERE id=?`, b.SourceID); err != nil {
					return err
				}
			}
		}
	}
	now := nowString()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO protocol_history(id,protocol_id,content_hash,definition,reason,created_at,archived_at)
		SELECT protocol_id || '~' || content_hash,protocol_id,content_hash,definition,'custom_deleted',created_at,? FROM protocol_revisions WHERE protocol_id=?`, now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO protocol_history(id,protocol_id,content_hash,definition,reason,is_draft,created_at,archived_at)
		SELECT protocol_id || '~' || content_hash,protocol_id,content_hash,definition,'custom_deleted',1,updated_at,? FROM protocol_drafts WHERE protocol_id=?
		ON CONFLICT(id) DO UPDATE SET is_draft=1`, now, id); err != nil {
		return err
	}
	for _, table := range []string{"protocol_activations", "protocol_drafts"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE protocol_id=?`, id); err != nil {
			return err
		}
	}
	// A retained legacy row must not resurrect an archived protocol on migration.
	if _, err := tx.ExecContext(ctx, `DELETE FROM custom_protocols WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

type ProtocolReference struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	SourceID string `json:"sourceId,omitempty"`
	Name     string `json:"name,omitempty"`
}

func (s *Store) protocolRevisionReferences(ctx context.Context, tx *sql.Tx, id, hash string) ([]ProtocolReference, error) {
	items := []ProtocolReference{}
	for _, q := range []struct{ kind, query string }{
		{"active", `SELECT protocol_id FROM protocol_activations WHERE protocol_id=? AND revision_hash=?`},
		{"draft", `SELECT protocol_id FROM protocol_drafts WHERE protocol_id=? AND content_hash=?`},
	} {
		var found string
		err := tx.QueryRowContext(ctx, q.query, id, hash).Scan(&found)
		if err == nil {
			items = append(items, ProtocolReference{Kind: q.kind, ID: found})
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT binding FROM protocol_bindings`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var b ProtocolBinding
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			rows.Close()
			return nil, err
		}
		if !b.Unbound && b.Binding.ProtocolID == id && b.Binding.RevisionHash == hash {
			items = append(items, ProtocolReference{Kind: b.Kind, ID: b.ModelID + b.GroupID, SourceID: b.SourceID})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT job,result FROM generation_jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		job, err := s.scanGenerationJob(rows)
		if err != nil {
			return nil, err
		}
		if (job.IngressID == id && job.IngressRevision == hash) || (job.Binding.ProtocolID == id && job.UpstreamRevision == hash) {
			items = append(items, ProtocolReference{Kind: "job", ID: job.Task.ID})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// Editor sessions retain a protocol ID rather than a revision hash. Preserve
	// its history until that session reference is removed by the operator.
	sessions, err := tx.QueryContext(ctx, `SELECT id,title FROM agent_sessions WHERE protocol_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer sessions.Close()
	for sessions.Next() {
		ref := ProtocolReference{Kind: "session"}
		if err := sessions.Scan(&ref.ID, &ref.Name); err != nil {
			return nil, err
		}
		items = append(items, ref)
	}
	return items, sessions.Err()
}

func (s *Store) ProtocolRevisionReferences(ctx context.Context, id, hash string) ([]ProtocolReference, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return s.protocolRevisionReferences(ctx, tx, id, hash)
}

func (s *Store) DeleteProtocolHistory(ctx context.Context, archiveID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, hash string
	if err := tx.QueryRowContext(ctx, `SELECT protocol_id,content_hash FROM protocol_history WHERE id=?`, archiveID).Scan(&id, &hash); errors.Is(err, sql.ErrNoRows) {
		return protocol.ErrNotFound
	} else if err != nil {
		return err
	}
	// 预置只读不变量的兜底：preset_replaced 历史行随引擎更新保留，不可物理删除。
	if protocol.IsPresetProtocolID(id) {
		return protocol.ErrRevisionConflict
	}
	refs, err := s.protocolRevisionReferences(ctx, tx, id, hash)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		return protocol.ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM protocol_verification_reports WHERE protocol_id=? AND revision_hash=?`, id, hash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM protocol_revisions WHERE protocol_id=? AND content_hash=?`, id, hash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM protocol_history WHERE id=?`, archiveID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) EnableModelFunctionTools(ctx context.Context, sourceID, modelID, baseline string, binding ProtocolBinding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actual, err := readProtocolUpgradeBaseline(ctx, tx)
	if err != nil {
		return err
	}
	if baseline == "" || actual != baseline {
		return protocol.ErrRevisionConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE models SET tools_capable=1,capability_source='manual' WHERE source_id=? AND id=?`, sourceID, modelID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return protocol.ErrNotFound
	}
	if err := saveProtocolBinding(ctx, tx, binding); err != nil {
		return err
	}
	return tx.Commit()
}
