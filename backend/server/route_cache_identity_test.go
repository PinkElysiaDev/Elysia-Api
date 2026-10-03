package server

import (
	"context"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

func TestStoreRoutingNeverResurrectsLegacyConfiguration(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.config.Groups = []config.ModelGroupConfig{{ID: "legacy", Name: "deleted", Enabled: true}}
	s.config.Tokens = []config.AccessToken{{Token: "deleted-key", Enabled: true}}
	if _, exists := s.findAccessToken("deleted-key"); exists {
		t.Fatal("missing persisted token resurrected from configuration")
	}
	if err := s.store.Close(); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	if groups := s.getGroups(); len(groups) != 0 {
		t.Fatal("database failure resurrected legacy routes", groups)
	}
	if _, exists := s.findAccessToken("deleted-key"); exists {
		t.Fatal("database failure resurrected legacy credentials")
	}
}

// 候选的源身份(地址/密钥)以源表为准:源保存后即便合并未跑(手动源、自动
// 拉取失败),models 行的旧快照不得再被热路径消费(回归:改源 url/key 后
// 既有模型仍按旧配置请求)。legacy 源(地址为空)例外,回落行内值。
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

	groups, ok := s.assembleGroupsFromStore()
	if !ok || len(groups) != 1 || len(groups[0].Models) != 1 {
		t.Fatalf("assembly failed: ok=%v groups=%+v", ok, groups)
	}
	ref := groups[0].Models[0]
	if ref.BaseURL != "https://fresh.example.com" {
		t.Fatalf("candidate must use fresh source url, got %q", ref.BaseURL)
	}
	if ref.APIKey != "sk-fresh" {
		t.Fatalf("single-key source must use fresh source key, got %q", ref.APIKey)
	}
}
