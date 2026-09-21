package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// 会话状态。
const (
	StatusIdle            = "idle"
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
)

// 权限策略值。
const (
	PermissionAsk    = "ask"
	PermissionAlways = "always"
	PermissionNever  = "never"
)

// 会话模式。
const (
	ModeCreate = "create"
	ModeEdit   = "edit"
)

// ErrSessionRunning 会话已有轮次在跑（单会话串行约束）。
var ErrSessionRunning = errors.New("agent session already has a running turn")

// ErrNoPendingApproval 会话不在等待审批状态。
var ErrNoPendingApproval = errors.New("agent session has no pending approval")

// Settings 是会话级可调设置。
type Settings struct {
	ModelSourceID   string `json:"modelSourceId"`
	ModelName       string `json:"modelName"`
	ThinkingEnabled bool   `json:"thinkingEnabled"`
	ThinkingEffort  string `json:"thinkingEffort,omitempty"` // ''|low|medium|high|adaptive
	PlanMode        bool   `json:"planMode,omitempty"`       // 计划模式：先出方案，用户确认后才允许修改/出站
	AllowLiveTest   string `json:"allowLiveTest,omitempty"`  // ask|always|never
	AllowSave       string `json:"allowSave,omitempty"`      // ask|always|never
	TestBaseURL     string `json:"testBaseUrl,omitempty"`
	TestAPIKeySet   bool   `json:"testApiKeySet,omitempty"` // 只读标记：是否已配置 key（不回传明文）
}

// Session 是引擎视角的会话聚合。TestAPIKey 由存储层读出时解密。
type Session struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	Mode          string          `json:"mode"` // create|edit
	ProtocolID    string          `json:"protocolId,omitempty"`
	SeedConfig    json.RawMessage `json:"seedConfig,omitempty"`   // 编辑模式的初始配置
	DraftConfig   json.RawMessage `json:"draftConfig,omitempty"`  // 最新草稿
	DraftRestore  json.RawMessage `json:"draftRestore,omitempty"` // 草稿还原点：最近一轮修改前的副本（单槽覆盖）
	Plan          []PlanStep      `json:"plan,omitempty"`         // 工作方案清单
	TestBaseURL   string          `json:"testBaseUrl,omitempty"`
	TestAPIKey    string          `json:"testApiKey,omitempty"` // 已解密；不出引擎
	Settings      Settings        `json:"settings"`
	Status        string          `json:"status"`
	PendingAction *PendingAction  `json:"pendingAction,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// SessionMeta 是工具可见的会话元信息子集（不含凭证）。
type SessionMeta struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Mode       string          `json:"mode"`
	ProtocolID string          `json:"protocolId,omitempty"`
	SeedConfig json.RawMessage `json:"seedConfig,omitempty"`
	Draft      json.RawMessage `json:"draft,omitempty"`
	Settings   Settings        `json:"settings"`
}

// SessionStateUpdate 是引擎运行中需要写回的会话状态增量。
type SessionStateUpdate struct {
	Status        *string         `json:"status,omitempty"`
	PendingAction *PendingAction  `json:"pendingAction,omitempty"` // nil 且 ClearPending 时清空
	ClearPending  bool            `json:"clearPending,omitempty"`
	DraftConfig   json.RawMessage `json:"draftConfig,omitempty"`
	DraftRestore  json.RawMessage `json:"draftRestore,omitempty"` // 非 nil 时覆盖草稿还原点（单槽）
	Plan          []PlanStep      `json:"plan,omitempty"`         // 非 nil 时整体替换
	Title         string          `json:"title,omitempty"`        // 空串表示不改
	TestBaseURL   string          `json:"testBaseUrl,omitempty"`  // 非空时更新测试目标 baseUrl
	TestAPIKey    string          `json:"testApiKey,omitempty"`   // 非空时更新测试目标 API key（存储层加密）
}

// Store 是引擎依赖的持久化接口（由 storage 包实现）。
type Store interface {
	// GetSession 读取会话（含解密后的测试凭证）。
	GetSession(ctx context.Context, id string) (*Session, error)
	// UpdateSessionState 写回引擎产生的状态增量。
	UpdateSessionState(ctx context.Context, id string, update SessionStateUpdate) error
	// AppendMessage 追加一条消息并返回分配的 seq。
	AppendMessage(ctx context.Context, sessionID string, role string, content any, model string, usage json.RawMessage) (int, error)
	// ListMessages 按 seq 升序返回会话全部消息。
	ListMessages(ctx context.Context, sessionID string) ([]Message, error)
	// TruncateMessages 删除 seq > afterSeq 的消息。
	TruncateMessages(ctx context.Context, sessionID string, afterSeq int) error
	// ResetRunningSessions 把遗留的 running 会话复位为 idle（进程启动对账；
	// waiting_approval 保留——待批动作仍可恢复）。
	ResetRunningSessions(ctx context.Context) error
}

// SettingsPatch 是设置的部分更新载荷：全指针字段，仅非 nil 字段生效
// （修复「只改思考等级却清空思考开关/模型选择」的整组覆盖缺陷）。
type SettingsPatch struct {
	ModelSourceID   *string `json:"modelSourceId,omitempty"`
	ModelName       *string `json:"modelName,omitempty"`
	ThinkingEnabled *bool   `json:"thinkingEnabled,omitempty"`
	ThinkingEffort  *string `json:"thinkingEffort,omitempty"`
	PlanMode        *bool   `json:"planMode,omitempty"`
	AllowLiveTest   *string `json:"allowLiveTest,omitempty"`
	AllowSave       *string `json:"allowSave,omitempty"`
	TestBaseURL     *string `json:"testBaseUrl,omitempty"`
}

// NormalizedPermission 归一化权限值（空值视为 ask）。
func NormalizedPermission(value string) string {
	switch value {
	case PermissionAlways, PermissionNever:
		return value
	default:
		return PermissionAsk
	}
}
