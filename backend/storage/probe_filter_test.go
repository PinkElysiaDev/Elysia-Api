package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// 回归：健康探测是真实计费请求，不得打向用户手动停用的模型（enabled=0）
// 或停用源下的模型；available=0（自动禁用）必须保留——恢复探测依赖它。
func TestListAllModelsForProbeSkipsDisabled(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "probe-filter.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	activeSource := ModelSource{ID: "src-on", Name: "src-on", Enabled: true, AutoFetchModels: false}
	disabledSource := ModelSource{ID: "src-off", Name: "src-off", Enabled: false, AutoFetchModels: false}
	for _, src := range []ModelSource{activeSource, disabledSource} {
		if err := s.UpsertSource(ctx, src); err != nil {
			t.Fatalf("UpsertSource %s: %v", src.ID, err)
		}
	}
	models := []Model{
		{ID: "probe-ok", Name: "ok", BaseURL: "https://a.example.com", Platform: "openai", Enabled: true, Available: true, Origin: "manual"},
		{ID: "probe-model-off", Name: "model-off", BaseURL: "https://a.example.com", Platform: "openai", Enabled: true, Available: true, Origin: "manual"},
		{ID: "probe-recovering", Name: "recovering", BaseURL: "https://a.example.com", Platform: "openai", Enabled: true, Available: true, Origin: "manual"},
	}
	if err := s.ReplaceSourceModels(ctx, activeSource, models); err != nil {
		t.Fatalf("ReplaceSourceModels: %v", err)
	}
	offModels := []Model{
		{ID: "probe-src-off", Name: "src-off-model", BaseURL: "https://b.example.com", Platform: "openai", Enabled: true, Available: true, Origin: "manual"},
	}
	if err := s.ReplaceSourceModels(ctx, disabledSource, offModels); err != nil {
		t.Fatalf("ReplaceSourceModels (disabled source): %v", err)
	}
	// ReplaceSourceModels 的 normalize 会强制 enabled/available 为 true，
	// 状态切换走真实的管理路径 UpdateModel。
	off := false
	if found, err := s.UpdateModel(ctx, "probe-model-off", "src-on", ModelPatch{Enabled: &off}); err != nil || !found {
		t.Fatalf("UpdateModel disable: found=%v err=%v", found, err)
	}
	if _, err := s.SetModelAvailability(ctx, "probe-recovering", "src-on", false); err != nil {
		t.Fatalf("SetModelAvailability: %v", err)
	}

	probed, err := s.ListAllModelsForProbe(ctx)
	if err != nil {
		t.Fatalf("ListAllModelsForProbe: %v", err)
	}
	got := map[string]bool{}
	for _, m := range probed {
		got[m.ID] = true
	}
	if !got["probe-ok"] {
		t.Fatal("enabled model of enabled source must be probed")
	}
	if !got["probe-recovering"] {
		t.Fatal("auto-disabled (available=0) model must stay in probe list for recovery")
	}
	if got["probe-model-off"] {
		t.Fatal("manually disabled model must not be probed (billable requests)")
	}
	if got["probe-src-off"] {
		t.Fatal("model of disabled source must not be probed (billable requests)")
	}
}
