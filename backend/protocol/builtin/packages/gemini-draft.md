# Gemini 请求与响应声明迁移草案

`google-generate-content.draft.json` 是未启用的 v3 Spec/Mapping 候选，参照当前
`google-generate-content` 2.1.6 的实现。它只声明 `decode_request`、`encode_request`、
`decode_response`、`encode_response`；生产加载器、编译器版本和现有预置均未改变。
候选编译器没有安装旧 codec，`mapping.samples` 为空，不构成激活证据。

## 已声明的范围

- generationConfig 的常规参数、未知参数余项、根扩展，以及 cachedContent 的资源缓存意图。
- systemInstruction.parts 到 system 消息，contents 的角色与 parts；图片、音频、视频、文档的
  inlineData/fileData 逐字段映射，不解码媒体字节。包装 role 不改变 system 权限。
- 文本、可见 thought、thoughtSignature、函数调用和结果；函数声明与独立 hosted 工具。
- 响应 ID/model、单 candidate、已知与原生未知 finish reason，以及基础 usage 和 thinking 计量。
- thinkingConfig 的 level、budget、includeThoughts 到 ReasoningIntent，以及保留未知余项的反向重建。
- 所有 Native 捕获直接来自原始 read 锚点，不添加旧实现的虚构路径或改写捕获对象。

模型不是从 Gemini 正文读取。候选先读可信 `runtime /scope/model`，当 context.model 非缺失时
使用该值覆盖，包括 null 和空字符串。空 Scope.Model 产生 JSON 空字符串，不自动补默认模型。
空或未提供的 context 本身可以为 null，因此先检查其对象形态再读取成员。

无 ID 调用使用调用独享索引：仅缺失 ID 的 functionCall 消耗 `gemini_call_N` 序号；
名称先解码为字符串再索引，避免转义拼写影响关联。无 ID result 仅匹配唯一的同名待处理调用，
匹配后清空该名称状态；显式 ID result 沿用旧实现，不消费名称状态。歧义场景保持拒绝，
但其诊断尚未等价，见下表。

usage 的 output 是可见 candidatesTokenCount 加 thoughtsTokenCount；反向输出时减去 thinking，
不重复计量。缺少可见输出时，仅在 total-input 恰好等于 thinking 且 tool-use input 为零时推断
output，保留 inferred 与 observed 的区别。加减使用现有 int64 原语。此处描述声明内容；
基础样例并未覆盖全部计数推断、溢出及冲突组合。

## 本批差分结果

入口为 `TestGeminiPackageDraftMigration`。现有定义共 38 个样例，其中四个 JSON 方向为
28 个（请求解码/编码各 10，响应解码/编码各 4），另 10 个事件样例不属于本草案。
对 14 个编码样例另行删除根 Native，确保比较确实经过声明渲染。

| 比较组 | 次数 | 完整相等 | 未闭合 |
|---|---:|---:|---:|
| 现有 JSON 解码样例 | 14 | 0 | 14 |
| 现有 JSON 编码样例 | 14 | 14 | 0 |
| 删除根 Native 后的编码 | 14 | 14 | 0 |
| Scope/context 模型边界 | 5 | 5 | 0 |
| 无 ID、system、响应 role、混合工具边界 | 5 | 0 | 5 |
| 无根 Native 的 system 包装扩展渲染 | 3 | 3 | 0 |
| thinkingConfig 正例的解码与无根 Native 编码 | 22 | 22 | 0 |
| 当前 thinking 编辑与删除 | 2 | 2 | 0 |
| thinking 来源与编码冲突负例 | 7 | 0 | 7 |
| 合计 | 86 | 60 | 26 |

86 次检查的台账测试通过，不表示 26 项差异已经等价。比较保留所有公开核心字段、来源、
Native、完整输出字节及完整错误树，唯一显式适配是核心 schemaVersion 1→2。

最初 19 项实际差异追加在共享静态账本 `../testdata/package-migration-gaps.json`，没有更改既有
Anthropic/Chat 条目。这批证据采自 `a977ea98` 工作区中的本候选；原始合成测试输出位于忽略目录
`.tmp-dev/protocol-dsl-2026-10-29/gemini-migration-observed.log`。账本顶部的 sourceCommit 仍指向
其最早一批记录，不代表本次 Gemini 的采集版本。正常测试只读取固定快照，不生成期望值。

### 来源位置与原生对象差异

| 范围 | 旧实现 | 候选的原始 wire 位置 |
|---|---|---|
| 请求 parts | `/contents/i/content/j` | `/contents/i/parts/j` |
| systemInstruction.parts | `/system/j` | `/systemInstruction/parts/j` |
| 响应消息 | `/candidates/i/content/0` | `/candidates/i/content` |
| 响应 parts | `/candidates/i/content/0/content/j` | `/candidates/i/content/parts/j` |

旧响应还会重编码 content 对象，改变 Native.Value 的键顺序；role 缺失时先补 `model`，
再保存该对象。候选捕获原值，因此这些字节和补字段差异也固定在快照中，没有将其当作相等。

混合工具项同时包含 functionDeclarations 和 googleSearch 时，旧 server-tool Native.Value
删除了 functionDeclarations，而位置仍指向整个工具项。候选捕获完整原对象，差异固定为
`/tools/1/native/value/functionDeclarations`。本批没有为这种派生对象放宽来源授权。

无 ID 复用边界的调用编号、名称、结果及其他语义字段相等；该比较仍因 parts 来源路径不同而
整体标记未闭合。不能据此宣称完整来源保真。

### 无 ID 歧义诊断差异

旧实现返回 `unsupported_capability`，路径 `/functionResponse`，原因是无 ID 结果无法唯一关联。
候选的唯一计数分支拒绝歧义，但返回 Mapping 的 `invalid_input`、声明路径及不同错误链。
两边的完整错误树已固定；不是仅检查“双方都失败”，也不是已完成诊断迁移。

### 已闭合：system 包装扩展与当前正文同时渲染

初稿直接在根对象合并 `parameters["wire:gemini"]`，当其中包含 systemInstruction 的未知
包装字段时，会与当前 system parts 产生同名字段冲突。去掉根 Native 后，旧 renderer 成功，
草案失败；修改当前 system 文本时也能复现。

现在先在 systemInstruction 内合并当前 parts 和该包装的未知字段，再合并剩余根扩展。
三次无根 Native 比较全部相等，分别覆盖未修改、修改当前 system 文本、显式删除扩展。
未知嵌套对象中的大整数与 null 保持原值；删除扩展不会从节点快照恢复旧包装字段。
旧来源路径与原生快照差异仍保持原静态记录，未通过改写捕获来源或放宽比较消除。
这不是通用深层扩展合并的完整验收；冲突诊断、非对象扩展等边界仍需单独闭合。

### 已迁移的 thinkingConfig 正常行为与未闭合诊断

thinkingLevel 对应 effort，非 null 时指定 reasoning scope；thinkingBudget 的 -1、0、正数分别
对应 auto、disabled、enabled；includeThoughts 的 true/false 对应 summary 的 auto/omitted。
显式 null 的 level/budget 保留为核心 null。空对象、null 桶和仅含未知字段的桶遵循旧实现的早退，
不会因没有生成 ReasoningIntent 而丢掉原参数。生成意图后，已知控制从参数桶移除，未知字段保留。

反向合并参数余项与当前意图，不根据 mode 猜造缺失预算；删除当前控制不会复活旧值。
同名 typed/legacy 控制以及同时指定非 null level 和 budget 继续拒绝。协议约束写在 JSON，
未增加 Go 供应商分支；通用 ReasoningIntent 校验继续使用既有核心逻辑。

11 个正例各比较解码和无根 Native 编码，另两项比较当前 level 编辑及删除，24 次完整相等。
正例使用空 contents，避免将已有来源位置差异混入本次参数比较。

7 个负例双方均拒绝，但完整诊断未等价：非对象桶、level 与 budget 并存、includeThoughts:null、
非法来源 level、过小 budget、typed/legacy 冲突，以及 canonical level/budget 并存。
旧实现全部报告 `decode_event / stream`；草案分别在 `decode_request / wire`、
`encode_request / evaluate` 或 `encode_request / mapping.effects` 报告，部分路径、错误码、建议及
包装树也不同。旧诊断方向未修改，不能将草案的较准确归因算作等价迁移。

这 7 项追加快照采自 `9794e509` 工作区，证据为忽略目录中的
`.tmp-dev/protocol-dsl-2026-10-29/gemini-thinking-observed.log`。原 19 项 Gemini 及其他协议的快照
保持不变，正常测试没有生成或自动更新账本的代码。多个同时错误时的首错顺序仍未证明。

## 尚未完成的边界

- 完整输入/输出 wire 类型契约、错误链与首错顺序；当前少数 effects 不是完整字段覆盖证明。
- thinkingConfig 的完整错误树和多故障顺序、safetySettings 验证及其他参数拒绝。
- toolConfig 的大小写兼容、单元素 allowedFunctionNames 规范化、非法值与名称空白规则。
- 非原生 system 位置、注解拒绝、独立 assistant 历史分组，以及逐 owner 的缓存/资源/未映射字段保护。
- 多候选、错误响应、响应分组、候选 metadata、usage modality 明细与未知 usage 扩展。
- 完整 Native 来源/作用域矩阵、混合 hosted 工具的派生快照、未知扩展的递归冲突合并。
- 无 ID 的缺失关联、显式 ID result 后再次无 ID 关联、多候选共享历史状态及所有非法类型诊断。
- SSE、模型发现、WebSocket、任务/Agent、组合证据和生产激活。

本批未运行性能验收，未调用供应商或读取私人正文。编码样例相等和合成关联成功不替代上述边界，
也不能用于宣称生产 Gemini 已经完成声明式迁移。
