# Agent 工具与提示职责

内置助手的模型只接收以下三个工具定义：

| 工具 | 职责 |
| --- | --- |
| `elysia_cli` | 执行 Elysia API 网关运维命令，唯一业务操作入口 |
| `ask_user` | 向用户提出带选项的问题，并等待回答 |
| `update_plan` | 记录分析、步骤及执行进度 |

`elysia_cli` 的参数保持为 `{"command":"elysia source ls"}`。不提供旧工具名别名，也不迁移历史消息；旧待审批调用走现有过期处理，须重新发起任务。

## 提示内容的职责

| 位置 | 内容 | 实现 |
| --- | --- | --- |
| System prompt | 角色、决策规则、中文交互、计划与提问、标题和图表；动态注入计划模式及编辑目标 ID | [agent_prompt.go](../backend/server/agent_prompt.go) |
| `elysia_cli` 工具描述 | 用途、命令前缀和分级帮助入口 | [agent_cli_tool.go](../backend/server/agent_cli_tool.go) |
| `elysia help` | 命令与参数、批处理及管道语法、业务约束、示例和协议接入流程 | [agent_cli_help.go](../backend/server/agent_cli_help.go) |

工具描述引导首次使用时先查 help，已知用法可直接执行；详细规则按需从帮助读取，不要求每次调用前重复查询，也不依赖内置助手的 system prompt。完整参考见 [自动生成的 CLI 文档](agent-cli.md)，与命令表和运行时 help 同源，不再另行维护内部工具参数手册。

## 执行与权限

[agent_cli.go](../backend/server/agent_cli.go) 继续使用现有解析器和业务处理器。内部处理器名只用于路由实现，不作为模型可调用工具公布。批次门控按解析出的实际命令聚合判定。

内置助手的权限键为 `save`、`live_test`、`delete`，分别受会话的 `ask`（暂停确认）、`always`（自动放行）、`never`（拒绝）控制；计划模式阻止受控操作。通过 REST/A2A 远程驱动内置助手时，同样遵循这些规则；MCP 的 `elysia_cli` 不进入该审批链。

MCP 只提供直接执行的 `elysia_cli`，复用相同描述中的语法契约、解析器和业务处理器。此入口持 `agent` 作用域 Key 直接执行，不调用内置模型，也不使用会话审批档或计划模式。MCP 每次调用创建临时 CLI 上下文，普通运维只需 `command`；协议草稿、测试目标和凭证需要在同一次 `command` 批处理中复用。REST/A2A 的内置助手会话仍由远程 Agent 服务管理，接入示例见 [远程 API 文档](remote-agent-api.md)。

两个入口都仅合并参数已知、无需观察中间结果的命令，批处理不提供整体事务或自动回滚。

## 验证

在 `backend` 目录运行 `go test ./agent ./server`，覆盖解析器、业务处理器、工具公布、审批恢复、权限模式、旧名称失效，以及 MCP 直接执行、作用域鉴权、无状态批处理、调用隔离和取消。前端的 `agent-stream-and-chart.spec.ts` 覆盖实时命令展示与历史回放。

真实模型任务默认跳过。显式提供包含 `source`（`storage.ModelSource`）和 `model`（`storage.Model`）的受保护 JSON 文件后，可运行：

```sh
ELYSIA_AGENT_EVAL_MODEL_FILE=/path/to/private-model-fixture.json go test ./server -run '^TestCLILivePromptTasks$' -count=1 -v -timeout 12m
```

此测试产生真实模型用量，但运维数据全部位于临时数据库，模型列表上游为本地合成服务；覆盖失败日志、创建模型组、空模型列表和协议草稿修改。检查测试日志中的命令顺序、help 查询和最终报告；测试完成后删除凭证文件。
