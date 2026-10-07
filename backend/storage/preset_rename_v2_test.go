package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// 预置 ID 改名必须同步迁移 v2 注册表（revisions/drafts/activations/reports/
// history、bindings JSON 内 protocolId、agent 会话引用）与 custom: 平台引用，
// 且对已迁移的库重放安全（新行不被破坏、旧值零行静默）。
func TestMigratePresetProtocolRenamesV2Registry(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "elysia.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	definitionSeed := func(id, hash string) {
		t.Helper()
		mustExec := func(query string, args ...any) {
			t.Helper()
			if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
				t.Fatalf("seed %s: %v", query, err)
			}
		}
		mustExec(`INSERT INTO protocol_revisions(protocol_id, content_hash, definition, created_at) VALUES(?,?,?,?)`, id, hash, `{"id":"`+id+`"}`, now)
		mustExec(`INSERT INTO protocol_drafts(protocol_id, content_hash, definition, updated_at) VALUES(?,?,?,?)`, id, hash, `{"id":"`+id+`"}`, now)
		mustExec(`INSERT INTO protocol_activations(protocol_id, revision_hash, generation, activated_at) VALUES(?,?,1,?)`, id, hash, now)
		mustExec(`INSERT INTO protocol_history(id, protocol_id, content_hash, definition, reason, created_at, archived_at) VALUES(?,?,?,?,?,?,?)`, id+"~h", id, hash, `{"id":"`+id+`"}`, "preset_replaced", now, now)
		mustExec(`INSERT INTO model_sources(id, name, base_url, api_key, platform, enabled, auto_fetch_models, created_at, updated_at) VALUES(?,?,?,?,?,1,1,?,?)`, "src-"+id, "s", "https://example.com", "k", "custom:"+id, now, now)
	}
	definitionSeed("chat-completions-api", "hash-old")
	definitionSeed("openai-chat-completions", "hash-new") // 预置新 ID 已被引擎重发的并存形态
	if err := store.SaveProtocolBinding(ctx, ProtocolBinding{Kind: "group", GroupID: "g1", Binding: protocol.Binding{ProtocolID: "chat-completions-api", RevisionHash: "hash-old"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO agent_sessions(id, title, protocol_id, created_at, updated_at) VALUES('sess1','','chat-completions-api',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}

	pairs := []ProtocolRenamePair{{OldID: "chat-completions-api", NewID: "openai-chat-completions"}}
	if _, err := store.MigratePresetProtocolRenames(ctx, pairs); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := store.MigratePresetProtocolRenames(ctx, pairs); err != nil {
		t.Fatalf("replay: %v", err)
	}

	var oldRows, newRows int
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := store.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", query, err)
		}
		return n
	}
	// 不同内容哈希各自保留（修订历史不丢）；同键（drafts/activations 的
	// protocol_id 主键）以新行胜出不重复。
	wants := map[string]int{"protocol_revisions": 2, "protocol_drafts": 1, "protocol_activations": 1, "protocol_history": 2}
	for table, want := range wants {
		if oldRows = count(`SELECT COUNT(*) FROM ` + table + ` WHERE protocol_id = 'chat-completions-api'`); oldRows != 0 {
			t.Fatalf("%s still carries the legacy id", table)
		}
		if newRows = count(`SELECT COUNT(*) FROM ` + table + ` WHERE protocol_id = 'openai-chat-completions'`); newRows != want {
			t.Fatalf("%s new-id rows = %d, want %d", table, newRows, want)
		}
	}
	bindings, err := store.ListProtocolBindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].Binding.ProtocolID != "openai-chat-completions" {
		t.Fatal("binding protocolId not rewritten", err, bindings)
	}
	// 两条源（旧引用被重写 + 本就指向新 ID）最终都指向新平台值。
	if n := count(`SELECT COUNT(*) FROM model_sources WHERE platform = 'custom:openai-chat-completions'`); n != 2 {
		t.Fatal("platform reference not rewritten")
	}
	if n := count(`SELECT COUNT(*) FROM agent_sessions WHERE protocol_id = 'openai-chat-completions'`); n != 1 {
		t.Fatal("agent session protocol reference not rewritten")
	}
}
