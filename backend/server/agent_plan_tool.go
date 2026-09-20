package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
)

// update_plan 工具：模型维护多步任务的工作方案清单，侧边栏「方案」页实时
// 展示。不门控——它只改会话内的展示态，不产生任何外部效果。

const agentToolUpdatePlan = "update_plan"

type updatePlanTool struct{}

func (t *updatePlanTool) Name() string { return agentToolUpdatePlan }
func (t *updatePlanTool) Description() string {
	return "更新工作方案清单（侧边栏实时展示）"
}
func (t *updatePlanTool) Gated() bool           { return false }
func (t *updatePlanTool) PermissionKey() string { return "" }

func (t *updatePlanTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function",
		Name: agentToolUpdatePlan,
		Description: "更新当前任务的工作方案清单（整体替换）。多步任务开始时先列出步骤（status=pending），" +
			"推进到某步时置 in_progress，完成后置 done——用户在侧边栏实时可见。步骤应是具体可验证的动作，通常 3-7 条。",
		Parameters: objectSchema(map[string]any{
			"plan": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":  map[string]any{"type": "string", "description": "步骤标题（简短动宾短语）"},
						"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}},
					},
					"required": []string{"title", "status"},
				},
				"description": "完整步骤列表（整体替换当前方案）",
			},
		}, "plan"),
	}
}

func (t *updatePlanTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	var params struct {
		Plan []agent.PlanStep `json:"plan"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.ToolResult{OK: false, Summary: "参数解析失败", Data: map[string]any{"error": err.Error()}}
	}
	if len(params.Plan) == 0 {
		return agent.ToolResult{OK: false, Summary: "plan 不能为空", Data: map[string]any{"error": "empty_plan"}}
	}
	if len(params.Plan) > 12 {
		return agent.ToolResult{OK: false, Summary: "方案步骤过多（上限 12 条），请合并", Data: map[string]any{"error": "too_many_steps"}}
	}
	seen := map[string]bool{}
	for _, step := range params.Plan {
		title := strings.TrimSpace(step.Title)
		if title == "" {
			return agent.ToolResult{OK: false, Summary: "步骤标题不能为空", Data: map[string]any{"error": "empty_title"}}
		}
		switch step.Status {
		case "pending", "in_progress", "done":
		default:
			return agent.ToolResult{OK: false, Summary: fmt.Sprintf("步骤 %q 的 status 非法（pending/in_progress/done）", title), Data: map[string]any{"error": "invalid_status"}}
		}
		if seen[title] {
			return agent.ToolResult{OK: false, Summary: fmt.Sprintf("步骤 %q 重复", title), Data: map[string]any{"error": "duplicate_title"}}
		}
		seen[title] = true
	}
	if err := tctx.SetPlan(params.Plan); err != nil {
		return agent.ToolResult{OK: false, Summary: "方案保存失败: " + err.Error(), Data: map[string]any{"error": err.Error()}}
	}
	done := 0
	for _, step := range params.Plan {
		if step.Status == "done" {
			done++
		}
	}
	return agent.ToolResult{OK: true, Summary: fmt.Sprintf("方案已更新（%d/%d 完成）", done, len(params.Plan)), Data: map[string]any{"steps": len(params.Plan), "done": done}}
}
