package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 裸配置（无任何 xxxEnc 密文）应能在没有 master.key 文件时成功加载。
// 这是零配置开箱即用的关键：新架构不再向 config.json 写密文。
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
		t.Fatalf("bare config without master.key should load, got error: %v", err)
	}
	if cfg.PanelAccessToken != "bare-token" {
		t.Fatalf("panelAccessToken not loaded: %q", cfg.PanelAccessToken)
	}
	if cfg.hasEncryptedSecrets() {
		t.Fatalf("bare config should have no encrypted secrets")
	}
}

// 含密文（dashboardTokenEnc）但缺少 master.key 时，应明确报错——
// 不能静默放过需要解密的配置。
func TestLoadEncryptedConfigRequiresMasterKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
  "host": "127.0.0.1",
  "port": 8765,
  "databasePath": "x.sqlite3",
  "dashboardTokenEnc": {"algorithm":"aes-256-gcm","nonce":"AAAA","ciphertext":"BBBB"}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// 确保环境变量不存在，强制走 master.key 文件路径。
	t.Setenv("ELYSIA_API_MASTER_KEY", "")
	os.Unsetenv("ELYSIA_API_MASTER_KEY")

	if _, err := Load(path); err == nil {
		t.Fatalf("encrypted config without master key should fail to load")
	}
}

// 含密文 + 提供 ELYSIA_API_MASTER_KEY 环境变量时，hasEncryptedSecrets 应为真，
// 且加载会尝试解密（这里用无效密文，预期解密失败而非"找不到 key"）。
func TestEncryptedConfigDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
  "port": 8765,
  "tokens": [{"name":"t1","tokenEnc":{"algorithm":"aes-256-gcm","nonce":"AAAA","ciphertext":"BBBB"}}]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("ELYSIA_API_MASTER_KEY", "some-key")

	// 提供了 key，但密文是无效的 base64/GCM，预期解密报错（说明确实走了解密分支）。
	if _, err := Load(path); err == nil {
		t.Fatalf("expected decryption error for invalid ciphertext")
	}
}
