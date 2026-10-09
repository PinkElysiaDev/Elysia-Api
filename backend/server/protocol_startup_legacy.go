package server

import (
	"context"
	"encoding/json"
	"fmt"
)

// Existing legacy rows may need repair, but missing presets are never seeded
// into the legacy registry. The executable definitions have already recovered.
func (s *Server) prepareLegacyProtocolState(ctx context.Context) error {
	if err := s.importLegacyConfig(); err != nil {
		return err
	}
	if err := s.migrateLegacyCustomProtocols(); err != nil {
		return err
	}
	if _, err := s.store.MigratePresetProtocolRenames(ctx, presetProtocolRenames); err != nil {
		return err
	}
	if _, err := s.store.ReconcileCustomProtocolConfigIDs(ctx); err != nil {
		return err
	}
	if _, err := s.store.StripGeminiModelIDPrefixes(ctx); err != nil {
		return err
	}
	rows, err := s.store.ListCustomProtocols(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !matchesAnyPresetHash(row.Config, legacyPresetHashes[row.ID]) {
			continue
		}
		preset, ok := findPresetConfig(row.ID)
		if !ok {
			continue
		}
		raw, err := json.Marshal(preset)
		if err != nil {
			return err
		}
		if row.Config == string(raw) {
			continue
		}
		if _, err = s.store.EnsureProtocolSnapshot(ctx); err != nil {
			return err
		}
		// Apply the idempotent path adjustment first so a failed write retries it.
		if suffix := relativePathPresets[row.ID]; suffix != "" {
			if _, err = s.store.AppendSourceBaseURLSuffix(ctx, "custom:"+row.ID, suffix); err != nil {
				return fmt.Errorf("legacy preset %s path: %w", row.ID, err)
			}
		}
		if err = s.store.UpsertCustomProtocol(ctx, customProtocolRow(preset, string(raw))); err != nil {
			return err
		}
	}
	return nil
}
