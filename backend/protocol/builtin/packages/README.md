# Anthropic 声明迁移草案

`anthropic.draft.json` 是未启用、未取得激活证据的 v3 Spec/Mapping 候选。
生产加载器仍只读取 `definitions/*.json`；本目录没有加入 embed 或加载流程。
文件保持真实 ProtocolPackage 格式，没有测试封套、隐藏开关或迁移拒绝兜底。

迁移基线是现有 dev.78 Anthropic codec，目标是保持当前接受行为、核心语义和原生保真。
使用现有样例和合成边界做差分；不读取私人请求，不调用供应商。
核心 schemaVersion 1→2 是声明格式迁移的显式差异，不是业务期望值修改。
独立的 history owner 切片另有明确的核心表示迁移：已识别的 keep-all 控制从 wire 参数移动到
HistoryRetention。该切片不使用下述既有样例的“仅适配版本号”比较口径。

## 已写入候选的内容

- 请求参数字段改名，顶层 system、消息、文本、图片、文档、工具定义和工具选择。
- 缓存意图的 value/TTL 分区，以及根 key、retention、options、prewarm 字段保存。
- thinking、签名、加密内容、工具结果的显式递归；只沿所选 content/children 数组遍历。
- `capture_native` 从原始 read 位置捕获消息、工具及内容块；不根据变换后的下标拼路径。
- 请求内容用 `with_index` 保存有序引用：原始 wire 键在容器转义前记入／查询，
  子数组构造前固定当前结果名称；未命中保持缺失，不扫描工具 arguments。
- thinking/output_config 已声明映射到 ReasoningIntent 并反向渲染；空/null 桶按旧早退规则保留，
  effort:null 仍产生 Reasoning，未知余项保留在原参数桶。
  interleaved 只读取可信 runtime 元信息，普通 context Values 不能覆盖。
- server/opaque 工具通过 `replay_native` 回放原生字段，再用当前 name/cache_control 重建可编辑字段；
  `native_matches` 识别原生 inline system 的消息位置，渲染保留当前数组顺序及子内容。
- context_management 的两个完整 keep-all 变体映射到 HistoryRetention；未匹配值完整留在原 wire owner。
- 请求渲染的对应字段，以及响应主体、终止原因、精确 int64 usage 加减的初稿。

这些内容尚不能组成一个完整替代协议。不得把文件存在、编译通过或部分样例通过写成迁移完成。

## 未闭合的边界

| 范围 | 当前阻塞 |
|---|---|
| 请求解码 | 完整负例诊断及多处输入错误的首错顺序 |
| 请求渲染 | 非原生 system 位置、完整 system 历史约束、注解拒绝及独立 assistant 历史分组 |
| 节点保护 | metadata/citations、scope、结构化拒绝及告警；多缓存、多资源和未映射核心字段的逐 owner 拒绝 |
| 扩展 | 递归冲突合并、跨族扩展拒绝及稳定诊断；媒体 data URI 的无转码解析 |
| 响应 | 错误包络、metadata、完整未知 finish reason 与 fallback；输出分组、完整 usage 扩展与反向校验 |
| 原生保真 | 完整节点回放矩阵；原生保存禁用时独立的来源证据；映射变更、scope 和修订矩阵 |
| 声明证据 | 动态 owner 的 emit/aggregate 证明尚未完成；现有效果声明不代表完整字段覆盖 |
| 其他能力 | SSE、模型发现、WebSocket、任务和 Agent 能力没有在本草案中迁移 |

当前没有加入 mapping.samples 激活证据。迁移测试直接使用现有定义中的请求/响应样例，
分别记录已闭合与未闭合结果；不通过修改原样例期望值掩盖差异。

## 当前差分记录

`TestAnthropicPackageDraftMigration` 直接编译并执行候选文件，无测试分支或供应商回调。
30 个既有 JSON 样例全部完整比较相等；另有 12 个边界场景，10 个相等。
比较包含所有公开核心字段、Native、输出字节及完整错误树；仅显式适配核心 schemaVersion。
错误树逐层记录具体类型、错误文字、ConversionError 的 Issues／CandidateOnly、
ResourceLimitError 的全部公开字段和 Unwrap 分支。

工具历史和数组形式工具结果的 name 差异已闭合。新增原始 `"<"` 与 `"\u003c"`
引用键差分，确认没有在容器规范化后错误匹配；递归内容和 Native 仍完整比较。

原 thinking-controls、hosted-tool-encode 和移除根 Native 后的 inline system 差异已闭合，
因此该主矩阵目前为 42 个场景中 40 个相等。
新增 14 组 reasoning 正例分别比较解码和移除根 Native 后的反向渲染，28 次比较均相等；
覆盖 enabled/disabled/adaptive/between_tools、display、未知余项、空/null 桶、effort:null、
可信 interleaved 及普通 Values 同名伪造。另有 9 组 reasoning 负例均保持拒绝，但诊断未等价。

新增 3 个原生编辑场景全部相等：移除根 Native 后，修改 hosted 工具名称并删除缓存、
将工具改为 opaque 后删除名称并设置 null 缓存，以及移动 inline system 并修改当前子内容。
这些比较确认可编辑字段来自当前语义对象，原生快照不会恢复已删除值；
inline system 的原始位置只用于分类，不要求当前数组下标相同。

复用已有大请求夹具，另比较 3 MiB 媒体和约 1,800 个小节点两个场景的解码，
以及移除根 Native 后的编码；四次比较完整相等，使用原预算，没有放宽节点或工作限制。
这验证草案在这些规模下仍能转换，不属于耗时、分配或峰值性能验收。

主矩阵仍有非法 caller、非法 is_error 两个场景未闭合：
两边均拒绝，但阶段／归因等诊断尚未等价。测试打印 `NOT CLOSED`，不是跳过。

9 组新增负例是缺失 mode、缺失/null/过小 manual budget、adaptive 携带 null budget、
未知/数组 display、effort:none 和非对象 output_config。差异主要在 wire 阶段与旧阶段、
诊断路径、原因或建议；不把两边都拒绝称为完整诊断保真。

旧 helper 对负例只要求两边拒绝，并且仅比较首个 ConversionError 的 Issues，
遗漏了包装层、CandidateOnly 和底层 cause；因此旧检查不能证明错误链等价。
增强比较后发现：旧侧通常有两层 ConversionError，草案通常有三层；schema 拒绝还包含
mappingError → fmt.wrapError → jsonschema.ValidationError。缺少 mode／budget 的草案另有
errors.errorString cause，旧侧没有。本次各层 CandidateOnly 均为 false。
这些是本次新明确的差异细节，仍属于原有 11 个未闭合负例，没有扩大已知差异场景范围。

[静态差异账本](../testdata/package-migration-gaps.json) 固定这 11 个场景的完整旧／新错误树，
不是把两边拒绝视为等价。首次快照来自 `c86b9d14` 上原 codec 与未修改草案的真实执行输出，
原始一次性采集留在仓库忽略目录 `.tmp-dev/protocol-dsl-2026-10-29/migration-gap-evidence.jsonl`。
正常测试只读取静态账本，不生成或更新期望；偏离任一侧记录或差异消失都会失败，须人工审查。
增强比较未在原先相等的样例中发现新差异。

台账检查通过只表示本批已覆盖场景没有偏离记录；**不表示上述 11 个未闭合场景通过了错误链或行为等价验收**。
未运行本草案的性能验收。

## HistoryRetention 的显式核心表示迁移

`TestAnthropicPackageHistoryOwner` 独立验证有限值 owner，不扩大旧差分的忽略名单：

| 表示 | core1 旧 codec | core2 候选 |
|---|---|---|
| 已识别的完整 keep-all 控制 | `parameters["wire:claude"].context_management` | `reasoning.historyRetention:"all"` |
| 未匹配的控制 | 原 wire 参数 | 原 wire 参数，完整保留 |
| 原始请求表示 | 根 Native | 根 Native，保留原字节与所用别名 |

已识别范围仅为一个 `clear_thinking_20251015` edit，keep 为字符串 `all` 或仅含 `type:"all"`
的对象，且 context_management 没有其他成员。转义拼写和字段顺序由 JSON 值相等处理；
任何额外属性或 edit 都不能被局部匹配后删除。

生成控制仍由原 `decode-reasoning` 负责，history 在独立组合表达式中加入。这避免 history-only
请求改变空/null thinking 与 output_config 桶的旧早退行为。未知 wire owner 继续按原空对象规则省略，
不会为满足证明器而创建空 `wire:claude`，完整请求/工具/消息程序也没有复制成两份。

六组已知表示测试将字段移动明确写入 core2 期望，再比较其余全部公开核心与 Native；同时检查
原生字节往返、修改 model、删除历史意图和去掉根 Native 后的渲染。最后一种情况使用声明的规范
字符串变体，不能称为与旧对象/转义别名逐字节相同。七种未匹配形态仍做完整旧/新核心比较、
原生往返和无根 Native 编码差分；canonical 与 unmatched 同时存在时拒绝。

这 14 个场景及升级为真实源的六项目标/模式矩阵已通过定向验证；原有 Anthropic 迁移差分也
保持通过，原静态诊断账本没有改动。这里的已知 owner 测试明确验证表示迁移，不计作旧核心字段
逐字节相等的新增样例。

真实源已接入[目标侧历史处置测试](history-target-projection.md)。该有限路径不证明未知 wire owner
在跨协议目标端已经受到完整保护，也不替代其他 context_management 语义的声明。

后续先完成请求方向的关联、保护和原生边界，再补全响应及其 wire contract。
四方向及原有能力完成回归、证据和性能验收后，才考虑接入生产预置加载。
