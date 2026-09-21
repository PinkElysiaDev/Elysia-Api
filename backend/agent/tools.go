package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/relay"
)

// Tool 是 Agent 可用的工具抽象。引擎对工具的具体行为一无所知；领域知识
// （如协议校验/上游测试）全部封装在实现里。
type Tool interface {
	// Name 工具名（模型可见，需为合法标识符）。
	Name() string
	// Definition 返回模型的工具定义（含 JSON Schema 参数）。
	Definition() relay.MaheshvaraTool
	// Gated 是否为门控动作（执行前需用户审批）。
	Gated() bool
	// PermissionKey 门控动作对应的会话权限键（如 live_test / save）；
	// 非门控工具返回空串。
	PermissionKey() string
	// Description 供审批卡片展示的一句人话说明。
	Description() string
	// Execute 执行工具。args 是模型给出的原始 JSON 参数。
	Execute(ctx context.Context, tctx ToolContext, args json.RawMessage) ToolResult
}

// ToolContext 是工具执行时可见的会话上下文。由宿主（server 层）实现，
// 引擎按工具逐轮构造。
type ToolContext interface {
	// SessionMeta 只读会话元信息。
	SessionMeta() SessionMeta
	// Draft 读当前草稿（无草稿返回 nil）。
	Draft() json.RawMessage
	// SetDraft 写当前草稿（如 update_protocol_draft）。
	SetDraft(draft json.RawMessage) error
	// TestTarget 真实测试目标凭证（baseUrl + apiKey，可能为空）。
	TestTarget() (baseURL, apiKey string)
	// SetTestTarget 记住测试目标凭证（工具拿到用户提供的新凭证后调用，
	// 加密落库，同会话后续测试复用）。
	SetTestTarget(baseURL, apiKey string) error
	// SetPlan 更新工作方案清单（update_plan 工具用）。
	SetPlan(steps []PlanStep) error
}

// ToolResult 是工具执行结果：Data 回传给模型（须精简），Summary 供 UI 展示。
type ToolResult struct {
	OK      bool
	Summary string
	Data    any // 会被 json.Marshal；字符串原样传递
}

// ToolResultJSON 把 Data 序列化为回传模型的原始 JSON（引擎在截断前调用）。
func (r ToolResult) MarshalData() json.RawMessage {
	if r.Data == nil {
		return json.RawMessage(`{}`)
	}
	switch value := r.Data.(type) {
	case string:
		encoded, err := json.Marshal(value)
		if err != nil {
			return json.RawMessage(`{}`)
		}
		return encoded
	case json.RawMessage:
		return value
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return json.RawMessage(`{}`)
		}
		return encoded
	}
}

// Registry 是工具注册表。新增能力 = 实现 Tool + 注册一行，引擎零改动。
type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry(tools ...Tool) (*Registry, error) {
	registry := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name())
		if name == "" {
			return nil, fmt.Errorf("agent tool with empty name")
		}
		if _, exists := registry.tools[name]; exists {
			return nil, fmt.Errorf("agent tool %q registered twice", name)
		}
		registry.tools[name] = tool
		registry.order = append(registry.order, name)
	}
	return registry, nil
}

// Get 按名查工具；不存在返回 nil。
func (r *Registry) Get(name string) Tool {
	if r == nil {
		return nil
	}
	return r.tools[strings.TrimSpace(name)]
}

// Definitions 返回全部工具定义（顺序稳定，便于提示词与缓存友好）。
func (r *Registry) Definitions() []relay.MaheshvaraTool {
	if r == nil {
		return nil
	}
	definitions := make([]relay.MaheshvaraTool, 0, len(r.order))
	for _, name := range r.order {
		definitions = append(definitions, r.tools[name].Definition())
	}
	return definitions
}
