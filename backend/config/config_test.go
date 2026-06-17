package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 裸配置应能在没有 master.key 文件时成功加载。新架构不再向 config.json 写密文，
// 模型组/token/密钥统一走 SQLite，因此 config.json 永远是零密文的瘦 bootstrap。
func TestLoadBareConfigWithoutMasterKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "bare-token",
  "databasePath": "bare.sqlite3",
  "logLevel": "info"
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("bare config should load, got error: %v", err)
	}
	if cfg.PanelAccessToken != "bare-token" {
		t.Fatalf("panelAccessToken not loaded: %q", cfg.PanelAccessToken)
	}
}

// 旧 orchestrator 风格的 config.json（含 modelGroups/tokens/密文字段）现在应被
// 静默忽略——这些字段已是 json:"-" 或已从结构体删除，不再参与反序列化，
// 加载不报错，且不会把它们的数据带进运行时。
func TestLegacyEncryptedFieldsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
  "host": "127.0.0.1",
  "port": 8765,
  "databasePath": "x.sqlite3",
  "panelAccessToken": "test-token",
  "tokens": [{"name":"t1","tokenEnc":{"algorithm":"aes-256-gcm","nonce":"AAAA","ciphertext":"BBBB"}}],
  "modelGroups": [{"id":"g1","name":"grp"}]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// 确保无 master.key 也不再报错（不再有密文解密路径）。
	t.Setenv("ELYSIA_API_MASTER_KEY", "")
	os.Unsetenv("ELYSIA_API_MASTER_KEY")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("legacy config with encrypted fields should load without error now, got: %v", err)
	}
	// modelGroups/tokens 是 json:"-"，不参与反序列化，运行时应为空。
	if len(cfg.Groups) != 0 {
		t.Fatalf("legacy modelGroups must NOT be loaded from config.json, got %d", len(cfg.Groups))
	}
	if len(cfg.Tokens) != 0 {
		t.Fatalf("legacy tokens must NOT be loaded from config.json, got %d", len(cfg.Tokens))
	}
}
