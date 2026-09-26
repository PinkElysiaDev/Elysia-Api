package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// API Key（访问令牌）管理工具：签发给客户端调用 /v1 接口的凭证 CRUD。
// 与管理端点同语义：明文只在创建结果里返回一次，其余出口一律脱敏；
// 编辑留空=保留原值。写操作门控 save，删除门控 delete。

const (
	agentToolListAPIKeys  = "list_api_keys"
	agentToolCreateAPIKey = "create_api_key"
	agentToolUpdateAPIKey = "update_api_key"
	agentToolDeleteAPIKey = "delete_api_key"
	agentKeySecretBytes   = 32
)

// newAgentTokenTools 返回 API Key 管理域全量工具。
func newAgentTokenTools(s *Server) []agent.Tool {
	return []agent.Tool{
		&listAPIKeysTool{server: s},
		&createAPIKeyTool{server: s},
		&updateAPIKeyTool{server: s},
		&deleteAPIKeyTool{server: s},
	}
}

// agentTokenView 令牌的安全视图（明文脱敏）。
func agentTokenView(item storage.APIToken) map[string]any {
	return map[string]any{
		"name": item.Name, "tokenMasked": maskSecret(item.Token),
		"enabled": item.Enabled, "allowedGroups": item.AllowedGroups,
		"scopes": item.Scopes, "createdAt": item.CreatedAt,
	}
}

// generateAPIKeySecret 生成新令牌明文（32 字节 URL-safe base64）。
func generateAPIKeySecret() (string, error) {
	buf := make([]byte, agentKeySecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ---- list_api_keys（只读）----

type listAPIKeysTool struct{ server *Server }

func (t *listAPIKeysTool) Name() string        { return agentToolListAPIKeys }
func (t *listAPIKeysTool) Description() string { return "查询 API Key（访问令牌）列表" }
func (t *listAPIKeysTool) Gated() bool         { return false }
func (t *listAPIKeysTool) PermissionKey() string {
	return ""
}
func (t *listAPIKeysTool) Meta() agent.ToolMeta { return readOnlyMeta() }

func (t *listAPIKeysTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolListAPIKeys,
		Description: "查询 API Key（访问令牌）列表。token 脱敏显示；allowedGroups 为空表示可访问全部模型组。" +
			"带 agent 作用域的是远程访问 Key（驱动 AI 助手专用，不参与推理），由用户在运行配置页管理——不可对其做写操作。",
		Parameters: objectSchema(map[string]any{}),
	}
}

func (t *listAPIKeysTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailableResult := toolStore(t.server)
	if store == nil {
		return unavailableResult
	}
	items, err := store.ListAPITokens(ctx)
	if err != nil {
		return agent.ToolError("查询失败: "+err.Error(), "list_failed")
	}
	views := make([]map[string]any, 0, len(items))
	for _, item := range items {
		views = append(views, agentTokenView(item))
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("共 %d 个 API Key", len(views)), Data: map[string]any{"items": views}}
}

// ---- create_api_key（门控 save）----

type createAPIKeyTool struct{ server *Server }

func (t *createAPIKeyTool) Name() string          { return agentToolCreateAPIKey }
func (t *createAPIKeyTool) Description() string   { return "创建 API Key（需审批）" }
func (t *createAPIKeyTool) Gated() bool           { return true }
func (t *createAPIKeyTool) PermissionKey() string { return agent.PermissionKeySave }
func (t *createAPIKeyTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "high", PreviewDirection: agent.ClampHead}
}

func (t *createAPIKeyTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolCreateAPIKey,
		Description: "创建 API Key，即客户端调用 /v1 接口用的推理访问令牌（用户审批后生效）。用户给定 secret 明文就按给定值原样创建" +
			"（弱口令可提醒风险，但不代为拒绝）；留空则自动生成随机明文。完整明文只在本次结果里返回一次，请提醒用户立即保存。" +
			"allowedGroups 为空表示可访问全部模型组（扩权面大，创建前先向用户确认授权范围）。远程访问 Key（驱动 AI 助手的那类）由用户在运行配置页管理，elysia key 命令不能创建。",
		Parameters: objectSchema(map[string]any{
			"name":          map[string]any{"type": "string", "description": "Key 名称（主键，创建后不可改）"},
			"secret":        map[string]any{"type": "string", "description": "Key 明文；留空自动生成随机值"},
			"enabled":       map[string]any{"type": "boolean", "description": "默认 true"},
			"allowedGroups": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "允许访问的模型组名称；空=不限制"},
		}, "name"),
	}
}

func (t *createAPIKeyTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailableResult := toolStore(t.server)
	if store == nil {
		return unavailableResult
	}
	var params struct {
		Name          string   `json:"name"`
		Secret        string   `json:"secret"`
		Enabled       *bool    `json:"enabled"`
		AllowedGroups []string `json:"allowedGroups"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return agent.ToolError("name 必填", "missing_name")
	}
	secret := strings.TrimSpace(params.Secret)
	generated := false
	if secret == "" {
		var err error
		secret, err = generateAPIKeySecret()
		if err != nil {
			return agent.ToolError("生成随机明文失败: "+err.Error(), "random_failed")
		}
		generated = true
	}
	item := storage.APIToken{Name: name, Token: secret, Enabled: true, AllowedGroups: params.AllowedGroups}
	if params.Enabled != nil {
		item.Enabled = *params.Enabled
	}
	if existing, found, _ := store.FindAPITokenByName(ctx, name); found {
		return agent.ToolResult{OK: false,
			Summary: fmt.Sprintf("已存在同名 API Key %q，如需修改请用 elysia key update", existing.Name),
			Data:    map[string]any{"error": "duplicate_name", "name": existing.Name}}
	}
	if err := store.UpsertAPIToken(ctx, item); err != nil {
		return agent.ToolError("创建失败: "+err.Error(), "persist_failed")
	}
	t.server.invalidateRouteCache()
	// 明文是本次操作的交付物，仅在结果里完整返回这一次；此后所有出口脱敏。
	origin := "用户指定"
	if generated {
		origin = "自动生成"
	}
	return agent.ToolResult{OK: true,
		Summary: fmt.Sprintf("API Key %q 已创建（%s），明文仅此一次显示：%s", name, origin, secret),
		Data: map[string]any{"name": name, "token": secret, "enabled": item.Enabled,
			"allowedGroups": item.AllowedGroups,
			"note":          "明文仅此一次显示，请立即保存"}}
}

// ---- update_api_key（门控 save）----

type updateAPIKeyTool struct{ server *Server }

func (t *updateAPIKeyTool) Name() string          { return agentToolUpdateAPIKey }
func (t *updateAPIKeyTool) Description() string   { return "修改 API Key（需审批）" }
func (t *updateAPIKeyTool) Gated() bool           { return true }
func (t *updateAPIKeyTool) PermissionKey() string { return agent.PermissionKeySave }
func (t *updateAPIKeyTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "high", PreviewDirection: agent.ClampHead}
}

func (t *updateAPIKeyTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUpdateAPIKey,
		Description: "修改已有 API Key（用户审批后生效）：启停、调整可访问的模型组、更换明文（newSecret 留空=保留原值）。" +
			"名称是主键不可修改；远程访问 Key（agent 作用域）由用户在运行配置页管理，elysia key 命令不可修改。allowedGroups 为空表示不限制（可访问全部模型组），调整前先向用户确认。",
		Parameters: objectSchema(map[string]any{
			"name":          map[string]any{"type": "string", "description": "Key 名称"},
			"enabled":       map[string]any{"type": "boolean"},
			"allowedGroups": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "整体替换允许访问的模型组；空=不限制"},
			"newSecret":     map[string]any{"type": "string", "description": "新明文；留空保留原值"},
		}, "name"),
	}
}

func (t *updateAPIKeyTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailableResult := toolStore(t.server)
	if store == nil {
		return unavailableResult
	}
	var params struct {
		Name          string   `json:"name"`
		Enabled       *bool    `json:"enabled"`
		AllowedGroups []string `json:"allowedGroups"`
		NewSecret     string   `json:"newSecret"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	existing, found, err := store.FindAPITokenByName(ctx, strings.TrimSpace(params.Name))
	if err != nil {
		return agent.ToolError("查询失败: "+err.Error(), "lookup_failed")
	}
	if !found {
		return agent.ToolError(fmt.Sprintf("API Key %q 不存在", params.Name), "not_found")
	}
	if existing.HasScope(storage.TokenScopeAgent) {
		return agent.ToolError(fmt.Sprintf("%q 是远程访问 Key（AI 助手专用），请在运行配置页管理", params.Name), "agent_key_managed_elsewhere")
	}
	item := existing
	if params.Enabled != nil {
		item.Enabled = *params.Enabled
	}
	// allowedGroups 未传（nil）保留原授权；传了（含空数组）整体替换。
	if params.AllowedGroups != nil {
		item.AllowedGroups = params.AllowedGroups
	}
	if strings.TrimSpace(params.NewSecret) != "" {
		item.Token = strings.TrimSpace(params.NewSecret)
	}
	if err := store.UpsertAPIToken(ctx, item); err != nil {
		return agent.ToolError("保存失败: "+err.Error(), "persist_failed")
	}
	t.server.invalidateRouteCache()
	updated, _, _ := store.FindAPITokenByName(ctx, item.Name)
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("API Key %q 已更新", item.Name), Data: agentTokenView(updated)}
}

// ---- delete_api_key（门控 delete）----

type deleteAPIKeyTool struct{ server *Server }

func (t *deleteAPIKeyTool) Name() string          { return agentToolDeleteAPIKey }
func (t *deleteAPIKeyTool) Description() string   { return "删除 API Key（不可逆，需审批）" }
func (t *deleteAPIKeyTool) Gated() bool           { return true }
func (t *deleteAPIKeyTool) PermissionKey() string { return agent.PermissionKeyDelete }
func (t *deleteAPIKeyTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "high", PreviewDirection: agent.ClampHead}
}

func (t *deleteAPIKeyTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolDeleteAPIKey,
		Description: "删除 API Key（用户审批后执行，不可逆）：使用该 Key 的客户端将立即无法调用。远程访问 Key（agent 作用域）请在运行配置页删除。删除前先向用户核对名称。",
		Parameters: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "description": "Key 名称"},
		}, "name"),
	}
}

func (t *deleteAPIKeyTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	store, unavailableResult := toolStore(t.server)
	if store == nil {
		return unavailableResult
	}
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	name := strings.TrimSpace(params.Name)
	existing, found, _ := store.FindAPITokenByName(ctx, name)
	if !found {
		return agent.ToolError(fmt.Sprintf("API Key %q 不存在", name), "not_found")
	}
	if existing.HasScope(storage.TokenScopeAgent) {
		return agent.ToolError(fmt.Sprintf("%q 是远程访问 Key（AI 助手专用），请在运行配置页删除", name), "agent_key_managed_elsewhere")
	}
	if err := store.DeleteAPIToken(ctx, name); err != nil {
		return agent.ToolError("删除失败: "+err.Error(), "persist_failed")
	}
	t.server.invalidateRouteCache()
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("API Key %q 已删除，使用它的客户端将无法再调用", name)}
}
