package agent

import (
	"encoding/json"
	"time"

	"github.com/elysia-api/backend/relay"
)

// 事件类型：引擎只产出结构化事件，SSE 编码由 HTTP 层完成。
const (
	EventStatus          = "status"            // 阶段变化（调用模型/执行工具/重试中）
	EventTextDelta       = "text_delta"        // 助手正文增量
	EventReasoningDelta  = "reasoning_delta"   // 思维链增量
	EventToolCall        = "tool_call"         // 模型发起工具调用
	EventToolResult      = "tool_result"       // 工具执行结果
	EventDraftUpdated    = "draft_updated"     // 会话草稿已更新
	EventPlanUpdated     = "plan_updated"      // 工作方案已更新
	EventApprovalPending = "approval_required" // 轮次暂停等待审批
	EventMessage         = "message"           // 一条消息已持久化
	EventTurnDone        = "turn_done"         // 轮次完成（含用量汇总）
	EventError           = "error"             // 轮次失败（retryable 标记是否可重试）
)

// Event 是引擎向外发布的唯一事件载体。字段按类型复用，未用字段为零值。
type Event struct {
	Type string `json:"type"`

	// status / error 共用的文本说明
	Text string `json:"text,omitempty"`
	// error 专用：失败是否可通过重发同一条消息恢复
	Retryable bool `json:"retryable,omitempty"`

	// message：刚持久化的消息（含 seq，前端据此对账）
	Message *Message `json:"message,omitempty"`

	// text_delta / reasoning_delta 的增量内容
	Delta string `json:"delta,omitempty"`

	// tool_call / tool_result
	CallID string          `json:"callId,omitempty"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Result *ToolResultInfo `json:"result,omitempty"`

	// draft_updated
	Draft json.RawMessage `json:"draft,omitempty"`

	// plan_updated
	Plan []PlanStep `json:"plan,omitempty"`

	// approval_required
	Approval *PendingAction `json:"approval,omitempty"`

	// turn_done
	Usage      *relay.MaheshvaraUsage `json:"usage,omitempty"`
	Model      string                 `json:"model,omitempty"`
	DurationMs int64                  `json:"durationMs,omitempty"`
	Rounds     int                    `json:"rounds,omitempty"`
}

// ToolResultInfo 是 tool_result 事件与消息内容共用的结构。
type ToolResultInfo struct {
	CallID     string          `json:"callId"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input,omitempty"`
	OK         bool            `json:"ok"`
	Summary    string          `json:"summary,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	DurationMs int64           `json:"durationMs,omitempty"`
}

// Message 是持久化的会话消息。Content 按 Role 有不同结构：
//   - user:        UserContent
//   - assistant:   AssistantContent
//   - tool_result: ToolResultInfo
//   - approval:    ApprovalContent
//   - system:      SystemContent
type Message struct {
	Seq       int             `json:"seq"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Model     string          `json:"model,omitempty"`
	Usage     json.RawMessage `json:"usage,omitempty"` // assistant 消息的本次调用用量
	CreatedAt time.Time       `json:"createdAt"`
}

// UserContent 是 user 消息内容。
type UserContent struct {
	Text      string     `json:"text,omitempty"`
	Documents []Document `json:"documents,omitempty"`
}

// Document 是用户随消息提供的材料：纯文本或 data: URL（图片/PDF）。
type Document struct {
	Name    string `json:"name,omitempty"`
	Mime    string `json:"mime,omitempty"`
	Text    string `json:"text,omitempty"`
	DataURL string `json:"dataUrl,omitempty"`
}

// AssistantContent 是 assistant 消息内容。
type AssistantContent struct {
	Text      string                     `json:"text,omitempty"`
	Reasoning string                     `json:"reasoning,omitempty"`
	ToolCalls []relay.MaheshvaraToolCall `json:"toolCalls,omitempty"`
}

// ApprovalContent 是 approval 消息内容（用户对门控动作的裁决）。
type ApprovalContent struct {
	Decision string   `json:"decision"` // approved | denied
	Names    []string `json:"names"`    // 被裁决的工具名
	Note     string   `json:"note,omitempty"`
}

// SystemContent 是 system 消息内容（错误提示等）。
type SystemContent struct {
	Text string `json:"text"`
	Kind string `json:"kind,omitempty"` // error | info
}

// 角色常量。
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleToolResult = "tool_result"
	RoleApproval   = "approval"
	RoleSystem     = "system"
)

// PlanStep 是工作方案清单的一项（update_plan 工具维护，侧边栏「方案」页展示）。
type PlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending | in_progress | done
}

// PendingAction 是等待审批的动作快照：本轮剩余未执行的门控工具调用。
type PendingAction struct {
	Calls  []relay.MaheshvaraToolCall `json:"calls"`
	Reason string                     `json:"reason,omitempty"` // 模型对动作意图的说明（取自正文）
}
