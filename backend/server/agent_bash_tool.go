package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
)

// bash 工具：模型唯一操作入口。一切网关操作都以 elysia CLI 命令表达，
// 支持批处理与 grep/head 管道。门控不在此声明——由 ProbeGates 按解析出
// 的具体命令决定（引擎在执行前询问）。

const agentToolBash = "bash"

type bashTool struct{ server *Server }

func (t *bashTool) Name() string        { return agentToolBash }
func (t *bashTool) Description() string { return "执行 elysia 命令（网关运维 CLI）" }
func (t *bashTool) Gated() bool         { return false }
func (t *bashTool) PermissionKey() string {
	return ""
}

func (t *bashTool) Meta() agent.ToolMeta {
	// 批处理可能串联多条长命令（如真实测试 120s），预算取批级上限；单条
	// 命令仍按各自目标工具的超时在执行器内约束。
	return agent.ToolMeta{RiskLevel: "medium", TimeoutMs: 600_000, PreviewDirection: agent.ClampTail}
}

func (t *bashTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolBash,
		Description: "在网关内置 CLI 中执行 elysia 命令，完成全部网关操作（模型源/模型/模型组/API Key/协议设计/用量统计/日志/出站策略）。" +
			"不确定命令或参数时先运行 elysia help、elysia help <组> 或 elysia help <组> <命令> 查看参考——不要臆测参数。" +
			"支持批处理：命令用 && 连接则前一条失败后停止，用 ; 或换行分隔则继续执行；尾管道支持 | grep <子串> 与 | head <n>。" +
			"涉及写入、真实出站、删除的命令会触发用户审批（命令与权限档会在确认卡展示）。输出超出预算会被截断并标注。",
		Parameters: objectSchema(map[string]any{
			"command": map[string]any{"type": "string", "description": "要执行的 elysia 命令（可多行批处理）"},
		}, "command"),
	}
}

func (t *bashTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	if strings.TrimSpace(params.Command) == "" {
		return agent.ToolError("command 不能为空（运行 elysia help 查看可用命令）", "empty_command")
	}
	return t.server.runAgentCLI(ctx, tctx, params.Command)
}

// ProbeGates 实现引擎的门控探针：解析批处理并把需要审批的命令上报。
// 只要识别出任何门控命令就返回 ok=true 走聚合判定——即使批内还混着解
// 析失败的语句（坏语句与门控命令共用同一解析器，执行时必失败，不会成
// 为越权通道）。完全没有可识别的门控命令且存在解析错误时返回 ok=false，
// 交给执行阶段产出可读错误。
func (t *bashTool) ProbeGates(args json.RawMessage) ([]agent.GateNote, bool) {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &params); err != nil || strings.TrimSpace(params.Command) == "" {
		return nil, false
	}
	notes, err := probeAgentCLI(params.Command)
	if len(notes) == 0 && err != nil {
		return nil, false
	}
	gates := make([]agent.GateNote, 0, len(notes))
	for _, note := range notes {
		gates = append(gates, agent.GateNote{Command: note.Command, PermissionKey: note.Key})
	}
	return gates, true
}
