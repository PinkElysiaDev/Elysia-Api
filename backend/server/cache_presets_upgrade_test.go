package server

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

func TestCachePresetUpgradeFromRealPreviousDefinitions(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	s, _ := newProtocolAdminTestServer(t)
	for _, latest := range PresetProtocolConfigsMust(t) {
		data, err := os.ReadFile("testdata/cache-presets-before/" + latest.ID + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var old relay.CustomProtocolConfig
		if err := json.Unmarshal(data, &old); err != nil {
			t.Fatal(err)
		}
		canonical, _ := json.Marshal(old)
		t.Logf("%s previous version=%s hash=%s", old.ID, old.Version, presetContentHash(string(canonical)))
		if !matchesAnyPresetHash(string(canonical), legacyPresetHashes[old.ID]) {
			t.Errorf("previous definition missing from hash chain: %s", old.ID)
		}
		if err := s.store.UpsertCustomProtocol(t.Context(), customProtocolRow(old, string(canonical))); err != nil {
			t.Fatal(err)
		}
		// A user-edited copy must survive both seeding and registry reload.
		old.ID = "user-" + old.ID
		old.Request.PathTemplate = "/user-endpoint"
		custom, _ := json.Marshal(old)
		if err := s.store.UpsertCustomProtocol(t.Context(), customProtocolRow(old, string(custom))); err != nil {
			t.Fatal(err)
		}
	}
	s.seedPresetProtocols()
	if err := s.syncCustomProtocolsQuiet(); err != nil {
		t.Fatal(err)
	}
	for _, latest := range PresetProtocolConfigsMust(t) {
		got, exists := relay.GetCustomProtocol(latest.ID)
		if !exists || got.Version != latest.Version {
			t.Errorf("registry not upgraded: %s", latest.ID)
		}
		custom, exists := relay.GetCustomProtocol("user-" + latest.ID)
		if !exists || custom.Request.PathTemplate != "/user-endpoint" {
			t.Errorf("custom definition changed: %s", latest.ID)
		}
	}
	if upgraded := s.seedPresetProtocols(); len(upgraded) != 0 {
		t.Fatalf("upgrade not idempotent: %v", upgraded)
	}
	// Recreate the server registry from persisted rows, as on restart.
	relay.ClearCustomProtocols()
	if err := s.syncCustomProtocolsQuiet(); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.store.ListCustomProtocols(t.Context())
	if len(rows) != 8 {
		t.Fatalf("unexpected rows: %d", len(rows))
	}
	for _, row := range rows {
		if _, exists := relay.GetCustomProtocol(row.ID); !exists {
			t.Errorf("restart lost %s", row.ID)
		}
	}
	// Editing the actual preset ID also prevents replacement.
	latest := PresetProtocolConfigsMust(t)[0]
	latest.Request.PathTemplate = "/edited-preset"
	edited, _ := json.Marshal(latest)
	if err := s.store.UpsertCustomProtocol(t.Context(), storage.CustomProtocol{ID: latest.ID, Name: latest.Name, Type: "llm", Config: string(edited)}); err != nil {
		t.Fatal(err)
	}
	s.seedPresetProtocols()
	if err := s.syncCustomProtocolsQuiet(); err != nil {
		t.Fatal(err)
	}
	got, _ := relay.GetCustomProtocol(latest.ID)
	if got.Request.PathTemplate != "/edited-preset" {
		t.Fatal("edited preset overwritten")
	}
}
