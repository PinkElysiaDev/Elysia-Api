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

// seedSource 落库模型源行（MergeSourceModels 只写 models 行，不建源行）。
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

func boolPtrFalse() *bool     { v := false; return &v }
func boolPtrTrue() *bool      { v := true; return &v }
func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

// 手动模型的源身份列（base_url/api_key/platform）是源身份的快照而非逐模型
// 覆盖：源换地址/密钥后再次合并必须刷新，否则请求仍打旧配置（回归：改源
// url/key 后手动模型走老地址）。能力/启停/origin 仍完全保留。
func TestMergeSourceModelsRefreshesManualSourceIdentity(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	seedSource(t, store, source)

	// 手动模型入库（旧身份随首次合并快照）。
	if _, err := store.MergeSourceModels(ctx, source, []Model{{ID: "manual-1", Origin: "manual"}}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	// 用户停用它——合并刷新身份时不得触碰启停。
	if _, err := store.UpdateModel(ctx, "manual-1", "src1", ModelPatch{Enabled: boolPtrFalse()}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// 源换地址/密钥/平台后再次合并。
	source.BaseURL = "https://relocated.example.com"
	source.APIKey = "sk-rotated"
	source.Platform = "openai-compatible"
	if _, err := store.MergeSourceModels(ctx, source, []Model{{ID: "manual-1", Origin: "manual"}}); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	models, err := store.ListModels(ctx)
	if err != nil || len(models) != 1 {
		t.Fatalf("list: %v models=%d", err, len(models))
	}
	m := models[0]
	if m.BaseURL != "https://relocated.example.com" {
		t.Fatalf("manual row must follow relocated source url, got %q", m.BaseURL)
	}
	if m.APIKey != "sk-rotated" {
		t.Fatalf("manual row must follow rotated source key, got %q", m.APIKey)
	}
	if m.Platform != "openai" {
		t.Fatalf("manual row platform must be normalized, got %q", m.Platform)
	}
	if m.Origin != "manual" || m.Enabled {
		t.Fatalf("origin must stay manual and user disable must survive: %+v", m)
	}
}

// legacy 导入源（BaseURL 为空）的 models 行携带逐模型地址：身份刷新必须跳过。
func TestMergeSourceModelsSkipsLegacyIdentityRefresh(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	legacy := ModelSource{ID: "legacy-config", Name: "Legacy", BaseURL: "", APIKey: "legacy-key", Platform: "openai", Enabled: true}
	seedSource(t, store, legacy)

	// legacy 逐模型地址经 ReplaceSourceModels 建立(ImportLegacyConfig 路径),
	// Merge 的插入分支只写源身份(空地址),不走它。
	if err := store.ReplaceSourceModels(ctx, legacy, []Model{
		{ID: "legacy-model", Origin: "manual", BaseURL: "https://per-model.example.com"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, err := store.MergeSourceModels(ctx, legacy, []Model{{ID: "legacy-model", Origin: "manual"}}); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	models, err := store.ListModels(ctx)
	if err != nil || len(models) != 1 {
		t.Fatalf("list: %v models=%d", err, len(models))
	}
	if models[0].BaseURL != "https://per-model.example.com" {
		t.Fatalf("legacy per-model url must survive merges, got %q", models[0].BaseURL)
	}
}

// 手动同步语义:SyncManualSourceModels 以 manual 集为权威——缺席的 manual 行
// 删除并清理组引用;空集清空全部;fetch 路径(MergeSourceModels)不受影响。
func TestSyncManualSourceModelsDeletesMissing(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	seedSource(t, store, source)

	seed := []Model{{ID: "keep-me", Origin: "manual"}, {ID: "drop-me", Origin: "manual"}, {ID: "fetch-keep", Origin: "fetched"}}
	if _, err := store.SyncManualSourceModels(ctx, source, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 用户在源编辑里删除 drop-me:保存同步的 manual 集不再包含它。
	result, err := store.SyncManualSourceModels(ctx, source, []Model{{ID: "keep-me", Origin: "manual"}, {ID: "fetch-keep"}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "drop-me" {
		t.Fatalf("drop-me must be reported removed: %+v", result.Removed)
	}
	models, err := store.ListModels(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range models {
		ids[m.ID] = true
	}
	if ids["drop-me"] || !ids["keep-me"] || !ids["fetch-keep"] {
		t.Fatalf("drop-me must be gone, others kept: %v", ids)
	}

	// 清空全部手动模型(空集)也要生效,而非提前返回跳过合并。
	if _, err := store.SyncManualSourceModels(ctx, source, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	models, _ = store.ListModels(ctx)
	for _, m := range models {
		if m.Origin == "manual" {
			t.Fatalf("manual rows must all be cleared, found %s", m.ID)
		}
	}

	// fetch 路径维持旧语义:上游缺席不删 manual 行。
	if _, err := store.MergeSourceModels(ctx, source, []Model{{ID: "fetch-keep"}}); err != nil {
		t.Fatalf("fetch merge: %v", err)
	}
}

// 组引用清理:被同步删除的手动模型若已加入模型组,组内引用一并移除。
func TestSyncManualSourceModelsCleansGroupRefs(t *testing.T) {
	store := openMergeTestStore(t)
	ctx := context.Background()
	source := mergeTestSource()
	seedSource(t, store, source)
	if _, err := store.SyncManualSourceModels(ctx, source, []Model{{ID: "grp-manual", Origin: "manual"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.UpsertGroup(ctx, ModelGroup{ID: "g1", Name: "grp", Enabled: true, Models: []string{"src1:grp-manual"}}); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := store.SyncManualSourceModels(ctx, source, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT COUNT(*) FROM model_group_models WHERE model_id = 'grp-manual'`)
	if err != nil {
		t.Fatalf("count refs: %v", err)
	}
	defer rows.Close()
	var count int
	if rows.Next() {
		_ = rows.Scan(&count)
	}
	if count != 0 {
		t.Fatalf("group references must be cleaned, got %d", count)
	}
}
