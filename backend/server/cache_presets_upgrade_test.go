package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/elysia-api/backend/relay"
)

func TestCachePresetUpgradeFromRealPreviousDefinitions(t *testing.T) {
	testPresetUpgradeFromFixtures(t, "testdata/cache-presets-before")
}

func TestAdapterPresetUpgradeFromRealPreviousDefinitions(t *testing.T) {
	testPresetUpgradeFromFixtures(t, "testdata/adapter-presets-before")
}

func testPresetUpgradeFromFixtures(t *testing.T, directory string) {
	t.Helper()
	s, _ := newProtocolAdminTestServer(t)
	for _, latest := range PresetProtocolConfigsMust(t) {
		data, err := os.ReadFile(directory + "/" + latest.ID + ".json")
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
	rows, err := s.store.ListCustomProtocols(t.Context())
	if err != nil || len(rows) != 8 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		var got relay.CustomProtocolConfig
		if err := json.Unmarshal([]byte(row.Config), &got); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(row.ID, "user-") {
			if got.Request.PathTemplate != "/user-endpoint" {
				t.Fatal("edited definition changed", row.ID)
			}
		} else if latest, ok := findPresetConfig(row.ID); !ok || got.Version != latest.Version {
			t.Fatal("preset not upgraded", row.ID)
		}
	}
	if upgraded := s.seedPresetProtocols(); len(upgraded) != 0 {
		t.Fatal("non-idempotent upgrade", upgraded)
	}
	preview, err := s.prepareProtocolUpgrade(t.Context(), protocolUpgradeInput{})
	if err != nil || preview.Ready {
		t.Fatal("edited legacy protocol bypassed review", err)
	}
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	if len(service.View().IDs()) != 0 {
		t.Fatal("migration preview executed legacy definitions")
	}
}
