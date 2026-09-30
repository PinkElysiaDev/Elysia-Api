# Agent 工具与权限

[文档首页](README.md) · **简体中文** · [English](agent-tools-catalog.en.md)

## 工具契约

内置助手向模型公布三个工具。`elysia` 是 `elysia_cli` 内的命令语法，不是可安装的终端程序。

| 工具 | 用途 |
| --- | --- |
| `elysia_cli` | 执行网关管理命令 |
| `ask_user` | 提问并等待用户选择或输入 |
| `update_plan` | 记录任务步骤与进度 |

调用参数：

```json
{"command":"elysia source ls"}
```

旧工具名没有别名兼容。历史消息不迁移；旧待审批调用过期后需要重新发起任务。

## 提示内容的职责

| 来源 | 内容 |
| --- | --- |
| [System prompt](../backend/server/agent_prompt.go) | 角色、中文交互、计划、提问、标题和图表；动态加入计划模式与编辑目标 ID |
| [工具描述](../backend/server/agent_cli_tool.go) | 用途、命令前缀和帮助入口 |
| [命令帮助](../backend/server/agent_cli_help.go) | 参数、批处理、管道、业务约束和接入示例 |

首次使用先查 `elysia help`，具体命令用 `elysia help <组> [命令]`。已知用法可直接执行。完整命令参考见 [CLI 手册](agent-cli.md)。

## 执行与权限

[CLI 解析器](../backend/server/agent_cli.go) 将命令交给业务处理器。内部处理器名不是模型工具。批次按实际解析出的命令聚合权限要求。

| 入口 | 权限与状态 |
| --- | --- |
| WebUI 内置助手、REST、A2A | 使用会话权限 `save`、`live_test`、`delete`；`ask` 暂停确认，`always` 放行，`never` 拒绝；计划模式阻止受控操作 |
| MCP | 只公布 `elysia_cli`；持 `agent` 作用域 Key 直接执行，不调用模型，不进入内置助手审批链，也不使用会话计划模式 |

MCP 每次调用创建临时 CLI 上下文。协议草稿、测试地址和凭证需要在同一次 `command` 批处理中复用。REST/A2A 的会话由远程 Agent 服务管理，见 [远程接入](remote-agent-api.md)。

两个入口的批处理均不提供整体事务或自动回滚。仅合并参数已知、不依赖中间结果的操作。真实上游测试可能产生费用；日志正文是否可读取取决于[捕获策略](deployment.md#请求日志)。

## 验证

在 `backend` 目录运行：

```sh
go test ./agent ./server
```

现有测试覆盖解析、工具公布、权限模式、审批恢复、旧名称失效，以及 MCP 鉴权、调用隔离、批处理和取消。前端 `agent-stream-and-chart.spec.ts` 验证实时命令展示与历史回放。

真实模型测试默认跳过。只有明确需要验证实际模型行为时，准备包含 `source`（`storage.ModelSource`）和 `model`（`storage.Model`）的私有 JSON 文件，再运行以下可选命令。此命令产生真实模型用量：

```sh
ELYSIA_AGENT_EVAL_MODEL_FILE=/path/to/private-model-fixture.json go test ./server -run '^TestCLILivePromptTasks$' -count=1 -v -timeout 12m
```

运维数据写入临时数据库，模型列表上游使用本地合成服务。检查失败日志、模型组创建、空模型列表和协议草稿修改任务的命令顺序、帮助查询及最终报告。凭证文件不要提交，测试完成后删除。

## 故障处理

- 命令被拒绝：先检查入口、Key 作用域、会话权限和计划模式。
- MCP 找不到草稿或测试目标：将相关操作放进同一次调用；跨调用不保留上下文。
- 批次中途失败：查看已完成操作，再决定补偿或重试；不要假定之前的写入已回滚。
