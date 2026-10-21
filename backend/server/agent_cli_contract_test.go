package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Permission semantics live in agent tests; this checks CLI approval/resume.
func TestCLIApprovalAndResume(t *testing.T) {
	t.Parallel()
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "elysia_cli", `{"command":"elysia group create --name regression-group"}`), "", nil), openAIChunk("c1", map[string]any{}, "tool_calls", nil), openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "结果已收到。"}, "", nil), openAIChunk("c2", map[string]any{}, "stop", nil), openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create","settings":{"modelSourceId":"s1","modelName":"fake-model","allowSave":"ask"}}`)
	s.adminCreateAgentSession(contextForTest(c), c)
	flushTestResponse(c)
	id := decodeAdminData(t, rec)["id"].(string)
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+id+"/messages", id, `{"content":"创建 regression-group"}`)
	s.adminSendAgentMessage(contextForTest(c), c)
	flushTestResponse(c)
	groups, _ := s.store.ListGroups(t.Context())
	if len(groups) != 0 || !hasAgentEvent(parseSSEEvents(t, rec.Body.String()), "approval_required") {
		t.Fatalf("write must wait for approval: %s", rec.Body.String())
	}
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+id+"/approve", id, `{"approved":true}`)
	s.adminApproveAgentAction(contextForTest(c), c)
	flushTestResponse(c)
	groups, _ = s.store.ListGroups(t.Context())
	if rec.Code != http.StatusOK || len(groups) != 1 {
		t.Fatalf("CLI did not resume: %s", rec.Body.String())
	}
}

// 文档直接取工具定义和三级 help，更新命令表后用环境变量重新生成。
func TestCLIReferenceUpToDate(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("# elysia CLI 参考\n\n[文档首页](README.md) · **简体中文** · [English](agent-cli.en.md)\n\n本页由工具定义、命令表和运行时帮助生成。`elysia` 是 `elysia_cli` 的命令语法，不是独立终端程序。下文 `text` 块中的尖括号和省略号表示待替换内容；不是可直接粘贴到 shell 的脚本。\n\n内置助手还提供 `ask_user` 和 `update_plan`，职责及权限见[工具目录](agent-tools-catalog.md)。REST/A2A 遵循会话审批；MCP 持 `agent` 作用域 Key 直接执行，不进入该审批链。MCP 每次调用无状态，草稿和测试目标须在同一次 `command` 批处理中复用，见[远程接入](remote-agent-api.md)。\n\n帮助原文保持运行时内容。阅读时注意：`key create` 的名称提示仍写有“创建后不可改”，但 `key update --new-name` 支持改名；“加密存储”以主密钥成功加载为前提；调用日志正文取决于[捕获策略](deployment.md#请求日志)，默认不保存。`--thinking` 的旧帮助档位也不等同于当前模型能力值 `both` / `non-thinking-only` / `thinking-only`；它与 Agent 的 `thinkingEffort` 是不同字段。协议完整字段以[定义参考](protocol-definition-reference.md)为准。\n\n")
	b.WriteString(`## 语法与命令框架迁移

使用 mvdan.cc/sh/v3 v3.14.1（POSIX 语法）和 Cobra v1.10.2 / pflag v1.0.10。解析器产出 AST，受限适配层调用原有网关 handler，不启动系统 shell、子进程，也不读取环境变量或执行文件通配。

支持字面参数、单/双引号拼接、空引号、反斜杠转义与续行、注释、分号、换行、&& 和原有内存 grep/head 过滤器。单词内的花括号、通配符及 ~ 保持字面值。含 $、反引号或 shell 运算符的 JSON、密钥等字面值请使用单引号。

### 兼容性变化

- POSIX 词法替代自定义扫描器：未引用的反斜杠转义后一字符，词首 # 开始注释。末尾 && 或 |、连续分号、未闭合引号均为语法错误。语法错误及禁止的顶层执行修饰符使整个脚本在执行前失败；其他不支持的语句或无效命令在对应语句处失败，保留 && / 分号的批处理语义。
- 不支持变量、算术、命令及进程替换、赋值、重定向（含 here-document）、后台执行、||、否定、子 shell 和控制结构；作为参数的同类文本须引用，解析器不会求值。
- 命令查找、未知命令/flag 错误及帮助排版采用 Cobra/pflag，不再限制命令路径深度。组级 help 列出子命令，命令级 help 展示 flag、示例、位置参数与完整语义；支持 group member 等嵌套组。
- 支持命令后 --help / -h。帮助不调用业务 handler，也不触发写审批；--help=false 仍执行并按业务规则审批。位置参数以 - 开头时使用 -- 结束 flag 解析；按照 pflag 规则，必需的 flag 值本身也可从 - 开始。
- 保留参数缺失与显式空值的区别、裸布尔 flag、大小写不敏感的 true/false、1/0、yes/no、可重复逗号列表及标量后值覆盖。显式布尔值使用 =；列表不采用 CSV 引号规则。
- CLI 回显、进度和 Agent 事件共用基于 AST 单词范围的密钥脱敏，覆盖转义空格及拼接引号；展示语法无效或含展开的脚本时使用诊断占位符。

业务响应结构、输出预算、取消、handler 超时、&& 短路及 Agent/MCP 权限策略保持原有语义。本次迁移涉及的应用层 JSON 调用使用 Sonic。

`)
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
