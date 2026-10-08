package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// ProtocolBinding stores model/source contracts separately from discovered
// model metadata, so a catalog refresh cannot overwrite an operator's binding.
type ProtocolBinding struct {
	Conversion *protocol.ConversionSelection `json:"conversion,omitempty"`
	// Unbound is an explicit operator decision, not an absent inherited binding.
	Unbound      bool                         `json:"unbound,omitempty"`
	Kind         string                       `json:"kind"`
	SourceID     string                       `json:"sourceId"`
	ModelID      string                       `json:"modelId"`
	GroupID      string                       `json:"groupId"`
	Binding      protocol.Binding             `json:"binding"`
	Combinations []protocol.CombinationReport `json:"combinations,omitempty"`
}

func (binding ProtocolBinding) key() (string, error) {
	switch binding.Kind {
	case "source":
		if binding.SourceID == "" || binding.ModelID != "" || binding.GroupID != "" {
			return "", fmt.Errorf("source binding requires only sourceId")
		}
	case "model":
		// Historical manually configured models have no source. The pair still
		// identifies them unambiguously and uses the same lookup as routing.
		if binding.ModelID == "" || binding.GroupID != "" {
			return "", fmt.Errorf("model binding requires modelId and an optional sourceId")
		}
	case "group":
		if binding.GroupID == "" || binding.SourceID != "" || binding.ModelID != "" {
			return "", fmt.Errorf("group binding requires only groupId")
		}
	default:
		return "", fmt.Errorf("unknown protocol binding kind")
	}
	key, err := json.Marshal([]string{binding.Kind, binding.SourceID, binding.ModelID, binding.GroupID})
	return string(key), err
}

// SaveProtocolBinding persists a contract checked by the shared protocol service.
func (store *Store) SaveProtocolBinding(ctx context.Context, binding ProtocolBinding) error {
	return saveProtocolBinding(ctx, store.db, binding)
}

// SaveManagedProtocolBinding keeps the displayed protocol and explicit contract
// consistent when an operator binds or clears an individual source/model.
func (store *Store) SaveManagedProtocolBinding(ctx context.Context, binding ProtocolBinding, expectedGeneration ...int64) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkConversionGeneration(ctx, tx, expectedGeneration); err != nil {
		return err
	}
	if err := saveProtocolBinding(ctx, tx, binding); err != nil {
		return err
	}
	platform := ""
	if !binding.Unbound {
		platform = "custom:" + binding.Binding.ProtocolID
	}
	if binding.Kind == "model" {
		_, err = tx.ExecContext(ctx, `UPDATE models SET platform=? WHERE source_id=? AND id=?`, platform, binding.SourceID, binding.ModelID)
	} else if binding.Kind == "source" {
		_, err = tx.ExecContext(ctx, `UPDATE model_sources SET platform=?,auto_fetch_models=CASE WHEN ?='' THEN 0 ELSE auto_fetch_models END WHERE id=?`, platform, platform, binding.SourceID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func saveProtocolBinding(ctx context.Context, executor protocolSQLExecutor, binding ProtocolBinding) error {
	key, err := binding.key()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, `INSERT INTO protocol_bindings(binding_key, binding) VALUES(?,?) ON CONFLICT(binding_key) DO UPDATE SET binding=excluded.binding`, key, string(raw))
	return err
}

// ListProtocolBindings loads a single immutable routing snapshot per request.
func (store *Store) ListProtocolBindings(ctx context.Context) ([]ProtocolBinding, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT binding FROM protocol_bindings ORDER BY binding_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []ProtocolBinding{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var binding ProtocolBinding
		if err := json.Unmarshal([]byte(raw), &binding); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}
