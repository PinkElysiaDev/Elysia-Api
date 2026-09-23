package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/storage"
)

// API Key 工具与删除类工具单测：明文只出一次、留空保留原值、
// 删除级联（组的令牌禁用、源的模型与组成员清理）、权限键映射。

func TestTokenToolRegistry(t *testing.T) {
	s := newOpsTestServer(t)
	tools := newAgentTokenTools(s)
	if len(tools) != 4 {
		t.Fatalf("token tools = %d", len(tools))
	}
	registry, err := agent.NewRegistry(tools...)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if registry.Get(agentToolListAPIKeys).Gated() {
		t.Fatalf("list_api_keys must not be gated")
	}
	if !registry.Get(agentToolCreateAPIKey).Gated() || registry.Get(agentToolCreateAPIKey).PermissionKey() != "save" {
		t.Fatalf("create_api_key gating wrong")
	}
	if !registry.Get(agentToolDeleteAPIKey).Gated() || registry.Get(agentToolDeleteAPIKey).PermissionKey() != "delete" {
		t.Fatalf("delete_api_key gating wrong")
	}
}

func TestAPIKeyToolsLifecycle(t *testing.T) {
	s := newOpsTestServer(t)
	ctx := context.Background()

	// 创建：自动生成明文且仅在结果里完整返回一次。
	created := opsExecute(t, &createAPIKeyTool{server: s}, `{"name":"k1","allowedGroups":[]}`)
	if !created.OK {
		t.Fatalf("create: %s", created.Summary)
	}
	createdData, _ := created.Data.(map[string]any)
	secret, _ := createdData["token"].(string)
	if len(secret) < 32 {
		t.Fatalf("generated secret too short: %q", secret)
	}

	// 重名拒绝，防止 upsert 静默改值。
	if result := opsExecute(t, &createAPIKeyTool{server: s}, `{"name":"k1","secret":"another-value-1234"}`); result.OK {
		t.Fatalf("duplicate name must fail")
	}

	// 列表出口脱敏：完整明文绝不出现。
	listed := opsExecute(t, &listAPIKeysTool{server: s}, `{}`)
	if !listed.OK {
		t.Fatalf("list: %s", listed.Summary)
	}
	encoded, _ := json.Marshal(listed.Data)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("list leaked plaintext: %s", encoded)
	}

	// 更新：newSecret 留空保留原值；授权组整体替换。
	updated := opsExecute(t, &updateAPIKeyTool{server: s}, `{"name":"k1","enabled":false,"allowedGroups":["g1"]}`)
	if !updated.OK {
		t.Fatalf("update: %s", updated.Summary)
	}
	stored, found, err := s.store.FindAPITokenByName(ctx, "k1")
	if err != nil || !found {
		t.Fatalf("lookup: %v found=%v", err, found)
	}
	if stored.Token != secret {
		t.Fatalf("secret must be preserved, got %q", stored.Token)
	}
	if stored.Enabled || len(stored.AllowedGroups) != 1 || stored.AllowedGroups[0] != "g1" {
		t.Fatalf("update not applied: %+v", stored)
	}

	// 显式换新明文。
	if result := opsExecute(t, &updateAPIKeyTool{server: s}, `{"name":"k1","newSecret":"sk-replacement-9"}`); !result.OK {
		t.Fatalf("rotate: %s", result.Summary)
	}
	stored, _, _ = s.store.FindAPITokenByName(ctx, "k1")
	if stored.Token != "sk-replacement-9" {
		t.Fatalf("rotation not applied: %q", stored.Token)
	}

	// 删除后消失。
	if result := opsExecute(t, &deleteAPIKeyTool{server: s}, `{"name":"k1"}`); !result.OK {
		t.Fatalf("delete: %s", result.Summary)
	}
	if _, found, _ := s.store.FindAPITokenByName(ctx, "k1"); found {
		t.Fatalf("token must be gone")
	}
	if result := opsExecute(t, &deleteAPIKeyTool{server: s}, `{"name":"k1"}`); result.OK {
		t.Fatalf("delete missing must fail")
	}
}

func TestDeleteGroupCascadeDisablesTokens(t *testing.T) {
	s := newOpsTestServer(t)
	ctx := context.Background()

	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "solo", Token: "tok-solo-1", Enabled: true, AllowedGroups: []string{"退役组"}}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := s.store.UpsertGroup(ctx, storage.ModelGroup{ID: "retired", Name: "退役组", Enabled: true, Models: []string{}}); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	result := opsExecute(t, &deleteGroupTool{server: s}, `{"group":"退役组"}`)
	if !result.OK {
		t.Fatalf("delete group: %s", result.Summary)
	}
	if !strings.Contains(result.Summary, "solo") {
		t.Fatalf("summary must surface disabled tokens: %s", result.Summary)
	}
	token, found, _ := s.store.FindAPITokenByName(ctx, "solo")
	if !found || token.Enabled {
		t.Fatalf("token must be cascade-disabled: %+v", token)
	}
}

func TestDeleteSourceCascadeAndModelTools(t *testing.T) {
	s := newOpsTestServer(t)
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	if result := opsExecute(t, &createSourceTool{server: s}, fmt.Sprintf(`{"name":"src1","baseUrl":%q,"manualModels":["m-a","m-b"]}`, upstream.URL)); !result.OK {
		t.Fatalf("create source: %s", result.Summary)
	}
	if err := s.store.UpsertGroup(ctx, storage.ModelGroup{ID: "g1", Name: "g1", Enabled: true, Models: []string{"src1:m-a"}}); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	// list_models：按源过滤，且不泄露 apiKey/baseUrl 冗余快照。
	listed := opsExecute(t, &listModelsTool{server: s}, `{"source":"src1"}`)
	if !listed.OK {
		t.Fatalf("list models: %s", listed.Summary)
	}
	encoded, _ := json.Marshal(listed.Data)
	if strings.Contains(string(encoded), "apiKey") || strings.Contains(string(encoded), "baseUrl") {
		t.Fatalf("model view leaked sensitive snapshot: %s", encoded)
	}
	if !strings.Contains(string(encoded), "m-a") {
		t.Fatalf("models missing: %s", encoded)
	}

	// update_model：启停落库。
	if result := opsExecute(t, &updateModelTool{server: s}, `{"source":"src1","model":"m-a","enabled":false}`); !result.OK {
		t.Fatalf("update model: %s", result.Summary)
	}
	models, _ := s.store.ListModelsFiltered(ctx, storage.ModelListFilter{SourceID: "src1"})
	for _, model := range models {
		if model.ID == "m-a" && model.Enabled {
			t.Fatalf("model must be disabled")
		}
	}

	// delete_model：单删后组内引用一并清理。
	if result := opsExecute(t, &deleteModelTool{server: s}, `{"source":"src1","model":"m-b"}`); !result.OK {
		t.Fatalf("delete model: %s", result.Summary)
	}
	models, _ = s.store.ListModelsFiltered(ctx, storage.ModelListFilter{SourceID: "src1"})
	if len(models) != 1 {
		t.Fatalf("models after delete = %d", len(models))
	}

	// delete_model_source：级联删模型与组成员引用，结果带影响面预告。
	result := opsExecute(t, &deleteSourceTool{server: s}, `{"source":"src1"}`)
	if !result.OK {
		t.Fatalf("delete source: %s", result.Summary)
	}
	if !strings.Contains(result.Summary, "g1") {
		t.Fatalf("summary must mention affected group: %s", result.Summary)
	}
	if models, _ := s.store.ListModelsFiltered(ctx, storage.ModelListFilter{SourceID: "src1"}); len(models) != 0 {
		t.Fatalf("source models must be gone")
	}
	groups, _ := s.store.ListGroups(ctx)
	for _, group := range groups {
		for _, ref := range group.Models {
			if strings.HasPrefix(ref, "src1:") {
				t.Fatalf("group member ref survived: %s", ref)
			}
		}
	}
	// 不存在的源直接报错。
	if result := opsExecute(t, &deleteSourceTool{server: s}, `{"source":"ghost"}`); result.OK {
		t.Fatalf("delete missing source must fail")
	}
}

func TestDeletePermissionMapping(t *testing.T) {
	settings := agent.Settings{AllowDelete: "always"}
	if got := agent.PermissionFor(settings, agent.PermissionKeyDelete); got != "always" {
		t.Fatalf("PermissionFor(delete) = %q", got)
	}
	// 未设置时与既有键同形态：PermissionFor 消费原始值（空串），归一化到
	// ask 发生在存储层写入/读取与 gateCall 的 switch 兜底。
	empty := agent.PermissionFor(agent.Settings{}, agent.PermissionKeyDelete)
	if empty != agent.PermissionFor(agent.Settings{}, agent.PermissionKeySave) {
		t.Fatalf("zero-value behavior must match save key: %q", empty)
	}
	if !agent.KnownPermissionKey(agent.PermissionKeyDelete) {
		t.Fatalf("delete key must be known")
	}
}
