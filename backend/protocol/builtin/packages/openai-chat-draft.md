# OpenAI Chat 请求与响应声明迁移草案

`openai-chat.draft.json` 是未启用的 v3 Spec/Mapping 候选。生产加载、编译器版本和预置定义不变；没有新增 embed、供应商调用或激活样例。编译测试不安装旧模块，JSON 中也没有将 Chat codec 包装成 DSL 原语。

迁移参照当前 `openai-chat-completions` 2.2.7、dev.78 实现，只包含 `decode_request`、`encode_request`、`decode_response`、`encode_response`。核心 schemaVersion 1→2 是比较时唯一显式适配；其余公开核心字段、来源、Native、数字拼写、输出字节和诊断都保留在差分中。

## 已写入的声明

- 请求常规参数逐字段映射，`max_tokens`／`max_completion_tokens` 对应 `max_output_tokens`；可见 reasoning effort 与 client stream options 分开保存。
- 消息标量与数组内容、工具调用、JSON arguments、工具结果、普通 thinking/refusal。工具名称关联使用现有 `with_index`，原始 JSON 调用 ID 在容器转义前进入调用独享索引。
- 工具选择使用实际核心 `{mode,name}`；具名函数反向输出 Chat 的 `{type:"function",function:{name}}`，不把核心对象直接当作 wire 对象。
- URL/data URI 图片、wav/mp3 与未知格式音频、文件容器；不解码或转码媒体字节。图片、工具调用和工具声明的嵌套未知字段进入对应 `wire:openai_chat` 扩展。
- 请求缓存 key、retention、options、prewarm，以及原生内容中的缓存意图读取。
- 基础 usage、缓存读写计数、已知输入/输出 details 和缺失 total 的精确 int64 加法。没有将大整数转换为浮点数。
- JSON 响应的普通文本、工具与 reasoning 渲染，以及 response 层的 `service_tier`、`system_fingerprint`。
- 每个捕获点直接使用 `capture_native` 读取原始位置；不根据构造后的数组下标伪造来源。

这些声明只覆盖下列差分账本，尚不构成可替换生产协议的完整实现。效果列表没有声称完整字段覆盖；请求资源拒绝声明对应旧 Chat 编码确实不支持请求级资源引用的边界。

## 当前可复核的差分账本

入口是 `TestChatPackageDraftMigration`。它使用现有 25 个非事件样例，额外将 11 个编码样例移除根 Native 后执行，避免只检查完整原生重放。再加入 4 个合成边界：常规请求参数与 stream options、工具结果缺省 name 及嵌套工具扩展、图片扩展、大整数 usage details。

另在既有图片请求形状上验证 5 个 URI 边界：空 MIME、非法 marker、含逗号正文、null、缺失；没有展开所有方向的组合。

再加入一个实际具名工具选择请求，比较解码及移除根 Native 后重编码。

共 **47 次比较：40 次完整相等，7 次未闭合**。原 document 差异已闭合；null 拒绝诊断仍未闭合。台账检查通过不等于这 7 次行为等价通过。

| 比较组 | 数量 | 当前结果 |
|---|---:|---|
| 现有 JSON 样例 | 25 | 20 相等，5 个来源位置差异未闭合 |
| 移除根 Native 的编码样例 | 11 | 全部相等 |
| 新增请求边界 | 3 | 全部相等 |
| 新增 response usage/details | 1 | 业务字段相等，来源位置未闭合 |
| 图片 URI 边界 | 5 | 4 相等，null 拒绝诊断未闭合 |
| 具名工具选择解码与无根 Native 编码 | 2 | 全部相等 |

所有登记差异都固定到完整的差异路径集合，并在[静态账本](../testdata/package-migration-gaps.json)中固定各路径的旧／新原始 JSON 值。缺失与显式 null 分开记录；不会仅因差异仍发生在同一路径就接受内容变化。

共用 helper 现在逐层比较错误类型、文字、ConversionError 的 Issues／CandidateOnly、ResourceLimitError 全部公开字段及 Unwrap 分支。新增路径、错误树变化、已登记值变化或差异消失都会失败，要求人工审查。未改写生产样例期望。

首次静态账本来自 `c86b9d14` 上旧 codec 与未修改草案的实际输出；一次性采集保留在仓库忽略目录 `.tmp-dev/protocol-dsl-2026-10-29/migration-gap-evidence.jsonl`。正常测试只读取账本，不生成或更新期望。

旧 helper 没有比较完整错误链，对 Anthropic 的已知拒绝项也只要求两边拒绝。增强检查揭示了额外 ConversionError 包装层和 schema cause，具体见本目录 README；没有在 Chat 原先相等的比较中发现新差异。固定快照只锁定既有未闭合状态，不构成错误链等价或激活证据。

### 已修正：具名工具选择的核心字段

初稿把具名选择写成 `{type,name}`，而实际核心契约是 `{mode,name}`。定向差分先复现了解码的 `/toolChoice/mode`、`/toolChoice/type` 差异，以及反向把核心对象直接写入 `tool_choice` 的错误。

现在两向声明对齐 `builtin/tool_choice.go`，新增的同一请求在解码和无根 Native 编码中均完整相等，原有 45 次比较未改变。此修正只作用于未启用草案；生产 codec 原本使用正确字段。没有扩展全模式矩阵，工具选择的完整拒绝与诊断仍需后续验证。

### 未闭合 1：Chat 响应中的旧来源位置

旧 `response.go` 把单个 `message` 包装成临时数组，再交给 `decodeMessages`，所以 message 的 Source／Native 路径为 `/choices/0/message/0`，工具调用也多一层 `/0`。草案捕获的是实际输入位置 `/choices/0/message`。

这影响 5 个既有响应解码样例和新增 usage 边界。当前其他字段和 Native.Value 相等，但路径仍是可观察字段，不能把它当作相等，也不能通过在 DSL 里填写假 source path 消除。后续需要独立决定旧位置的兼容策略和可信来源表示，不能绕过捕获权限。

### 已闭合：媒体 data URI 拆分

图片和文件复用同一 `decode-media-url` 声明，调用通用 `split_data_uri`，不硬编码 MIME 或样例正文。只有 `kind=base64` 且 MIME 非空时拆为 data/mime；空 MIME、非法 marker 保留原 URL。文件层按原约定保留未拆分的 file_data，图片层保留 detail。缺失字段先由声明处理，不调用字符串原语；显式 null 仍拒绝。

`document-decode` 现在完整相等，对应静态 gap 已明确移除。原始账本保留在 Git 历史中，没有通过修改旧期望隐藏差异。

### 未闭合 2：null 图片 URL 的拒绝诊断

本批实际测得双方均拒绝，但旧 codec 的阶段为 `runtime`、路径 `/`、原因 `expected string, got null`；草案为 `evaluate`、具体表达式路径、原因 `expected a string`，包装层也不同。

仅此实际执行的 null 场景新增了完整旧／新错误树快照，未修改已有来源路径或 Anthropic 错误快照。原始证据保留在 `.tmp-dev/protocol-dsl-2026-10-29/media-uri-migration-evidence.jsonl`。这是接受行为相同、诊断未等价，不能记为完整通过。

## 尚未完成的生产边界

以下不是上述 47 次比较已证明的能力：

- 请求 wire 的完整类型拒绝、冲突 token 上限、旧 functions/function_call 拒绝、n 范围、非法参数的首错顺序和诊断归因。
- 工具选择全部模式及 names/disableParallel 等组合的拒绝边界；具名函数两向相等不代表这些规则已经闭合。
- 完整消息顺序、工具结果紧邻约束、混合工具结果的有序拆分、多段 thinking 的历史投影、节点资源与异协议扩展保护。
- 模型、scope、映射指纹变化后的原生节点重放；服务端/opaque 工具及任意未映射核心字段的逐 owner 保护。
- 文件 ID 与引用、媒体 metadata、完整 cache TTL/owner 校验和所有缓存反向布局。多个同类缓存意图不能把第一项选择当成完整聚合证明。
- choice/message metadata 的来源位置和承载者、logprobs、annotations、多 choice 结果、独立响应 item 的分组、未知 finish reason 与错误包络。
- usage 的 DeepSeek hit/miss 推断、别名冲突、完整未知 usage 扩展及其反向协调；基础 details 相等不能替代这些边界。
- 任意未知字段的递归冲突合并与无根 Native 的完整保真；当前的浅层 `merge` 不等于旧扩展处理的全部规则。
- 模型发现、SSE、WebSocket、任务/Agent 操作以及激活证据。

上述范围必须继续按旧实现差分补齐。当前未运行本草案的性能验收，也未使用真实供应商或私人会话。
