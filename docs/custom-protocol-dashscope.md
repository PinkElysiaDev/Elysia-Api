# 阿里 dashscope(百炼)协议接入指南

dashscope 有两种接入方式,**优先使用兼容模式**;native 模式经自定义协议无损支持(已由
`TestChatCompletionsDashscopeNativeStreamingEndToEnd` 端到端验证)。

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

## 已知不适用场景

自定义协议流式映射的既有边界(与 dashscope 无关,列出备查):

- 整条流共用一份映射,不能按帧类型切换路径(异构帧协议不可);
- 终止条件仅支持 `data: [DONE]` 字面量、`finishReasonPath` 非空与 `status == completed`,
  不支持任意字段级终止表达式;
- `mode` 是流级全局,不能文本累计、tool 参数增量混用;
- 多 choice(n>1)只取第一个。
