package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
)

// 复现真实坏库的启动路径：v2 注册表停留在旧预置 ID（改名前存量）。
// 启动序列必须把它迁到新 ID 并完成运行时刷新，而不是被基线冲突卡死。
func TestStartupRecoversFromLegacyIDRegistry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "elysia.sqlite3")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// 旧 ID 时代的 v2 注册表：旧预置 ID 的 revision/draft/activation。
	shipped, err := builtin.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	legacyIDs := map[string]string{
		"openai-chat-completions": "chat-completions-api",
		"openai-responses":        "responses-api",
		"anthropic-messages":      "anthropic-api",
		"google-generate-content": "gemini-api",
	}
	nowString := time.Now().UTC().Format(time.RFC3339Nano)
	for _, value := range shipped {
		var definition protocol.Definition
		if err := value.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		legacy, exists := legacyIDs[definition.ID]
		if !exists {
			continue
		}
		legacyDefinition := definition
		legacyDefinition.ID = legacy
		// v1 行：config 内部 id 也是旧 ID（旧版播种形态）。
		v1Config := `{"id":"` + legacy + `","request":{"method":"POST","path":"/x"}}`
		if err := store.UpsertCustomProtocol(ctx, storage.CustomProtocol{ID: legacy, Name: legacyDefinition.Name, Version: "1", Type: "llm", Config: v1Config}); err != nil {
			t.Fatal(err)
		}
	}
	_ = nowString
	for _, value := range shipped {
		var definition protocol.Definition
		if err := value.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		legacy, exists := legacyIDs[definition.ID]
		if !exists {
			continue
		}
		legacyDefinition := definition
		legacyDefinition.ID = legacy
		legacyValue := mustEncodedProtocolValue(t, legacyDefinition)
		compiled := compileFixtureDefinition(t, legacyDefinition)
		revision := protocol.Revision{ProtocolID: legacy, Hash: compiled.Hash(), Definition: legacyValue, CreatedAt: time.Now().UTC()}
		if err := store.SaveProtocolRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetProtocolActivation(ctx, legacy, revision.Hash, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SaveProtocolDraft(ctx, protocol.Draft{ProtocolID: legacy, Hash: revision.Hash, Definition: legacyValue, UpdatedAt: time.Now().UTC()}, ""); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	adminServer, _ := newProtocolAdminTestServer(t)
	adminServer.store.Close()
	adminServer.store = openStoreAt(t, path)

	// 坏库现场（在最终 Open 之后注入，避免被启动迁移重建）：
	// 主文件丢 protocol_history + 上一代成功迁移留下的回执。
	dropProtocolHistory(t, path)
	seedUpgradeReceipt(t, path)
	adminServer.migratePresetProtocolRenames()
	adminServer.seedPresetProtocols()
	if err := adminServer.initializeProtocolRuntime(ctx); err != nil {
		t.Fatalf("startup must recover, got: %v", err)
	}
	service, err := adminServer.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"openai-chat-completions", "openai-responses", "anthropic-messages", "google-generate-content"} {
		if compiled, ok := service.Pin(id); !ok || compiled == nil {
			t.Fatalf("preset %s missing after recovery", id)
		}
	}
}

func openStoreAt(t *testing.T, path string) *storage.Store {
	t.Helper()
	store, err := storage.Open(path)
	if err != nil {
		t.Fatalf("open store at %s: %v", path, err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// dropProtocolHistory 直连 SQLite 把 protocol_history 表移除，模拟拷库丢
// WAL 后的坏库现场（storage.Open 的迁移会重建它，所以用裸连接事后删除）。
func dropProtocolHistory(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE IF EXISTS protocol_history`); err != nil {
		t.Fatalf("drop: %v", err)
	}
}

// seedUpgradeReceipt 写入上一代成功迁移的回执（键与结构与存储层一致），
// 模拟「已完成过 v2 升级」的存量库。
func seedUpgradeReceipt(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer db.Close()
	receipt := `{"planHash":"legacy","baseline":"legacy","compilerVersion":"old","backup":"legacy","completedAt":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`
	if _, err := db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('protocol_engine_v2_migration',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, receipt, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
}
