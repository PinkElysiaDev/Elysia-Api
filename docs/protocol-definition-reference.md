# 自定义协议定义参考

Elysia-Api 的转换核心是「引擎在代码里,协议在数据里」:一份协议定义(JSON)声明
某上游线制的请求渲染与响应/流式映射,引擎负责与内部统一格式 Maheshvara 互转。
四大标准线制(Chat Completions / Responses / Anthropic / Gemini)本身也是以此
模型表达的**预置定义**(首次启动播种入库,可编辑、可复制)。

本文是全部可配置语义的参考。字段目录与校验约束见设计器「预览与测试」所用的
`GET /api/admin/custom-protocols/schema`。

## 顶层结构

```json
{
  "id": "vendor-x",            // 必填,短小写英文
  "name": "Vendor X", "version": "1", "type": "llm",
  "request":  { ... },          // 网关 → 上游
  "response": { ... },          // 上游 → Maheshvara(非流式 + 流式默认)
  "models":   { ... },          // 可选:模型列表发现端点
  "aliases":  { ... },          // 可选:提取阶段键名别名覆盖
  "metadata": { ... }           // 自由元数据(metadata.preset 标记预置)
}
```

## 条件原语 Match

多处配置共用同一条件语义:`{"path": "<点路径>", "op": "<操作符>", "value": <任意 JSON>}`。

- 操作符:`nonEmpty`(默认)/`isEmpty`/`equals`/`notEquals`/`in`/`notIn`/
  `contains`/`isNull`/`notNull`/`isTrue`/`isFalse`/`gt`/`gte`/`lt`/`lte`
- 类型化比较:数字按数值(`4096 == 4096.0`)、布尔按布尔(字符串 `"false"` 不等于
  布尔 `false`)、对象/数组按规范化 JSON 文本(键序无关)
- `nonEmpty` 语境下的「空」:空串/空白、`false`、`0`、`[]`、`{}`、null/缺失
- `isTrue`/`isFalse` 对缺失字段按 `false` 处理(`isFalse` 对缺失为真)

## request(网关 → 上游)

- `method`(默认 POST)/`path`(相对源 baseUrl,支持 `{{maheshvara.<字段>}}` 插值)
  /`pathStream`(流式请求的路径覆盖,如 Gemini `:generateContent` vs
  `:streamGenerateContent?alt=sse`)/`headers`/`query`/`contentType`
- `auth`:`bearer`(默认)/`header`(默认 `x-api-key`,可配 `prefix`)/`query`/`none`
- `shape`:`openai-chat`/`anthropic`/`gemini`/`responses`——模板上下文的
  `messages`/`tools`(responses 另含 `input`/`input_items`)切换为对应线制形状,
  复用内置整形器。与标准线制同形的供应商不要再手写字段级消息转换。
- `body`:字段级构造树。容器为普通 JSON;叶子:
  - 字段引用 `{"field", "mode", "default", "omitIfEmpty", "omitIf", "when"}`
    - `field` 允许「目录字段.子路径」(如 `thinking.enabled`)
    - `omitIfEmpty`:渲染后为空则删键;`omitIf`:渲染后值类型化等于给定字面量
      则删键(覆盖 `false`/`0` 场景);`when`:Match 对 Maheshvara 请求求值,
      不成立则整键省略(「enable_thinking 仅在开启思考时携带」)
  - 常量 `{"value": <任意 JSON>}`
- `bodyTemplate`(legacy 自由文本):占位符 `{{maheshvara.<路径>}}`,过滤器
  `|json` / `|default:<json>` / `|bool` / `|int` / `|string`

## response(上游 → Maheshvara)

- 直接路径:`idPath`/`modelPath`/`statusPath`/`textPath`/`reasoningPath`/
  `toolCallsPath`/`usagePath`/`finishReasonPath`/`errorPath`(点路径,支持下标
  `choices[0].delta.content`)
- `textFilter`/`reasoningFilter`:Match。路径指向对象数组时按元素过滤再提取
  (Anthropic 分离 thinking/text 块、Gemini 分离 thought 部件)
- 或 `body` 构造树 / `fields` 行列表(映射标注,见字段目录)
- 非流式要求至少映射出一个输出(text/reasoning/tool_calls)

## stream(流式映射)

```json
"stream": {
  "payloadPath": "output",          // 事件 JSON 内载荷路径(先解包再映射)
  "mode": "delta|cumulative",       // 全局差分模式
  "modes": { "text": "...", "reasoning": "...", "arguments": "..." },  // 按族覆盖
  "doneValues": ["[DONE]"],          // 整串文本终止值(默认 [DONE])
  "done": [{ "raw": "END" }, { "json": true }],  // 类型化终止值
  "doneValuesReplace": true,         // 移除默认 [DONE]
  "eventKeys": ["type", "event"],    // JSON 载荷内事件名判别键
  "finishWhen": { "path": "finished", "op": "isTrue" },   // 终止判定覆盖
  "statusWhen": { "path": "phase", "op": "equals", "value": "DONE" },
  "frames": [ ... ],                 // 异构帧逐帧映射(优先于 events)
  "events": ["message"],             // legacy 事件名白名单
  "response": { ... }                // 流帧默认映射(帧未带 response 时回退)
}
```

- 缺省终止语义:`finishReasonPath` 映射值字符串化非空、`status == "completed"`、
  doneValue 字面量;`finishWhen`/`statusWhen` 配置后按 Match 语义判定
- 终态后约 2 秒排水窗:继续接收 usage 尾帧与错误帧,其余丢弃;窗内无数据视为
  干净结束
- 空补全(finish_reason 有值但零输出)按成功处理;仅 `[DONE]` 兜底的空流报错

### frames(异构帧)

```json
{ "event": "content_block_delta",      // 事件名(SSE event 字段或 eventKeys 判别键)
  "match": { "path": "delta.type", "op": "equals", "value": "text_delta" },
  "payloadPath": "...",                 // 帧级载荷路径(缺省继承流级)
  "tool": { ... },                      // 分帧工具拼装
  "response": { ... },                  // 该帧自己的映射
  "terminal": true }                    // 命中即判流终态
```

- `event` 与 `match` 至少一个;同时给出须同时成立;首个命中生效
- 无事件名协议(Gemini data-only 帧)用 `match` 谓词选帧;末位放一个通用谓词帧
  作为兜底(如 `{"path": "candidates[0].content.parts[0].text", "op": "nonEmpty"}`)
- 未命中任何帧的事件跳过

### frame.tool(分帧工具拼装)

```json
{ "indexPath": "index", "idPath": "content_block.id", "namePath": "content_block.name" }
{ "indexPath": "index", "argumentsPath": "delta.partial_json" }
```

- 身份帧(content_block_start / output_item.added)声明 `idPath`/`namePath`,
  可选 `indexPath` 注册「下标 → 身份」关联
- 参数帧(input_json_delta / function_call_arguments.delta)只带 `argumentsPath`
  (+`indexPath` 或直接 `idPath`),片段**原样**追加拼接(delta 模式)或按累计
  快照差分(`argumentsMode: "cumulative"`)
- 仅身份无参数的帧不产生参数增量

## aliases(提取键名覆盖)

提供即整体替换该类默认表;`usage`/`toolCall` 条目支持点路径。

```json
"aliases": {
  "textKeys": ["text", "content", "summary"],
  "usage":   { "input": ["inTokens"], "cached": ["prompt_tokens_details.cached_tokens"] },
  "toolCall": { "id": ["ref"], "name": ["fn"], "arguments": ["params"] }
}
```

## models(模型列表发现)

```json
"models": { "method": "GET", "path": "/v1/models", "listPath": "data", "idPath": "id", "namePath": "display_name" }
```

声明后引用本协议的模型源可开启自动拉取;`auth` 缺省继承 `request.auth`。

## 预置协议

- 四份预置(`openai-chat`/`anthropic-messages`/`gemini-generate`/`openai-responses`)
  内嵌于二进制,**首次启动(协议表为空)时写入数据库**,此后作为普通协议行:
  可编辑、可删除、库中优先、升级不覆盖;全部行被清空时下次启动重新播种
- 以 `custom:<id>` 平台被模型源引用;设计器列表显示「预置」徽标(metadata.preset),
  支持一键复制为新协议作为定制基底
- AI 助手以 anthropic-messages 预置作为 few-shot 范例,并在提示词中说明全部
  新能力(shape/when/finishWhen/frames/frame.tool/aliases/过滤)

## 边界(设计如此,非缺陷)

- 客户端侧(网关对四大协议客户端的 SSE 渲染)为引擎本体,保持代码实现
- 四大标准平台值(chat_completions/responses/anthropic/gemini)仍走内置 Go 快速
  路径;预置是等价性的验证台与定制基底
- SSE 传输层解析遵循规范(多行 data 拼接、未知字段忽略、裸行 JSON 兜底);
  空闲/排水超时为运维参数;transform 目录为代码能力
