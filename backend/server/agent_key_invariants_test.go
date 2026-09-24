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

	"github.com/gin-gonic/gin"
)

// 远程访问 Key 独立化的回归：判定只认 scopes、agent 键绑定强制为 ["agent"]
//（纯展示值）、普通键绑定真实 "agent" 组合法、推理隔离、重命名、agent
// 工具拒绝操作 agent 键、方案分析摘要链路。

func TestAgentKeyStorageInvariants(t *testing.T) {
	s := newOpsTestServer(t)
	ctx := context.Background()

	// agent 作用域键：无论传什么组，落库强制为 ["agent"]。
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{
		Name: "remote", Token: "tok-remote-1", Enabled: true,
		AllowedGroups: []string{"g1", "g2"}, Scopes: []string{storage.TokenScopeAgent},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item, _, _ := s.store.FindAPITokenByName(ctx, "remote")
	if len(item.AllowedGroups) != 1 || item.AllowedGroups[0] != "agent" {
		t.Fatalf("agent key groups not forced: %v", item.AllowedGroups)
	}

	// 普通键绑定真实 "agent" 组：完全合法（组名不保留）。
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{
		Name: "normal", Token: "tok-normal-1", Enabled: true, AllowedGroups: []string{"agent"},
	}); err != nil {
		t.Fatalf("normal key with agent group must be legal: %v", err)
	}
	normal, _, _ := s.store.FindAPITokenByName(ctx, "normal")
	if len(normal.Scopes) != 0 || len(normal.AllowedGroups) != 1 || normal.AllowedGroups[0] != "agent" {
		t.Fatalf("normal key corrupted: %+v", normal)
	}

	// 重命名：保留其余字段；目标名占用报错。
	if err := s.store.RenameAPIToken(ctx, "remote", "remote-2"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renamed, found, _ := s.store.FindAPITokenByName(ctx, "remote-2")
	if !found || renamed.Token != "tok-remote-1" || renamed.AllowedGroups[0] != "agent" {
		t.Fatalf("rename lost fields: %+v", renamed)
	}
	if err := s.store.RenameAPIToken(ctx, "remote-2", "normal"); err == nil {
		t.Fatalf("rename onto existing name must fail")
	}
}

// 推理隔离：agent 作用域 Key 在 /v1 鉴权层被拒，普通键照常放行。
func TestAgentKeyInferenceIsolation(t *testing.T) {
	s := newAgentIntegrationServer(t)
	ctx := context.Background()
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "remote", Token: "tok-remote-1", Enabled: true, Scopes: []string{storage.TokenScopeAgent}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "normal", Token: "tok-normal-1", Enabled: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s.invalidateRouteCache()

	router := gin.New()
	passed := false
	router.POST("/v1/chat/completions", s.authMiddleware(), func(c *gin.Context) { passed = true; c.Status(200) })
	post := func(auth string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("tok-remote-1"); code == http.StatusOK {
		t.Fatalf("agent key must not access inference (passed=%v)", passed)
	}
	if code := post("tok-normal-1"); code != http.StatusOK || !passed {
		t.Fatalf("normal key blocked: %d passed=%v", code, passed)
	}
}

// admin 端点重命名（newName）与 agent 工具对 agent 键的拒绝。
func TestAgentKeyRenameAndToolGuards(t *testing.T) {
	s := newOpsTestServer(t)
	ctx := context.Background()
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "remote", Token: "tok-remote-1", Enabled: true, Scopes: []string{storage.TokenScopeAgent}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// PUT /api-tokens/:name + newName：改名成功且绑定不变式保持。
	body := `{"enabled":true,"newName":"cursor"}`
	c, rec := remoteContext(http.MethodPut, "/api/admin/api-tokens/remote", body, nil)
	c.Params = gin.Params{{Key: "name", Value: "remote"}}
	s.adminUpsertToken(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	item, found, _ := s.store.FindAPITokenByName(ctx, "cursor")
	if !found || item.Token != "tok-remote-1" || item.AllowedGroups[0] != "agent" {
		t.Fatalf("renamed key wrong: %+v", item)
	}
	if _, found, _ := s.store.FindAPITokenByName(ctx, "remote"); found {
		t.Fatalf("old name must be gone")
	}

	// 工具守卫：update/delete 对 agent 键拒绝。
	if result := opsExecute(t, &updateAPIKeyTool{server: s}, `{"name":"cursor","enabled":false}`); result.OK {
		t.Fatalf("update agent key must be rejected")
	}
	if result := opsExecute(t, &deleteAPIKeyTool{server: s}, `{"name":"cursor"}`); result.OK {
		t.Fatalf("delete agent key must be rejected")
	}
	// 普通键不受影响。
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "normal", Token: "tok-normal-1", Enabled: true}); err != nil {
		t.Fatalf("seed normal: %v", err)
	}
	if result := opsExecute(t, &updateAPIKeyTool{server: s}, `{"name":"normal","enabled":false}`); !result.OK {
		t.Fatalf("update normal key: %s", result.Summary)
	}
}

// 方案分析摘要：update_plan 的 analysis 落库、事件携带、plan 型暂停透传。
func TestPlanSummaryFlow(t *testing.T) {
	s := newOpsTestServer(t)
	created, err := s.store.CreateAgentSession(context.Background(), storage.AgentSessionUpsert{Mode: "create"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	session, err := s.store.GetSession(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	tctx := &sessionToolContext{ctx: context.Background(), store: s.store, session: session}
	result := (&updatePlanTool{}).Execute(context.Background(), tctx, json.RawMessage(
		`{"analysis":"已确认源 s1 可用且模型齐备；风险：上游限速。","plan":[{"title":"创建模型组","status":"pending"}]}`))
	if !result.OK {
		t.Fatalf("update_plan: %s", result.Summary)
	}
	reloaded, _ := s.store.GetSession(context.Background(), created.ID)
	if !strings.Contains(reloaded.PlanSummary, "上游限速") || len(reloaded.Plan) != 1 {
		t.Fatalf("plan summary roundtrip: %q plan=%v", reloaded.PlanSummary, reloaded.Plan)
	}
	// plan 型暂停的脱敏副本携带摘要。
	pending := &agent.PendingAction{Kind: "plan", Plan: reloaded.Plan, PlanSummary: reloaded.PlanSummary}
	masked := agent.MaskedPendingAction(pending)
	if masked.PlanSummary != pending.PlanSummary {
		t.Fatalf("masked pending lost plan summary")
	}
	_ = fmt.Sprint(masked.Kind)
}
