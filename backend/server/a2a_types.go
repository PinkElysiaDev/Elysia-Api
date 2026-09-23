package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// A2A wire 类型（双线格式）。内部规范形态采用 v0.3 线格式（小写状态、
// kind 判别 Part、小写 role），出站时按请求的 A2A-Version 头编码为 v0.3
// 或 v1.0（大写蛇形枚举、无 kind Part、ROLE_* role）。
//
// 数据模型映射：contextId = 会话 id；task = 一轮（taskId = 会话id:用户消息
// seq）。waiting_approval（审批/提问/方案三型）→ input-required。

const (
	a2aVersionV03 = "0.3.0"
	a2aVersionV10 = "1.0.0"

	a2aStateSubmitted     = "submitted"
	a2aStateWorking       = "working"
	a2aStateInputRequired = "input-required"
	a2aStateCompleted     = "completed"
	a2aStateFailed        = "failed"
	a2aStateCanceled      = "canceled"
)

// v1.0 的大写蛇形状态对照。
var a2aV1States = map[string]string{
	a2aStateSubmitted:     "TASK_STATE_SUBMITTED",
	a2aStateWorking:       "TASK_STATE_WORKING",
	a2aStateInputRequired: "TASK_STATE_INPUT_REQUIRED",
	a2aStateCompleted:     "TASK_STATE_COMPLETED",
	a2aStateFailed:        "TASK_STATE_FAILED",
	a2aStateCanceled:      "TASK_STATE_CANCELED",
}

// a2aTaskEntry 的流帧 kind。
const (
	a2aFrameStatus   = "status"
	a2aFrameArtifact = "artifact"
)

// a2aPart 消息/产物部件（入站解析 v0.3 与 v1.0 两种形态；出站按线格式编码）。
type a2aPart struct {
	Kind string `json:"kind,omitempty"` // v0.3: text|data|file；v1.0 无判别字段
	Text string `json:"text,omitempty"`
	Data any    `json:"data,omitempty"`
}

// a2aMessage 消息（canonical：v0.3 形态）。
type a2aMessage struct {
	MessageID string    `json:"messageId"`
	Role      string    `json:"role"` // user | agent
	Parts     []a2aPart `json:"parts"`
	TaskID    string    `json:"taskId,omitempty"`
	ContextID string    `json:"contextId,omitempty"`
}

// a2aTaskStatus 任务状态（canonical）。
type a2aTaskStatus struct {
	State     string      `json:"state"`
	Message   *a2aMessage `json:"message,omitempty"`
	Timestamp string      `json:"timestamp,omitempty"`
}

// a2aArtifact 任务产物。
type a2aArtifact struct {
	ArtifactID  string    `json:"artifactId"`
	Name        string    `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	Parts       []a2aPart `json:"parts"`
}

// a2aTask 任务（canonical；registry 与响应共用）。
type a2aTask struct {
	ID        string         `json:"id"`
	ContextID string         `json:"contextId"`
	Status    a2aTaskStatus  `json:"status"`
	Artifacts []a2aArtifact  `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// a2aStreamFrame 是登记表里的流帧（canonical 结构，按线格式编码出站）。
type a2aStreamFrame struct {
	Kind     string      // status | artifact
	State    string      // status 帧状态
	Message  string      // status.message 文本
	Artifact a2aArtifact // artifact 帧载荷
	Final    bool        // v0.3 final:true（终态/中断态收尾帧）
}

// a2aWire 按线格式编码出站对象。
type a2aWire struct {
	v1 bool
}

func (w a2aWire) state(state string) string {
	if w.v1 {
		if mapped, ok := a2aV1States[state]; ok {
			return mapped
		}
	}
	return state
}

func (w a2aWire) role(role string) string {
	if !w.v1 {
		return role
	}
	switch role {
	case "user":
		return "ROLE_USER"
	default:
		return "ROLE_AGENT"
	}
}

func (w a2aWire) part(part a2aPart) gin.H {
	if w.v1 {
		encoded := gin.H{}
		if part.Text != "" {
			encoded["text"] = part.Text
		}
		if part.Data != nil {
			encoded["data"] = part.Data
		}
		return encoded
	}
	if part.Kind == "" {
		if part.Data != nil {
			part.Kind = "data"
		} else {
			part.Kind = "text"
		}
	}
	return gin.H{"kind": part.Kind, "text": part.Text, "data": part.Data}
}

func (w a2aWire) parts(parts []a2aPart) []gin.H {
	encoded := make([]gin.H, 0, len(parts))
	for _, part := range parts {
		encoded = append(encoded, w.part(part))
	}
	return encoded
}

func (w a2aWire) message(message *a2aMessage) gin.H {
	if message == nil {
		return nil
	}
	return gin.H{
		"messageId": message.MessageID, "role": w.role(message.Role),
		"parts":  w.parts(message.Parts),
		"taskId": message.TaskID, "contextId": message.ContextID,
	}
}

// task 编码 canonical 任务为出站形态。
func (w a2aWire) task(task a2aTask) gin.H {
	status := gin.H{"state": w.state(task.Status.State)}
	if task.Status.Message != nil {
		status["message"] = w.message(task.Status.Message)
	}
	if task.Status.Timestamp != "" {
		status["timestamp"] = task.Status.Timestamp
	}
	artifacts := make([]gin.H, 0, len(task.Artifacts))
	for _, artifact := range task.Artifacts {
		artifacts = append(artifacts, gin.H{
			"artifactId": artifact.ArtifactID, "name": artifact.Name,
			"description": artifact.Description, "parts": w.parts(artifact.Parts),
		})
	}
	view := gin.H{
		"id": task.ID, "contextId": task.ContextID, "status": status,
		"artifacts": artifacts,
	}
	if task.Metadata != nil {
		view["metadata"] = task.Metadata
	}
	return view
}

// frame 把登记帧编码为 SSE 的 JSON-RPC result。
func (w a2aWire) frame(taskID, contextID string, frame a2aStreamFrame) gin.H {
	switch frame.Kind {
	case a2aFrameArtifact:
		artifact := frame.Artifact
		payload := gin.H{
			"taskId": taskID, "contextId": contextID,
			"artifact": gin.H{
				"artifactId": artifact.ArtifactID, "name": artifact.Name,
				"description": artifact.Description, "parts": w.parts(artifact.Parts),
			},
			"append": false, "lastChunk": true,
		}
		return payload
	default:
		status := gin.H{"state": w.state(frame.State)}
		if frame.Message != "" {
			status["message"] = w.message(&a2aMessage{
				MessageID: "status-" + taskID, Role: "agent",
				Parts: []a2aPart{{Kind: "text", Text: frame.Message}}, TaskID: taskID, ContextID: contextID,
			})
		}
		if !w.v1 {
			return gin.H{"taskId": taskID, "contextId": contextID, "status": status, "final": frame.Final}
		}
		return gin.H{"taskId": taskID, "contextId": contextID, "status": status}
	}
}

// ---- 入站解析 ----

// a2aIncomingMessage 解析后的入站消息（两线格式归一）。
type a2aIncomingMessage struct {
	MessageID string
	TaskID    string
	ContextID string
	Text      string
	// Decision 是 data part 里的审批决策（approved/answer/note/apiKey/baseUrl）。
	Decision *agentApprovalDecision
	HasData  bool
}

type agentApprovalDecision struct {
	Approved *bool  `json:"approved"`
	Answer   string `json:"answer"`
	Note     string `json:"note"`
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseUrl"`
}

// parseA2AMessage 从 params.message 解析（v0.3 kind 判别与 v1.0 无判别两种）。
func parseA2AMessage(raw json.RawMessage) (a2aIncomingMessage, error) {
	var wire struct {
		MessageID string `json:"messageId"`
		TaskID    string `json:"taskId"`
		ContextID string `json:"contextId"`
		Parts     []struct {
			Kind string          `json:"kind"`
			Text string          `json:"text"`
			Data json.RawMessage `json:"data"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return a2aIncomingMessage{}, err
	}
	out := a2aIncomingMessage{MessageID: wire.MessageID, TaskID: wire.TaskID, ContextID: wire.ContextID}
	texts := []string{}
	for _, part := range wire.Parts {
		switch {
		case part.Kind == "text" || (part.Kind == "" && part.Text != ""):
			if strings.TrimSpace(part.Text) != "" {
				texts = append(texts, part.Text)
			}
		case part.Kind == "data" || (part.Kind == "" && len(part.Data) > 0):
			out.HasData = true
			var decision agentApprovalDecision
			if err := json.Unmarshal(part.Data, &decision); err == nil {
				out.Decision = &decision
			}
		}
	}
	out.Text = strings.Join(texts, "\n")
	return out, nil
}

// a2aNowTime RFC3339 时间戳。
func a2aNowTime() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ---- Agent Card ----

// handleAgentCard 输出 Agent Card（按 A2A-Version 头选线格式；卡本身公开，
// 端点在路由层挂 agentRemoteGate）。
func (s *Server) handleAgentCard(c *gin.Context) {
	wire := a2aWireFromRequest(c)
	base := strings.TrimRight(s.publicBaseURL(c), "/")
	skill := gin.H{
		"id": "elysia-gateway-ops", "name": "网关运维与远程配置",
		"description": "驱动 Elysia API 网关内置 AI 助手：配置模型源/模型组/API Key/自定义协议、拉取模型列表、查询用量与排障。" +
			"等待审批时任务进入 input-required，用带 data part（approved/answer/note）的消息续发即可恢复。",
		"tags":        []string{"gateway", "configuration", "ops", "ai-agent"},
		"inputModes":  []string{"text/plain"},
		"outputModes": []string{"text/plain", "application/json"},
	}
	if wire.v1 {
		c.JSON(http.StatusOK, gin.H{
			"name":        "Elysia API Gateway Agent",
			"description": "Elysia API 网关内置智能体的远程运维面：通过任务驱动网关配置与诊断。",
			"version":     a2aVersionV10,
			"capabilities": gin.H{
				"streaming": true, "pushNotifications": false,
				"extendedAgentCard": gin.H{"supported": false},
			},
			"defaultInputModes":  []string{"text/plain"},
			"defaultOutputModes": []string{"text/plain", "application/json"},
			"skills":             []gin.H{skill},
			"securitySchemes": gin.H{
				"bearerAuth": gin.H{"httpAuthSecurityScheme": gin.H{"scheme": "bearer", "description": "网关签发的 API Key（需 agent 作用域）"}},
			},
			"securityRequirements": []gin.H{{"bearerAuth": []string{}}},
			"supportedInterfaces": []gin.H{
				{"url": base + "/a2a", "protocolBinding": "JSONRPC", "protocolVersion": a2aVersionV10},
				{"url": base + "/a2a", "protocolBinding": "JSONRPC", "protocolVersion": a2aVersionV03},
			},
			"provider": gin.H{"organization": "Elysia API", "url": base},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"protocolVersion":    a2aVersionV03,
		"name":               "Elysia API Gateway Agent",
		"description":        "Elysia API 网关内置智能体的远程运维面：通过任务驱动网关配置与诊断。",
		"url":                base + "/a2a",
		"preferredTransport": "JSONRPC",
		"version":            a2aVersionV03,
		"capabilities": gin.H{
			"streaming": true, "pushNotifications": false, "stateTransitionHistory": false,
		},
		"defaultInputModes":  []string{"text/plain"},
		"defaultOutputModes": []string{"text/plain", "application/json"},
		"skills":             []gin.H{skill},
		"securitySchemes": gin.H{
			"bearerAuth": gin.H{"type": "http", "scheme": "bearer", "description": "网关签发的 API Key（需 agent 作用域）"},
		},
		"security": []gin.H{{"bearerAuth": []string{}}},
		"provider": gin.H{"organization": "Elysia API", "url": base},
	})
}

// a2aWireFromRequest 按版本头选线格式（缺省 v0.3；未知值由 dispatch 拒绝）。
func a2aWireFromRequest(c *gin.Context) a2aWire {
	return a2aWire{v1: strings.TrimSpace(c.Request.Header.Get("A2A-Version")) == a2aVersionV10}
}
