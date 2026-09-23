package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// 运维工具：模型源 / 模型组管理 + 用量统计与日志查询。与协议工具共用
// agent.Tool 抽象与审批门控：读取不门控；持久化写入门控 save（「写入配置」）；
// 真实出站（模型拉取）门控 live_test。写入复用管理端点的同套校验
//（SSRF 防护、自定义协议校验、重名检查、路由缓存失效）。

const (
	agentToolListSources   = "list_sources"
	agentToolListGroups    = "list_model_groups"
	agentToolUsageStats    = "query_usage_stats"
	agentToolUsageTrend    = "query_usage_trend"
	agentToolUsageLogs     = "query_usage_logs"
	agentToolUsageDetail   = "get_usage_log_detail"
	agentToolSystemLogs    = "query_system_logs"
	agentToolCreateSource  = "create_model_source"
	agentToolUpdateSource  = "update_model_source"
	agentToolRefreshSource = "refresh_model_source"
	agentToolCreateGroup   = "create_model_group"
	agentToolUpdateGroup   = "update_model_group"
	agentToolOutbound      = "update_outbound_policy"
)

// newAgentOpsTools 返回运维域全量工具。
func newAgentOpsTools(s *Server) []agent.Tool {
	return []agent.Tool{
		&listSourcesTool{server: s},
		&listGroupsTool{server: s},
		&usageStatsTool{server: s},
		&usageTrendTool{server: s},
		&usageLogsTool{server: s},
		&usageLogDetailTool{server: s},
		&systemLogsTool{server: s},
		&createSourceTool{server: s},
		&updateSourceTool{server: s},
		&refreshSourceTool{server: s},
		&createGroupTool{server: s},
		&updateGroupTool{server: s},
		&outboundPolicyTool{server: s},
	}
}

// toolStore 取存储并在不可用时返回统一的失败 ToolResult。
// 14 处工具 Execute 开头的样板由此收敛为一行守卫。
func toolStore(server *Server) (*storage.Store, agent.ToolResult) {
	if server.store == nil {
		return nil, agent.ToolError("存储不可用", "store_unavailable")
	}
	return server.store, agent.ToolResult{}
}

// ---- 时间窗与查找辅助 ----

// 用量查询的窗口与限额锚点。
const (
	usageDefaultDays       = 7
	usageMaxDays           = 366
	usageLogsDefaultLimit  = 20
	usageLogsMaxLimit      = 100
	systemLogsDefaultLimit = 30
	systemLogsMaxLimit     = 100
	maxUTCOffsetMinutes    = 840 // UsageDaily 允许的最大时区偏移
)

// usageQueryParams 是用量类工具共享的参数集：一次解码，时间窗与过滤器共用。
type usageQueryParams struct {
	Days       int    `json:"days"`
	From       string `json:"from"`
	To         string `json:"to"`
	ModelName  string `json:"modelName"`
	KeyName    string `json:"keyName"`
	GroupName  string `json:"groupName"`
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode"`
	Limit      int    `json:"limit"`
}

func decodeUsageQueryArgs(args json.RawMessage) usageQueryParams {
	var params usageQueryParams
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}
	return params
}

// usageWindow 解析查询时间窗：days（默认 7、上限 366）或 from/to（RFC3339）。
func (p usageQueryParams) usageWindow() (from, to time.Time, err error) {
	to = time.Now()
	if strings.TrimSpace(p.To) != "" {
		parsed, perr := time.Parse(time.RFC3339, strings.TrimSpace(p.To))
		if perr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to 不是合法的 RFC3339 时间: %w", perr)
		}
		to = parsed
	}
	if strings.TrimSpace(p.From) != "" {
		parsed, perr := time.Parse(time.RFC3339, strings.TrimSpace(p.From))
		if perr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from 不是合法的 RFC3339 时间: %w", perr)
		}
		return parsed, to, nil
	}
	return to.AddDate(0, 0, -clampInt(p.Days, usageDefaultDays, usageMaxDays)), to, nil
}

func (p usageQueryParams) usageQuery() (storage.UsageQuery, error) {
	from, to, err := p.usageWindow()
	if err != nil {
		return storage.UsageQuery{}, err
	}
	status := strings.ToLower(strings.TrimSpace(p.Status))
	if status != "success" && status != "failed" {
		status = ""
	}
	return storage.UsageQuery{
		From: from, To: to,
		ModelName:  strings.TrimSpace(p.ModelName),
		KeyName:    strings.TrimSpace(p.KeyName),
		GroupName:  strings.TrimSpace(p.GroupName),
		Status:     status,
		StatusCode: p.StatusCode,
	}, nil
}

// clampInt 把 v 钳进 [min, max]；v 落在零值区（未传）时取 min。
func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// agentLocalUTCOffset 本地时区偏移（分钟，钳制到 UsageDaily 允许范围）。
func agentLocalUTCOffset() int {
	_, seconds := time.Now().Zone()
	return clampInt(seconds/60, -maxUTCOffsetMinutes, maxUTCOffsetMinutes)
}

// agentFindSource 按 id 或名称查找模型源。
func agentFindSource(ctx context.Context, store *storage.Store, ref string) (storage.ModelSource, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return storage.ModelSource{}, false
	}
	sources, err := store.ListSources(ctx)
	if err != nil {
		return storage.ModelSource{}, false
	}
	for _, source := range sources {
		if source.ID == ref {
			return source, true
		}
	}
	for _, source := range sources {
		if strings.EqualFold(source.Name, ref) {
			return source, true
		}
	}
	return storage.ModelSource{}, false
}

func agentFindGroup(ctx context.Context, store *storage.Store, ref string) (storage.ModelGroup, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return storage.ModelGroup{}, false
	}
	groups, err := store.ListGroups(ctx)
	if err != nil {
		return storage.ModelGroup{}, false
	}
	for _, group := range groups {
		if group.ID == ref || strings.EqualFold(group.Name, ref) {
			return group, true
		}
	}
	return storage.ModelGroup{}, false
}

// agentManualModels 把模型名列表组装为手动模型行（与手动源存储形态一致）。
func agentManualModels(source storage.ModelSource, names []string) []storage.Model {
	models := make([]storage.Model, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		models = append(models, storage.Model{
			ID: name, Name: name, SourceID: source.ID, SourceName: source.Name,
			BaseURL: source.BaseURL, Platform: source.Platform,
			Type: "llm", Enabled: true, Available: true, Origin: "manual",
		})
	}
	return models
}

// agentSourceView 源的安全视图（密钥脱敏）。
func agentSourceView(source storage.ModelSource, modelCount int) map[string]any {
	return map[string]any{
		"id": source.ID, "name": source.Name, "baseUrl": source.BaseURL,
		"platform": source.Platform, "enabled": source.Enabled,
		"autoFetchModels": source.AutoFetchModels,
		"keyStrategy":     source.KeyStrategy,
		"apiKeyMasked":    maskSecret(source.APIKey),
		"keyCount":        len(source.APIKeys),
		"modelCount":      modelCount,
	}
}

// readOnlyMeta 只读查询工具的元数据：同批可并行，结果保留头部。
func readOnlyMeta() agent.ToolMeta {
	return agent.ToolMeta{ConcurrentSafe: true, RiskLevel: "low", PreviewDirection: "head"}
}

func (t *listSourcesTool) Meta() agent.ToolMeta    { return readOnlyMeta() }
func (t *listGroupsTool) Meta() agent.ToolMeta     { return readOnlyMeta() }
func (t *usageStatsTool) Meta() agent.ToolMeta     { return readOnlyMeta() }
func (t *usageTrendTool) Meta() agent.ToolMeta     { return readOnlyMeta() }
func (t *usageLogsTool) Meta() agent.ToolMeta      { return readOnlyMeta() }
func (t *usageLogDetailTool) Meta() agent.ToolMeta { return readOnlyMeta() }
func (t *systemLogsTool) Meta() agent.ToolMeta     { return readOnlyMeta() }

func agentSourceModelCounts(ctx context.Context, store *storage.Store) map[string]int {
	models, err := store.ListModels(ctx)
	if err != nil {
		return map[string]int{}
	}
	counts := make(map[string]int, len(models))
	for _, model := range models {
		counts[model.SourceID]++
	}
	return counts
}

// ---- list_sources ----

type listSourcesTool struct{ server *Server }

func (t *listSourcesTool) Name() string          { return agentToolListSources }
func (t *listSourcesTool) Description() string   { return "列出全部模型源（密钥脱敏）" }
func (t *listSourcesTool) Gated() bool           { return false }
func (t *listSourcesTool) PermissionKey() string { return "" }

func (t *listSourcesTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolListSources,
		Description: "列出全部模型源：平台、baseUrl、启停、自动拉取、密钥策略（脱敏）、模型数与最近拉取状态。不显示任何密钥明文。",
		Parameters:  objectSchema(map[string]any{}),
	}
}

func (t *listSourcesTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	sources, err := store.ListSources(ctx)
	if err != nil {
		return agent.ToolError("读取失败: "+err.Error(), err.Error())
	}
	counts := agentSourceModelCounts(ctx, store)
	items := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		view := agentSourceView(source, counts[source.ID])
		state := t.server.sourceRefreshStateOf(source.ID)
		if state.Refreshing {
			view["refreshing"] = true
		}
		if state.LastFinishedAt != "" {
			view["lastRefreshAt"] = state.LastFinishedAt
			view["lastRefreshCount"] = state.LastCount
			if state.LastError != "" {
				view["lastRefreshError"] = state.LastError
			}
		}
		items = append(items, view)
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("共 %d 个模型源", len(items)), Data: map[string]any{"sources": items}}
}

// ---- list_model_groups ----

type listGroupsTool struct{ server *Server }

func (t *listGroupsTool) Name() string          { return agentToolListGroups }
func (t *listGroupsTool) Description() string   { return "列出全部模型组及成员" }
func (t *listGroupsTool) Gated() bool           { return false }
func (t *listGroupsTool) PermissionKey() string { return "" }

func (t *listGroupsTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolListGroups,
		Description: "列出全部模型组：名称、启停、策略、重试、并发/限额与成员（sourceId:modelId 引用，也可能只显示模型 id）。",
		Parameters:  objectSchema(map[string]any{}),
	}
}

func (t *listGroupsTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	groups, err := store.ListGroups(ctx)
	if err != nil {
		return agent.ToolError("读取失败: "+err.Error(), err.Error())
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("共 %d 个模型组", len(groups)), Data: map[string]any{"groups": groups}}
}

// ---- query_usage_stats ----

type usageStatsTool struct{ server *Server }

func (t *usageStatsTool) Name() string          { return agentToolUsageStats }
func (t *usageStatsTool) Description() string   { return "查询用量统计汇总与模型分布" }
func (t *usageStatsTool) Gated() bool           { return false }
func (t *usageStatsTool) PermissionKey() string { return "" }

func (t *usageStatsTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUsageStats,
		Description: "查询网关用量统计：请求量/成功失败/token 消耗/缓存命中/平均耗时汇总 + 按模型分布。" +
			"时间窗用 days（默认 7）或 from/to（RFC3339）；可按模型名、key 名、模型组过滤。",
		Parameters: objectSchema(map[string]any{
			"days":      map[string]any{"type": "integer", "description": "最近 N 天（默认 7，最大 366）"},
			"from":      map[string]any{"type": "string", "description": "起始时间（RFC3339），与 to 搭配"},
			"to":        map[string]any{"type": "string", "description": "结束时间（RFC3339），默认现在"},
			"modelName": map[string]any{"type": "string", "description": "按模型名过滤"},
			"keyName":   map[string]any{"type": "string", "description": "按 API token 名过滤"},
			"groupName": map[string]any{"type": "string", "description": "按模型组过滤"},
		}),
	}
}

func (t *usageStatsTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	params := decodeUsageQueryArgs(args)
	query, err := params.usageQuery()
	if err != nil {
		return agent.ToolError(err.Error(), err.Error())
	}
	totals, err := store.UsageTotals(ctx, query)
	if err != nil {
		return agent.ToolError("统计查询失败: "+err.Error(), err.Error())
	}
	byModel, err := store.UsageByModel(ctx, query)
	if err != nil {
		return agent.ToolError("模型分布查询失败: "+err.Error(), err.Error())
	}
	requests, _ := totals["requests"].(int64)
	if requests == 0 {
		if v, ok := totals["requests"].(int); ok {
			requests = int64(v)
		}
	}
	summary := fmt.Sprintf("窗口内 %v 次请求", requests)
	return agent.ToolResult{OK: true, Summary: summary, Data: map[string]any{
		"window": map[string]any{"from": query.From.Format(time.RFC3339), "to": query.To.Format(time.RFC3339)},
		"totals": totals, "byModel": byModel,
	}}
}

// ---- query_usage_trend ----

type usageTrendTool struct{ server *Server }

func (t *usageTrendTool) Name() string          { return agentToolUsageTrend }
func (t *usageTrendTool) Description() string   { return "查询用量按日趋势" }
func (t *usageTrendTool) Gated() bool           { return false }
func (t *usageTrendTool) PermissionKey() string { return "" }

func (t *usageTrendTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUsageTrend,
		Description: "查询用量按日趋势（请求数/成功失败/token），适合生成趋势图表。窗口与过滤参数同 query_usage_stats。",
		Parameters: objectSchema(map[string]any{
			"days":      map[string]any{"type": "integer", "description": "最近 N 天（默认 7）"},
			"from":      map[string]any{"type": "string"},
			"to":        map[string]any{"type": "string"},
			"modelName": map[string]any{"type": "string"},
			"keyName":   map[string]any{"type": "string"},
			"groupName": map[string]any{"type": "string"},
		}),
	}
}

func (t *usageTrendTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	params := decodeUsageQueryArgs(args)
	query, err := params.usageQuery()
	if err != nil {
		return agent.ToolError(err.Error(), err.Error())
	}
	buckets, err := store.UsageDaily(ctx, query, agentLocalUTCOffset())
	if err != nil {
		return agent.ToolError("趋势查询失败: "+err.Error(), err.Error())
	}
	// 直接给出图表 spec，模型侧可原样交给 ```chart 或自行组织。
	dates := make([]string, 0, len(buckets))
	requests := make([]int, 0, len(buckets))
	tokens := make([]int, 0, len(buckets))
	for _, bucket := range buckets {
		dates = append(dates, bucket.Date)
		requests = append(requests, bucket.Requests)
		tokens = append(tokens, bucket.Tokens)
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("%d 个自然日", len(buckets)), Data: map[string]any{
		"buckets": buckets,
		"chart": map[string]any{
			"type": "bar", "title": "每日请求量与 Token 消耗",
			"x": dates,
			"series": []map[string]any{
				{"name": "请求数", "data": requests},
				{"name": "Token", "data": tokens},
			},
		},
	}}
}

// ---- query_usage_logs ----

type usageLogsTool struct{ server *Server }

func (t *usageLogsTool) Name() string          { return agentToolUsageLogs }
func (t *usageLogsTool) Description() string   { return "查询调用日志（可过滤错误）" }
func (t *usageLogsTool) Gated() bool           { return false }
func (t *usageLogsTool) PermissionKey() string { return "" }

func (t *usageLogsTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUsageLogs,
		Description: "查询调用日志列表（新→旧）：状态码、错误与错误类别、模型、key、耗时、token。status=failed 只看失败请求；" +
			"需要深入某个请求时用 get_usage_log_detail 取捕获的请求/响应体。",
		Parameters: objectSchema(map[string]any{
			"status":     map[string]any{"type": "string", "description": "success | failed"},
			"statusCode": map[string]any{"type": "integer", "description": "精确状态码过滤"},
			"modelName":  map[string]any{"type": "string"},
			"keyName":    map[string]any{"type": "string"},
			"groupName":  map[string]any{"type": "string"},
			"days":       map[string]any{"type": "integer", "description": "最近 N 天（默认 7）"},
			"from":       map[string]any{"type": "string"},
			"to":         map[string]any{"type": "string"},
			"limit":      map[string]any{"type": "integer", "description": "返回条数（默认 20，最大 100）"},
		}),
	}
}

func (t *usageLogsTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	params := decodeUsageQueryArgs(args)
	query, err := params.usageQuery()
	if err != nil {
		return agent.ToolError(err.Error(), err.Error())
	}
	query.Limit = clampInt(params.Limit, usageLogsDefaultLimit, usageLogsMaxLimit)
	total, items, err := store.QueryUsageLogs(ctx, query)
	if err != nil {
		return agent.ToolError("日志查询失败: "+err.Error(), err.Error())
	}
	failed := 0
	for _, item := range items {
		if item.StatusCode < 200 || item.StatusCode >= 400 {
			failed++
		}
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("命中 %d 条（失败 %d），返回最新 %d 条", total, failed, len(items)), Data: map[string]any{
		"total": total, "items": items,
	}}
}

// ---- get_usage_log_detail ----

type usageLogDetailTool struct{ server *Server }

func (t *usageLogDetailTool) Name() string { return agentToolUsageDetail }
func (t *usageLogDetailTool) Description() string {
	return "读取单条调用日志详情（含捕获体）"
}
func (t *usageLogDetailTool) Gated() bool           { return false }
func (t *usageLogDetailTool) PermissionKey() string { return "" }

func (t *usageLogDetailTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUsageDetail,
		Description: "按 requestId 读取单条调用日志的完整记录：四段捕获体（入站/出站/上游响应/回给客户端的响应）、重试链、错误详情。" +
			"用于深入分析失败请求。",
		Parameters: objectSchema(map[string]any{
			"requestId": map[string]any{"type": "string", "description": "调用日志的 requestId"},
		}, "requestId"),
	}
}

func (t *usageLogDetailTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	var params struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	if strings.TrimSpace(params.RequestID) == "" {
		return agent.ToolError("缺少 requestId", "missing_request_id")
	}
	raw, found, err := store.GetUsageRecordJSON(ctx, strings.TrimSpace(params.RequestID))
	if err != nil {
		return agent.ToolError("读取失败: "+err.Error(), err.Error())
	}
	if !found {
		return agent.ToolError("记录不存在", "not_found")
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		record = map[string]any{"raw": string(raw)}
	}
	return agent.ToolResult{OK: true, Summary: "已读取 " + params.RequestID, Data: record}
}

// ---- query_system_logs ----

type systemLogsTool struct{ server *Server }

func (t *systemLogsTool) Name() string          { return agentToolSystemLogs }
func (t *systemLogsTool) Description() string   { return "查询系统日志" }
func (t *systemLogsTool) Gated() bool           { return false }
func (t *systemLogsTool) PermissionKey() string { return "" }

func (t *systemLogsTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolSystemLogs,
		Description: "查询系统运行日志（info/warn/error 级别），排查网关自身问题时使用。",
		Parameters: objectSchema(map[string]any{
			"level": map[string]any{"type": "string", "description": "info | warn | error（缺省全部）"},
			"limit": map[string]any{"type": "integer", "description": "返回条数（默认 30，最大 100）"},
		}),
	}
}

func (t *systemLogsTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	var params struct {
		Level string `json:"level"`
		Limit int    `json:"limit"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}
	switch strings.ToLower(strings.TrimSpace(params.Level)) {
	case "info", "warn", "error":
		params.Level = strings.ToLower(strings.TrimSpace(params.Level))
	default:
		params.Level = ""
	}
	params.Limit = clampInt(params.Limit, systemLogsDefaultLimit, systemLogsMaxLimit)
	total, items, err := store.QuerySystemLogs(ctx, params.Limit, 0, params.Level)
	if err != nil {
		return agent.ToolError("查询失败: "+err.Error(), err.Error())
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("共 %d 条，返回 %d 条", total, len(items)), Data: map[string]any{"items": items}}
}

// ---- create_model_source（门控 save）----

type createSourceTool struct{ server *Server }

func (t *createSourceTool) Name() string          { return agentToolCreateSource }
func (t *createSourceTool) Description() string   { return "创建模型源（需审批）" }
func (t *createSourceTool) Gated() bool           { return true }
func (t *createSourceTool) PermissionKey() string { return agent.PermissionKeySave }
func (t *createSourceTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "high", PreviewDirection: "head"}
}

func (t *createSourceTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolCreateSource,
		Description: "创建模型源（用户审批后生效）。platform 支持 openai/anthropic/gemini/responses 或 custom:<协议ID>；" +
			"autoFetchModels=true 自动拉取模型列表（创建后需另经 refresh_model_source 拉取，或用户在页面手动拉取）；" +
			"手动模型用手动列表或 manualModels。创建前先向用户确认 baseUrl、平台与密钥来源。",
		Parameters: objectSchema(map[string]any{
			"name":            map[string]any{"type": "string", "description": "源名称（显示用）"},
			"baseUrl":         map[string]any{"type": "string", "description": "上游 baseUrl（http/https）"},
			"platform":        map[string]any{"type": "string", "description": "openai（默认）/anthropic/gemini/responses/custom:<id>"},
			"apiKey":          map[string]any{"type": "string", "description": "API key（加密存储）"},
			"autoFetchModels": map[string]any{"type": "boolean", "description": "是否自动拉取模型列表"},
			"manualModels":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "手动模型名列表"},
			"fetchBaseUrl":    map[string]any{"type": "string", "description": "模型列表拉取地址（缺省同 baseUrl）"},
		}, "name", "baseUrl"),
	}
}

func (t *createSourceTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	var params struct {
		Name            string   `json:"name"`
		BaseURL         string   `json:"baseUrl"`
		Platform        string   `json:"platform"`
		APIKey          string   `json:"apiKey"`
		AutoFetchModels *bool    `json:"autoFetchModels"`
		ManualModels    []string `json:"manualModels"`
		FetchBaseURL    string   `json:"fetchBaseUrl"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	if strings.TrimSpace(params.Name) == "" || strings.TrimSpace(params.BaseURL) == "" {
		return agent.ToolError("name 与 baseUrl 必填", "missing_fields")
	}
	platform := strings.TrimSpace(params.Platform)
	if platform == "" {
		platform = "openai"
	}
	item := storage.ModelSource{
		ID: slugID(params.Name), Name: strings.TrimSpace(params.Name),
		BaseURL: strings.TrimSpace(params.BaseURL), APIKey: params.APIKey,
		Platform: platform, Enabled: true,
	}
	// slugID 撞 id 即静默覆盖既有源（UpsertSource 是纯 upsert，连 API key 一起
	// 换掉）——「创建」审批卡实际产生破坏性修改。重名直接拒绝，改走 update。
	if existing, found := agentFindSource(ctx, store, item.ID); found {
		return agent.ToolResult{OK: false,
			Summary: fmt.Sprintf("已存在同名模型源 %q（id=%s），如需修改请用 update_model_source", existing.Name, existing.ID),
			Data:    map[string]any{"error": "duplicate_source", "id": existing.ID}}
	}
	if params.AutoFetchModels != nil {
		item.AutoFetchModels = *params.AutoFetchModels
	}
	if strings.TrimSpace(params.FetchBaseURL) != "" {
		item.FetchBaseURL = strings.TrimSpace(params.FetchBaseURL)
	}
	if err := t.server.validateAgentSource(ctx, &item, nil); err != nil {
		return agent.ToolError("校验失败: "+err.Error(), err.Error())
	}
	if err := store.UpsertSource(ctx, item); err != nil {
		return agent.ToolError("保存失败: "+err.Error(), err.Error())
	}
	created, _ := agentFindSource(ctx, store, item.ID)
	if len(params.ManualModels) > 0 {
		models := agentManualModels(created, params.ManualModels)
		if _, err := store.SyncManualSourceModels(ctx, created, models); err != nil {
			return agent.ToolResult{OK: true, Summary: "源已创建，但手动模型列表写入失败: " + err.Error(), Data: map[string]any{"id": item.ID, "warning": err.Error()}}
		}
	}
	t.server.invalidateRouteCache()
	counts := agentSourceModelCounts(ctx, store)
	return agent.ToolResult{OK: true,
		Summary: fmt.Sprintf("模型源 %q 已创建（id=%s）", item.Name, item.ID),
		Data:    agentSourceView(created, counts[item.ID])}
}

// ---- update_model_source（门控 save）----

type updateSourceTool struct{ server *Server }

func (t *updateSourceTool) Name() string          { return agentToolUpdateSource }
func (t *updateSourceTool) Description() string   { return "修改模型源（需审批）" }
func (t *updateSourceTool) Gated() bool           { return true }
func (t *updateSourceTool) PermissionKey() string { return agent.PermissionKeySave }
func (t *updateSourceTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "high", PreviewDirection: "head"}
}

func (t *updateSourceTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUpdateSource,
		Description: "修改已有模型源（用户审批后生效）：启停、改名、换 baseUrl/平台、更换 API key（留空=保留原 key）、" +
			"调整自动拉取或手动模型列表。source 用源 id 或名称指定。",
		Parameters: objectSchema(map[string]any{
			"source":          map[string]any{"type": "string", "description": "源 id 或名称"},
			"enabled":         map[string]any{"type": "boolean"},
			"name":            map[string]any{"type": "string"},
			"baseUrl":         map[string]any{"type": "string"},
			"platform":        map[string]any{"type": "string"},
			"apiKey":          map[string]any{"type": "string", "description": "新 API key；留空表示保留原 key"},
			"autoFetchModels": map[string]any{"type": "boolean"},
			"manualModels":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "整体替换手动模型列表"},
		}, "source"),
	}
}

func (t *updateSourceTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	var params struct {
		Source          string   `json:"source"`
		Enabled         *bool    `json:"enabled"`
		Name            string   `json:"name"`
		BaseURL         string   `json:"baseUrl"`
		Platform        string   `json:"platform"`
		APIKey          string   `json:"apiKey"`
		AutoFetchModels *bool    `json:"autoFetchModels"`
		ManualModels    []string `json:"manualModels"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	existing, found := agentFindSource(ctx, store, params.Source)
	if !found {
		return agent.ToolError(fmt.Sprintf("模型源 %q 不存在", params.Source), "not_found")
	}
	item := existing
	if params.Enabled != nil {
		item.Enabled = *params.Enabled
	}
	if strings.TrimSpace(params.Name) != "" {
		item.Name = strings.TrimSpace(params.Name)
	}
	if strings.TrimSpace(params.BaseURL) != "" {
		item.BaseURL = strings.TrimSpace(params.BaseURL)
	}
	if strings.TrimSpace(params.Platform) != "" {
		item.Platform = strings.TrimSpace(params.Platform)
	}
	if strings.TrimSpace(params.APIKey) != "" {
		item.APIKey = params.APIKey
	}
	if params.AutoFetchModels != nil {
		item.AutoFetchModels = *params.AutoFetchModels
	}
	if err := t.server.validateAgentSource(ctx, &item, &existing); err != nil {
		return agent.ToolError("校验失败: "+err.Error(), err.Error())
	}
	if err := store.UpsertSource(ctx, item); err != nil {
		return agent.ToolError("保存失败: "+err.Error(), err.Error())
	}
	if params.ManualModels != nil {
		updated, _ := agentFindSource(ctx, store, item.ID)
		models := agentManualModels(updated, params.ManualModels)
		if _, err := store.SyncManualSourceModels(ctx, updated, models); err != nil {
			return agent.ToolResult{OK: true, Summary: "源已更新，但手动模型列表写入失败: " + err.Error(), Data: map[string]any{"warning": err.Error()}}
		}
	}
	t.server.invalidateRouteCache()
	updated, _ := agentFindSource(ctx, store, item.ID)
	counts := agentSourceModelCounts(ctx, store)
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("模型源 %q 已更新", updated.Name), Data: agentSourceView(updated, counts[updated.ID])}
}

// validateAgentSource 源写入的统一校验（与管理端点同套）：
// 自定义协议校验 + baseUrl 出站 SSRF 校验。
func (s *Server) validateAgentSource(ctx context.Context, item *storage.ModelSource, existing *storage.ModelSource) error {
	if item.ID == "" {
		return fmt.Errorf("源 id 为空")
	}
	if err := validateCustomSourceProtocol(item); err != nil {
		return err
	}
	if err := s.validateOutbound(item.BaseURL); err != nil {
		return fmt.Errorf("baseUrl 校验失败: %w", err)
	}
	if strings.TrimSpace(item.FetchBaseURL) != "" {
		if err := s.validateOutbound(item.FetchBaseURL); err != nil {
			return fmt.Errorf("fetchBaseUrl 校验失败: %w", err)
		}
	}
	if item.APIKey == "" && existing != nil {
		// 留空保留原 key（与管理端点语义一致）。
		item.APIKey = existing.APIKey
		item.APIKeys = existing.APIKeys
	}
	return nil
}

// ---- refresh_model_source（门控 live_test）----

type refreshSourceTool struct{ server *Server }

func (t *refreshSourceTool) Name() string { return agentToolRefreshSource }
func (t *refreshSourceTool) Description() string {
	return "拉取模型列表（真实出站，需审批）"
}
func (t *refreshSourceTool) Gated() bool           { return true }
func (t *refreshSourceTool) PermissionKey() string { return agent.PermissionKeyLiveTest }
func (t *refreshSourceTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "high", PreviewDirection: "tail", TimeoutMs: 60_000}
}

func (t *refreshSourceTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolRefreshSource,
		Description: "从上游拉取某模型源的模型列表（用户审批后执行，真实出站请求）。手动源则会同步手动模型列表。" +
			"新建自动拉取源后用这个工具取回模型。",
		Parameters: objectSchema(map[string]any{
			"source": map[string]any{"type": "string", "description": "源 id 或名称"},
		}, "source"),
	}
}

func (t *refreshSourceTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	var params struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	source, found := agentFindSource(ctx, store, params.Source)
	if !found {
		return agent.ToolError(fmt.Sprintf("模型源 %q 不存在", params.Source), "not_found")
	}
	summary, err := t.server.refreshSourceByValue(ctx, source)
	t.server.invalidateRouteCache()
	if err != nil {
		return agent.ToolResult{OK: false, Summary: "拉取失败: " + err.Error(), Data: map[string]any{"error": err.Error(), "summary": summary}}
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("拉取完成，共 %d 个模型", summary.Count), Data: summary}
}

// ---- create_model_group（门控 save）----

type createGroupTool struct{ server *Server }

func (t *createGroupTool) Name() string          { return agentToolCreateGroup }
func (t *createGroupTool) Description() string   { return "创建模型组（需审批）" }
func (t *createGroupTool) Gated() bool           { return true }
func (t *createGroupTool) PermissionKey() string { return agent.PermissionKeySave }
func (t *createGroupTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "medium", PreviewDirection: "head"}
}

func (t *createGroupTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolCreateGroup,
		Description: "创建模型组（用户审批后生效）：模型引用列表（sourceId:modelId 或模型名）、调度策略、重试。" +
			"名称需唯一；创建前先 list_sources / list_model_groups 确认可用模型与重名。",
		Parameters: objectSchema(map[string]any{
			"name":                  map[string]any{"type": "string", "description": "组名（客户端调用时用的模型名）"},
			"models":                map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "成员模型引用（sourceId:modelId 或模型名）"},
			"strategy":              map[string]any{"type": "string", "description": "round-robin（默认）/random/sequential"},
			"maxRetries":            map[string]any{"type": "integer", "description": "失败重试次数（默认 3）"},
			"enabled":               map[string]any{"type": "boolean", "description": "默认 true"},
			"maxConcurrency":        map[string]any{"type": "integer", "description": "并发上限（0=不限）"},
			"dailyLimitMaxRequests": map[string]any{"type": "integer", "description": "每日请求上限（0=不限）"},
			"dailyLimitMaxTokens":   map[string]any{"type": "integer", "description": "每日 token 上限（0=不限）"},
		}, "name"),
	}
}

func (t *createGroupTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	var params struct {
		Name                  string   `json:"name"`
		Models                []string `json:"models"`
		Strategy              string   `json:"strategy"`
		MaxRetries            int      `json:"maxRetries"`
		Enabled               *bool    `json:"enabled"`
		MaxConcurrency        int      `json:"maxConcurrency"`
		DailyLimitMaxRequests int      `json:"dailyLimitMaxRequests"`
		DailyLimitMaxTokens   int      `json:"dailyLimitMaxTokens"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	if strings.TrimSpace(params.Name) == "" {
		return agent.ToolError("name 必填", "missing_name")
	}
	group := storage.ModelGroup{
		ID: slugID(params.Name), Name: strings.TrimSpace(params.Name), Enabled: true,
		Models: params.Models, Strategy: strings.TrimSpace(params.Strategy),
		MaxRetries: params.MaxRetries, MaxConcurrency: params.MaxConcurrency,
		DailyLimitMaxRequests: params.DailyLimitMaxRequests, DailyLimitMaxTokens: params.DailyLimitMaxTokens,
	}
	if params.Enabled != nil {
		group.Enabled = *params.Enabled
	}
	if err := store.UpsertGroup(ctx, group); err != nil {
		return agent.ToolError("创建失败: "+err.Error(), err.Error())
	}
	t.server.invalidateRouteCache()
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("模型组 %q 已创建（%d 个成员）", group.Name, len(group.Models)), Data: map[string]any{"id": group.ID, "name": group.Name, "models": group.Models}}
}

// ---- update_model_group（门控 save）----

type updateGroupTool struct{ server *Server }

func (t *updateGroupTool) Name() string          { return agentToolUpdateGroup }
func (t *updateGroupTool) Description() string   { return "修改模型组（需审批）" }
func (t *updateGroupTool) Gated() bool           { return true }
func (t *updateGroupTool) PermissionKey() string { return agent.PermissionKeySave }
func (t *updateGroupTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "medium", PreviewDirection: "head"}
}

func (t *updateGroupTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUpdateGroup,
		Description: "修改已有模型组（用户审批后生效）：启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。",
		Parameters: objectSchema(map[string]any{
			"group":                 map[string]any{"type": "string", "description": "组名或 id"},
			"addModels":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"removeModels":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"enabled":               map[string]any{"type": "boolean"},
			"strategy":              map[string]any{"type": "string"},
			"maxRetries":            map[string]any{"type": "integer"},
			"maxConcurrency":        map[string]any{"type": "integer"},
			"dailyLimitMaxRequests": map[string]any{"type": "integer"},
			"dailyLimitMaxTokens":   map[string]any{"type": "integer"},
		}, "group"),
	}
}

// updateGroupParams 是 update_model_group 的参数集（成员增删 + 字段补丁）。
type updateGroupParams struct {
	Group                 string   `json:"group"`
	AddModels             []string `json:"addModels"`
	RemoveModels          []string `json:"removeModels"`
	Enabled               *bool    `json:"enabled"`
	Strategy              string   `json:"strategy"`
	MaxRetries            *int     `json:"maxRetries"`
	MaxConcurrency        *int     `json:"maxConcurrency"`
	DailyLimitMaxRequests *int     `json:"dailyLimitMaxRequests"`
	DailyLimitMaxTokens   *int     `json:"dailyLimitMaxTokens"`
}

func (t *updateGroupTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailable := toolStore(t.server)
	if store == nil {
		return unavailable
	}
	var params updateGroupParams
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	group, found := agentFindGroup(ctx, store, params.Group)
	if !found {
		return agent.ToolError(fmt.Sprintf("模型组 %q 不存在", params.Group), "not_found")
	}
	// 成员增删走原子接口；其余字段整体覆盖。
	if len(params.RemoveModels) > 0 {
		if _, err := store.RemoveGroupMembers(ctx, group.ID, params.RemoveModels); err != nil {
			return agent.ToolError("移除成员失败: "+err.Error(), err.Error())
		}
	}
	if len(params.AddModels) > 0 {
		if _, err := store.AddGroupMembers(ctx, group.ID, params.AddModels); err != nil {
			return agent.ToolError("添加成员失败: "+err.Error(), err.Error())
		}
	}
	if applyGroupPatch(&group, params) {
		// 成员以当前库内状态为准，避免用陈旧引用整体覆盖。
		fresh, ok := agentFindGroup(ctx, store, group.ID)
		if ok {
			group.Models = fresh.Models
		}
		if err := store.UpsertGroup(ctx, group); err != nil {
			return agent.ToolError("保存失败: "+err.Error(), err.Error())
		}
	}
	t.server.invalidateRouteCache()
	updated, _ := agentFindGroup(ctx, store, group.ID)
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("模型组 %q 已更新（%d 个成员）", updated.Name, len(updated.Models)), Data: updated}
}

// applyGroupPatch 把参数里的字段补丁应用到组上，报告是否有任何字段变化。
func applyGroupPatch(group *storage.ModelGroup, params updateGroupParams) bool {
	fieldsChanged := false
	applyBool := func(patch *bool, target *bool) {
		if patch != nil {
			*target = *patch
			fieldsChanged = true
		}
	}
	applyInt := func(patch *int, target *int) {
		if patch != nil {
			*target = *patch
			fieldsChanged = true
		}
	}
	applyBool(params.Enabled, &group.Enabled)
	if trimmed := strings.TrimSpace(params.Strategy); trimmed != "" {
		group.Strategy = trimmed
		fieldsChanged = true
	}
	applyInt(params.MaxRetries, &group.MaxRetries)
	applyInt(params.MaxConcurrency, &group.MaxConcurrency)
	applyInt(params.DailyLimitMaxRequests, &group.DailyLimitMaxRequests)
	applyInt(params.DailyLimitMaxTokens, &group.DailyLimitMaxTokens)
	return fieldsChanged
}

// ---- update_outbound_policy（门控 save）----

// outboundPolicyTool 维护出站禁止 IP 段列表（SSRF 防护策略）。典型场景：
// 上游是用户本机/内网服务（如 127.0.0.1 的本地网关）被默认禁止段拦截时，
// 从列表移除对应段（如 127.0.0.0/8）放行。省略 ranges 时仅查询当前策略。
type outboundPolicyTool struct{ server *Server }

func (t *outboundPolicyTool) Name() string { return agentToolOutbound }
func (t *outboundPolicyTool) Description() string {
	return "查询或修改出站禁止 IP 段（需审批）"
}
func (t *outboundPolicyTool) Gated() bool           { return true }
func (t *outboundPolicyTool) PermissionKey() string { return agent.PermissionKeySave }
func (t *outboundPolicyTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "high", PreviewDirection: "head"}
}

func (t *outboundPolicyTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolOutbound,
		Description: "查询或整体替换出站禁止 IP 段列表（SSRF 防护）。不传参数 = 只读返回当前列表与预置默认；" +
			"ranges = 整体替换（CIDR 数组，空数组 = 全放行）；resetDefault = 恢复预置默认。" +
			"上游是本机/内网地址（如 127.0.0.1）被 \"refused to dial denied IP\" 拦截时，" +
			"从 ranges 中去掉对应段（环回 127.0.0.0/8、私网 10.0.0.0/8、172.16.0.0/12、192.168.0.0/16）即可放行。" +
			"修改全列表为高影响操作，先向用户说明改动范围再调用。",
		Parameters: objectSchema(map[string]any{
			"ranges":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "整体替换后的禁止段 CIDR 列表（空数组 = 放行所有地址）"},
			"resetDefault": map[string]any{"type": "boolean", "description": "恢复预置默认禁止段（忽略 ranges）"},
		}),
	}
}

func (t *outboundPolicyTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	var params struct {
		Ranges       []string `json:"ranges"`
		ResetDefault bool     `json:"resetDefault"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}

	var rollback func()
	view := func(summary string) agent.ToolResult {
		return agent.ToolResult{OK: true, Summary: summary, Data: map[string]any{
			"deniedIpRanges":        t.server.config.GetOutboundConfig().DeniedIPRanges,
			"defaultDeniedIpRanges": relay.DefaultDeniedIPRanges,
		}}
	}

	if params.ResetDefault {
		rollback, _ = t.server.applyOutboundDeniedRanges(append([]string(nil), relay.DefaultDeniedIPRanges...))
	} else if params.Ranges == nil {
		// 只读查询，不落盘。
		return view("当前出站禁止段如下（未修改）")
	} else {
		cleaned := make([]string, 0, len(params.Ranges))
		for _, entry := range params.Ranges {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(trimmed); err != nil {
				return agent.ToolResult{OK: false, Summary: fmt.Sprintf("非法 CIDR: %q", trimmed), Data: map[string]any{"error": "invalid_cidr", "entry": trimmed}}
			}
			cleaned = append(cleaned, trimmed)
		}
		rollback, _ = t.server.applyOutboundDeniedRanges(cleaned)
	}
	if err := t.server.config.Save(); err != nil {
		// 落盘失败回滚内存与 relay 下发：否则热重载会静默恢复旧策略，
		// 而运行时行为已按新策略放行/拦截（内存与磁盘分叉）。
		if rollback != nil {
			rollback()
		}
		return agent.ToolError("策略修改已回滚（落盘失败）: "+err.Error(), err.Error())
	}
	if params.ResetDefault {
		return view("出站禁止段已恢复预置默认")
	}
	return view(fmt.Sprintf("出站禁止段已更新（%d 段）", len(t.server.config.GetOutboundConfig().DeniedIPRanges)))
}
