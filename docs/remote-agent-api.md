# AI 助手远程暴露面（REST / MCP / A2A）

外部程序可以通过 REST / A2A 驱动内置 AI 助手（会话 / 消息 / 审批三原语），也可以通过 MCP 的 `elysia_cli` 直接执行运维命令。REST/A2A 共用会话引擎和审批规则；MCP 复用 CLI 解析器与业务处理器，不调用内置模型。

| 协议面 | 端点 | 适用客户端 |
|---|---|---|
| REST | `/api/agent/*` | 脚本、curl、第三方面板 |
| MCP（Model Context Protocol） | `POST /mcp` | Claude Desktop / Cursor / 各类 agent 框架 |
| A2A（Agent2Agent） | `POST /a2a` + `GET /.well-known/agent-card.json` | 其他 agent（LangChain、CrewAI、a2a-go 客户端等） |

## 鉴权与总开关

- 三个面统一要求 **Bearer API Key 且带 `agent` 作用域**：在「运行配置 → AI 助手远程访问」创建远程访问 Key，并通过 `Authorization: Bearer <Key>` 传入。普通网关调用 Key 不具备该作用域；无作用域返回 403，无效或未提供 Key 返回 401（带 `WWW-Authenticate`）。
- `config.json` 的 `agentRemote.enabled`（默认 `true`）为总开关，`false` 时三个面全部 404；`agentRemote.publicUrl` 用于反向代理后 Agent Card 的绝对地址（缺省按请求 Host 推导）。
- `allowedGroups`（模型组维度）与 `scopes`（端点维度）正交：控制助手不要求任何模型组授权。

## REST：`/api/agent/*`

与 `/api/admin/agent/*` 完全同语义（同一组 handler），仅鉴权链不同。响应信封 `{ok, data}` / `{ok, error:{code,message}}`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/agent/sessions?status=&limit=&offset=` | 会话列表；`status` 取 `idle/running/waiting_approval`（引擎运行态口径），`limit` ≤ 200，返回 `{items, total}`（管理面板不带参数即全量） |
| POST | `/api/agent/sessions` | 建会话 `{title?, mode?, protocolId?, settings?}` |
| GET | `/api/agent/sessions/:id` | 详情 + 全部消息 |
| PATCH | `/api/agent/sessions/:id` | 标题/设置增量（含权限档 allowLiveTest/allowSave/allowDelete） |
| DELETE | `/api/agent/sessions/:id` | 删除（先停轮次） |
| POST | `/api/agent/sessions/:id/messages` | 发送消息，**SSE 事件流**（与管理面板同 14 种事件，15s 心跳） |
| POST | `/api/agent/sessions/:id/approve` | 审批/作答/方案确认 `{approved, answer?, note?, apiKey?, baseUrl?}`，SSE 续跑 |
| POST | `/api/agent/sessions/:id/stop` | 停止轮次 |
| DELETE | `/api/agent/sessions/:id/messages?afterSeq=` | 清空/截断消息 |
| POST | `/api/agent/sessions/:id/restore-draft` | 草稿回滚 |

轮次与 HTTP 连接解耦：SSE 断开不终止轮次，结果持续落库，可重连或轮询列表获取终态。

## MCP：`POST /mcp`（Streamable HTTP）

无状态单端点，双世代并存：

- **Legacy（initialize 握手，2024-11-05 ~ 2025-11-25 语义）**——存量客户端（Claude Desktop、Cursor 等）：`initialize`（版本回显协商）→ `notifications/initialized` → `ping` / `tools/list` / `tools/call`。不签发 `Mcp-Session-Id`（无状态合法）；GET/DELETE 返回 405；`MCP-Protocol-Version` 头非法返回 400。
- **Modern（2026-07-28）**：无握手，每请求 `params._meta` 携带 `io.modelcontextprotocol/protocolVersion`；`MCP-Protocol-Version` 头与 body 一致（否则 `-32020`）、镜像头 `Mcp-Method`/`Mcp-Name` 校验、`server/discover`、所有 result 带 `resultType`、`tools/list` 带 `ttlMs/cacheScope`；**SSE 断流即取消**当前 CLI 批处理。

约束：POST 的 `Accept` 必须同时含 `application/json` 与 `text/event-stream`（否则 406）；带 `Origin` 头的浏览器请求须同源或回环（防 DNS rebinding）；批量请求不支持（2025-06-18 起已从规范移除）。

### 工具集（1 个）

MCP 只公布 `elysia_cli`。内置 Agent 的会话、消息和审批工具不通过 MCP 暴露；它们仍由上面的 REST 接口和 A2A 接口提供。

| 工具 | 说明 |
|---|---|
| `elysia_cli` | **流式工具**：执行 `elysia` 网关运维命令，每次调用无状态 |

工具执行的业务失败以 `isError: true` 返回（可自我纠正），协议级错误（未知工具等）走 JSON-RPC error。

### 外部 Agent 直接运维

在「运行配置 → AI 助手远程访问」的每个已启用 Key 旁点击「复制 MCP JSON」，即可取得包含 MCP 地址与该 Key 的配置。地址优先使用「对外基础地址」，留空时使用当前访问地址。复制时才按需读取真实 Key；远程访问或 Key 停用时不可复制。

生成的配置使用常见的 `mcpServers` 格式，适用于支持此结构和 HTTP 传输的客户端；客户端若有自己的配置格式，使用其中的 `url` 与 `headers` 填入对应项：

```json
{
  "mcpServers": {
    "elysia": {
      "type": "http",
      "url": "https://gw.example.com/mcp",
      "headers": {"Authorization": "Bearer <远程访问 Key>"}
    }
  }
}
```

客户端使用 Streamable HTTP 连接网关的 `/mcp`，携带远程访问 Key。`tools/list` 包含 `elysia_cli`，最小调用为：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "elysia_cli",
    "arguments": {"command": "elysia source ls"}
  }
}
```

该入口直接执行查询、修改、真实出站和删除，出站策略、命令校验和业务约束仍然生效。需要驱动内置模型时，请使用 REST/A2A 会话接口，它们遵循会话审批规则。

参数：

| 参数 | 必填 | 说明 |
|---|---|---|
| `command` | 是 | 每条业务命令以 `elysia` 开头；通过 `elysia help`、`elysia help <组>`、`elysia help <组> <命令>` 按需查询 |

MCP 每次调用创建一次临时 CLI 上下文。`elysia protocol draft ; elysia protocol preview ; elysia protocol test` 可以在同一次 `command` 中复用草稿、测试目标和凭证；调用结束后这些状态立即丢弃，下一次调用无法读取。若请求显式传入 `sessionId`，服务端返回无状态参数错误，并提示将依赖命令合并到同一次批处理中。

更新已有协议时，在同一次批处理中先提交完整草稿，再执行 `elysia protocol save --update <协议id>`。目标必须存在且与草稿 ID 一致；不传 `--update` 时仍按新建处理，同名协议不会被覆盖。

CLI 响应为 SSE。提供 `params._meta.progressToken` 时逐命令发送 `notifications/progress`；最终结果同时包含 JSON 文本 `content` 和 `structuredContent`，便于只支持文本的客户端读取：

```json
{
  "ok": true,
  "summary": "执行 1 条命令：1 成功、0 失败",
  "output": "$ elysia source ls\n…",
  "exitCode": 0
}
```

批处理部分失败返回 `isError: true`，同时保留已执行命令的输出；参数验证失败可能只有错误文本。断开连接或超时会取消当前 CLI 调用，已经完成的操作不会回滚。输出可能截断，限制查询记录数优先使用命令的 `--limit`，`head` 仅截取文本行。完整命令参考与运行时 help 同源，见 [CLI 文档](agent-cli.md)。

## A2A：`POST /a2a` + Agent Card

双线格式按 `A2A-Version` 头分派（缺省 `0.3.0`；`1.0.0` 用 PascalCase 方法名与 `TASK_STATE_*` 枚举）；未知版本 400。Agent Card 在 `GET /.well-known/agent-card.json`（按版本头返回对应形态）。

### 任务模型

- `contextId` = 会话 id（不传或未知时服务端新建会话）；**task = 一轮**（`taskId = 会话id:用户消息seq`）。
- 状态映射：轮次中 → `working`；`waiting_approval`（审批/提问/方案三型）→ **`input-required`**；完成 → `completed`（产物 = 助手终稿 TextPart + DataPart{rounds,model,durationMs}）；失败 → `failed`；取消 → `canceled`。

### 方法

- `message/send` / `SendMessage`：**非阻塞**——落库消息即返回 `working` 快照，终态用 `tasks/get` 轮询或 `message/stream` 跟踪。
- `message/stream` / `SendStreamingMessage`：SSE 全程跟踪（每帧一个 JSON-RPC response：Task 快照 → statusUpdate → 终态/中断态关流；v0.3 终帧 `final: true`）。
- `tasks/get` / `GetTask`；`tasks/cancel` / `CancelTask`（运行中停止轮次，暂停中以拒绝收尾）；`tasks/resubscribe` / `SubscribeToTask`（帧回放 + 跟随）；`ListTasks`（v1.0，游标分页）。
- pushNotificationConfig 系列与扩展卡：规范可选，本实现声明不支持（专用错误码）。

### 审批恢复约定（多轮核心）

任务进入 `input-required` 后，客户端**带同一 `taskId` 重发消息**即恢复：

- **审批 / 方案确认型**：必须带 data part 显式决策——`{"parts":[{"kind":"data","data":{"approved": true}}]}`（v1.0 part 无 `kind` 字段）；方案拒绝时文本作为修改意见（`note`）。缺 data part 报 `-32602`。
- **提问型**：文本即作答（`data.answer` 优先）。

不带 `taskId` 的消息在同一 `contextId` 下开新一轮。

### 幂等

按 `messageId` 去重（单实例内存 LRU）：同 id 重发返回已建任务，不重复执行副作用。

## 安全要点

- 作用域即最小权限面：无 `agent` 作用域的 Key 与三者完全隔离。
- 通过 REST / A2A 远程驱动内置助手时，三层权限（save / live_test / delete 的 ask|always|never）与计划模式仍然生效；审批暂停以 `waiting_approval` / `input-required` 暴露给外部调用方。
- MCP `elysia_cli` 持 `agent` 作用域 Key 直接执行；该调用不创建 Agent 会话、不写入消息或 pending action。
- 所有出口（会话视图、pendingAction、工具结果）密钥脱敏，与面板同口径。
