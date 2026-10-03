package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
)

// elysia_cli is the thin adapter exposed to the built-in Agent. The CLI
// handlers themselves are independent of agent.Tool; this adapter is where
// session state, approval probing, and Agent result types meet the CLI.
const agentToolCLI = "elysia_cli"

const cliToolDescription = "执行 Elysia API 网关运维命令。每条命令须以 elysia 开头；" +
	"首次使用先查 elysia help，具体用法按需查 elysia help <组> [命令]。"

type elysiaCLITool struct{ server *Server }

func (t *elysiaCLITool) Name() string          { return agentToolCLI }
func (t *elysiaCLITool) Description() string   { return "执行 Elysia API 网关运维命令。" }
func (t *elysiaCLITool) Gated() bool           { return false }
func (t *elysiaCLITool) PermissionKey() string { return "" }

func (t *elysiaCLITool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "medium", TimeoutMs: 600_000, PreviewDirection: agent.ClampTail, MaxModelBytes: 48 * 1024}
}

func (t *elysiaCLITool) Definition() agent.FunctionDefinition {
	return agent.FunctionDefinition{
		Name:        agentToolCLI,
		Description: cliToolDescription,
		Parameters: objectSchema(map[string]any{
			"command": map[string]any{"type": "string", "description": "要执行的 elysia 命令（可多行批处理）"},
		}, "command"),
	}
}

type cliToolParams struct {
	Command string `json:"command"`
}

func (t *elysiaCLITool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	params, err := parseCLIToolParams(args)
	if err != nil {
		return agent.ToolError("参数解析失败", err.Error())
	}
	if strings.TrimSpace(params.Command) == "" {
		return agent.ToolError("command 不能为空（运行 elysia help 查看可用命令）", "empty_command")
	}
	adapter := &agentCLIContext{inner: tctx}
	return agentResultFromCLI(t.server.runCLIWithOptions(ctx, adapter, params.Command, true))
}

// runAgentCLI 仅供既有集成测试直接驱动批处理;生产工具面走
// elysiaCLITool.Execute(同一 runCLIWithOptions 实现)。
func (s *Server) runAgentCLI(ctx context.Context, tctx agent.ToolContext, script string) agent.ToolResult {
	return agentResultFromCLI(s.runCLIWithOptions(ctx, &agentCLIContext{inner: tctx}, script, true))
}

func agentResultFromCLI(result CLIResult) agent.ToolResult {
	return agent.ToolResult{OK: result.OK, Summary: result.Summary, Data: result.Data, SecretValues: result.SecretValues}
}

type agentCLIContext struct{ inner agent.ToolContext }

func (c *agentCLIContext) Draft() json.RawMessage               { return c.inner.Draft() }
func (c *agentCLIContext) SetDraft(draft json.RawMessage) error { return c.inner.SetDraft(draft) }
func (c *agentCLIContext) TestTarget() (string, string)         { return c.inner.TestTarget() }
func (c *agentCLIContext) SetTestTarget(baseURL, apiKey string) error {
	return c.inner.SetTestTarget(baseURL, apiKey)
}
func (c *agentCLIContext) SetTitle(title string) error { return c.inner.SetTitle(title) }
func (c *agentCLIContext) EditProtocolID() string {
	meta := c.inner.SessionMeta()
	if meta.Mode == agent.ModeEdit {
		return meta.ProtocolID
	}
	return ""
}
func (c *agentCLIContext) ReportProgress(text string) {
	if reporter, ok := c.inner.(agent.ProgressReporter); ok {
		reporter.ReportProgress(text)
	}
}

// ProbeGates is owned by the Agent adapter. CLI execution itself has no
// approval policy; the adapter maps neutral command effects to Agent keys.
func (t *elysiaCLITool) ProbeGates(args json.RawMessage) ([]agent.GateNote, bool) {
	params, err := parseCLIToolParams(args)
	if err != nil || strings.TrimSpace(params.Command) == "" {
		return nil, false
	}
	notes, parseErr := probeAgentCLI(params.Command)
	if len(notes) == 0 && parseErr != nil {
		return nil, false
	}
	return notes, true
}

func probeAgentCLI(script string) ([]agent.GateNote, error) {
	segments, err := cliSplitStatements(script)
	if err != nil {
		return nil, err
	}
	notes := []agent.GateNote{}
	var parseErr error
	for _, segment := range segments {
		statement, err := cliParseStatement(segment.raw)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		if isCLIHelpArgs(statement.args) {
			continue
		}
		if len(statement.args) >= 2 && statement.args[0] == "session" && statement.args[1] == "title" {
			continue
		}
		inv, err := cliResolve(statement.args)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		handler := inv.command.handler(nil)
		permissionKey := ""
		switch cliEffectOf(handler) {
		case CLIEffectWrite:
			permissionKey = agent.PermissionKeySave
		case CLIEffectOutbound:
			permissionKey = agent.PermissionKeyLiveTest
		case CLIEffectDelete:
			permissionKey = agent.PermissionKeyDelete
		}
		if permissionKey != "" {
			notes = append(notes, agent.GateNote{Command: "$ " + redactCLICommandLine(segment.raw), PermissionKey: permissionKey})
		}
	}
	return notes, parseErr
}

func parseCLIToolParams(args json.RawMessage) (cliToolParams, error) {
	var params cliToolParams
	if err := json.Unmarshal(args, &params); err != nil {
		return params, fmt.Errorf("%w", err)
	}
	return params, nil
}
