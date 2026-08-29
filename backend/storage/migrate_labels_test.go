package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// 命名统一数据迁移：历史 usage 记录中的 canonical_* 展示标签应被改写为
// maheshvara_*（列值 + record_json 内嵌值），且幂等。
func TestMigrateMaheshvaraLabels(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "labels-migrate.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	started := time.Now().Add(-time.Hour)
	legacyJSON := `{"requestId":"r1","usageSource":"canonical_estimate","conversionChain":["openai_responses_request","canonical_request","openai_chat_request"],"downstreamResponse":{"content":"x \"canonical_request\" y"}}`
	if err := store.SaveUsageRecordJSON(ctx, []byte(legacyJSON), UsageLogItem{
		RequestID: "r1", StartedAt: started, ModelName: "m", UsageSource: "canonical_estimate",
	}, started.Add(time.Second)); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	freshJSON := `{"requestId":"r2","usageSource":"maheshvara_estimate"}`
	if err := store.SaveUsageRecordJSON(ctx, []byte(freshJSON), UsageLogItem{
		RequestID: "r2", StartedAt: started, ModelName: "m", UsageSource: "maheshvara_estimate",
	}, started.Add(time.Second)); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	// 迁移随 Open 已执行（种子是之后写入的）——手动再跑一次迁移后断言。
	if err := store.migrateMaheshvaraLabels(ctx); err != nil {
		t.Fatalf("run migration: %v", err)
	}
	var legacySource, freshSource string
	if err := store.db.QueryRowContext(ctx, `SELECT usage_source FROM usage_records WHERE request_id='r1'`).Scan(&legacySource); err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT usage_source FROM usage_records WHERE request_id='r2'`).Scan(&freshSource); err != nil {
		t.Fatalf("read fresh: %v", err)
	}
	if legacySource != "maheshvara_estimate" {
		t.Fatalf("legacy usage_source = %q, want maheshvara_estimate", legacySource)
	}
	if freshSource != "maheshvara_estimate" {
		t.Fatalf("fresh usage_source = %q, unchanged", freshSource)
	}

	var legacyJSONOut string
	if err := store.db.QueryRowContext(ctx, `SELECT record_json FROM usage_records WHERE request_id='r1'`).Scan(&legacyJSONOut); err != nil {
		t.Fatalf("read legacy json: %v", err)
	}
	for _, want := range []string{`"usageSource":"maheshvara_estimate"`, `"maheshvara_request"`} {
		if !contains(legacyJSONOut, want) {
			t.Fatalf("record_json missing %s: %s", want, legacyJSONOut)
		}
	}
	if contains(legacyJSONOut, `"canonical_request"`) || contains(legacyJSONOut, `"canonical_estimate"`) {
		t.Fatalf("record_json still has canonical labels: %s", legacyJSONOut)
	}
	// 捕获体里带引号的同串也会被改写（展示字段，可接受）——此处仅确认数组元素已被改写。

	// 幂等：手动重跑迁移无变化、无错误。
	if err := store.migrateMaheshvaraLabels(ctx); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records WHERE usage_source='canonical_estimate' OR record_json LIKE '%"canonical_request"%'`).Scan(&count); err != nil {
		t.Fatalf("count residual: %v", err)
	}
	if count != 0 {
		t.Fatalf("residual canonical labels after re-run: %d", count)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
