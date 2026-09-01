package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// 回归：master-key 丢失/更换后（a) 启动探测必须报告 decryptOK=false；
// (b) 单行解密失败不得毒化整个列表——行保留、密钥清空，网关保底可用。
func TestSecretIntegrityProbeAndRowTolerance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-integrity.sqlite3")
	keyA := []byte("key-a-material-32-bytes-xxxxxxxx") // 任意长度，内部派生
	keyB := []byte("key-b-different-material-yyyyyyy")

	sA, err := OpenWithKey(path, keyA)
	if err != nil {
		t.Fatalf("open with keyA: %v", err)
	}
	ctx := context.Background()
	if err := sA.UpsertSource(ctx, ModelSource{ID: "src", Name: "src", Enabled: true, APIKey: "upstream-secret", AutoFetchModels: false}); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if err := sA.UpsertAPIToken(ctx, APIToken{Name: "tok", Token: "panel-secret", Enabled: true}); err != nil {
		t.Fatalf("UpsertAPIToken: %v", err)
	}
	// 正确密钥：探测通过。
	if has, ok, err := sA.SecretIntegrityProbe(ctx); err != nil || !has || !ok {
		t.Fatalf("probe with correct key = (%v,%v,%v), want (true,true,nil)", has, ok, err)
	}
	sA.Close()

	// 错误密钥打开同一库：探测必须报告不可解。
	sB, err := OpenWithKey(path, keyB)
	if err != nil {
		t.Fatalf("open with keyB: %v", err)
	}
	defer sB.Close()
	if has, ok, err := sB.SecretIntegrityProbe(ctx); err != nil || !has || ok {
		t.Fatalf("probe with wrong key = (%v,%v,%v), want (true,false,nil)", has, ok, err)
	}

	// 行级容错：列表不再整体失败，行保留、密钥清空。
	sources, err := sB.ListSources(ctx)
	if err != nil {
		t.Fatalf("ListSources must tolerate undecryptable rows: %v", err)
	}
	if len(sources) != 1 || sources[0].ID != "src" || sources[0].APIKey != "" {
		t.Fatalf("source row must survive with cleared key: %+v", sources)
	}
	tokens, err := sB.ListAPITokens(ctx)
	if err != nil {
		t.Fatalf("ListAPITokens must tolerate undecryptable rows: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Name != "tok" || tokens[0].Token != "" {
		t.Fatalf("token row must survive with cleared plaintext: %+v", tokens)
	}
}

// 无密文（全新库）时探测报告 hasEncrypted=false，不应告警。
func TestSecretIntegrityProbeCleanDatabase(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "clean.sqlite3"), []byte("some-key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if has, ok, err := s.SecretIntegrityProbe(context.Background()); err != nil || has || !ok {
		t.Fatalf("probe on clean db = (%v,%v,%v), want (false,true,nil)", has, ok, err)
	}
}
