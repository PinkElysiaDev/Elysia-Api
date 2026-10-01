# 远程 Agent 接口

[文档索引](README.md) · **简体中文** · [English](remote-agent-api.en.md)

REST / A2A 驱动内置助手，使用持久化会话、模型调用和审批。MCP 直接执行 `elysia_cli` 运维命令，不调用内置模型、不创建助手会话。

| 接口 | 端点 | 用途 |
| --- | --- | --- |
| REST | `/api/agent/*` | 脚本、面板、会话管理 |
| MCP | `POST /mcp` | 外部 Agent 直接运维 |
| A2A | `POST /a2a` | 任务与 Agent 协作 |
| Agent Card | `GET /.well-known/agent-card.json` | 公开发现信息，受远程总开关控制 |

## 鉴权

在运行配置的远程访问区域创建带 `agent` 作用域的 Key：

```http
Authorization: Bearer <AGENT_KEY>
```

| 凭证 | 使用范围 |
| --- | --- |
| 面板令牌 `panelAccessToken` | `/api/admin/*` 管理面 |
| 无 `agent` 作用域的推理令牌 | `/v1/*`、`/v1beta/*`，受模型组授权约束 |
| 带 `agent` 作用域的 Key | `/api/agent/*`、`/mcp`、`/a2a`；推理端点拒绝此类 Key |

无效或未提供远程 Key 返回 401，无 `agent` 作用域返回 403，均提供 `WWW-Authenticate`。Agent Card 本身不要求 Key。模型组授权 `allowedGroups` 与端点作用域 `scopes` 是不同维度。

`agentRemote.enabled` 缺省 `true`；设为 `false` 后上述远程端点及 Agent Card 返回 404。`agentRemote.publicUrl` 设置反向代理后的基础地址；空时根据请求地址生成 Agent Card。

## REST

除鉴权外，与 `/api/admin/agent/*` 共用 handler。普通响应使用 `{ok,data}` / `{ok,error:{code,message}}`；消息和审批返回 SSE。

下表路径前缀为 `/api/agent`：

| 方法 | 路径 | 请求 / 行为 |
| --- | --- | --- |
| GET | `/sessions` | 可选 `status`、`limit`、`offset`；返回 `{items,total}`，不传分页参数返回全量 |
| POST | `/sessions` | `{title?,mode?,protocolId?,settings?}`；`mode` 为 `create` 或 `edit`，编辑需已有协议 ID |
| GET | `/sessions/:id` | 返回 `{session,messages}`，消息按 `seq` 排序 |
| PATCH | `/sessions/:id` | `{title?,settings?,apiKey?,clearApiKey?}` 增量更新 |
| DELETE | `/sessions/:id` | 先停止轮次，再删除会话 |
| POST | `/sessions/:id/messages` | `{content?,documents?,afterSeq?}`；`afterSeq` 先截断后续消息 |
| POST | `/sessions/:id/approve` | `{approved,answer?,note?,apiKey?,baseUrl?}`，审批或作答后 SSE 续跑 |
| POST | `/sessions/:id/stop` | 停止当前轮次，返回 `{stopped}` |
| DELETE | `/sessions/:id/messages?afterSeq=0` | 清空消息；正数按序号截断，保留会话和设置 |
| POST | `/sessions/:id/restore-draft` | 恢复草稿快照 |

状态为 `idle`、`running`、`waiting_approval`；`limit` 必须为正数，最大生效值 200，`offset` 非负。已有轮次运行时再次发消息返回 409。

`settings` 包括 `modelSourceId`、`modelName`、`thinkingEnabled`、`thinkingEffort`、`planMode`、`allowSave`、`allowLiveTest`、`allowDelete`、`testBaseUrl`、`maxModelCalls`（单轮工具循环的模型调用上限，0-100，0=默认 30）。权限值为 `ask` / `always` / `never`；计划模式阻止受控操作。测试密钥加密存储，会话视图只给出设置标记，不返回明文。

创建会话示例：

```bash
curl http://127.0.0.1:8765/api/agent/sessions \
  -H "Authorization: Bearer <AGENT_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"title":"Gateway inspection","settings":{"modelSourceId":"<SOURCE_ID>","modelName":"<MODEL_ID>"}}'
```

取得返回的会话 ID 后发送消息。此操作调用模型，会产生上游用量：

```bash
curl -N "http://127.0.0.1:8765/api/agent/sessions/<SESSION_ID>/messages" \
  -H "Authorization: Bearer <AGENT_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"content":"List model sources without changing configuration."}'
```

`documents` 条目为 `{name,mime?,text?,dataUrl?}`。使用文本或数据 URL 传文档，不把服务器本地文件路径当作上传内容。

### SSE 与生命周期

| 事件 | 内容 |
| --- | --- |
| `status` | 调用模型、执行工具等阶段 |
| `text_delta` / `reasoning_delta` | 模型返回的正文 / 推理文本增量 |
| `tool_call` / `tool_progress` / `tool_result` | 工具调用、进度与结果 |
| `draft_updated` / `plan_updated` | 草稿 / 工作计划变化 |
| `approval_required` | 等待审批、作答或方案确认 |
| `message` | 已持久化消息，含 `seq` |
| `context_updated` / `context_compacted` | 上下文水位 / 压缩结果 |
| `turn_done` / `error` | 完成汇总 / 失败，错误可包含 `retryable` |

流每 15 秒发送保活。REST 的 HTTP 断开不会停止轮次；结果继续落库，可读取会话详情和列表确认终态。主动停止使用 `/stop`。拒绝审批会生成工具拒绝结果，供助手继续处理。

## MCP

本实现接受两组交互方式：

| 模式 | 契约 |
| --- | --- |
| Legacy | 接受版本 `2024-11-05`、`2025-03-26`、`2025-06-18`、`2025-11-25`；`initialize` → `notifications/initialized` → 工具调用；不签发会话 ID |
| Modern | 版本 `2026-07-28`；每请求在 `params._meta` 传 `io.modelcontextprotocol/protocolVersion`；支持 `server/discover`，result 包含 `resultType` |

POST 的 `Accept` 必须包含 `application/json` 和 `text/event-stream`，否则 406。GET / DELETE 返回 405；不支持 JSON-RPC batch。浏览器 `Origin` 需同源或符合回环规则。版本头与 Modern body 不一致返回 `-32020`；提供 `Mcp-Method` / `Mcp-Name` 镜像头时必须与请求一致。Modern `tools/list` 还返回 `ttlMs` / `cacheScope`。

### 客户端配置

在运行配置中点击已启用远程 Key 的“复制 MCP JSON”。地址优先用 `publicUrl`，否则用当前访问地址；复制时按需读取明文 Key。常见 HTTP 客户端配置：

```json
{
  "mcpServers": {
    "elysia": {
      "type": "http",
      "url": "https://gw.example.com/mcp",
      "headers": {"Authorization": "Bearer <AGENT_KEY>"}
    }
  }
}
```

客户端格式不同时，迁移其中的 `url` 和 `headers`。MCP 只公布一个工具 `elysia_cli`，参数为必填字符串 `command`。Legacy 初始化后最小调用：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {"name": "elysia_cli", "arguments": {"command": "elysia source ls"}}
}
```

### 执行边界

- 每条业务命令以 `elysia` 开头；通过 `elysia help` 查询语法。它不是系统 shell，也不是独立终端可执行文件。
- MCP 持 Agent Key 直接执行查询、写入、真实测试和删除，**不经过内置助手审批**；命令校验、出站策略和业务限制仍然生效。
- 每次调用独立，草稿、测试目标和凭证只在同一次 `command` 内复用，结束即丢弃；显式传 `sessionId` 会报错。
- 更新协议需在同一批处理中准备完整草稿，再用 `elysia protocol save --update <id>`；ID 必须匹配已有协议，不指定 `--update` 不覆盖同名记录。
- `&&` 失败后跳过所在链，`;` 继续执行。批处理无整体事务，断开或超时取消剩余执行，已完成操作不回滚。
- 工具返回 SSE；提供 `params._meta.progressToken` 时逐命令发送进度。结果含 JSON 文本 `content` 与 `structuredContent`，结构为 `{ok,summary,output,exitCode}`。
- 业务失败以 `isError: true` 返回，协议错误使用 JSON-RPC error；验证失败可能只有错误文本。查询优先用 `--limit`；`head` 只截文本行，输出仍可能被截断。

完整语义见 [CLI 手册](agent-cli.md)。

## A2A

`A2A-Version` 缺省 `0.3.0`，还支持 `1.0.0`；未知版本返回 400。Agent Card 随版本头返回对应结构。

| v0.3 方法 | v1.0 方法 | 行为 |
| --- | --- | --- |
| `message/send` | `SendMessage` | 非阻塞创建轮次并返回快照 |
| `message/stream` | `SendStreamingMessage` | SSE 跟踪任务至终态或等待输入 |
| `tasks/get` | `GetTask` | 读取任务 |
| `tasks/cancel` | `CancelTask` | 停止运行轮次；等待输入时以拒绝结束 |
| `tasks/resubscribe` | `SubscribeToTask` | 回放并跟随 |
| — | `ListTasks` | 游标分页 |

`contextId` 对应会话；缺省或未知时新建。一个任务对应一轮，`taskId` 为 `会话ID:用户消息seq`。运行、等待输入、完成、失败、取消分别映射至 `working`、`input-required`、`completed`、`failed`、`canceled`；v1.0 使用 `TASK_STATE_*` 枚举。完成产物包含最终文本及 `{rounds,model,durationMs}`。

恢复等待输入的任务时携带同一 `taskId`：审批或方案确认必须提供 data part 的 `approved`；提问可用文本作答，`data.answer` 优先。方案拒绝的文本作为 `note`。v0.3 决策片段：

```json
{"parts":[{"kind":"data","data":{"approved":true}}]}
```

v1.0 的 part 不使用 `kind`。审批缺少 data part 返回 `-32602`。不带 `taskId` 则在该会话创建新一轮。消息按 `messageId` 在单实例内存 LRU 中去重，不提供跨重启的持久幂等保证。Push notifications 与扩展卡未实现。

## 排障与权限

| 现象 | 检查 |
| --- | --- |
| 401 / 403 | Key 是否启用，是否含 `agent` 作用域 |
| 404 | `agentRemote.enabled` 及代理路径 |
| MCP 406 | `Accept` 是否同时包含两种类型 |
| 调用结束后找不到草稿 | MCP 无状态，合并依赖操作到同一调用 |
| REST 断开后仍在执行 | 轮次独立于连接，调用 `/stop` |
| 等待审批不继续 | 权限档、计划模式及明确的恢复决策 |

保护 Agent Key，按管理凭证对待。真实测试和助手调用可能产生费用；会话输出与工具输出会脱敏已知秘密字段，但不要上传不必要的凭证或把完整对话作为公开排障材料。
