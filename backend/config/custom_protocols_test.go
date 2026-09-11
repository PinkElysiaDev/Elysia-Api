package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTakeDeprecatedCustomProtocols(t *testing.T) {
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

	entries := cfg.TakeDeprecatedCustomProtocols()
	if len(entries) != 2 {
		t.Fatalf("expected 2 deprecated entries, got %d", len(entries))
	}
	var first map[string]any
	if err := json.Unmarshal(entries[0], &first); err != nil || first["id"] != "a" {
		t.Fatalf("unexpected entry: %s", entries[0])
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
	if entries := cfg.TakeDeprecatedCustomProtocols(); entries != nil {
		t.Fatalf("second take should return nil, got %d entries", len(entries))
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("file must be untouched when the key is absent")
	}
}
