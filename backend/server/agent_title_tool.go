package server

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
)

// update_title：模型在理解任务后把会话标题改成对目标的简短概括。总览卡片
// 用它做名称，所以必须短、必须是目标而不是用户原话的截断。

const (
	agentToolUpdateTitle = "update_title"
	titleRuneLimit       = 16
)

type updateTitleTool struct{}

func (t *updateTitleTool) Name() string { return agentToolUpdateTitle }
func (t *updateTitleTool) Description() string {
	return "用一句话概括当前任务并设为会话标题"
}
func (t *updateTitleTool) Gated() bool           { return false }
func (t *updateTitleTool) PermissionKey() string { return "" }
func (t *updateTitleTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{RiskLevel: "low"}
}

func (t *updateTitleTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolUpdateTitle,
		Description: "把会话标题改写成对任务目标的简洁概括（动宾短语，不超过 16 个字，不要复述用户原话）。理解任务后调用一次；任务目标变化时再更新。",
		Parameters: objectSchema(map[string]any{
			"title": map[string]any{"type": "string", "description": "新标题，不超过 16 个字"},
		}, "title"),
	}
}

func (t *updateTitleTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	var params struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolError("参数解析失败", "invalid_args")
	}
	title := strings.TrimSpace(params.Title)
	if title == "" {
		return agent.ToolError("标题不能为空", "empty_title")
	}
	if utf8.RuneCountInString(title) > titleRuneLimit {
		return agent.ToolError("标题超过 16 个字，请缩短到能一眼看懂任务目标", "title_too_long")
	}
	if err := tctx.SetTitle(title); err != nil {
		return agent.ToolError("标题保存失败: "+err.Error(), "save_failed")
	}
	return agent.ToolResult{OK: true, Summary: "标题已更新为「" + title + "」"}
}
