# 请求范围与组合验证

`mapping.verificationScopes` 为 HTTP 生成声明独立验证的核心请求范围。声明属于目标 Mapping；来源协议仍然只负责自己的 wire 与大自在天 core2 互转。Spec 描述外部格式，通用 DSL 编译和执行只读谓词。协议 Go 代码不根据供应商选择转换路径。

例如 Gemini 的 `media-with-mime` 范围要求图片及其他已识别媒体节点携带 MIME。Chat／Responses 原有的普通 URL 样例保持不变，完整组合报告中的 MIME 失败也保留；新增 data URI 样例为较窄的范围提供独立正例。网关不下载 URL、猜测 MIME 或补默认类型。

```json
{
  "verificationScopes": [{
    "id": "media-with-mime",
    "direction": "encode_request",
    "capabilities": ["images"],
    "transports": ["http_json", "sse"],
    "checks": [{
      "id": "typed-media-mime",
      "scope": "node",
      "path": "/payload/mime",
      "when": {"op": "ref", "ref": "is-typed-media-without-mime"},
      "reason": "Typed media must carry an explicit MIME type in this independently verified request range."
    }]
  }]
}
```

这里的引用须在同一 Mapping 的 `expressions` 中声明。`when=true` 表示整个请求不属于该范围；`path` 相对于实际核心节点。运行器遍历完整请求的 `content`、`children` 和 `reasoningContent`，不深入工具参数或工具结果中的不透明 JSON。一个请求同时含有 data URI 和无 MIME URL 时，无论顺序，都不满足范围。

当前合同只支持 `encode_request`、HTTP 生成传输和 `node` 检查。谓词可以使用 `literal/read/if/choose/enum/exists/present/type/is_integer/not/equal/equal_text/all/any/length/has_prefix/contains/has_control/matches_charset`。`read` 只能从 `input` 或 `root` 读取明确的 `kind/role/payload/status/reasoningForm` 字段路径；不能读取整个节点、Native、来源身份、属性、资源、运行上下文或状态，不能替换输入、迭代或写入。引用的传递闭包遵守同一限制。谓词必须产生布尔值；类型错误、取消和资源超限都是执行失败，不能作为范围外样例跳过。

离线验证与实际路由都在请求转换之后、目标编码之前检查完整核心请求。范围内的样例仍执行目标编码、外部输入合同和可用的反向重建；response 与 SSE 完整事件序列继续独立验证。每个声明能力必须具有范围内、转换后仍实际存在的正向请求样例。只有文本请求或图片响应不能证明图片请求范围。

每份范围报告携带 `requestScope.id/hash/ownerRevision/boundary` 和独立 `transport`。摘要覆盖声明及引用表达式；样例证据身份也包含范围摘要。报告继续绑定双方修订、编译器、完整模型能力合同、策略和上下文。刷新必须同时保存原始完整报告及各适用范围的结果，包括失败结果。不能把 images 和 tools 两份单独通过的报告拼成联合支持；同一请求必须匹配一整份经过独立重放的报告。JSON 证据不能授权 SSE。

保存模型或来源协议绑定会重新验证。绑定详情中的“已验证的请求范围”列出当前通过的范围、传输和能力；失败报告仍可从绑定 API 的 `combinations` 查看。修改 Mapping、样例、策略或相关身份后，应重新保存绑定以生成当前证据。目标编码守卫仍负责真实可表示性；范围报告不自动授权 `count_tokens`、会话或任务操作。

图片 `detail` 属于独立的预处理意图。MIME 范围本身不删除 detail，也不宣称不同供应商的预处理或 token 计数相同；是否允许具名兼容取舍由目标 Mapping 的转换规则决定。
