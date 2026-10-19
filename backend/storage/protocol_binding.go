package storage

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
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

// BindingKey identifies a contract independently of its protocol and evidence.
type BindingKey struct {
	Kind, SourceID, ModelID, GroupID string
}

// Key returns the same identity used for persistence and inheritance.
func (binding ProtocolBinding) Key() BindingKey {
	return BindingKey{binding.Kind, binding.SourceID, binding.ModelID, binding.GroupID}
}

// Affects includes model contracts which inherit a changed source policy.
func (key BindingKey) Affects(other BindingKey) bool {
	return key == other || (key.Kind == "source" && other.Kind == "model" && key.SourceID == other.SourceID)
}

func (binding ProtocolBinding) key() (string, error) {
	switch binding.Kind {
	case "source":
		if binding.SourceID == "" || binding.ModelID != "" || binding.GroupID != "" {
			return "", fmt.Errorf("source binding requires only sourceId")
		}
	case "model":
		if binding.SourceID == "" || binding.ModelID == "" || binding.GroupID != "" {
			return "", fmt.Errorf("model binding requires sourceId and modelId")
		}
	case "group":
		if binding.GroupID == "" || binding.SourceID != "" || binding.ModelID != "" {
			return "", fmt.Errorf("group binding requires only groupId")
		}
	default:
		return "", fmt.Errorf("unknown protocol binding kind")
	}
	key, err := sonic.ConfigStd.Marshal([]string{binding.Kind, binding.SourceID, binding.ModelID, binding.GroupID})
	return string(key), err
}

// SaveProtocolBinding persists a contract checked by the shared protocol service.
func (store *Store) SaveProtocolBinding(ctx context.Context, binding ProtocolBinding) error {
	return saveProtocolBinding(ctx, store.db, binding)
}

// SaveManagedProtocolBindings commits a changed contract, all affected evidence,
// and its displayed protocol under the same generation check.
func (store *Store) SaveManagedProtocolBindings(ctx context.Context, changed BindingKey, bindings []ProtocolBinding, expectedGeneration ...int64) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkConversionGeneration(ctx, tx, expectedGeneration); err != nil {
		return err
	}
	binding, err := saveBindingUpdates(ctx, tx, changed, bindings)
	if err != nil {
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

func saveBindingUpdates(ctx context.Context, executor protocolSQLExecutor, changed BindingKey, bindings []ProtocolBinding) (ProtocolBinding, error) {
	var primary ProtocolBinding
	seen := make(map[BindingKey]bool, len(bindings))
	for _, binding := range bindings {
		key := binding.Key()
		if !changed.Affects(key) || seen[key] {
			return primary, fmt.Errorf("unrelated or duplicate binding in update")
		}
		seen[key] = true
		if key == changed {
			primary = binding
		}
		if err := saveProtocolBinding(ctx, executor, binding); err != nil {
			return primary, err
		}
	}
	if !seen[changed] {
		return primary, fmt.Errorf("changed binding is missing from update")
	}
	return primary, nil
}

func saveProtocolBinding(ctx context.Context, executor protocolSQLExecutor, binding ProtocolBinding) error {
	key, err := binding.key()
	if err != nil {
		return err
	}
	raw, err := sonic.ConfigStd.Marshal(binding)
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
		if err := sonic.ConfigStd.Unmarshal([]byte(raw), &binding); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}
