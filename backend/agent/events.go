package agent

import (
	"encoding/json"
	"time"

	"github.com/elysia-api/backend/relay"
)

// 事件类型：引擎只产出结构化事件，SSE 编码由 HTTP 层完成。
const (
	EventStatus           = "status"            // 阶段变化（调用模型/执行工具/重试中）
	EventTextDelta        = "text_delta"        // 助手正文增量
	EventReasoningDelta   = "reasoning_delta"   // 思维链增量
	EventToolCall         = "tool_call"         // 模型发起工具调用
	EventToolProgress     = "tool_progress"     // 工具仍在执行（耗时心跳）
	EventToolResult       = "tool_result"       // 工具执行结果
	EventDraftUpdated     = "draft_updated"     // 会话草稿已更新
	EventPlanUpdated      = "plan_updated"      // 工作方案已更新
	EventApprovalPending  = "approval_required" // 轮次暂停等待审批
	EventMessage          = "message"           // 一条消息已持久化
	EventTurnDone         = "turn_done"         // 轮次完成（含用量汇总）
	EventError            = "error"             // 轮次失败（retryable 标记是否可重试）
	EventContextUpdated   = "context_updated"   // 本轮上下文水位
	EventContextCompacted = "context_compacted" // 微压缩或摘要压缩完成
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

	// tool_call / tool_progress / tool_result
	CallID string          `json:"callId,omitempty"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Result *ToolResultInfo `json:"result,omitempty"`
	// tool_progress：已执行毫秒数
	ElapsedMs int64 `json:"elapsedMs,omitempty"`

	// draft_updated
	Draft json.RawMessage `json:"draft,omitempty"`

	// plan_updated
	Plan []PlanStep `json:"plan,omitempty"`

	// approval_required
	Approval *PendingAction `json:"approval,omitempty"`

	// context_updated / context_compacted
	Context    *ContextUsage `json:"context,omitempty"`
	Compaction *Compaction   `json:"compaction,omitempty"`

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
	Kind string `json:"kind,omitempty"` // error | info | summary
	// BoundarySeq 摘要覆盖到的最后一条消息序号（含）。之后的消息仍原样回放。
	BoundarySeq int `json:"boundarySeq,omitempty"`
}

// 角色常量。
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleToolResult = "tool_result"
	RoleApproval   = "approval"
	RoleSystem     = "system"
)

// ContextUsage 是一次模型调用后的上下文水位。
type ContextUsage struct {
	InputTokens  int     `json:"inputTokens"`
	WindowTokens int     `json:"windowTokens"`
	Ratio        float64 `json:"ratio"`
}

// Compaction 是一次上下文压缩的边界指标。
type Compaction struct {
	Kind         string `json:"kind"` // micro | summary
	Summarized   int    `json:"summarized"`
	Kept         int    `json:"kept"`
	BeforeTokens int    `json:"beforeTokens,omitempty"`
	AfterTokens  int    `json:"afterTokens,omitempty"`
}

// PlanStep 是工作方案清单的一项（update_plan 工具维护，侧边栏「方案」页展示）。
type PlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending | in_progress | done
}

// PendingAction 是等待用户的动作快照。Kind 为空或 approval 时是门控工具审批；
// question 是 ask_user 的提问；plan 是方案定稿确认。
type PendingAction struct {
	Kind     string                     `json:"kind,omitempty"`
	Calls    []relay.MaheshvaraToolCall `json:"calls"`
	Reason   string                     `json:"reason,omitempty"` // 模型对动作意图的说明（取自正文）
	Question *AskQuestion               `json:"question,omitempty"`
	Plan     []PlanStep                 `json:"plan,omitempty"`
}

// AskQuestion 是 ask_user 暂停时交给用户的问题。
type AskQuestion struct {
	CallID string `json:"callId"`
	// AllowCustom 不带 omitempty：前端以 `!== false` 判断是否显示自定义
	// 作答框，false 被省略会让「禁止自定义」永远传不到前端。
	Question    string      `json:"question"`
	Options     []AskOption `json:"options,omitempty"`
	AllowCustom bool        `json:"allowCustom"`
}

// AskOption 是提问的一个预设选项。
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}
