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
	key, err := json.Marshal([]string{binding.Kind, binding.SourceID, binding.ModelID, binding.GroupID})
	return string(key), err
}

// SaveProtocolBinding persists a contract checked by the shared protocol service.
func (store *Store) SaveProtocolBinding(ctx context.Context, binding ProtocolBinding) error {
	key, err := binding.key()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO protocol_bindings(binding_key, binding) VALUES(?,?) ON CONFLICT(binding_key) DO UPDATE SET binding=excluded.binding`, key, string(raw))
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
