package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func mcpCLITool() mcpTool {
	return mcpTool{
		name:        agentToolCLI,
		title:       "执行 Elysia API 网关运维命令",
		description: cliToolDescription + "每次调用无状态，草稿和测试目标仅在同一次批处理中复用。",
		schema: objectSchema(map[string]any{
			"command": map[string]any{"type": "string", "description": "要执行的 elysia 命令（可多行批处理）"},
		}, "command"),
		streaming: true,
		invoke: func(ctx context.Context, s *Server, args json.RawMessage, progress func(string)) (any, error) {
			return s.runMCPCLI(ctx, args, progress)
		},
	}
}

func (s *Server) runMCPCLI(ctx context.Context, args json.RawMessage, progress func(string)) (any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, fmt.Errorf("参数解析失败: %w", err)
	}
	if _, exists := raw["sessionId"]; exists {
		return nil, fmt.Errorf("MCP elysia_cli 是无状态工具，不支持 sessionId；请把需要复用草稿或测试目标的命令放在同一次 command 批处理中")
	}
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("参数解析失败: %w", err)
	}
	if strings.TrimSpace(params.Command) == "" {
		return nil, fmt.Errorf("command 不能为空（运行 elysia help 查看可用命令）")
	}

	execCtx, cancel := context.WithTimeout(ctx, cliBatchTimeout)
	defer cancel()
	result := s.runCLI(execCtx, &mcpCLIContext{progress: progress}, params.Command)
	executionErr := execCtx.Err()
	view := map[string]any{}
	_ = json.Unmarshal(result.MarshalData(), &view)
	view["ok"], view["summary"] = result.OK, result.Summary
	if executionErr != nil {
		view["ok"], view["exitCode"] = false, 1
		return view, fmt.Errorf("执行已取消或超时；部分命令可能已经完成，请按返回结果核对：%w", executionErr)
	}
	if !result.OK {
		return view, fmt.Errorf("%s", result.Summary)
	}
	return view, nil
}

// mcpCLIContext holds state only for one tools/call invocation. It deliberately
// has no storage or Agent session identity.
type mcpCLIContext struct {
	draft    json.RawMessage
	baseURL  string
	apiKey   string
	progress func(string)
}

func (c *mcpCLIContext) Draft() json.RawMessage {
	return append(json.RawMessage(nil), c.draft...)
}
func (c *mcpCLIContext) SetDraft(draft json.RawMessage) error {
	c.draft = append(json.RawMessage(nil), draft...)
	return nil
}
func (c *mcpCLIContext) TestTarget() (string, string) { return c.baseURL, c.apiKey }
func (c *mcpCLIContext) SetTestTarget(baseURL, apiKey string) error {
	if strings.TrimSpace(baseURL) != "" {
		c.baseURL = strings.TrimSpace(baseURL)
	}
	if strings.TrimSpace(apiKey) != "" {
		c.apiKey = apiKey
	}
	return nil
}
func (*mcpCLIContext) EditProtocolID() string { return "" }
func (c *mcpCLIContext) ReportProgress(message string) {
	if c.progress != nil {
		c.progress(message)
	}
}
