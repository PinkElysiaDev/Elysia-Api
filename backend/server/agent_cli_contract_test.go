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
	b.WriteString("# elysia CLI 参考\n\n[文档首页](README.md) · **简体中文** · [English](agent-cli.en.md)\n\n本页由工具定义、命令表和运行时帮助生成。`elysia` 是 `elysia_cli` 的命令语法，不是独立终端程序。下文 `text` 块中的尖括号和省略号表示待替换内容；不是可直接粘贴到 shell 的脚本。\n\n内置助手还提供 `ask_user` 和 `update_plan`，职责及权限见[工具目录](agent-tools-catalog.md)。REST/A2A 遵循会话审批；MCP 持 `agent` 作用域 Key 直接执行，不进入该审批链。MCP 每次调用无状态，草稿和测试目标须在同一次 `command` 批处理中复用，见[远程接入](remote-agent-api.md)。\n\n帮助原文保持运行时内容。阅读时注意：`key create` 的名称提示仍写有“创建后不可改”，但 `key update --new-name` 支持改名；“加密存储”以主密钥成功加载为前提；调用日志正文取决于[捕获策略](deployment.md#请求日志)，默认不保存。`--thinking` 的旧帮助档位也不等同于当前模型能力值 `both` / `non-thinking-only` / `thinking-only`；它与 Agent 的 `thinkingEffort` 是不同字段。协议完整字段以[定义参考](protocol-definition-reference.md)为准。\n\n")
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
	b.WriteString("## 文档同步\n\n中文内容由本测试生成；排版修改应落在生成模板，不直接编辑生成文件。英文完整译本人工维护，命令、参数、默认值、约束和示例必须与同次中文生成结果逐项同步。运行时帮助仍使用中文。\n\n在 `backend` 目录重新生成中文，再检查一致性：\n\n```sh\nUPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1\ngo test ./server -run '^TestCLIReferenceUpToDate$' -count=1\n```\n")
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
