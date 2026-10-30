# OpenAI Responses 请求与响应声明迁移草案

`openai-responses.draft.json` 是独立的 v3 Spec/Mapping 候选，参照当前 Responses 2.2.8、dev.78 实现。只包含请求解码、请求编码、JSON 响应解码与编码；不包含 SSE，也没有加入生产加载、embed 或激活样例。测试编译器不安装旧 codec 模块。

## 当前声明范围

- 普通消息、字符串 input、input_text/output_text、URL/data URI 图片和文件容器。
- JSON 与自由文本工具声明、调用和结果；参数字符串使用已有 parse_json/stringify_json。结果名称使用调用独享的原始 JSON ID 索引，不扫描工具 arguments。
- 服务端/opaque 工具使用已有受目标与 scope 检查的 replay_native，输出时重新叠加当前 name/cache。
- reasoning 的 summary、content、加密资源，以及旧实现对单段可见正文的规范化。仅请求方向把 `content:null` 当作无可见数组；不把它改成空数组。
- 请求常规参数、reasoning effort/summary、工具选择及请求缓存意图。effort 派生 enabled/disabled 模式，空/null 控制保留原参数表示。
- 普通原生 instructions 的首节点位置与当前文本。判断读取现有 Native 来源，不创建或改写 source path。
- 基础 usage、缓存读写、已知 input/output details、精确 int64 total 推断；普通 response 状态与 finish reason。
- 原始节点捕获和 output_text 的 annotations/logprobs。捕获点来自原始 read；没有根据重排后的下标伪造来源。

这些声明不是完整替代品。效果声明目前只证明已声明的局部作用，不代表所有核心字段都有处理证明。

## 差分账本

`TestResponsesPackageDraftMigration` 使用现有定义中的 **36 个非事件样例**，并将其中 **16 个编码样例移除根 Native** 后再比较。另有两个合成边界，各比较请求解码和移除根 Native 后编码：

- 带 ID、空 summary、`content:null` 和合成 encrypted_content 的 reasoning 历史。
- 原生 instructions、字符串 input、具名函数工具选择及 effort/summary 控制。

本批再使用既有图片请求形状验证一次正常 base64 data URI，确认图片也接入同一媒体表达式。

上述基准集合合计 **57 次比较，全部完整相等**。原 document 解码差异已闭合，16 次无根 Native 编码仍全部相等。这是该样例集合的结果，不是全部协议行为、拒绝诊断或激活验收通过。

共用 helper 对正常结果比较完整输出字节、所有公开核心字段、Source 和 Native，只显式适配核心 schemaVersion 1→2。错误按完整树比较类型、文字、ConversionError 的 Issues/CandidateOnly、ResourceLimitError 字段和 Unwrap 分支。当前样例以成功路径为主，不能据此宣称全部拒绝诊断也已等价。

### 已闭合：文件 data URI 的容器拆分

现有 `document-decode` 原先在以下路径有差异：

- `/content/0/children/0/payload/data`
- `/content/0/children/0/payload/mime`

现在图片和文件复用 `decode-media-url`，使用通用 `split_data_uri` 拆分非空 MIME 的 base64 容器，不做媒体解码或转码。空 MIME、非法 marker 保留原 URL；文件层仍保留未拆分的 file_data。缺失由声明先处理，显式 null 不被当成缺失吞掉。

该样例已完整相等，对应 Responses 静态 gap 明确移除，旧记录仍在 Git 历史中。首次未闭合证据保留在 `.tmp-dev/protocol-dsl-2026-10-29/responses-migration-evidence.jsonl`；没有改写样例期望值。

精简 URI 边界表集中在 Chat 的一个既有请求形状，Responses 只增加正常图片确认，不机械复制全方向测试。Chat 的 null URL 本次发现拒绝诊断差异，单独记录在其说明和静态账本中，不能据此宣称 Responses 的所有拒绝路径已验证。

### 已闭合：请求自身 owner 的双向保留

`decode-parameters` 现在将 `prompt_cache_options` 中 `mode/ttl` 之外的成员放回
`wire:openai_responses`；原有未知根字段继续保留。编码由 `merge_deep` 合并自身 owner：
仅 object/object 重叠时递归，标量、null、数组相撞均拒绝，即使值相等。
普通编码对象提取为 `encode-request-fields`，没有 owner 时直接执行它，不进入深合并。
新增原语不认识 Responses 字段，实际取舍仍在 JSON 声明中。

原直接 `EncodeRequest` 允许 model 为任意 JSON，最终线格式校验才限制非空字符串。
草案没有提前收紧类型：当 model 存在、owner 又不含 model 时，条件 emit 才承诺完整复制；
owner 提供 model 时执行原有补入／深合并规则，不宣称这是原样复制。
编译器只证明明确的条件与互斥首级字段，不在运行时比较两份完整正文。

在原57次比较上新增26次成功路径比较，合计83次完整相等；覆盖未知缓存项、原始Native、
模型编辑、移除根Native及直接编码的模型类型边界。另有8次拒绝核对仍有诊断差异：

- 五种合并冲突：旧版 `unsupported_capability / wire`，新原语 `invalid_input / evaluate`，
  新错误链保留 `ObjectMergeConflictError.Path`。不把两边均拒绝视为完整错误树相等。
- 三种非对象 owner：旧版在扩展读取处拒绝；新声明可能先在条件读取 `owner/model` 时拒绝。
  没有新增错误后回退、放行或修改旧静态差异快照。

一般未知参数、其他 owner 和缓存重复意图仍未闭合；这批不能用作生产激活证据。

## 仍须迁移的边界

- wire 输入/输出的完整类型约束、负例诊断与首错顺序；当前 wire contract 只声明根对象。
- 混合 message 子项分段、独立响应内容的相邻分组、工具结果内部块和完整工具关联拒绝。
- instructions 被改为非系统/带 metadata/多正文后的拒绝；首项之外的原生 instructions，以及 scope/映射变化后的全部行为。
- 所有 request/session 引用、跨协议资源和未知字段保护；当前普通字段直接渲染不能代替逐 owner 的保留/丢弃/拒绝证明。
- 请求普通参数、工具/节点扩展的递归合并与拒绝诊断；缓存 owner、重复意图和完整反向布局。请求自身 owner 的正常重建已接通，但错误树仍有上述差异。
- 完整 reasoning parts 类型拒绝与扩展规范化、独立 reasoning_text 的来源限制、修改摘要/签名后所有保护边界。
- 响应根及 item/message 的完整 metadata、output_text 一致性、turn-state/turn-tags、未知响应状态和错误包络。当前内容 metadata 通过 Native 捕获取得位置，关闭原生保存时还需独立的可信位置来源，不能用空路径冒充。
- usage 未知扩展、别名冲突、账单归因、工具 usage 及其原生协调；基础计数样例相等不能替代这些检查。
- Native 关闭、不同 scope、修订与映射变化下的完整输入隔离、拒绝行为和保留量验证。
- SSE、模型发现、异步任务、WebSocket、Agent 及真实客户端验收。

请求用户标识已补为纯声明，优先已有user，再映射anthropic_metadata.user_id；
拒绝未知metadata和真实冲突。与Chat共用的差分及已在dev.81独立修正的typed空白null边界见
[身份迁移记录](../../../../docs/protocol-tracking-identity.md)，剩余诊断差异仍未闭合。

后续应先处理原生保存独立位置问题，再补齐逐 owner 字段处理与错误/metadata 边界。本批未调用供应商，未使用私人请求，也未运行性能验收。
