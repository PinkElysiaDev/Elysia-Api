package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// 缺表不再让升级预备段失败：基线指纹跳过缺失的表并继续（告警留痕），
// 生成开关与预置播种不被一个缺表卡死（真实案例：拷库丢 WAL 后
// protocol_history 表消失）。
func TestProtocolUpgradeBaselineToleratesMissingTable(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "elysia.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	if _, err := store.ProtocolUpgradeBaseline(ctx); err != nil {
		t.Fatalf("baseline before drop: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE protocol_history`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := store.ProtocolUpgradeBaseline(ctx); err != nil {
		t.Fatalf("baseline must tolerate a missing table, got: %v", err)
	}
}
