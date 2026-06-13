package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretCodecRoundTrip(t *testing.T) {
	codec, err := newSecretCodec([]byte("test-master-key"))
	if err != nil {
		t.Fatalf("newSecretCodec: %v", err)
	}
	plain := "sk-super-secret-123"
	enc, err := codec.encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(enc, secretPrefix) {
		t.Fatalf("expected ciphertext prefix, got %q", enc)
	}
	if strings.Contains(enc, plain) {
		t.Fatalf("ciphertext must not contain plaintext")
	}
	dec, err := codec.decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("round trip mismatch: got %q want %q", dec, plain)
	}
}

func TestSecretCodecPlaintextModePassThrough(t *testing.T) {
	codec, _ := newSecretCodec(nil) // 明文模式
	enc, err := codec.encrypt("abc")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc != "abc" {
		t.Fatalf("plaintext mode should pass through, got %q", enc)
	}
	// 明文模式读历史明文应原样返回
	dec, _ := codec.decrypt("abc")
	if dec != "abc" {
		t.Fatalf("plaintext decrypt mismatch: %q", dec)
	}
}

func TestSecretCodecBackwardCompatPlaintextRead(t *testing.T) {
	codec, _ := newSecretCodec([]byte("key"))
	// 启用加密后读到历史明文（无前缀），应原样返回以兼容迁移。
	dec, err := codec.decrypt("legacy-plaintext")
	if err != nil {
		t.Fatalf("decrypt legacy plaintext: %v", err)
	}
	if dec != "legacy-plaintext" {
		t.Fatalf("legacy plaintext should pass through, got %q", dec)
	}
}

func TestSecretCodecEncryptIdempotent(t *testing.T) {
	codec, _ := newSecretCodec([]byte("key"))
	once, _ := codec.encrypt("secret")
	twice, _ := codec.encrypt(once)
	if once != twice {
		t.Fatalf("encrypting ciphertext again should be a no-op")
	}
}

// 端到端：用 key 打开 store，写入 token / source，验证 DB 里是密文、
// 读出来是明文，且 FindAPIToken 仍能命中。
func TestStoreEncryptsSecretsAtRest(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "enc.sqlite3")
	store, err := OpenWithKey(dbPath, []byte("master-key-xyz"))
	if err != nil {
		t.Fatalf("OpenWithKey: %v", err)
	}
	defer store.Close()

	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k1", Token: "client-tok-abc", Enabled: true}); err != nil {
		t.Fatalf("UpsertAPIToken: %v", err)
	}
	if err := store.UpsertSource(ctx, ModelSource{ID: "s1", Name: "S1", BaseURL: "https://api.example.com", APIKey: "sk-upstream-999", Platform: "openai", Enabled: true}); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}

	// 直接读底层列，应为密文。
	var rawToken, rawKey string
	if err := store.db.QueryRowContext(ctx, `SELECT token FROM api_tokens WHERE name='k1'`).Scan(&rawToken); err != nil {
		t.Fatalf("raw token scan: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT api_key FROM model_sources WHERE id='s1'`).Scan(&rawKey); err != nil {
		t.Fatalf("raw key scan: %v", err)
	}
	if !strings.HasPrefix(rawToken, secretPrefix) || strings.Contains(rawToken, "client-tok-abc") {
		t.Fatalf("token not encrypted at rest: %q", rawToken)
	}
	if !strings.HasPrefix(rawKey, secretPrefix) || strings.Contains(rawKey, "sk-upstream-999") {
		t.Fatalf("api_key not encrypted at rest: %q", rawKey)
	}

	// 通过 API 读出应为明文。
	tokens, err := store.ListAPITokens(ctx)
	if err != nil || len(tokens) != 1 || tokens[0].Token != "client-tok-abc" {
		t.Fatalf("ListAPITokens decrypt failed: %+v err=%v", tokens, err)
	}
	sources, err := store.ListSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].APIKey != "sk-upstream-999" {
		t.Fatalf("ListSources decrypt failed: %+v err=%v", sources, err)
	}

	// FindAPIToken 应能用明文命中。
	found, ok, err := store.FindAPIToken(ctx, "client-tok-abc")
	if err != nil || !ok || found.Name != "k1" {
		t.Fatalf("FindAPIToken failed: ok=%v err=%v item=%+v", ok, err, found)
	}
	if _, ok, _ := store.FindAPIToken(ctx, "wrong-token"); ok {
		t.Fatalf("FindAPIToken should not match wrong token")
	}
}

// 迁移兼容：旧库（明文 key 打开），换成带 key 打开后仍能读出历史明文。
func TestStoreReadsLegacyPlaintextAfterEnablingKey(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite3")

	plain, err := Open(dbPath) // 无 key：明文写入
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := plain.UpsertAPIToken(ctx, APIToken{Name: "k1", Token: "legacy-tok", Enabled: true}); err != nil {
		t.Fatalf("UpsertAPIToken: %v", err)
	}
	plain.Close()

	// 重新用 key 打开，应能读出历史明文。
	enc, err := OpenWithKey(dbPath, []byte("new-key"))
	if err != nil {
		t.Fatalf("OpenWithKey: %v", err)
	}
	defer enc.Close()
	found, ok, err := enc.FindAPIToken(ctx, "legacy-tok")
	if err != nil || !ok || found.Name != "k1" {
		t.Fatalf("legacy plaintext read failed: ok=%v err=%v", ok, err)
	}
}
