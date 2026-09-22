package server

import (
	"context"
	"encoding/json"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
)

// ask_user 不在这里执行提问：引擎看到这个工具调用就暂停轮次，把问题交给
// 用户。这里的 Execute 只是兜底——正常路径到不了。

const agentToolAskUser = "ask_user"

type askUserTool struct{}

func (t *askUserTool) Name() string          { return agentToolAskUser }
func (t *askUserTool) Description() string   { return "向用户提出一个需要选择的问题" }
func (t *askUserTool) Gated() bool           { return false }
func (t *askUserTool) PermissionKey() string { return "" }
func (t *askUserTool) Meta() agent.ToolMeta {
	return agent.ToolMeta{ReadOnly: true, RiskLevel: "low"}
}

func (t *askUserTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{
		Type: "function", Name: agentToolAskUser,
		Description: "信息不足以继续、且有明确选项时，向用户提问并暂停本轮。用户作答后你会拿到 answer 继续。不要用它做开放式寒暄。",
		Parameters: objectSchema(map[string]any{
			"question":     map[string]any{"type": "string", "description": "要问用户的问题"},
			"options":      map[string]any{"type": "array", "description": "预设选项", "items": map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}}}},
			"allow_custom": map[string]any{"type": "boolean", "description": "是否允许用户输入选项之外的答案"},
		}),
	}
}

func (t *askUserTool) Execute(ctx context.Context, tctx agent.ToolContext, args json.RawMessage) agent.ToolResult {
	return agent.ToolResult{OK: false, Summary: "提问应由引擎暂停处理", Data: map[string]string{"error": "unhandled_question"}}
}

// parseAskQuestion 从工具参数解析提问。问题为空时返回 false。
func parseAskQuestion(call relay.MaheshvaraToolCall) (agent.AskQuestion, bool) {
	var payload struct {
		Question    string            `json:"question"`
		Options     []agent.AskOption `json:"options"`
		AllowCustom bool              `json:"allow_custom"`
	}
	if err := json.Unmarshal(call.Arguments, &payload); err != nil || payload.Question == "" {
		return agent.AskQuestion{}, false
	}
	return agent.AskQuestion{CallID: call.ID, Question: payload.Question, Options: payload.Options, AllowCustom: payload.AllowCustom}, true
}
