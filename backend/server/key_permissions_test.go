package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/storage"
)

func newKeyPermissionTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "keys.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	s := newUnboundTestServer(nil)
	s.store = store
	activateDiscoveryPresets(t, s)
	return s
}

func TestPerKeyModelDiscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer key-a":
			w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
		case "Bearer key-b":
			w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"claude-3-5"}]}`))
		default:
			w.WriteHeader(401)
		}
	}))
	defer srv.Close()

	s := newKeyPermissionTestServer(t)
	ctx := context.Background()
	source := storage.ModelSource{
		ID: "src1", Name: "Relay", BaseURL: srv.URL, Platform: "openai",
		Enabled: true, AutoFetchModels: true,
		APIKeys: []storage.SourceAPIKey{{Value: "key-a"}, {Value: "key-b"}},
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert source: %v", err)
	}

	summary, err := s.refreshSourceByID(ctx, source.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if summary.Count != 3 {
		t.Fatalf("union should have 3 models, got %d", summary.Count)
	}
	if len(summary.Keys) != 2 || summary.Keys[0].Count != 2 || summary.Keys[1].Count != 2 {
		t.Fatalf("per-key outcomes wrong: %+v", summary.Keys)
	}
	if summary.Keys[0].Index != 0 || summary.Keys[1].Index != 1 {
		t.Fatalf("outcome indices must map to key list positions: %+v", summary.Keys)
	}

	// 并集入库。
	models, err := s.store.ListModels(ctx)
	if err != nil || len(models) != 3 {
		t.Fatalf("expected 3 merged models, got %d (%v)", len(models), err)
	}

	// per-key 权限字段持久化：fetchedModels = 各自拉到的集合，allowedModels = nil。
	sources, err := s.store.ListSources(ctx)
	if err != nil || len(sources) != 1 {
		t.Fatalf("list sources: %v", len(sources))
	}
	keys := sources[0].APIKeys
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys persisted, got %d", len(keys))
	}
	if len(keys[0].FetchedModels) != 2 || len(keys[1].FetchedModels) != 2 {
		t.Fatalf("fetchedModels not persisted per key: %+v", keys)
	}
	if keys[0].AllowedModels != nil || keys[1].AllowedModels != nil {
		t.Fatalf("untouched selection must remain nil: %+v", keys)
	}

	// 权限判定：gpt-4o 两个 key 都能服务；claude-3-5 仅 key-b。
	if !keys[0].KeyAllowsModel("gpt-4o") || !keys[1].KeyAllowsModel("gpt-4o") {
		t.Fatalf("shared model must be allowed by both keys")
	}
	if keys[0].KeyAllowsModel("claude-3-5") || !keys[1].KeyAllowsModel("claude-3-5") {
		t.Fatalf("key-a must not serve claude-3-5, key-b must")
	}
}

func TestPerKeyPartialFailureKeepsStalePermissions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good" {
			w.Write([]byte(`{"data":[{"id":"m1"}]}`))
			return
		}
		w.WriteHeader(401)
	}))
	defer srv.Close()

	s := newKeyPermissionTestServer(t)
	ctx := context.Background()
	source := storage.ModelSource{
		ID: "src1", Name: "Relay", BaseURL: srv.URL, Platform: "openai",
		Enabled: true, AutoFetchModels: true,
		APIKeys: []storage.SourceAPIKey{
			{Value: "good"},
			// bad key 带有旧的拉取结果（上次成功时留下的权限集）。
			{Value: "bad", FetchedModels: []string{"m1", "m2"}, AllowedModels: []string{"m1"}},
		},
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if err := s.store.UpdateSourceAPIKeys(ctx, source.ID, source.APIKeys); err != nil {
		t.Fatal(err)
	}
	summary, err := s.refreshSourceByID(ctx, source.ID)
	if err == nil {
		t.Fatal("partial failure must fail the whole refresh")
	}
	if len(summary.Keys) != 2 || summary.Keys[1].Error == "" {
		t.Fatalf("bad key must record its error: %+v", summary.Keys)
	}
	sources, _ := s.store.ListSources(ctx)
	keys := sources[0].APIKeys
	if keys[0].FetchedModels != nil {
		t.Fatalf("successful key must also retain previous permissions: %+v", keys[0])
	}
	// 失败 key 保持旧值（不清空、不覆盖）。
	if len(keys[1].FetchedModels) != 2 || len(keys[1].AllowedModels) != 1 {
		t.Fatalf("failed key must keep stale permissions: %+v", keys[1])
	}
}

// 装配过滤：多 key 源中没有任何 key 可服务的模型从组内候选剔除。
func TestAssemblyFiltersModelsByPerKeyPermissions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer key-a":
			w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
		case "Bearer key-b":
			w.Write([]byte(`{"data":[{"id":"beta"}]}`))
		default:
			w.WriteHeader(401)
		}
	}))
	defer srv.Close()

	s := newKeyPermissionTestServer(t)
	ctx := context.Background()
	source := storage.ModelSource{
		ID: "src1", Name: "Relay", BaseURL: srv.URL, Platform: "openai",
		Enabled: true, AutoFetchModels: true, KeyStrategy: storage.KeyStrategyRoundRobin,
		APIKeys: []storage.SourceAPIKey{{Value: "key-a"}, {Value: "key-b"}},
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := s.refreshSourceByID(ctx, source.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// gamma 不在任何 key 的集合里，但通过手动途径入库（模拟历史残留/手动模型）。
	if _, err := s.store.MergeSourceModels(ctx, source, []storage.Model{
		{ID: "alpha"}, {ID: "beta"}, {ID: "gamma", Origin: "manual"},
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// 手动模型合并不会更新 key 权限字段——gamma 仍无 key 可服务，应被剔除。
	if err := s.store.UpsertGroup(ctx, storage.ModelGroup{
		ID: "g1", Name: "all", Enabled: true,
		Models: []string{"src1:alpha", "src1:beta", "src1:gamma"},
	}); err != nil {
		t.Fatalf("upsert group: %v", err)
	}

	groups, ok := s.assembleGroupsFromStore()
	if !ok || len(groups) != 1 {
		t.Fatalf("assembly failed: ok=%v groups=%d", ok, len(groups))
	}
	refs := groups[0].Models
	if len(refs) != 2 {
		t.Fatalf("gamma (no serving key) must be excluded, got %d refs", len(refs))
	}
	for _, ref := range refs {
		if ref.ID == "gamma" {
			t.Fatalf("gamma must not appear in candidates")
		}
		if len(ref.APIKeys) != 1 {
			t.Fatalf("each model must keep exactly its permitted key: %s -> %v", ref.ID, ref.APIKeys)
		}
	}
	// alpha 只能 key-a，beta 只能 key-b。
	byID := map[string]int{}
	for i, ref := range refs {
		byID[ref.ID] = i
	}
	if refs[byID["alpha"]].APIKeys[0] != "key-a" || refs[byID["beta"]].APIKeys[0] != "key-b" {
		t.Fatalf("per-model key filtering wrong: %+v", refs)
	}
}

func TestSingleKeyRefreshUsesDiscoveredModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer only" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2-new"}]}`))
	}))
	defer srv.Close()

	s := newKeyPermissionTestServer(t)
	ctx := context.Background()
	source := storage.ModelSource{
		ID: "src1", Name: "Shrunk", BaseURL: srv.URL, Platform: "openai",
		Enabled: true, AutoFetchModels: true,
		APIKeys: []storage.SourceAPIKey{
			// 仅剩一个启用 key，但带着多 key 时代的旧拉取集（不含 m2-new）。
			{Value: "only", FetchedModels: []string{"m1"}, AllowedModels: []string{"m1"}},
			{Value: "gone", Disabled: true, FetchedModels: []string{"m1"}},
		},
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if err := s.store.UpdateSourceAPIKeys(ctx, source.ID, source.APIKeys); err != nil {
		t.Fatal(err)
	}
	if _, err := s.refreshSourceByID(ctx, source.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	keys, err := s.store.ListSources(ctx)
	if err != nil || len(keys) == 0 || len(keys[0].APIKeys) != 2 {
		t.Fatalf("list sources: %v", err)
	}
	active, disabled := keys[0].APIKeys[0], keys[0].APIKeys[1]
	if len(active.FetchedModels) != 2 || active.AllowedModels != nil || !active.KeyAllowsModel("m1") || !active.KeyAllowsModel("m2-new") {
		t.Fatalf("single-key discovery must enable discovered models: %+v", active)
	}
	if len(disabled.FetchedModels) != 1 || !disabled.Disabled {
		t.Fatalf("disabled key metadata changed: %+v", disabled)
	}
	if err := s.store.UpsertGroup(ctx, storage.ModelGroup{
		ID: "g1", Name: "all", Enabled: true, Models: []string{"src1:m2-new"},
	}); err != nil {
		t.Fatalf("upsert group: %v", err)
	}
	groups, ok := s.assembleGroupsFromStore()
	if !ok || len(groups) != 1 || len(groups[0].Models) != 1 {
		t.Fatalf("m2-new must be available after discovery: ok=%v groups=%+v", ok, groups)
	}
}
