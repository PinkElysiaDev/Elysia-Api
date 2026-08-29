# Maheshvara Protocol v1

Maheshvara（大自在天）是 Elysia API 的内部核心协议。所有生产转发链路遵循同一条规则：

```text
client wire protocol
  -> parse to Maheshvara
  -> apply routing / filtering / accounting
  -> render from Maheshvara
  -> upstream wire protocol
```

当前支持的四种内建 wire protocol：

- OpenAI Chat Completions（扩展兼容型，而非只接受严格官方字段）
- Anthropic Messages
- Gemini GenerateContent
- OpenAI Responses

全部类型与函数已统一使用 `Maheshvara*` 命名（历史上的 `Canonical*` 别名已移除）。协议版本常量为 `MaheshvaraProtocolVersion = "1"`。

## 1. 设计原则

1. **核心优先**：协议转换不是 `A -> B` 的成对特判，而是 `A -> Maheshvara -> B`。
2. **可表达性优先**：保留目标协议能够准确表达的字段；不把密文、签名或未知块伪装成普通提示词。
3. **显式丢失**：无法安全表达的字段被过滤，或在会导致请求语义错误时返回本地转换错误。
4. **合法 wire payload**：不会为兼容上游而生成空占位 Part、空函数名或其他协议非法对象。
5. **扩展兼容**：Chat Completions 接受常见中转站扩展字段，并将它们映射到明确的 Maheshvara 字段。
6. **流式一致性**：非流式和流式都经过 Maheshvara；流式使用状态化 decoder / renderer，而不是逐行字符串替换。
7. **安全自定义**：用户自定义协议只能使用受限 JSON 模板和字段映射，不执行任意代码。

## 2. 请求模型

Maheshvara request 的主要结构如下。以下为说明性结构，不是独立 HTTP endpoint：

```json
{
  "model": "provider-model",
  "instructions": "system and developer instructions",
  "messages": [],
  "input_items": [],
  "max_output_tokens": 4096,
  "temperature": 0.7,
  "top_p": 0.9,
  "top_k": 40,
  "stop": ["END"],
  "seed": 7,
  "stream": true,
  "stream_options": {"include_usage": true},
  "tools": [],
  "tool_choice": "auto",
  "parallel_tool_calls": true,
  "response_format": {},
  "reasoning": {},
  "thinking": {},
  "metadata": {},
  "raw_extra": {}
}
```

### 2.1 生成参数

核心字段覆盖：

- token 限制：`max_output_tokens`、`min_output_tokens`、`max_tool_calls`
- 采样：`temperature`、`top_p`、`top_k`、`typical_p`、`min_p`、`top_a`
- 惩罚：`presence_penalty`、`frequency_penalty`、`repetition_penalty`
- 候选与随机性：`n`、`seed`
- logprobs：`logprobs`、`top_logprobs`
- 输出控制：`stop`、`response_format`、`modalities`、`audio`、`prediction`、`verbosity`
- 请求策略：`service_tier`、`safety_identifier`、`safety_settings`、`cache_control`
- Responses 状态：`previous_response_id`、`store`、`include`、`truncation`、`background`、`conversation`、`prompt`
- 缓存与跟踪：`prompt_cache_key`、`prompt_cache_retention`、`request_id`、`session_id`、`timeout_ms`

无法归入稳定字段的扩展值保存在 `RawExtra`。它用于兼容和自定义模板，不代表所有目标协议都会重新发出这些字段。

### 2.2 消息

```json
{
  "role": "user | assistant | system | developer | tool",
  "content": [],
  "tool_calls": [],
  "tool_call_id": "call_1",
  "name": "optional-name",
  "cache_control": {},
  "metadata": {}
}
```

系统和 developer 消息在渲染目标协议时按目标能力聚合：

- OpenAI Chat：`system` / `developer` message
- Anthropic：顶层 `system`
- Gemini：`systemInstruction`
- Responses：`instructions`

### 2.3 内容 Part

`MaheshvaraContentPart.type` 支持：

| 类型 | 主要字段 | 说明 |
| --- | --- | --- |
| `text` | `text` | 普通文本 |
| `reasoning` | `reasoning_text`, `text`, `signature`, `signature_provider`, `encrypted_content`, `reasoning_summary` | 可见推理、来源绑定签名和 opaque reasoning |
| `refusal` | `text`, `annotations` | 拒答内容 |
| `image` | `image_url`, `image_base64`, `media_type`, `detail` | URL 或 base64 图片 |
| `audio` | `audio_url`, `audio_base64`, `media_type`, `text` | 音频及可选转写 |
| `video` | `video_url`, `video_base64`, `media_type` | 视频 |
| `file` / `document` | `file_id`, `file_name`, `file_data`, `uri`, `media_type` | 文件或文档 |
| `tool_output` | `tool_call_id`, `tool_output` | 工具结果 |
| `tool_call` | tool call 字段 | 内容级工具调用兼容形式 |

多模态字段同时保留通用 `uri` / `data` 和协议常用专用字段，渲染器选择目标协议能够表达的表示。

### 2.4 工具

函数工具使用：

```json
{
  "type": "function",
  "name": "lookup",
  "description": "look up data",
  "parameters": {"type": "object"},
  "input_schema": {"type": "object"},
  "strict": true
}
```

同时覆盖 Responses 的内建工具类型：`web_search_preview`、`file_search`、`computer_use_preview`、`code_interpreter`、`image_generation`。目标协议不支持内建工具时返回明确转换错误，不静默伪装成函数工具。

工具调用统一为 `id`、`type`、`name`、`arguments`、`arguments_text`，以及成对出现的 `thought_signature` / `thought_signature_provider`。工具结果必须通过 `tool_call_id` 与调用关联；Gemini `functionResponse` 需要函数名时，会使用该关联恢复函数名，无法恢复则返回带消息索引和调用 ID 的本地错误。

## 3. 响应模型

```json
{
  "id": "resp_...",
  "model": "provider-model",
  "created_at": 0,
  "status": "completed",
  "output": [],
  "stop_reason": "stop",
  "incomplete_details": {},
  "metadata": {},
  "service_tier": "default",
  "system_fingerprint": "...",
  "usage": {},
  "error": null
}
```

`output` item 支持 `message`、`function_call`、`reasoning`、`web_search_call`、`file_search_call`、`image_generation_call`。消息内容继续使用统一 Part；函数调用使用 `call_id`、`name`、`arguments`；reasoning item 使用 `reasoning`、`summary` 和内容 Part。

### 3.1 Usage

Usage 不只保留三项总数，还覆盖：

- `input_tokens`、`output_tokens`、`total_tokens`
- cached / cache creation / reasoning tokens
- text / image / audio 输入输出 token
- tool use token
- accepted / rejected prediction token
- web/file/image/code/computer tool call count
- 本地估算值及 `estimated` 标志
- `source`、`provider` 和 provider raw usage

## 4. Reasoning 安全约定

Maheshvara 区分三类数据：

1. 可见 reasoning 文本；
2. provider signature；
3. 不可解释的 encrypted / redacted reasoning。

非空 Anthropic `thinking` 会映射为 `reasoning` Part。空 thinking 被过滤。未知 Anthropic block 被过滤。

签名必须携带来源：Anthropic 为 `anthropic`，Gemini 为 `gemini`，OpenAI opaque reasoning 为 `openai`。渲染器只会把签名恢复到同一 provider；Anthropic 签名绝不会伪装成 Gemini `thoughtSignature`，Gemini 签名也不会伪装成 Anthropic `signature`。

Chat Completions 扩展字段 `tool_calls[*].extra_content.google.thought_signature` 映射为 Gemini 来源签名。向 Gemini 迁移其他模型的历史函数调用而没有合法 Gemini 签名时，只在首个 `functionCall` Part 使用 `skip_thought_signature_validator`，不复用其他 provider 的 opaque 字节。

外部 `redacted_thinking` 默认不会转成普通文本，也不会直接转发其密文。历史版本生成的 `maheshvara-reasoning-v1:` envelope 仅保留只读解码兼容，用于恢复已有记录的 `encrypted_content`；当前渲染器不再把 Maheshvara envelope 写入 Anthropic 或 Gemini 的 provider 签名字段。不可表达的 encrypted reasoning 仍可由 Maheshvara、Responses 或自定义协议保留。

## 5. Gemini Part 不变量

发送给 Gemini 的每个 `contents[*].parts[*]` 必须且只能具有一种有效数据表示：

- 非空 `text`
- `inlineData`
- `fileData`
- `functionCall`
- `functionResponse`

reasoning 使用非空 `text` + `thought: true`；`thought` 是属性，不是独立 data oneof。

实现禁止 `{"text":""}` 空占位。过滤后没有 Part 的消息被丢弃；因此形成的相邻同角色消息按原顺序合并。整个请求或响应没有任何可表达内容时，转换器返回本地错误，不向 Gemini 发送畸形 payload。

## 6. 四协议请求映射

| Maheshvara | Chat Completions | Anthropic Messages | Gemini GenerateContent | OpenAI Responses |
| --- | --- | --- | --- | --- |
| `model` | `model` | `model` | URL model | `model` |
| `instructions` | system/developer message | `system` | `systemInstruction` | `instructions` |
| messages | `messages` | `messages` content blocks | `contents.parts` | `input` message items |
| text/image/audio/file | 扩展 content parts | Anthropic blocks | `text` / `inlineData` / `fileData` | input content items |
| reasoning/thinking | `reasoning_content` 等扩展字段 | `thinking`（仅恢复 Anthropic 签名） | thought Part / thinking config（仅恢复 Gemini 签名） | `reasoning` / reasoning input item |
| function tools | `tools[].function` | `tools[].input_schema` | `functionDeclarations` | function tool |
| tool call/result | assistant `tool_calls` + tool message | `tool_use` / `tool_result` | `functionCall` / `functionResponse` | function call / output item |
| response format | `response_format` | 支持时映射 output config | `responseMimeType` / schema | `text.format` |
| generation params | 官方字段 + 常见扩展 | Anthropic 可表达子集 | `generationConfig` | Responses 可表达子集 |

Chat Completions 使用扩展兼容策略：识别 `reasoning_content`、`reasoning_effort`、`repetition_penalty`、`min_p`、`top_a`、音频、文件和常见 usage details 等字段。稳定字段进入 Maheshvara；未知扩展进入 `RawExtra`。

## 7. 四协议响应映射

所有 provider response 先转成 `MaheshvaraResponse`，再按客户端入口渲染：

| Maheshvara output | Chat Completions | Anthropic | Gemini | Responses |
| --- | --- | --- | --- | --- |
| message text | choice message | `text` block | text Part | `output_text` |
| refusal | `refusal` / finish reason | text + refusal stop reason | text + safety finish reason | refusal content |
| reasoning | `reasoning_content` | `thinking`（provider-bound signature） | thought Part（provider-bound signature） | reasoning item + summary/encrypted content |
| function call | `tool_calls` | `tool_use` | `functionCall` | function call item |
| usage | Chat usage details | Anthropic usage/cache fields | `usageMetadata` | Responses usage details |

请求和响应的 4×4 基础矩阵均有回归测试；协议特有字段另由 reasoning、多模态、工具调用和 usage 测试覆盖。

## 8. 流式协议

流式链路为：

```text
upstream SSE / NDJSON
  -> source protocol decoder
  -> MaheshvaraStreamEvent(s)
  -> target protocol renderer
  -> downstream SSE
```

Maheshvara stream event 覆盖：

- response created / in progress / completed / failed
- output item added / done
- content part added / done
- text delta / done
- reasoning delta / done / summary delta / signature delta
- refusal delta / done
- function call added / arguments delta / arguments done
- usage delta

decoder 和 renderer 都是有状态对象，用于维护 tool index、content block index、累计参数、usage、terminal 状态和协议要求的事件顺序。

### 8.1 SSE reader

统一 SSE reader 支持：

- 多行 `data:` 按 SSE 语义用换行拼接
- `event`、`id`、`retry`
- EOF 前最后一个未以空行结束的事件
- context cancel 和 idle timeout
- 单行 NDJSON 兼容

### 8.2 Terminal 契约

- 已收到明确 provider terminal event 时，转换为目标协议 terminal。
- provider 流结束但没有配置的 terminal value / finish reason 时，自定义协议报错。
- 完成前没有任何可表达输出时，不先写“成功完成”，而是发送明确失败 terminal。
- OpenAI `[DONE]` 只由需要它的目标 renderer 生成。
- Anthropic content block start/stop、Responses sequence 和 Gemini Part 合法性由 renderer 维护。

## 9. 自定义协议

模型源的 `platform` 写成 `custom:<protocol-id>` 后，Elysia API 从 bootstrap `config.json` 的 `customProtocols` 注册对应协议。注册和热重载使用原子替换；新配置验证失败时，运行中的旧注册表保持可用。

WebUI 的模型源表单可选择“自定义 Maheshvara 协议”并填写协议 ID。自定义协议当前不定义模型发现 endpoint，因此该类源必须关闭 `autoFetchModels` 并配置 `manualModels`；后端管理 API 会验证协议已注册并拒绝错误的自动发现配置。

完整示例：

```json
{
  "relay": {
    "passthrough": false
  },
  "customProtocols": [
    {
      "id": "vendor-json",
      "name": "Vendor JSON API",
      "version": "1",
      "request": {
        "method": "POST",
        "path": "/v2/generate/{{maheshvara.model}}",
        "contentType": "application/json",
        "headers": {
          "X-Model": "{{maheshvara.model}}"
        },
        "query": {
          "api-version": "2026-01-01"
        },
        "auth": {
          "mode": "header",
          "header": "x-api-key",
          "prefix": ""
        },
        "bodyTemplate": "{\"model\":{{maheshvara.model | json}},\"messages\":{{maheshvara.messages}},\"tools\":{{maheshvara.tools}},\"temperature\":{{maheshvara.temperature | default:0.2}},\"stream\":{{maheshvara.stream}}}",
        "omitIfEmpty": ["tools", "temperature"]
      },
      "response": {
        "idPath": "request_id",
        "modelPath": "model",
        "textPath": "result.message.text",
        "reasoningPath": "result.message.reasoning",
        "toolCallsPath": "result.message.tool_calls",
        "usagePath": "usage",
        "finishReasonPath": "finish_reason",
        "errorPath": "error",
        "fieldMappings": [
          {
            "target": "metadata.vendor",
            "value": "vendor-json"
          },
          {
            "target": "service_tier",
            "source": "meta.tier",
            "transform": "string",
            "omitIfEmpty": true
          }
        ],
        "stream": {
          "payloadPath": "payload",
          "mode": "cumulative",
          "events": ["message"],
          "doneValues": ["[DONE]", "END"],
          "response": {
            "textPath": "text",
            "reasoningPath": "reasoning",
            "toolCallsPath": "tool_calls",
            "usagePath": "usage",
            "finishReasonPath": "finish_reason"
          }
        }
      }
    }
  ]
}
```

### 9.1 Request 模板

模板上下文只读：

- `maheshvara.*`
- `request.*`（兼容别名）

常用路径包括：

- `model`、`instructions`、`messages`、`input_items`
- 所有生成参数
- `tools`、`tool_choice`、`parallel_tool_calls`
- `response_format`、`reasoning`、`thinking`
- `metadata`、`user`、`stream`、`stream_options`
- `raw_extra` / `extra`

占位符规则：

```json
{
  "asString": "{{maheshvara.model}}",
  "asNativeJSON": {{maheshvara.messages}},
  "explicitJSON": {{maheshvara.tools | json}},
  "withDefault": {{maheshvara.temperature | default:0.2}}
}
```

- 放在 JSON 字符串中的占位符会进行 JSON 字符串转义。
- 未放在引号中的占位符插入原生 JSON 值。
- `json` 明确要求 JSON 值语义。
- `default:<JSON literal>` 在字段不存在或为空时提供默认值。
- `omitIfEmpty` 在渲染后删除指定空路径。

模板限制：最大 4 MiB、最大 2048 个占位符、渲染 JSON 最大深度 64。模板必须在注册时和运行时都生成合法 JSON。

路径、headers 和 query 支持同一上下文的字符串插值。自定义 path 必须相对于模型源 `baseUrl`，不能包含独立 scheme。

### 9.2 Auth

| mode | 行为 |
| --- | --- |
| `bearer` 或空 | 使用模型源 API key 生成 Bearer Authorization |
| `none` | 不注入 API key |
| `header` | 将 API key 写入指定 header，可使用 `prefix` |
| `query` | 将 API key 写入指定 query key |

静态 `headers` 不能覆盖 relay 管理的认证头和传输层头。`auth.header` 允许 `Authorization`、`x-api-key` 等端到端认证头，但拒绝 `Host`、`Content-Length`、`Transfer-Encoding`、`Connection` 和 `Proxy-Authorization`。header 名称、静态值、动态渲染值、auth prefix 和 header 型 API key 都拒绝 CR/LF。

### 9.3 Response 路径

点路径和数组路径均受支持：

```text
result.text
output[0].content[0].text
videos[0].url
$['data'][0].message.content
```

直接路径可映射 `id`、`model`、`status`、text、reasoning、tool calls、usage、finish reason 和 error。`mappings` 是这些直接路径的兼容简写。

`fieldMappings` 可以写入受控的 Maheshvara response 顶层字段：

- `id`、`model`、`created_at`、`status`、`stop_reason`
- `incomplete_details`、`metadata`、`service_tier`、`system_fingerprint`
- `output`、`usage`、`error`

每个 mapping 使用 `source`、固定 `value` 或 `default`，并可配置 `omitIfEmpty`。

支持的 transform：

- identity：空值、`identity`、`raw`
- 文本：`string`、`text`、`join`
- 数值：`int`、`integer`、`number`、`float`、`timestamp_ms`
- 布尔：`bool`、`boolean`
- JSON：`json`、`parse_json`、`json_string`
- 集合：`first`
- 协议结构：`usage`、`content_parts`、`tool_calls`、`output_items`

所有直接路径、source、target、transform 以及嵌套 stream response mapping 都在协议注册时验证，避免错误配置只在首个线上请求中暴露。

### 9.4 Stream mapping

- `payloadPath`：从事件 JSON 中选择实际 payload。
- `mode: delta`：每个事件值就是新增 delta。
- `mode: cumulative`：每个事件值是累计全文，decoder 只输出相对上一次的新后缀；provider 回退或改写全文时输出当前值，避免错误截断。
- `events`：只处理指定 SSE event name。
- `doneValues`：匹配原始 `data` 后生成完成事件。
- `response`：为 stream payload 定义独立 response mapping。

自定义流最终仍交给目标 Maheshvara renderer，因此同一个 provider stream 可以输出为 Chat、Anthropic、Gemini 或 Responses SSE。

## 10. Passthrough

`relay.passthrough` 默认是 `false`。默认情况下，即使客户端和上游使用同一协议，也执行 `wire -> Maheshvara -> wire`，以确保过滤、usage、工具和错误约定一致。

passthrough 只作为显式 opt-in 的兼容逃生口，用于必须原样保留尚未纳入 Maheshvara 的同协议私有字段。开启后，同协议且未发生 vision 过滤的请求可以绕过核心转换。它不是默认架构，也不适用于跨协议转换。

## 11. 可表达性与错误策略

- 空文本不会被渲染为协议 Part。
- 未知内容块不会自动 JSON 字符串化成提示词。
- 外部 opaque reasoning 不会降级为普通文本。
- 缺少函数名的 function call 会在需要函数名的目标协议上报错。
- 无法关联函数名的 Gemini function response 会在本地报错。
- 内建工具无法被目标协议表达时返回转换错误。
- 整个请求或响应没有可发送内容时返回明确错误。
- 自定义非流式响应没有映射出 text、reasoning、tool call 或 error 时返回映射错误。
- 自定义流没有 terminal 或没有任何可表达输出时返回流错误。

这些规则避免用空格、伪提示词或无效对象绕过 provider 校验，从而避免污染模型上下文。

## 12. 实现位置与验证

主要实现：

- `backend/relay/maheshvara_types.go`：Maheshvara v1 数据模型
- `backend/relay/maheshvara_convert.go`：四协议非流式转换
- `backend/relay/maheshvara_protocol.go`：Maheshvara 命名 API
- `backend/relay/maheshvara_reasoning.go`：provider-bound signature 与 legacy envelope decoder
- `backend/relay/maheshvara_stream*.go`：统一流式 decoder / renderer
- `backend/relay/custom_protocol.go`：自定义协议注册、模板和 auth
- `backend/relay/custom_mapping.go`：路径、fieldMappings 和 transform
- `backend/relay/custom_stream_mapping.go`：自定义流式状态
- `backend/server/custom_protocol.go`、`backend/server/custom_stream.go`：生产接入

核心回归：

- 四协议请求 4×4 matrix
- 四协议响应 4×4 matrix
- provider-bound signature、legacy envelope 只读兼容与外部 redacted filtering
- Anthropic thinking 到 Gemini 合法 Part
- 多模态、function call / result、usage details
- 多行 SSE、并行 tools、累计自定义 stream、terminal/error
- 自定义数组路径、nested output、原子 reload 和 header 安全

版本升级原则：新增可选字段保持 v1 兼容；改变核心字段语义或 stream terminal 契约时必须提升协议版本。安全收紧不得删除旧数据 decoder，但可以停止生成会被 provider 误判为其原生 opaque 字段的兼容载荷。
