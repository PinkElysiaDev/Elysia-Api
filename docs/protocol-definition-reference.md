# 自定义协议参考

[文档索引](README.md) · **简体中文** · [English](protocol-definition-reference.en.md)

协议定义描述上游请求、响应、流式事件和可选模型发现。定义保存在 SQLite，通过协议设计器或管理 API 保存，验证成功后更新注册表。模型源用 `custom:<id>` 引用。

字段目录：`GET /api/admin/custom-protocols/schema`。实现见 [custom_protocol.go](../backend/relay/custom_protocol.go) 与[映射编译器](../backend/relay/custom_protocol_mapping.go)。

## 最小定义

以下是虚构 JSON 上游的完整配置示例；路径和响应字段需按实际 API 修改：

```json
{
  "id": "vendor-json",
  "name": "Vendor JSON",
  "version": "1",
  "type": "llm",
  "request": {
    "method": "POST",
    "path": "/generate",
    "shape": "openai-chat",
    "auth": {"mode": "bearer"},
    "body": {
      "model": {"field": "model", "mode": "string"},
      "messages": {"field": "messages", "mode": "json"},
      "stream": {"field": "stream"}
    }
  },
  "response": {
    "textPath": "result.text",
    "usagePath": "usage",
    "finishReasonPath": "finish_reason"
  }
}
```

## 顶层结构

| 字段 | 说明 |
| --- | --- |
| `id`、`name`、`version` | 协议标识、显示名、定义版本；ID 需通过注册校验 |
| `type` | 缺省 `llm`；`embedding` / `reranker` 仅为声明式预留；`x-` 前缀扩展不由核心解释 |
| `request` | 上游请求构造 |
| `response` | 响应与 `response.stream` 流式映射 |
| `models` | 可选模型发现端点 |
| `aliases` | 提取键名覆盖 |
| `metadata` | 自定义元数据；`preset` 用于预置标识 |

## 条件 Match

结构为 `path`、`op`、可选 `value`：

```json
{"path":"thinking.enabled","op":"isTrue"}
```

支持 `nonEmpty`（缺省）、`isEmpty`、`equals`、`notEquals`、`in`、`notIn`、`contains`、`isNull`、`notNull`、`isTrue`、`isFalse`、`gt`、`gte`、`lt`、`lte`。

比较保留类型：数值按数值比较，字符串 `"false"` 不等于布尔值 `false`，对象不受键顺序影响。空白文本、`false`、`0`、空数组/对象、null 和缺失值均视为 `nonEmpty` 的空值；缺失值的 `isFalse` 为真。

## 请求构造

| 字段 | 说明 |
| --- | --- |
| `method` | 缺省 POST |
| `path` / `pathStream` | 相对源 `baseUrl` 的路径；流式可覆盖；不允许独立 scheme |
| `headers` / `query` / `contentType` | 静态或插值后的请求头、查询、内容类型 |
| `shape` | `openai-chat`、`anthropic`、`gemini`、`responses`；使消息和工具采用目标线路形状 |
| `body` | 推荐的字段构造树 |
| `bodyTemplate` / `submitBody` | 兼容文本模板；前者优先 |
| `omitIfEmpty` | 渲染后删除指定空路径 |

`body` 的容器为 JSON 对象/数组。叶子为字段引用或常量；`field` 可含子路径，`mode` 为 `json` 或 `string`：

```json
{
  "temperature": {"field":"temperature","default":0.7,"omitIfEmpty":true},
  "enable_thinking": {"field":"thinking.enabled","when":{"path":"thinking.enabled","op":"isTrue"}},
  "api_version": {"value":"2026-01-01"}
}
```

上例为 `request.body` 片段。`omitIf` 在渲染值等于指定字面量时删键；`when` 不成立时省略整键。`shape: responses` 还提供 `input` / `input_items`。

模板上下文为 `maheshvara.*`，兼容别名 `request.*`。可访问生成参数、消息、工具、reasoning、metadata、stream 和 `raw_extra`。字符串内占位符转义为 JSON 字符串，未加引号的占位符插入原生 JSON；支持 `json`、`default:<JSON>`、`bool`、`int`、`string` 过滤器。模板语法片段（不是可直接提交的 JSON）：

```text
{"model": {{maheshvara.model | json}}, "messages": {{maheshvara.messages | json}}}
```

模板最大 4 MiB、2048 个占位符，渲染 JSON 深度最大 64。注册和运行时均检查结果合法性，不执行任意代码。

### 鉴权

`auth.mode` 支持 `bearer`（缺省）、`header`、`query`、`none`。`header` 缺省名为 `x-api-key`，可指定 `prefix`；`query` 指定参数名。密钥来自模型源。

静态头不能覆盖受 relay 管理的认证/传输头。自定义认证头拒绝 `Host`、`Content-Length`、`Transfer-Encoding`、`Connection`、`Proxy-Authorization` 等传输头；头名、值、插值结果和认证 prefix 拒绝 CR/LF。

## 响应映射

路径支持点号、数组下标及括号键名，例如 `output[0].content[0].text`、`$['data'][0].text`。

| 字段 | 目标 |
| --- | --- |
| `idPath` / `modelPath` / `statusPath` | 标识、模型、状态 |
| `textPath` / `reasoningPath` / `refusalPath` | 文本、推理文本、拒答 |
| `signaturePath` / `signatureProviderPath` / `signatureProvider` | 签名及其来源；线制无来源字段时使用常量 |
| `encryptedContentPath` / `citationsPath` | 加密推理、引用标注 |
| `toolCallsPath` / `usagePath` / `finishReasonPath` / `errorPath` | 工具调用、用量、终止原因、错误 |
| `textFilter` / `reasoningFilter` | 对路径指向的对象数组先按 Match 过滤；条件数组要求全部成立 |
| `body` / `fields` | 示例结构树或行式映射，二选一 |
| `sample` | 用于预览的上游示例响应 |

`response.body` 叶子为 `{"field":"text","value":"example"}` 等映射标注，路径从树位置提取。只含 `value` 的叶子是结构示例，不产生字段映射。`fields` 行为 `{path,field,transform?}`，不允许重复映射同一字段；`usage.*` 缺省按整数处理。

兼容写法 `mappings` 提供直接路径别名；`fieldMappings` 每项使用 `target` 与 `source` / 固定 `value` / `default`，支持 `omitIfEmpty`。受控目标包括 `id`、`model`、`created_at`、`status`、`stop_reason`、`incomplete_details`、`metadata`、`service_tier`、`system_fingerprint`、`output`、`usage`、`error`。

转换函数：`identity` / `raw`、`string` / `text` / `join`、`int` / `integer` / `number` / `float` / `timestamp_ms`、`bool` / `boolean`、`json` / `parse_json` / `json_string`、`first`、`usage` / `content_parts` / `tool_calls` / `output_items`。路径、目标和 transform 在注册时校验。

## 流式映射

流配置位于 **`response.stream`**。以下为该对象的示例，不是完整协议：

```json
{
  "mode": "delta",
  "modes": {"text":"cumulative","reasoning":"delta","arguments":"delta"},
  "doneValues": ["[DONE]"],
  "eventKeys": ["type","event"],
  "finishWhen": {"path":"finished","op":"isTrue"},
  "response": {"textPath":"text","usagePath":"usage"}
}
```

| 字段 | 语义 |
| --- | --- |
| `payloadPath` | 事件内的实际载荷路径 |
| `mode` | 缺省 `delta`；`cumulative` 将累计快照转换为后缀增量 |
| `modes` | 按 `text`、`reasoning`、`arguments` 覆盖全局模式 |
| `doneValues` / `done` | 原始终止字面量 / 类型化终止值；`done` 使用 `{raw: ...}` 或 `{json: ...}` |
| `doneValuesReplace` | 清除默认 `[DONE]` 后再加入显式值 |
| `eventKeys` | JSON 事件名字段，缺省 `type`、`event` |
| `finishWhen` / `statusWhen` | Match 终止条件，覆盖对应缺省判断 |
| `frames` | 异构帧规则，优先于旧 `events` 白名单 |
| `response` | 流级响应映射；未配置时继承外层 |

累计快照回退或改写时，decoder 可输出当前值；不能承诺所有上游改写都能无损转换。默认终止条件包括非空 finish reason、`status == completed`、终止字面量。显式 finish reason 的空补全可成功；仅 `[DONE]` 且从未输出、也无 finish reason 时失败。终态后约 2 秒空闲排水窗口继续接收 usage 与错误尾帧。

### 异构帧

每帧至少有 `event` 或 `match`，两者都有时须同时成立；首个命中生效。无事件名的协议使用 Match。未命中规则的帧跳过，需要通用兜底时在末尾配置匹配规则。

帧支持 `payloadPath`、`response`、`terminal`、`tool`、`toolDone`。帧内响应不能再嵌套 `stream`；匹配帧省略响应时使用流级映射。

### 工具调用分帧

`tool` 支持 `path`、`idPath`、`indexPath`、`namePath`、`argumentsPath`、`argumentsMode`。`path` 可指向工具数组，此时其余路径相对数组元素；否则相对原始帧。

身份帧登记 ID、名称及下标；参数帧按 ID 或下标关联。`argumentsMode` 缺省 `delta`，片段原样追加；`cumulative` 按快照差分。身份帧本身不产生参数增量；`toolDone: true` 标记参数完成。支持一帧多个工具及跨帧拼接，不要沿用旧文档中的“不支持”限制。

## 提取别名

提供某类别的别名即替换该类别默认表。`usage` 与 `toolCall` 支持点路径。以下为顶层片段：

```json
{
  "aliases": {
    "textKeys": ["text","content","summary"],
    "usage": {"input":["inTokens"],"cached":["prompt_tokens_details.cached_tokens"]},
    "toolCall": {"id":["ref"],"name":["fn"],"arguments":["params"]}
  }
}
```

## 模型发现

定义 `models` 后，引用此协议的源可开启自动发现；否则使用手工模型。该对象支持 `method`（GET/POST，缺省 GET）、`path`、`headers`、`query`、`auth`、`listPath`、`idPath`（缺省 `id`）、`namePath`。鉴权缺省继承 `request.auth`。

顶层片段：

```json
{"models":{"method":"GET","path":"/v1/models","listPath":"data","idPath":"id","namePath":"display_name"}}
```

## 预置与维护

协议表为空时播种 `chat-completions-api`、`responses-api`、`anthropic-api`、`gemini-api`。已有数据库记录优先，升级不覆盖用户修改；清空全部协议后，下次启动会重新播种。旧厂商式预置 ID 的迁移同时更新 `custom:<id>` 引用。

标准平台 `chat_completions` / `responses` / `anthropic` / `gemini` 仍使用内置路径；预置定义用于自定义基底和等价性测试，不意味着所有流量都由 JSON 定义处理。客户端侧 SSE 渲染、传输解析和转换函数仍是 Go 实现。

## 接入与验证

1. 复制接近的预置或从最小定义开始，按上游样例填写字段。
2. 在设计器执行离线预览，检查请求和响应映射；它不发起真实上游调用。
3. 用测试模型源执行非流式、流式、工具、错误和终止帧测试。真实测试可能收费。
4. 保存协议，创建 `custom:<id>` 模型源；仅在配置模型发现时开启自动拉取。
5. 通过模型组发起客户端请求，对照 usage、终止原因、日志和预期输出。

AI 助手使用同一协议与 CLI 工具完成草稿、预览、测试和保存，受会话权限控制。实现不再提供旧文档中的 `/custom-protocols/assist` 独立端点。转换边界见 [Maheshvara](maheshvara-protocol.md)。
