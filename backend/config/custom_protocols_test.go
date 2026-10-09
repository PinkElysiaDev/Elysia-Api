package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportDeprecatedCustomProtocolsPreservesUntilCompletion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	initial := `{"host":"127.0.0.1","port":1,"customProtocols":[{"id":"a"},{"id":"b"}],"keepMe":{"x":1}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	beforeRead, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cfg.ReadDeprecatedCustomProtocols()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 deprecated entries, got %d", len(entries))
	}
	var first map[string]any
	if err := json.Unmarshal(entries[0], &first); err != nil || first["id"] != "a" {
		t.Fatalf("unexpected entry: %s", entries[0])
	}
	untouched, err := os.ReadFile(path)
	if err != nil || string(untouched) != string(beforeRead) {
		t.Fatal("read modified the import source", err)
	}
	if err := cfg.FinishDeprecatedCustomProtocols(entries); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), "customProtocols") {
		t.Fatalf("key must be stripped from config.json, got: %s", raw)
	}
	if !strings.Contains(string(raw), "keepMe") {
		t.Fatalf("other keys must survive, got: %s", raw)
	}

	// 幂等：键已移除后再次调用返回 nil 且文件不变。
	before, _ := os.ReadFile(path)
	if entries, err := cfg.ReadDeprecatedCustomProtocols(); err != nil || entries != nil {
		t.Fatalf("second take should return nil, got %d entries", len(entries))
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("file must be untouched when the key is absent")
	}
}

func TestDeprecatedCustomProtocolsRejectChangedSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"customProtocols":[{"id":"first"}]}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cfg.ReadDeprecatedCustomProtocols()
	if err != nil {
		t.Fatal(err)
	}
	changed := `{"customProtocols":[{"id":"operator-edit"}]}`
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.FinishDeprecatedCustomProtocols(entries); err == nil {
		t.Fatal("concurrent source change was discarded")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != changed {
		t.Fatal("source changed", err)
	}
}
