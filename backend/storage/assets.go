// Attachment metadata belongs to the same transaction as its request record.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
)

type UsageAsset struct {
	File       string `json:"file"`
	SizeBytes  int64  `json:"sizeBytes"`
	Referenced bool   `json:"referenced"`
}

func saveUsageAssetTx(ctx context.Context, tx *sql.Tx, requestID string, a UsageAsset) error {
	if !assetNamePattern.MatchString(a.File) || a.SizeBytes < 0 {
		return fmt.Errorf("invalid usage asset: %q", a.File)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_assets(asset_file,size_bytes) VALUES(?,?) ON CONFLICT(asset_file) DO UPDATE SET size_bytes=excluded.size_bytes`, a.File, a.SizeBytes); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO usage_asset_refs(asset_file,request_id) VALUES(?,?)`, a.File, requestID)
	return err
}

func (s *Store) UsageAssets(ctx context.Context) ([]UsageAsset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT asset_file,size_bytes,EXISTS(SELECT 1 FROM usage_asset_refs r WHERE r.asset_file=a.asset_file) FROM usage_assets a`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var assets []UsageAsset
	for rows.Next() {
		var a UsageAsset
		if err := rows.Scan(&a.File, &a.SizeBytes, &a.Referenced); err != nil {
			return nil, err
		}
		assets = append(assets, a)
	}
	return assets, rows.Err()
}

// The caller holds the same file-lifecycle lock as the writer until deletion and
// ForgetUnusedAsset have both completed. A failed unlink leaves metadata for retry.
func (s *Store) ForgetUnusedAsset(ctx context.Context, file string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM usage_assets WHERE asset_file=? AND NOT EXISTS(SELECT 1 FROM usage_asset_refs r WHERE r.asset_file=usage_assets.asset_file)`, file)
	return err
}

func (s *Store) ReferencedAssetFiles(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT asset_file FROM usage_asset_refs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		files[name] = true
	}
	return files, rows.Err()
}

func (s *Store) UsageAssetsRoot() string { return filepath.Join(filepath.Dir(s.path), "usage-assets") }
func (s *Store) HasUsageAsset(ctx context.Context, requestID, file string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM usage_asset_refs WHERE request_id=? AND asset_file=?)`, requestID, file).Scan(&exists)
	return exists, err
}
