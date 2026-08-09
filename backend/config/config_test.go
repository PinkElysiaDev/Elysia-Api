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

// 旧配置格式的 config.json（含 modelGroups/tokens/密文字段）现在应被
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

// EnsureConfig 在 config.json 不存在时，应自动写入一份带随机
// panelAccessToken 的默认配置并加载返回，created=true。
func TestEnsureConfigCreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, created, err := EnsureConfig(path)
	if err != nil {
		t.Fatalf("EnsureConfig on missing file should succeed, got: %v", err)
	}
	if !created {
		t.Fatalf("created should be true when file was missing")
	}
	if cfg == nil {
		t.Fatal("cfg should not be nil")
	}

	// 生成的 token 必须非空、且不能是已知占位符 change-me。
	if cfg.PanelAccessToken == "" {
		t.Fatal("generated panelAccessToken should not be empty")
	}
	if cfg.PanelAccessToken == "change-me" {
		t.Fatal("generated token must not be the insecure change-me placeholder")
	}

	// 文件应已落盘（权限断言放宽：Windows 忽略 WriteFile 的 perm 位，
	// 0o600 实际落盘为 0666，仅校验文件存在与可读）。
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config.json should exist after EnsureConfig: %v", err)
	}

	// 落盘的文件应能被再次 Load，且 token 与首次返回一致。
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("reloading generated config should succeed: %v", err)
	}
	if cfg2.PanelAccessToken != cfg.PanelAccessToken {
		t.Fatalf("reloaded token mismatch: %q vs %q", cfg2.PanelAccessToken, cfg.PanelAccessToken)
	}
}

// EnsureConfig 在 config.json 已存在时应直接加载，不创建、不覆盖。
func TestEnsureConfigLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "preexisting-token",
  "logLevel": "info"
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, created, err := EnsureConfig(path)
	if err != nil {
		t.Fatalf("EnsureConfig on existing file should succeed: %v", err)
	}
	if created {
		t.Fatal("created should be false when file already exists")
	}
	if cfg.PanelAccessToken != "preexisting-token" {
		t.Fatalf("existing token must be preserved, got %q", cfg.PanelAccessToken)
	}
}

// EnsureConfig 在配置文件存在但解析失败时，应返回错误且不覆盖原文件——
// 只有 genuinely missing（not-exist）才自举，避免静默吞掉合法的解析错误。
func TestEnsureConfigParseErrorNotAutoCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	bad := []byte("{ not valid json")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, created, err := EnsureConfig(path)
	if err == nil {
		t.Fatal("EnsureConfig should return an error on unparseable config")
	}
	if created {
		t.Fatal("created should be false on parse error")
	}
	if cfg != nil {
		t.Fatal("cfg should be nil on parse error")
	}

	// 原文件应保持不变，不被覆盖为默认配置。
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	if string(got) != string(bad) {
		t.Fatalf("parse-error file must not be overwritten; got %q", got)
	}
}

func TestRelayPassthroughDefaultsToEnabled(t *testing.T) {
	cfg := &Config{}
	if !cfg.IsRelayPassthroughEnabled() {
		t.Fatal("relay passthrough must be enabled by default")
	}

	disabled := false
	cfg.Relay.Passthrough = &disabled
	if cfg.IsRelayPassthroughEnabled() {
		t.Fatal("explicit relay passthrough opt-out was ignored")
	}
}
