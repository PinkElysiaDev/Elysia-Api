package server

import (
	"context"
	"encoding/json"
)

// MCP 工具面只暴露独立的 elysia_cli。内置 Agent 的会话、消息和审批
// 仍通过 REST/A2A 服务提供，不在 MCP 注册或执行。

// mcpTool 是一个 MCP 工具的静态定义与执行入口。
type mcpTool struct {
	name        string
	title       string
	description string
	schema      map[string]any
	// streaming=true 时 invoke 拿到 progress 回调并由传输层走 SSE。
	streaming bool
	// invoke 返回 structuredContent（map）；业务失败返回 error → isError。
	invoke func(ctx context.Context, s *Server, args json.RawMessage, progress func(message string)) (any, error)
}

// mcpToolset 返回静态工具定义；不持有会话或执行状态。
func mcpToolset() []mcpTool {
	return []mcpTool{mcpCLITool()}
}

// mcpFindTool 按名取工具定义。
func mcpFindTool(name string) *mcpTool {
	tools := mcpToolset()
	for i := range tools {
		if tools[i].name == name {
			return &tools[i]
		}
	}
	return nil
}
