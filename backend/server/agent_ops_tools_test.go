package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// 运维工具单测：查询工具的返回形状、写工具的门控与落库、密钥脱敏、
// SSRF 拒绝、成员增删。

func newOpsTestServer(t *testing.T) *Server {
	t.Helper()
	return newAgentIntegrationServer(t)
}

func opsExecute(t *testing.T, tool agent.Tool, args string) agent.ToolResult {
	t.Helper()
	result := tool.Execute(context.Background(), &opsTestContext{}, json.RawMessage(args))
	return result
}

// opsTestContext 最小 ToolContext 桩（ops 工具不依赖会话状态）。
type opsTestContext struct{}

func (c *opsTestContext) SessionMeta() agent.SessionMeta             { return agent.SessionMeta{} }
func (c *opsTestContext) Draft() json.RawMessage                     { return nil }
func (c *opsTestContext) SetDraft(draft json.RawMessage) error       { return nil }
func (c *opsTestContext) TestTarget() (string, string)               { return "", "" }
func (c *opsTestContext) SetTestTarget(baseURL, apiKey string) error { return nil }
func (c *opsTestContext) SetPlan(steps []agent.PlanStep) error       { return nil }
func (c *opsTestContext) SetPlanSummary(summary string) error        { return nil }
func (c *opsTestContext) SetTitle(title string) error                { return nil }

func TestOpsToolRegistry(t *testing.T) {
	s := newOpsTestServer(t)
	tools := newAgentOpsTools(s)
	if len(tools) != 18 {
		t.Fatalf("ops tools = %d", len(tools))
	}
	registry, err := agent.NewRegistry(tools...)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for _, name := range []string{agentToolListSources, agentToolCreateSource, agentToolCreateGroup, agentToolRefreshSource, agentToolOutbound} {
		if registry.Get(name) == nil {
			t.Fatalf("missing tool %s", name)
		}
	}
	// 门控划分
	if !registry.Get(agentToolCreateSource).Gated() || registry.Get(agentToolCreateSource).PermissionKey() != "save" {
		t.Fatalf("create_source gating wrong")
	}
	if !registry.Get(agentToolRefreshSource).Gated() || registry.Get(agentToolRefreshSource).PermissionKey() != "live_test" {
		t.Fatalf("refresh_source gating wrong")
	}
	if registry.Get(agentToolListSources).Gated() || registry.Get(agentToolUsageStats).Gated() {
		t.Fatalf("read tools must not be gated")
	}
	// 删除类单独门控 delete
	for _, name := range []string{agentToolDeleteSource, agentToolDeleteGroup, agentToolDeleteModel} {
		tool := registry.Get(name)
		if tool == nil || !tool.Gated() || tool.PermissionKey() != "delete" {
			t.Fatalf("%s gating wrong", name)
		}
	}
}

func TestOpsSourceToolsCreateUpdateList(t *testing.T) {
	s := newOpsTestServer(t)
	// 上游假源（供 baseUrl 校验跳过 SSRF：测试 Server 已设 skipOutboundValidation）。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}]}`))
	}))
	defer upstream.Close()

	create := &createSourceTool{server: s}
	result := opsExecute(t, create, fmt.Sprintf(`{"name":"测试源","baseUrl":%q,"platform":"openai","apiKey":"sk-very-secret-key-123","manualModels":["m-a","m-b"]}`, upstream.URL))
	if !result.OK {
		t.Fatalf("create: %s", result.Summary)
	}
	data := result.Data.(map[string]any)
	view := data
	if masked, _ := view["apiKeyMasked"].(string); masked == "" || strings.Contains(masked, "very-secret") {
		t.Fatalf("api key not masked: %q", masked)
	}
	if view["modelCount"] != 2 {
		t.Fatalf("manual models not synced: %#v", view)
	}

	// 落库验证：密钥不以明文出现在 list_sources 结果中
	list := &listSourcesTool{server: s}
	listResult := opsExecute(t, list, `{}`)
	if !listResult.OK {
		t.Fatalf("list: %s", listResult.Summary)
	}
	listData, _ := listResult.Data.(map[string]any)
	items, _ := listData["sources"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("sources = %d", len(items))
	}
	if id, _ := items[0]["id"].(string); id != createID("测试源") && id == "" {
		t.Fatalf("source id missing")
	}

	// 更新：留空 key 保留原值；切换启停
	update := &updateSourceTool{server: s}
	result = opsExecute(t, update, `{"source":"测试源","enabled":false}`)
	if !result.OK {
		t.Fatalf("update: %s", result.Summary)
	}
	sources, _ := s.store.ListSources(context.Background())
	if len(sources) != 1 {
		t.Fatalf("source count = %d", len(sources))
	}
	if sources[0].Enabled {
		t.Fatalf("source not disabled")
	}
	if sources[0].APIKey == "" {
		t.Fatalf("api key wiped by empty-key update")
	}

	// SSRF 拒绝：内网地址（skipOutboundValidation=false 的服务器）
	strict := newOpsTestServer(t)
	strict.skipOutboundValidation = false
	strictTool := &createSourceTool{server: strict}
	result = opsExecute(t, strictTool, `{"name":"内网","baseUrl":"http://192.168.1.1/v1"}`)
	if result.OK {
		t.Fatalf("private IP baseUrl must be rejected")
	}
}

func createID(name string) string { return slugID(name) }

func TestOpsSourceGroupTools(t *testing.T) {
	s := newOpsTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	if result := opsExecute(t, &createSourceTool{server: s}, fmt.Sprintf(`{"name":"src1","baseUrl":%q}`, upstream.URL)); !result.OK {
		t.Fatalf("create source: %s", result.Summary)
	}

	create := &createGroupTool{server: s}
	result := opsExecute(t, create, `{"name":"主力组","models":["src1:m-a","src1:m-b"],"strategy":"random"}`)
	if !result.OK {
		t.Fatalf("create group: %s", result.Summary)
	}
	// 重名拒绝
	if result := opsExecute(t, create, `{"name":"主力组"}`); result.OK {
		t.Fatalf("duplicate group name must fail")
	}

	// 成员增删 + 启停
	update := &updateGroupTool{server: s}
	if result := opsExecute(t, update, `{"group":"主力组","addModels":["src1:m-c"],"removeModels":["src1:m-b"],"enabled":false}`); !result.OK {
		t.Fatalf("update group: %s", result.Summary)
	}
	groups, _ := s.store.ListGroups(context.Background())
	if len(groups) != 1 {
		t.Fatalf("groups = %d", len(groups))
	}
	group := groups[0]
	if len(group.Models) != 2 || group.Enabled {
		t.Fatalf("group state wrong: %+v", group)
	}
	joined := strings.Join(group.Models, ",")
	if !strings.Contains(joined, "m-a") || !strings.Contains(joined, "m-c") || strings.Contains(joined, "m-b") {
		t.Fatalf("members wrong: %v", group.Models)
	}

	// list_model_groups 可见
	if result := opsExecute(t, &listGroupsTool{server: s}, `{}`); !result.OK {
		t.Fatalf("list groups: %s", result.Summary)
	}
}

func TestOpsUsageQueryTools(t *testing.T) {
	s := newOpsTestServer(t)
	// 造两条用量记录：一条成功一条失败
	seed := func(id string, statusCode int, model string) {
		payload := fmt.Sprintf(`{"requestId":%q,"modelName":%q,"statusCode":%d}`, id, model, statusCode)
		summary := storage.UsageLogItem{RequestID: id, ModelName: model, StatusCode: statusCode, Platform: "openai", StartedAt: time.Now().Add(-time.Minute)}
		if err := s.store.SaveUsageRecordJSON(context.Background(), []byte(payload), summary, time.Now()); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("req-ok-1", 200, "m1")
	seed("req-fail-1", 502, "m1")

	stats := opsExecute(t, &usageStatsTool{server: s}, `{"days":1}`)
	if !stats.OK {
		t.Fatalf("stats: %s", stats.Summary)
	}
	logs := opsExecute(t, &usageLogsTool{server: s}, `{"days":1,"status":"failed"}`)
	if !logs.OK {
		t.Fatalf("logs: %s", logs.Summary)
	}
	encoded, _ := json.Marshal(logs.Data)
	if !strings.Contains(string(encoded), "req-fail-1") || strings.Contains(string(encoded), "req-ok-1") {
		t.Fatalf("failed filter wrong: %s", encoded)
	}
	detail := opsExecute(t, &usageLogDetailTool{server: s}, `{"requestId":"req-fail-1"}`)
	if !detail.OK {
		t.Fatalf("detail: %s", detail.Summary)
	}

	trend := opsExecute(t, &usageTrendTool{server: s}, `{"days":1}`)
	if !trend.OK {
		t.Fatalf("trend: %s", trend.Summary)
	}
	trendData, _ := trend.Data.(map[string]any)
	chart, _ := trendData["chart"].(map[string]any)
	if chart == nil || chart["type"] != "bar" {
		t.Fatalf("trend chart spec missing: %#v", trendData)
	}

	syslogs := opsExecute(t, &systemLogsTool{server: s}, `{"limit":5}`)
	if !syslogs.OK {
		t.Fatalf("system logs: %s", syslogs.Summary)
	}
}

func TestOpsRefreshSourceGatedLiveTest(t *testing.T) {
	s := newOpsTestServer(t)
	tool := &refreshSourceTool{server: s}
	if !tool.Gated() || tool.PermissionKey() != "live_test" {
		t.Fatalf("refresh gating wrong")
	}
	// 不存在的源直接报错
	if result := opsExecute(t, tool, `{"source":"nope"}`); result.OK {
		t.Fatalf("missing source must fail")
	}
}

func TestOpsUpdateSourceUnknownRef(t *testing.T) {
	s := newOpsTestServer(t)
	if result := opsExecute(t, &updateSourceTool{server: s}, `{"source":"ghost"}`); result.OK {
		t.Fatalf("unknown source must fail")
	}
	if result := opsExecute(t, &updateGroupTool{server: s}, `{"group":"ghost"}`); result.OK {
		t.Fatalf("unknown group must fail")
	}
}

// update_plan 工具：方案校验、落库与事件。
func TestOpsUpdatePlanTool(t *testing.T) {
	tool := &updatePlanTool{}
	if tool.Gated() {
		t.Fatalf("plan tool must not be gated")
	}
	// 空方案拒绝
	if result := opsExecute(t, tool, `{"plan":[]}`); result.OK {
		t.Fatalf("empty plan must fail")
	}
	// 非法状态拒绝
	if result := opsExecute(t, tool, `{"plan":[{"title":"a","status":"oops"}]}`); result.OK {
		t.Fatalf("invalid status must fail")
	}

	s := newOpsTestServer(t)
	created, err := s.store.CreateAgentSession(context.Background(), storage.AgentSessionUpsert{Mode: "create"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// 带真实会话上下文执行：SetPlan 落库
	engine := s.protocolAgentEngine()
	if engine == nil {
		t.Fatalf("engine unavailable")
	}
	session, err := s.store.GetSession(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	tctx := &sessionToolContext{ctx: context.Background(), store: s.store, session: session}
	result := (&updatePlanTool{}).Execute(context.Background(), tctx, json.RawMessage(
		`{"plan":[{"title":"分析文档","status":"done"},{"title":"生成草稿","status":"in_progress"},{"title":"真实测试","status":"pending"}]}`))
	if !result.OK {
		t.Fatalf("update_plan: %s", result.Summary)
	}
	reloaded, _ := s.store.GetSession(context.Background(), created.ID)
	if len(reloaded.Plan) != 3 || reloaded.Plan[0].Status != "done" || reloaded.Plan[1].Status != "in_progress" {
		t.Fatalf("plan roundtrip: %+v", reloaded.Plan)
	}
}

// sessionToolContext 直接包装 store+session 的真实 ToolContext（复用引擎实现语义）。
type sessionToolContext struct {
	ctx     context.Context
	store   *storage.Store
	session *agent.Session
}

func (c *sessionToolContext) SessionMeta() agent.SessionMeta {
	return agent.SessionMeta{ID: c.session.ID, Mode: c.session.Mode, Settings: c.session.Settings}
}
func (c *sessionToolContext) Draft() json.RawMessage               { return c.session.DraftConfig }
func (c *sessionToolContext) SetDraft(draft json.RawMessage) error { return nil }
func (c *sessionToolContext) TestTarget() (string, string) {
	return c.session.TestBaseURL, c.session.TestAPIKey
}
func (c *sessionToolContext) SetTestTarget(baseURL, apiKey string) error {
	update := agent.SessionStateUpdate{}
	if strings.TrimSpace(baseURL) != "" {
		update.TestBaseURL = baseURL
		c.session.TestBaseURL = baseURL
	}
	if strings.TrimSpace(apiKey) != "" {
		update.TestAPIKey = apiKey
		c.session.TestAPIKey = apiKey
	}
	if update.TestBaseURL == "" && update.TestAPIKey == "" {
		return nil
	}
	return c.store.UpdateSessionState(c.ctx, c.session.ID, update)
}
func (c *sessionToolContext) SetTitle(title string) error {
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, agent.SessionStateUpdate{Title: title}); err != nil {
		return err
	}
	c.session.Title = title
	return nil
}
func (c *sessionToolContext) SetPlan(steps []agent.PlanStep) error {
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, agent.SessionStateUpdate{Plan: steps}); err != nil {
		return err
	}
	c.session.Plan = steps
	return nil
}

func (c *sessionToolContext) SetPlanSummary(summary string) error {
	value := summary
	if err := c.store.UpdateSessionState(c.ctx, c.session.ID, agent.SessionStateUpdate{PlanSummary: &value}); err != nil {
		return err
	}
	c.session.PlanSummary = summary
	return nil
}

// test_upstream 凭证参数：参数优先、成功后自动记住（sessionToolContext 的
// SetTestTarget 直接复用引擎语义写库）。
func TestOpsTestUpstreamCredentialParams(t *testing.T) {
	s := newOpsTestServer(t)
	created, _ := s.store.CreateAgentSession(context.Background(), storage.AgentSessionUpsert{Mode: "create"})
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":{"text":"ok"},"finish":"stop"}`))
	}))
	defer vendor.Close()
	if err := s.store.UpdateSessionState(context.Background(), created.ID, stateUpdateWithDraft(json.RawMessage(agentTestConfig("cred-proto")))); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	session, _ := s.store.GetSession(context.Background(), created.ID)
	tctx := &sessionToolContext{ctx: context.Background(), store: s.store, session: session}

	// 无参数且会话无 baseUrl → 明确报缺少目标
	result := (&testUpstreamTool{server: s}).Execute(context.Background(), tctx, json.RawMessage(`{}`))
	if result.OK || !strings.Contains(result.Summary, "baseUrl") {
		t.Fatalf("missing target must fail with baseUrl hint: %s", result.Summary)
	}

	// 参数凭证（真实工具执行，含 SetTestTarget 自动记住）
	result = (&testUpstreamTool{server: s}).Execute(context.Background(), tctx,
		json.RawMessage(fmt.Sprintf(`{"baseUrl":%q,"apiKey":"sk-param-key"}`, vendor.URL)))
	if !result.OK {
		t.Fatalf("test_upstream: %s", result.Summary)
	}
	reloaded, _ := s.store.GetSession(context.Background(), created.ID)
	if reloaded.TestBaseURL != vendor.URL || reloaded.TestAPIKey != "sk-param-key" {
		t.Fatalf("credentials not remembered: %q %q", reloaded.TestBaseURL, reloaded.TestAPIKey)
	}

	// 无参数重试：回退到已记住的凭证，仍然成功
	session2, _ := s.store.GetSession(context.Background(), created.ID)
	tctx2 := &sessionToolContext{ctx: context.Background(), store: s.store, session: session2}
	if result := (&testUpstreamTool{server: s}).Execute(context.Background(), tctx2, json.RawMessage(`{}`)); !result.OK {
		t.Fatalf("fallback to remembered credentials failed: %s", result.Summary)
	}
}

// 出站策略工具：只读查询、整体替换、非法 CIDR 拒绝、恢复默认。
func TestOutboundPolicyTool(t *testing.T) {
	s := newOpsTestServer(t)
	// 落盘需要真实 config.json：给集成服务器换上带路径的配置。
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	s.config = cfg
	tool := &outboundPolicyTool{server: s}
	t.Cleanup(func() { relay.SetDeniedIPRanges(relay.DefaultDeniedIPRanges) })

	// 只读：返回当前（默认预置）与默认列表，且不下发改动。
	read := opsExecute(t, tool, `{}`)
	if !read.OK {
		t.Fatalf("read-only call failed: %+v", read)
	}
	readData, _ := read.Data.(map[string]any)
	if _, ok := readData["defaultDeniedIpRanges"]; !ok {
		t.Fatalf("read-only result should include default ranges")
	}

	// 替换：移除环回段后 127.0.0.1 放行、私网仍拦；同步已下发到 relay。
	trimmed := []string{}
	for _, entry := range relay.DefaultDeniedIPRanges {
		if entry != "127.0.0.0/8" {
			trimmed = append(trimmed, entry)
		}
	}
	encoded, _ := json.Marshal(trimmed)
	replaced := opsExecute(t, tool, fmt.Sprintf(`{"ranges":%s}`, encoded))
	if !replaced.OK {
		t.Fatalf("replace failed: %+v", replaced)
	}
	if relay.IsDeniedIP(net.ParseIP("127.0.0.1")) {
		t.Fatalf("loopback should be allowed after removing 127.0.0.0/8")
	}
	if !relay.IsDeniedIP(net.ParseIP("10.0.0.1")) {
		t.Fatalf("private range should remain denied")
	}
	// 落盘检查：config.json 的 outbound 块同步更新。
	if ranges := s.config.GetOutboundConfig().DeniedIPRanges; len(ranges) != len(trimmed) {
		t.Fatalf("config not updated, got %d ranges", len(ranges))
	}

	// 非法 CIDR 拒绝且不产生副作用。
	if bad := opsExecute(t, tool, `{"ranges":["10.0.0.0/not-a-cidr"]}`); bad.OK {
		t.Fatalf("invalid CIDR must be rejected")
	}

	// 恢复默认。
	if reset := opsExecute(t, tool, `{"resetDefault":true}`); !reset.OK {
		t.Fatalf("reset failed: %+v", reset)
	}
	if !relay.IsDeniedIP(net.ParseIP("127.0.0.1")) {
		t.Fatalf("loopback should be denied again after reset")
	}
}

// 回归（质量轮 A2）：clampInt 曾把「小于默认值的合法入参」当未传抬升——
// days=3 应保留 3 天窗口、limit=5 应保留 5 条。
func TestDefaultIntKeepsSmallValues(t *testing.T) {
	cases := []struct{ v, def, max, want int }{
		{0, 7, 366, 7},     // 未传 → 默认
		{-2, 7, 366, 7},    // 非正 → 默认
		{3, 7, 366, 3},     // 合法小值保留
		{1, 7, 366, 1},     // 边界最小合法值
		{400, 7, 366, 366}, // 超限钳到上限
		{5, 20, 100, 5},    // limit 语义同款
	}
	for _, tc := range cases {
		if got := defaultInt(tc.v, tc.def, tc.max); got != tc.want {
			t.Fatalf("defaultInt(%d,%d,%d) = %d, want %d", tc.v, tc.def, tc.max, got, tc.want)
		}
	}
}
