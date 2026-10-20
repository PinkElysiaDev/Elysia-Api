package server

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elysia-api/backend/internal/routing"
	"github.com/elysia-api/backend/storage"
)

func TestRouteCacheConcurrentReloadFailsClosed(t *testing.T) {
	t.Parallel()
	store, err := storage.Open(filepath.Join(t.TempDir(), "routes.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := &Server{store: store}
	if err := store.UpsertAPIToken(t.Context(), storage.APIToken{Name: "key", Token: "test-key", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	checkConcurrent := func(want bool) {
		t.Helper()
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				<-start
				if _, exists := s.findAccessToken("test-key"); exists != want {
					t.Errorf("token authorization = %t, want %t", exists, want)
				}
			})
		}
		close(start)
		wg.Wait()
	}
	checkConcurrent(true)
	if err := store.DeleteAPIToken(t.Context(), "key"); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	checkConcurrent(false)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	checkConcurrent(false)
	if s.routeSnapshot() != nil {
		t.Fatal("database failure published a routing snapshot")
	}
	if groups := s.getGroups(); len(groups) != 0 {
		t.Fatal("database failure resurrected legacy routes", groups)
	}
}

// 候选的源身份(地址/密钥)以源表为准:源保存后即便合并未跑(手动源、自动
// 拉取失败),models 行的旧快照不得再被热路径消费(回归:改源 url/key 后
// 既有模型仍按旧配置请求)。源缺少地址时不可路由，不回落到模型行的快照。
func TestAssemblyPrefersSourceIdentityOverStaleSnapshots(t *testing.T) {
	s := newKeyPermissionTestServer(t)
	ctx := context.Background()

	source := storage.ModelSource{
		ID: "src1", Name: "Relay", BaseURL: "https://old.example.com", Platform: "openai",
		Enabled: true, APIKeys: []storage.SourceAPIKey{{Value: "sk-stale"}},
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	// 手动模型入库:models 行快照旧地址+旧密钥,此后源换配置不再触发合并。
	if _, err := s.store.MergeSourceModels(ctx, source, []storage.Model{{ID: "m1", Origin: "manual"}}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	source.BaseURL = "https://fresh.example.com"
	source.APIKeys = []storage.SourceAPIKey{{Value: "sk-fresh"}}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("re-upsert source: %v", err)
	}
	if err := s.store.UpsertGroup(ctx, storage.ModelGroup{ID: "g1", Name: "grp", Enabled: true, Models: []string{"src1:m1"}}); err != nil {
		t.Fatalf("upsert group: %v", err)
	}

	groups, err := routing.AssembleGroups(ctx, s.store)
	if err != nil || len(groups) != 1 || len(groups[0].Models) != 1 {
		t.Fatalf("assembly failed: err=%v groups=%+v", err, groups)
	}
	ref := groups[0].Models[0]
	if ref.BaseURL != "https://fresh.example.com" {
		t.Fatalf("candidate must use fresh source url, got %q", ref.BaseURL)
	}
	if ref.APIKey != "sk-fresh" {
		t.Fatalf("single-key source must use fresh source key, got %q", ref.APIKey)
	}
}
