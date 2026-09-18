# 阿里 dashscope(百炼)协议接入指南

dashscope 有两种接入方式,**优先使用兼容模式**;native 模式经自定义协议无损支持(已由
`TestChatCompletionsDashscopeNativeStreamingEndToEnd` 端到端验证)。完整的协议定义
语义(Match 条件、别名覆盖、frames 谓词帧、分帧工具拼装、request.shape、四协议
预置)见 [protocol-definition-reference.md](./protocol-definition-reference.md)。

## 方式一(推荐):OpenAI 兼容模式,零配置

直接新建模型源,协议选 **Chat Completions API**,Base URL 填:

```
https://dashscope.aliyuncs.com/compatible-mode/v1
```

API Key 填百炼的 `sk-xxx`。流式/非流式、usage 统计开箱即用——兼容模式就是标准
OpenAI Chat Completions chunk 形态(`choices[0].delta.content` + 末帧 `finish_reason` +
`data: [DONE]`),网关原生支持。

## 方式二:native Generation 协议(自定义协议模板)

适用需要 native 端点(`/api/v1/services/aigc/text-generation/generation`)或
`incremental_output`、`X-DashScope-SSE` 等 native 参数的场景。在协议设计器新建协议,
JSON 粘贴以下配置:

```json
{
  "id": "dashscope-native",
  "request": {
    "method": "POST",
    "path": "/api/v1/services/aigc/text-generation/generation",
    "headers": { "X-DashScope-SSE": "enable" },
    "bodyTemplate": "{\"model\":{{maheshvara.model | json}},\"input\":{\"messages\":{{maheshvara.messages | json}}},\"parameters\":{\"incremental_output\":true,\"result_format\":\"message\"}}"
  },
  "response": {
    "stream": {
      "mode": "cumulative",
      "response": {
        "textPath": "output.choices[0].message.content",
        "finishReasonPath": "output.choices[0].finish_reason",
        "usagePath": "usage"
      }
    }
  }
}
```

要点:
- `X-DashScope-SSE: enable` 请求头开启 SSE(自定义协议的 `headers` 字段);
- `incremental_output: true` 写死在请求模板中(dashscope 默认输出累计全文,模板注入后为纯增量;
  若省略该参数,把 `stream.mode` 保持 `cumulative` 也可由网关做后缀差分);
- `textPath` 指向 `output.choices[0].message.content`——native 的 content 是
  `[{\"text\": …}]` 对象数组,取值器会自动解出文本;
- `usagePath: usage` 覆盖 native 的 `input_tokens/output_tokens` 键名(别名表内置);
- 多模态系列(qwen-vl 等)端点为 `.../multi-modal-generation/multimodal-conversation`,
  需把 `path` 换成对应端点。

## 流式行为说明

- **finish 后尾帧排水**:映射出 `finish_reason`/`status=completed` 后网关不会立刻断流,
  而是继续读取(约 2 秒空闲窗口)以接收滞后下发的 usage 尾帧与 `doneValues` 终止字面量。
  OpenAI 兼容流 `stream_options.include_usage` 的「末帧 usage」正是这一形态;
- **空补全放行**:`finish_reason` 有值但零输出(如内容过滤 stop)按成功处理,与内置协议
  路径一致;只有 `[DONE]` 兜底且从未出现 finish reason 的空流才报错。

## 异构帧协议:stream.frames[]

整条流共用一份映射无法覆盖「每类事件载荷形状不同」的协议(典型即 Responses 型类型化
事件:`response.output_text.delta` 载荷是 `{delta}`,`response.completed` 载荷是
`{response}`)。`stream.frames[]` 按事件名(SSE `event:` 字段,缺省取 JSON `type`/`event`
字段)逐帧匹配,命中即用该帧自己的映射;未声明帧型的事件跳过(需要兜底时用
`stream.response` 声明默认映射);`terminal: true` 命中即判定流终态,覆盖
`response.completed`/`message_stop` 这类以事件名收尾的协议:

```json
"stream": {
  "frames": [
    { "event": "response.output_text.delta", "response": { "textPath": "delta" } },
    { "event": "response.reasoning_text.delta", "response": { "reasoningPath": "delta" } },
    { "event": "response.completed", "terminal": true, "payloadPath": "response",
      "response": { "usagePath": "usage" } }
  ]
}
```

`frames` 存在时优先于 legacy `events` 白名单;帧内 `response` 不得再嵌套 `stream`。

## 故障排查：502「completed without representable output」

含义：流收到了终止标记（`[DONE]` 等），但整条流没有映射出任何输出，也没见到 finish reason。
按概率排查：

1. **空嵌套映射（历史版本设计器踩坑，已自愈）**：旧版「流式映射」开关会写入
   `stream.response = {body:{}}`，运行时把顶层映射整个顶掉。现在空嵌套自动视为
   「继承顶层映射」，注册时也会被净化——升级后存量配置无需重存即恢复；
2. **映射路径与流帧形状不符**：最常见的是 `result_format` 缺省（dashscope native 的
   text 格式正文在 `output.text`，与 message 格式的 `output.choices[0].message.content`
   不同路径），或 `payloadPath` 指错。在设计器「预览与测试」开流式发送，对照
   「上游流事件采样」与「解码出的 Maheshvara 流事件」即可定位是哪条路径没映射上；
3. **finish 与终止标记都没映射到**：确认 `finishReasonPath`（或独立映射里的
   `stop_reason` 字段位）与 `doneValues` 至少一个能命中上游帧。

## 已知不适用场景

自定义协议流式映射的既有边界(与 dashscope 无关,列出备查):

- 终止条件支持 `doneValues` 字面量、`finishReasonPath` 非空、`status == completed` 与
  frames 的 `terminal` 帧型,不支持任意字段级终止表达式;
- `mode` 是流级全局,不能文本累计、tool 参数增量混用;
- 多 choice(n>1)只取第一个;
- 跨帧工具参数拼装(工具调用身份帧与参数增量帧分立的协议,如 Anthropic 式
  `content_block_start`/`content_block_delta`)只能靠 completed 快照在结束时一次性还原,
  无法增量下发;
- 每帧必须是完整 JSON 或 `doneValues` 字面量——增量追加式部分 JSON
  (read-completed 型分块拼接)与二进制帧不支持。
