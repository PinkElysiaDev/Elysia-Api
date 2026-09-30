# Maheshvara 协议

[文档索引](README.md) · **简体中文** · [English](maheshvara-protocol.en.md)

Maheshvara 是网关的内部请求、响应和流事件模型。它用于跨协议转换，不是独立 HTTP API。当前 `MaheshvaraProtocolVersion` 为 `"2"`，对应推理信封版本；v1 信封保留读取兼容。

## 转换路径

跨协议和自定义协议通过统一模型转换：

```text
client wire → Maheshvara → routing / filtering → upstream wire
upstream wire → Maheshvara → client wire
```

支持 Chat Completions API、Responses API、Anthropic API、Gemini API。同协议满足条件时自动透传，不应将上述转换图理解为每条请求都重写协议。

转换原则：保留目标协议可表达的数据；无法表达的语义明确报错或按已定义规则过滤；不将未知块、签名和密文伪装为普通提示词。自定义协议仅执行受限数据映射。

## 请求模型

下例说明内部字段，不是任一供应商的完整请求体：

```json
{
  "model": "provider-model",
  "instructions": "system and developer instructions",
  "messages": [],
  "input_items": [],
  "max_output_tokens": 4096,
  "temperature": 0.7,
  "top_p": 0.9,
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

### 生成参数

| 类别 | 字段 |
| --- | --- |
| 限制 | `max_output_tokens`、`min_output_tokens`、`max_tool_calls` |
| 采样 | `temperature`、`top_p`、`top_k`、`typical_p`、`min_p`、`top_a` |
| 惩罚 | `presence_penalty`、`frequency_penalty`、`repetition_penalty` |
| 候选与概率 | `n`、`seed`、`logprobs`、`top_logprobs` |
| 输出 | `stop`、`response_format`、`modalities`、`audio`、`prediction`、`verbosity` |
| 策略 | `service_tier`、`safety_identifier`、`safety_settings`、`cache_control` |
| Responses 状态 | `previous_response_id`、`store`、`include`、`truncation`、`background`、`conversation`、`prompt` |
| 缓存与跟踪 | `prompt_cache_key`、`prompt_cache_retention`、`request_id`、`session_id`、`timeout_ms` |

稳定字段之外的扩展进入 `RawExtra`，供兼容和模板使用；不保证每个目标协议都重新发出这些字段。

### 消息与内容

消息包含 `role`、`content`、`tool_calls`、`tool_call_id`、`name`、`cache_control`、`metadata`。角色为 `user`、`assistant`、`system`、`developer`、`tool`。系统指令按目标协议聚合到消息、`system`、`systemInstruction` 或 `instructions`。

| Part 类型 | 主要字段 |
| --- | --- |
| `text` | `text` |
| `reasoning` | `reasoning_text`、`text`、`signature`、`signature_provider`、`encrypted_content`、`reasoning_summary` |
| `refusal` | `text`、`annotations` |
| `image` | `image_url`、`image_base64`、`media_type`、`detail` |
| `audio` | `audio_url`、`audio_base64`、`media_type`、可选转写 `text` |
| `video` | `video_url`、`video_base64`、`media_type` |
| `file` / `document` | `file_id`、`file_name`、`file_data`、`uri`、`media_type` |
| `tool_output` | `tool_call_id`、`tool_output` |
| `tool_call` | 工具调用字段 |

多模态数据也可能保留通用 `uri` / `data`；渲染器选择目标可表达的形态。

### 工具

函数定义包含 `type: "function"`、`name`、`description`、`parameters` / `input_schema`、`strict`。调用使用 `id`、`type`、`name`、`arguments` / `arguments_text`；`thought_signature` 与来源字段成对保存。

工具结果通过 `tool_call_id` 关联调用。Gemini 的 `functionResponse` 需要名称，无法从历史关联恢复时返回带消息索引和调用 ID 的转换错误。

Responses 内建工具（例如搜索、文件、代码执行、图像生成）带有执行语义；目标协议无法表达时明确报错，不自动伪装为函数工具。

## 响应模型

```json
{
  "id": "resp-example",
  "model": "provider-model",
  "created_at": 0,
  "status": "completed",
  "output": [],
  "stop_reason": "stop",
  "incomplete_details": {},
  "metadata": {},
  "service_tier": "default",
  "system_fingerprint": "example",
  "usage": {},
  "error": null
}
```

`output` 可包含消息、函数调用、reasoning 和可表达的供应商工具结果。函数项使用 `call_id`、`name`、`arguments`；reasoning 可含正文、摘要及加密内容。

用量除输入、输出、总 token 外，还记录缓存命中/创建、推理、文本/图像/音频、工具使用、预测接受/拒绝、内建工具调用数，以及 `estimated`、来源、供应商原始用量。未提供统计的字段不等于精确的零消耗。

## 推理与签名

区分可见推理文本、供应商签名和不可解释的加密／redacted 内容。签名恢复按来源匹配，不能把 Anthropic 签名直接充当 Gemini `thoughtSignature`。

当前信封前缀为 `maheshvara-reasoning-v2:`，携带文本、密文、摘要、供应商和模型信息，可经 Claude 签名槽在客户端往返后恢复。读取也兼容 v1。已知来源不匹配的密文不能作为另一供应商的原生签名回放；Responses 路径保留 v1 无来源密文的兼容处理，不能将其描述为严格的模型级加密验证。

Chat 扩展 `tool_calls[*].extra_content.google.thought_signature` 被标为 Gemini 来源。跨供应商历史缺少合法 Gemini 签名时，必要的首个函数调用使用 `skip_thought_signature_validator`。未知块、空 thinking 和不能解释的外部 redacted 数据按转换规则处理，不变成普通文本。

实现与回归见 [reasoning.go](../backend/relay/maheshvara_reasoning.go)、[v2 测试](../backend/relay/maheshvara_reasoning_v2_test.go)。

## Gemini Part 约束

每个 Part 必须包含一个有效数据表示：非空 `text`、`inlineData`、`fileData`、`functionCall` 或 `functionResponse`。`thought` 是文本 Part 的属性，不是独立内容。

过滤后无 Part 的消息被丢弃，相邻同角色消息可按序合并。转换器不使用空文本占位；没有任何可表达内容时返回本地错误。

## 请求映射

| 内部字段 | Chat Completions | Anthropic | Gemini | Responses |
| --- | --- | --- | --- | --- |
| 模型 | `model` | `model` | URL 模型 | `model` |
| 指令 | system/developer 消息 | `system` | `systemInstruction` | `instructions` |
| 消息 | `messages` | content blocks | `contents.parts` | `input` items |
| 多模态 | 扩展 content parts | blocks | `inlineData` / `fileData` | input content |
| 推理 | `reasoning_content` 等扩展 | `thinking` | thought Part / config | reasoning item / config |
| 函数定义 | `tools[].function` | `input_schema` | `functionDeclarations` | function tool |
| 调用与结果 | `tool_calls` / tool 消息 | `tool_use` / `tool_result` | `functionCall` / `functionResponse` | call / output item |
| 输出结构 | `response_format` | 可表达的 output config | MIME / schema | `text.format` |
| 生成参数 | 官方字段与扩展 | 可表达子集 | `generationConfig` | 可表达子集 |

Chat 扩展可识别 `reasoning_content`、`reasoning_effort`、`repetition_penalty`、`min_p`、`top_a`、媒体和 usage details；识别字段进入内部模型，其他扩展进入 `RawExtra`。

## 响应映射

| 内部输出 | Chat Completions | Anthropic | Gemini | Responses |
| --- | --- | --- | --- | --- |
| 文本 | choice message | text block | text Part | `output_text` |
| 拒答 | refusal / finish reason | text / stop reason | text / safety finish | refusal content |
| 推理 | `reasoning_content` | thinking | thought Part | reasoning / summary / encrypted content |
| 调用 | `tool_calls` | `tool_use` | `functionCall` | function call item |
| 用量 | usage details | usage / cache | `usageMetadata` | usage details |

表格描述映射类别，不保证任意私有字段都能跨协议保留。

## 流式

SSE / NDJSON → 状态化 decoder → `MaheshvaraStreamEvent` → 客户端 renderer。SSE reader 支持多行 `data`、`event` / `id` / `retry`、EOF 前未以空行结束的事件、取消、空闲超时和单行 NDJSON。

renderer 维护工具调用状态、Anthropic block 起止、Responses 顺序及 Gemini Part 合法性。`[DONE]` 只由需要它的目标生成。自定义流需可识别终止条件；有 finish reason 的空补全可成功，仅结束标记而无可表达输出的流失败。字段族模式、异构帧和工具拼装见[流式定义](protocol-definition-reference.md#流式映射)。

## 自定义协议

协议通过 SQLite 与设计器管理，保存时校验并更新注册表；无效新配置不能替换当前有效注册表。配置了 `models` 的协议可以自动发现模型，否则使用手工列表。旧 `/custom-protocols/assist` 不是当前路由，AI 接入使用 Agent 与 CLI。

请求树、响应树、模板、auth、transform、aliases 和字段目录统一见[定义参考](protocol-definition-reference.md)。这些内容在该页维护，避免多份参数表冲突。

## 同协议透传

客户端与选中上游协议一致、且请求未因能力过滤改写时，聊天转发可自动使用透传；Responses 也有对应透传路径。没有需要手工开启的 `relay.passthrough` 配置项。

透传基于原 JSON 重建，只按路由修改模型名，并在需要时补流式标记及 OpenAI usage 选项。它保留未知字段，不意味着字节完全相同或零开销。跨协议、自定义映射及需要过滤的请求使用转换路径。

## 错误与验证

缺失工具名称、无法关联的工具结果、不能表达的内建工具、无有效输出、映射路径错误或流无终止条件均可能导致本地转换错误。不要用空格或伪对象绕过验证。

在 `backend/` 执行：

```bash
go test ./relay ./server
```

现有回归覆盖请求/响应 4×4 矩阵、签名与信封、多模态和工具、usage、SSE、透传、累计快照、异构帧及注册安全。此命令验证本地夹具，不证明所有供应商线上兼容。

主要实现：[类型](../backend/relay/maheshvara_types.go)、[转换](../backend/relay/maheshvara_request_in.go)、[自定义映射](../backend/relay/custom_mapping.go)、[流式映射](../backend/relay/custom_stream_mapping.go)。改变字段语义或终止契约时，应评估版本与旧数据兼容，而不是只改文档中的版本号。
