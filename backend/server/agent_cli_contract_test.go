package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/agent"
)

func TestCLIPromptResponsibilities(t *testing.T) {
	tool := &elysiaCLITool{}
	definition := tool.Definition()
	if tool.Name() != "elysia_cli" || definition.Name != tool.Name() || tool.Description() != "执行 Elysia API 网关运维命令。" {
		t.Fatalf("unexpected CLI contract: %+v", definition)
	}
	for _, term := range []string{"Elysia API 网关运维命令", "以 elysia 开头", "首次使用先查 elysia help", "elysia help <组> [命令]"} {
		if !strings.Contains(definition.Description, term) {
			t.Errorf("tool description missing %q", term)
		}
	}
	overview := renderCLIHelp(nil)
	for _, term := range []string{"&&", "事务", "回滚", "分次调用", "grep", "head", "--limit", "服务端权限", "截断"} {
		if strings.Contains(definition.Description, term) {
			t.Errorf("tool description duplicates CLI help detail %q", term)
		}
		if !strings.Contains(overview, term) {
			t.Errorf("CLI help lost detail %q", term)
		}
	}
	prompt := agentSystemPrompt(&agent.Session{})
	for _, absent := range []string{"&&", "grep", "head", "--strategy", "--secret", "--base-url", "模型列表为空", "bash", "本会话会自动记住"} {
		if strings.Contains(prompt, absent) {
			t.Errorf("system prompt still owns CLI detail %q", absent)
		}
	}
	for _, present := range []string{"简体中文", "实际返回结果", "ask_user", "update_plan", "elysia session title", "```chart", "拿到结果后"} {
		if !strings.Contains(prompt, present) {
			t.Errorf("system prompt lost rule %q", present)
		}
	}
	scoped := agentSystemPrompt(&agent.Session{Mode: agent.ModeEdit, ProtocolID: "target-protocol", Settings: agent.Settings{PlanMode: true}})
	for _, present := range []string{"target-protocol", "保持 ID 不变", "当前为计划模式"} {
		if !strings.Contains(scoped, present) {
			t.Errorf("missing dynamic constraint %q", present)
		}
	}
	if strings.Contains(prompt, "当前为计划模式") {
		t.Fatal("plan mode leaked into ordinary session")
	}
	for _, command := range cliCommandTable() {
		help := helpCommand(command)
		if name := cliHandlerName(command.handler(nil)); name != "" && strings.Contains(help, name) {
			t.Errorf("%s help leaks an internal handler name", command.Path())
		}
		for _, obsolete := range []string{"审批", "会话已记住", "会话测试目标", "执行前会暂停等待用户确认", "后才会出现", "去掉对应段"} {
			if strings.Contains(help, obsolete) {
				t.Errorf("%s has obsolete claim %q", command.Path(), obsolete)
			}
		}
	}
	if !strings.Contains(renderCLIHelp([]string{"model", "ls"}), "空列表不能单独证明未刷新") {
		t.Fatal("model help lost cache semantics")
	}
	if !strings.Contains(renderCLIHelp([]string{"group", "create"}), "sequential=失败回退") {
		t.Fatal("group help lost strategy semantics")
	}
	if !strings.Contains(renderCLIHelp([]string{"protocol", "draft"}), "接入工作流") {
		t.Fatal("draft help lost workflow")
	}
	if !strings.Contains(renderCLIHelp([]string{"outbound", "set"}), "先核实目标地址") {
		t.Fatal("outbound help must check target and authorization")
	}
}

func TestCLIRenamedToolPermissions(t *testing.T) {
	for _, mode := range []string{"ask", "always", "never", "plan", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			s := newAgentIntegrationServer(t)
			name := "elysia_cli"
			if mode == "legacy" {
				name = "bash"
			}
			fake := newFakeAgentModelServer(t, [][]string{
				{openAIChunk("c1", toolCallDelta(0, "call_1", name, `{"command":"elysia group create --name regression-group"}`), "", nil), openAIChunk("c1", map[string]any{}, "tool_calls", nil), openAIDone()},
				{openAIChunk("c2", map[string]any{"role": "assistant", "content": "结果已收到。"}, "", nil), openAIChunk("c2", map[string]any{}, "stop", nil), openAIDone()},
			})
			seedAgentModel(t, s, fake.URL)
			permission := mode
			if mode == "plan" || mode == "legacy" {
				permission = "always"
			}
			c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", fmt.Sprintf(`{"mode":"create","settings":{"modelSourceId":"s1","modelName":"fake-model","allowSave":%q,"planMode":%t}}`, permission, mode == "plan"))
			s.adminCreateAgentSession(c)
			id := decodeAdminData(t, rec)["id"].(string)
			c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+id+"/messages", id, `{"content":"创建 regression-group"}`)
			s.adminSendAgentMessage(c)
			var request struct {
				Tools []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tools"`
			}
			if err := json.Unmarshal([]byte(fake.bodies[0]), &request); err != nil {
				t.Fatal(err)
			}
			names := map[string]bool{}
			for _, entry := range request.Tools {
				names[entry.Function.Name] = true
			}
			if len(names) != 3 || !names["elysia_cli"] || !names["ask_user"] || !names["update_plan"] {
				t.Fatalf("published tools = %v", names)
			}
			groups, _ := s.store.ListGroups(t.Context())
			if (len(groups) == 1) != (mode == "always") {
				t.Fatalf("unexpected write before approval in %s", mode)
			}
			events := parseSSEEvents(t, rec.Body.String())
			if hasAgentEvent(events, "approval_required") != (mode == "ask") {
				t.Fatalf("wrong approval state in %s: %s", mode, rec.Body.String())
			}
			if mode == "legacy" && !strings.Contains(rec.Body.String(), "unknown_tool") {
				t.Fatal("legacy name did not take unknown-tool path")
			}
			if mode == "ask" {
				c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+id+"/approve", id, `{"approved":true}`)
				s.adminApproveAgentAction(c)
				groups, _ = s.store.ListGroups(t.Context())
				if rec.Code != http.StatusOK || len(groups) != 1 {
					t.Fatalf("renamed tool did not resume: %s", rec.Body.String())
				}
			}
		})
	}
}

// 文档直接取工具定义和三级 help，更新命令表后用环境变量重新生成。
func TestCLIReferenceUpToDate(t *testing.T) {
	var b strings.Builder
	b.WriteString("# elysia CLI 参考（Agent elysia_cli 工具）\n\n> 内置模型可见工具：`elysia_cli`、`ask_user`、`update_plan`。本文件由工具定义、命令表与 help 渲染器生成。\n\n")
	b.WriteString("System prompt 负责角色、决策、交互与会话约束；工具描述负责用途、命令前缀与帮助入口；help 负责命令参数、批处理与管道语法、业务约束、示例和流程。\n\n")
	b.WriteString("MCP 也提供 `elysia_cli`：持 `agent` 作用域 Key 直接执行。每次调用无状态；协议草稿和测试目标需要在同一次 command 批处理中复用，接入见 [远程 API 文档](remote-agent-api.md)。\n\n")
	b.WriteString("## 调用契约\n\n" + (&elysiaCLITool{}).Definition().Description + "\n\n```json\n{\"command\":\"elysia source ls\"}\n```\n\n")
	b.WriteString("## 总览\n\n```text\n" + renderCLIHelp(nil) + "```\n\n")
	table := cliCommandTable()
	for _, group := range groupNames(table) {
		b.WriteString("## " + group + "\n\n````text\n" + renderCLIHelp([]string{group}) + "````\n\n")
		for _, command := range commandsOfGroup(table, group) {
			if command.name == "" {
				continue
			}
			b.WriteString("### " + command.Path() + "\n\n````text\n" + helpCommand(command) + "````\n\n")
		}
	}
	b.WriteString("更新方式（在 backend 目录执行）：\n\n```sh\nUPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1\n```\n")
	path := filepath.Join("..", "..", "docs", "agent-cli.md")
	if os.Getenv("UPDATE_AGENT_CLI_DOCS") == "1" {
		if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != b.String() {
		t.Fatal("CLI docs are stale; regenerate with UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1")
	}
}
