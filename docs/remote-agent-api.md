# AI 助手远程暴露面（REST / MCP / A2A）

把网关内置 AI 助手（会话 / 消息 / 审批三原语）暴露给外部程序，实现远程配置与自动化运维。三个协议面共用同一套服务层与引擎，行为一致：

| 协议面 | 端点 | 适用客户端 |
|---|---|---|
| REST | `/api/agent/*` | 脚本、curl、第三方面板 |
| MCP（Model Context Protocol） | `POST /mcp` | Claude Desktop / Cursor / 各类 agent 框架 |
| A2A（Agent2Agent） | `POST /a2a` + `GET /.well-known/agent-card.json` | 其他 agent（LangChain、CrewAI、a2a-go 客户端等） |

## 鉴权与总开关

- 三个面统一要求 **Bearer API Key 且带 `agent` 作用域**：在「API Key」页编辑令牌勾选「允许控制 AI 助手」，或经 agent 工具 `create_api_key` / `update_api_key` 传 `scopes: ["agent"]`。无作用域的 Key 调用返回 403，无效 Key 返回 401（带 `WWW-Authenticate`）。
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
- **Modern（2026-07-28）**：无握手，每请求 `params._meta` 携带 `io.modelcontextprotocol/protocolVersion`；`MCP-Protocol-Version` 头与 body 一致（否则 `-32020`）、镜像头 `Mcp-Method`/`Mcp-Name` 校验、`server/discover`、所有 result 带 `resultType`、`tools/list` 带 `ttlMs/cacheScope`；**SSE 断流即取消**（尽力停止对应轮次）。

约束：POST 的 `Accept` 必须同时含 `application/json` 与 `text/event-stream`（否则 406）；带 `Origin` 头的浏览器请求须同源或回环（防 DNS rebinding）；批量请求不支持（2025-06-18 起已从规范移除）。

### 工具集（9 个）

| 工具 | 说明 |
|---|---|
| `agent_list_sessions` | 状态过滤 + 分页的会话列表 |
| `agent_create_session` / `agent_update_session` | 建会话 / 改标题与设置（含权限档——自动化可预设 `allowSave` 等） |
| `agent_get_session` | 详情 + 消息（`pendingAction` 已脱敏） |
| `agent_delete_session` / `agent_clear_messages` | 删除会话 / 清空消息 |
| `agent_send_message` | **流式工具**：驱动完整一轮（SSE progress 通知 + 终帧 structuredContent：`{sessionId,status,reply,pendingAction,usage,rounds}`） |
| `agent_respond` | 处理待批动作（审批 `approved` / 提问作答 `answer` / 方案确认），续跑同上 |
| `agent_stop` | 停止轮次 |

工具执行的业务失败以 `isError: true` 返回（可自我纠正），协议级错误（未知工具等）走 JSON-RPC error。

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
- 助手自身的三层权限（save / live_test / delete 的 ask|always|never）在远程驱动下同样生效——审批暂停会如实以 `waiting_approval` / `input-required` 暴露给外部调用方，由其决定批准与否。
- 所有出口（会话视图、pendingAction、工具结果）密钥脱敏，与面板同口径。
