package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elysia-api/backend/relay"
)

// writeOutboundConfig 落一份 config.json 并 Load。
func writeOutboundConfig(t *testing.T, content string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// 未配置 outbound 块 → 物化预置默认；旧 allowFakeIPOutbound=true → 默认去掉
// fake-ip 两段（保持 TUN 部署既有行为）。
func TestOutboundDefaultsAndLegacyMigration(t *testing.T) {
	cfg := writeOutboundConfig(t, `{"panelAccessToken":"x"}`)
	if !reflect.DeepEqual(cfg.Outbound.DeniedIPRanges, relay.DefaultDeniedIPRanges) {
		t.Fatalf("expected default preset when outbound block absent")
	}

	cfg = writeOutboundConfig(t, `{"allowFakeIPOutbound":true}`)
	expected := []string{}
	for _, entry := range relay.DefaultDeniedIPRanges {
		if entry != "198.18.0.0/15" && entry != "240.0.0.0/4" {
			expected = append(expected, entry)
		}
	}
	if !reflect.DeepEqual(cfg.Outbound.DeniedIPRanges, expected) {
		t.Fatalf("expected legacy fake-ip switch to strip 198.18.0.0/15 and 240.0.0.0/4")
	}
}

// 显式空列表是合法状态（全放行），不得被默认值覆盖；Save 后仍保持空数组。
func TestOutboundExplicitEmptyListRoundtrip(t *testing.T) {
	cfg := writeOutboundConfig(t, `{"outbound":{"deniedIpRanges":[]}}`)
	if cfg.Outbound.DeniedIPRanges == nil || len(cfg.Outbound.DeniedIPRanges) != 0 {
		t.Fatalf("explicit empty list must survive load as non-nil empty slice")
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(cfg.path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	block, ok := doc["outbound"]
	if !ok {
		t.Fatalf("outbound block must persist after save")
	}
	if string(block) == "{}" || !containsJSONKey(block, "deniedIpRanges") {
		t.Fatalf("deniedIpRanges must persist as [] (not omitted), got %s", block)
	}
	// 旧键清除。
	if _, exists := doc["allowFakeIPOutbound"]; exists {
		t.Fatalf("legacy allowFakeIPOutbound key should be dropped on save")
	}
}

func containsJSONKey(raw json.RawMessage, key string) bool {
	var obj map[string]json.RawMessage
	return json.Unmarshal(raw, &obj) == nil && obj[key] != nil
}
