package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func openMergeTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "merge.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func mergeTestSource() ModelSource {
	return ModelSource{ID: "src1", Name: "Relay One", BaseURL: "https://old.example.com", APIKey: "sk-old", Platform: "openai", Enabled: true, AutoFetchModels: true}
}

// seedSource 落库模型源行（MergeSourceModels 只写 models 行，不建源行；
// ListGroups 的成员查询按源 enabled 过滤，测试需先建源）。
func seedSource(t *testing.T, store *Store, source ModelSource) {
	t.Helper()
	if err := store.UpsertSource(context.Background(), source); err != nil {
		t.Fatalf("upsert source %s: %v", source.ID, err)
	}
}

func enabledGroup(id, name string, models []string) ModelGroup {
	return ModelGroup{ID: id, Name: name, Enabled: true, Models: models}
}

// 合并策略核心用例：新增入 added、源身份刷新、用户启停保留、上游消失删除并清理组引用。
func TestMergeSourceModelsCoreSemantics(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	seedSource(t, store, source)

	// 第一次拉取：两个模型入库。
	first := []Model{
		{ID: "gpt-4o", VisionCapable: true, CapabilitySource: "catalog"},
		{ID: "text-model"},
	}
	result, err := store.MergeSourceModels(ctx, source, first)
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if len(result.Added) != 2 || len(result.Removed) != 0 {
		t.Fatalf("first merge summary wrong: %+v", result)
	}
	models, err := store.ListModels(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	for _, m := range models {
		if !m.Enabled || !m.Available {
			t.Fatalf("new models must be enabled+available: %+v", m)
		}
		if m.Origin != "fetched" {
			t.Fatalf("origin must be fetched: %+v", m)
		}
		if m.BaseURL != "https://old.example.com" {
			t.Fatalf("source identity must be snapshotted: %+v", m)
		}
	}

	// 用户手动停用 gpt-4o（刷新应保留）。
	if _, err := store.UpdateModel(ctx, "gpt-4o", "src1", ModelPatch{Enabled: boolPtrFalse()}); err != nil {
		t.Fatalf("disable model: %v", err)
	}

	// 把 gpt-4o 关联进模型组，验证上游消失时组引用同步清理。
	if err := store.UpsertGroup(ctx, enabledGroup("g1", "group1", []string{"src1:gpt-4o", "src1:text-model"})); err != nil {
		t.Fatalf("upsert group: %v", err)
	}

	// 第二次拉取：源换了地址；gpt-4o 消失、新增 gpt-4.1；text-model 保留。
	source.BaseURL = "https://new.example.com"
	source.APIKey = "sk-new"
	second := []Model{
		{ID: "text-model", Name: "text-model"},
		{ID: "gpt-4.1", CapabilitySource: "catalog", ToolsCapable: true},
	}
	result, err = store.MergeSourceModels(ctx, source, second)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "gpt-4.1" {
		t.Fatalf("added wrong: %+v", result.Added)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "gpt-4o" {
		t.Fatalf("removed wrong: %+v", result.Removed)
	}

	models, err = store.ListModels(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models after merge, got %d", len(models))
	}
	for _, m := range models {
		if m.BaseURL != "https://new.example.com" {
			t.Fatalf("source identity must be refreshed: %+v", m)
		}
		if m.ID == "gpt-4o" {
			t.Fatalf("gpt-4o must be deleted after upstream removal")
		}
	}

	// 组引用同步清理：g1 只剩 text-model。
	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Models) != 1 || groups[0].Models[0] != "src1:text-model" {
		t.Fatalf("group refs must be cleaned after removal: %+v", groups)
	}
}

// manual 行与用户编辑过的能力字段在刷新时保留。
func TestMergeSourceModelsPreservesManualData(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	seedSource(t, store, source)

	// 手动模型（autoFetch=false 的源走 manual origin）。
	manual := []Model{{ID: "my-model", Origin: "manual", VisionCapable: true}}
	if _, err := store.MergeSourceModels(ctx, source, manual); err != nil {
		t.Fatalf("seed manual: %v", err)
	}
	// 用户编辑过能力的 fetched 模型。
	if _, err := store.MergeSourceModels(ctx, source, []Model{{ID: "fetched-model"}}); err != nil {
		t.Fatalf("seed fetched: %v", err)
	}
	if _, err := store.UpdateModel(ctx, "fetched-model", "src1", ModelPatch{ToolsCapable: boolPtrTrue(), MaxTokens: intPtr(4096)}); err != nil {
		t.Fatalf("edit fetched: %v", err)
	}

	// 刷新：上游列表只有 fetched-model（my-model 消失）。
	refreshed := []Model{{ID: "fetched-model", ToolsCapable: false, MaxTokens: 0, VisionCapable: true}}
	result, err := store.MergeSourceModels(ctx, source, refreshed)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("manual model must not be removed: %+v", result.Removed)
	}
	models, err := store.ListModels(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected manual+fetched to survive, got %d", len(models))
	}
	for _, m := range models {
		switch m.ID {
		case "my-model":
			if m.Origin != "manual" || !m.VisionCapable {
				t.Fatalf("manual model must be untouched: %+v", m)
			}
		case "fetched-model":
			// 能力字段一经手动编辑即整组冻结（简单且安全）：用户编辑值保留，
			// 目录新值（vision=true）不再覆盖。
			if !m.ToolsCapable || m.MaxTokens != 4096 || m.VisionCapable {
				t.Fatalf("manual capability edits must survive refresh: %+v", m)
			}
			if m.CapabilitySource != "manual" {
				t.Fatalf("capability source must stay manual: %+v", m)
			}
		}
	}
}

// 模型 PATCH：能力字段更新并置 manual；启停即时生效。
func TestUpdateAndDeleteModel(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	seedSource(t, store, source)
	if _, err := store.MergeSourceModels(ctx, source, []Model{{ID: "m1"}, {ID: "m2"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	found, err := store.UpdateModel(ctx, "m1", "src1", ModelPatch{Name: strPtr("Pretty Name"), Enabled: boolPtrFalse()})
	if err != nil || !found {
		t.Fatalf("update m1: found=%v err=%v", found, err)
	}
	models, _ := store.ListModels(ctx)
	for _, m := range models {
		if m.ID == "m1" && (m.Name != "Pretty Name" || m.Enabled) {
			t.Fatalf("update not applied: %+v", m)
		}
	}
	// 未触及能力字段时 capability_source 不变。
	if models[0].CapabilitySource == "manual" && models[0].ID == "m2" {
		t.Fatalf("untouched model must not gain manual source")
	}

	// 删除 m2 并验证组引用清理。
	if err := store.UpsertGroup(ctx, enabledGroup("g1", "group1", []string{"src1:m1", "src1:m2"})); err != nil {
		t.Fatalf("group: %v", err)
	}
	deleted, err := store.DeleteModel(ctx, "m2", "src1")
	if err != nil || !deleted {
		t.Fatalf("delete m2: deleted=%v err=%v", deleted, err)
	}
	groups, _ := store.ListGroups(ctx)
	if len(groups[0].Models) != 1 || groups[0].Models[0] != "src1:m1" {
		t.Fatalf("group refs must be cleaned: %+v", groups[0].Models)
	}
	missing, err := store.DeleteModel(ctx, "m2", "src1")
	if err != nil || missing {
		t.Fatalf("deleting missing model must return false, got %v %v", missing, err)
	}
}

// 组成员原子增删：追加去重、位置顺延；移除支持复合键。
func TestAddAndRemoveGroupMembers(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	seedSource(t, store, source)
	if _, err := store.MergeSourceModels(ctx, source, []Model{{ID: "a"}, {ID: "b"}, {ID: "c"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.UpsertGroup(ctx, enabledGroup("g1", "group1", []string{"src1:a"})); err != nil {
		t.Fatalf("group: %v", err)
	}

	added, err := store.AddGroupMembers(ctx, "g1", []string{"src1:b", "src1:b", "src1:a", "src1:c"})
	if err != nil {
		t.Fatalf("add members: %v", err)
	}
	if added != 2 {
		t.Fatalf("dedup add must count 2, got %d", added)
	}
	groups, _ := store.ListGroups(ctx)
	if len(groups[0].Models) != 3 || groups[0].Models[0] != "src1:a" || groups[0].Models[2] != "src1:c" {
		t.Fatalf("members appended in order: %+v", groups[0].Models)
	}

	removed, err := store.RemoveGroupMembers(ctx, "g1", []string{"src1:b"})
	if err != nil || removed != 1 {
		t.Fatalf("remove member: removed=%d err=%v", removed, err)
	}
	groups, _ = store.ListGroups(ctx)
	if len(groups[0].Models) != 2 {
		t.Fatalf("expected 2 members after removal: %+v", groups[0].Models)
	}
}

// 检索过滤：sourceId 精确 + search 模糊（含通配符转义）。
func TestListModelsFiltered(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	if _, err := store.MergeSourceModels(ctx, source, []Model{{ID: "gpt-4o"}, {ID: "claude-3"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	other := ModelSource{ID: "src2", Name: "Relay Two", BaseURL: "https://two.example.com", Platform: "anthropic", Enabled: true, AutoFetchModels: true, APIKey: "k"}
	if _, err := store.MergeSourceModels(ctx, other, []Model{{ID: "gpt-4o"}}); err != nil {
		t.Fatalf("seed2: %v", err)
	}

	bySource, err := store.ListModelsFiltered(ctx, ModelListFilter{SourceID: "src1"})
	if err != nil || len(bySource) != 2 {
		t.Fatalf("filter by source: %d %v", len(bySource), err)
	}
	bySearch, err := store.ListModelsFiltered(ctx, ModelListFilter{Search: "claude"})
	if err != nil || len(bySearch) != 1 || bySearch[0].ID != "claude-3" {
		t.Fatalf("search: %+v %v", bySearch, err)
	}
	// 通配符按字面匹配，不展开。
	literal, err := store.ListModelsFiltered(ctx, ModelListFilter{Search: "gpt-4%"})
	if err != nil || len(literal) != 0 {
		t.Fatalf("like wildcards must be escaped: %+v %v", literal, err)
	}
	combined, err := store.ListModelsFiltered(ctx, ModelListFilter{SourceID: "src1", Search: "gpt"})
	if err != nil || len(combined) != 1 {
		t.Fatalf("combined filter: %+v %v", combined, err)
	}
}

func boolPtrFalse() *bool { v := false; return &v }
func boolPtrTrue() *bool  { v := true; return &v }
func intPtr(v int) *int   { return &v }
func strPtr(v string) *string { return &v }
