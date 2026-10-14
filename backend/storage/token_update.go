package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var (
	ErrTokenExists   = errors.New("API token name already exists")
	ErrTokenNotFound = errors.New("API token not found")
	ErrTokenScope    = errors.New("remote access tokens must be managed in runtime configuration")
)

// APITokenUpdate preserves omitted fields. Empty groups/scopes explicitly clear them.
type APITokenUpdate struct {
	NewName, Token        string
	Enabled               *bool
	AllowedGroups, Scopes []string
	RequireInferenceKey   bool
}

// SaveAPIToken commits creation or a partial update, including rename, atomically.
func (s *Store) SaveAPIToken(ctx context.Context, name string, update APITokenUpdate, isCreate bool) (APIToken, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APIToken{}, err
	}
	defer tx.Rollback()
	item, err := s.scanAPIToken(tx.QueryRowContext(ctx, `SELECT name, token, enabled, allowed_groups_json, scopes, created_at, updated_at FROM api_tokens WHERE name=?`, name))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, err
	}
	if isCreate {
		if err == nil {
			return APIToken{}, ErrTokenExists
		}
		item = APIToken{Name: name, Enabled: true}
	} else {
		if errors.Is(err, sql.ErrNoRows) {
			return APIToken{}, ErrTokenNotFound
		}
		if update.RequireInferenceKey && item.HasScope(TokenScopeAgent) {
			return APIToken{}, ErrTokenScope
		}
	}
	if token := strings.TrimSpace(update.Token); token != "" {
		item.Token = token
	}
	if item.Token == "" {
		return APIToken{}, errors.New("token must not be empty")
	}
	if update.Enabled != nil {
		item.Enabled = *update.Enabled
	}
	if update.AllowedGroups != nil {
		item.AllowedGroups = update.AllowedGroups
	}
	if update.Scopes != nil {
		item.Scopes = update.Scopes
	}
	if newName := strings.TrimSpace(update.NewName); newName != "" && newName != name {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM api_tokens WHERE name=?`, newName).Scan(&exists)
		if err == nil {
			return APIToken{}, ErrTokenExists
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return APIToken{}, err
		}
		if !isCreate {
			if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET name=? WHERE name=?`, newName, name); err != nil {
				return APIToken{}, err
			}
		}
		item.Name = newName
	}
	if err := s.writeAPIToken(ctx, tx, item, !isCreate); err != nil {
		return APIToken{}, err
	}
	item, err = s.scanAPIToken(tx.QueryRowContext(ctx, `SELECT name, token, enabled, allowed_groups_json, scopes, created_at, updated_at FROM api_tokens WHERE name=?`, item.Name))
	if err != nil {
		return APIToken{}, err
	}
	return item, tx.Commit()
}
