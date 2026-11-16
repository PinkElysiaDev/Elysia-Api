# 执行速度与分次执行用量

[三分契约](protocol-three-part-contract.md) · [转换配置](conversion-implementation.md)

大自在天 Usage 区分模型主计数与执行事实。`executionSpeed`、`executions` 跟随来源 Usage 保存；客户端无法表达时，由目标 Mapping 具名投影。它们不属于显示元数据，不因为响应正文关闭、截断或客户端省略字段而消失。

## 中立合同

`executionSpeed` 表示实际观察到的执行速度，取 `standard`、`fast` 或 null。缺失与显式 null 不同；不从请求、服务档位或模型默认值回填，也不包含固定费率倍率。

`executions` 为完整结果的一份有序账本，允许缺失、null、空数组。每项包含：

| 成员 | 含义 |
|---|---|
| purpose | `sampling`、`context-compaction`、`advisor` 或 `fallback-result` |
| model | 实际上报的模型字符串或 null；可缺失，不使用请求别名补全 |
| topLevelRelation | `included`、`excluded` 或 `unknown`，只描述与父用量的包含关系 |
| input／output／cacheRead／cacheCreation | 必需的 `{count:int64, origin:"observed"}`；显式零是观测值 |
| cacheCreationByRetention | 可缺失、null 或以 `5m`／`1h` 为键的 Counter 对象；只细分该项 cacheCreation |

input 包含缓存读取与创建计数。缓存不能超过本项 input；TTL 小计不能超过 cacheCreation，两个已知 TTL 都出现时总和必须相等。已标记 included 的各项不能累计超过已知父计数，但不要求账本完整覆盖父计数。excluded／unknown 不参与父计数推导。账本没有隐式 Total，也不会改变模型主 Total。

这是观测账本，不接受推测或客户端占位计数。未知核心成员、用途、包含关系、非法计数和递归账本由通用核心校验拒绝。没有稳定执行身份时，数组下标只表示这份快照中的顺序，不是跨事件更新键。MergeUsage 的缺失保留、显式值整体替换适用于中立快照，不授权来源 Mapping 猜测数组更新语义。

## Anthropic 的来源与目标声明

只有 Anthropic Spec／Mapping 理解外部 `usage.speed`、`usage.iterations` 和四种 iteration type。Spec 校验已知结构；Mapping 将 ordinary input 加缓存计数归一化，并在自身编码时减回。数值程序由根用量与账本项共用，账本项不递归调用包含整个账本的根解码器。

普通 message 项在没有 fallback 结果项时归入 included；存在 fallback 时，不能只靠模型相同或位置判断执行 attempt，message 项保持 unknown。fallback 的结果项为 included，advisor／compaction 为 excluded。excluded 不意味着额外收费，unknown 也不意味着零费用。缺少价格修订、实际收费判断和 credit 证据时，不据账本生成金额。

JSON 的已知账本可以去 Native 重建和编辑。缺失、null、空数组、模型缺失和缓存分项均保持。未知 type、未知项成员或未知缓存分项使整个 iterations 字段继续归来源自有余项；不同时生成一份不完整的核心账本，不删未知成员后计算总量。跨目标仍拒绝这些未映射语义。

SSE 当前只支持 message_start 中的实际 speed；后续计数更新缺少 speed 时保留先前观测。自身目标只在 message_start 发出 speed，晚到的新增或冲突档位明确拒绝，不能放进未声明该字段的 message_delta。来源 message_delta 若实际带 speed（包括 null 或未知值），整个原始帧留在来源自有余项中，不覆盖中立速度观测；只允许经原始帧证据验证的同源重放，跨协议和去 Native 重建拒绝。

SSE 中任何显式 iterations（包括 null 和空数组）仍由来源 Mapping 拒绝为不支持的语义；Spec 承认其合法结构。归档合同尚未确定数组是快照、追加还是部分更新，因此不按末帧、相同模型、数组位置或长度变化猜测合并，也不宣称去 Native 后能重建源更新历史。该限制独立于 JSON 账本支持。现有通用流余项分支只检查帧顶层，不能自动保留嵌套 usage.iterations；因此此处明确拒绝，避免把未保留的历史称为已支持。

`cache_creation:null` 和 `output_tokens_details:null` 不会补造零计数。JSON 将这些观察保留为来源自有余项，同源去 Native 后仍可重建；与后来编辑的具体明细对象冲突时拒绝。SSE 在 message_start／message_delta 出现这些显式 null 时保留完整来源帧，只允许具有有效原始帧证据的同源重放；去 Native、跨协议和汇总为 JSON 时明确拒绝。后续具体计数仍正常更新，但不会擦除早先 null 的来源事实。字段缺失与已知非 null 明细对象不会因此被标记为未知余项。

## 客户端转换配置

Chat、Responses、Gemini 的目标 Mapping 只读取中立字段，默认提供四个可按 ID 禁用的通用规则：

- `response-execution-speed-disclosure`、`response-execution-usage-disclosure`；
- `event-execution-speed-disclosure`、`event-execution-usage-disclosure`。

compatible 对实际存在的字段执行投影，记录 `conversion_degraded`／`lossy_compatible`、实际核心路径、规则 ID 和策略修订。standard、null 和空数组仍是明确观测，不当作无损默认值。strict 拒绝披露损失；规则禁用后目标编码守卫拒绝残留字段。规则通过现有 `core_controls`、usage 选择器及预览机制执行，不增加供应商 Go 分派。

速度不映射为 service_tier；分次执行不会并入目标普通 usage，也不会借字段投影放行尚未支持的 fallback 正文、credit 或其他来源扩展。

## 内部记录与资源

JSON 在客户端转换前保存来源 Usage，SSE 在来源事件重放后、下游投影前保存。`ProtocolUsage` 的 JSON 保留账本；旧 token 列与汇总只存父计数，不新增 SQL 金额列。正文截断或 BodyOnErrorOnly 不删除执行事实。关闭日志持久化仍按用户配置不落盘，不承诺关闭日志也永久保存。

异步队列计入 Usage 的可变 JSON 字节、计数器、字段键与结构保留成本，沿用累计队列预算与同步写入边界。入队时使用纯所有权快照，不通过 MergeUsage 补造缺失总数。生成的核心读写、克隆、规范化和资源计量必须随模型声明重新生成；新字段参与相同的字节、节点、深度及取消边界，不能提高限额绕过失败。

管理 API／Agent 继续读取原有记录 JSON。浏览器详情适配层将 `protocolUsage` 保留为 `protocolUsageJSON` 精确字符串，摘要和导出携带该字符串，避免将超过 JavaScript 安全整数范围的计数舍入后作为权威账本。服务器的原始记录 API 仍返回原来的核心对象形式。

这些变化需要更新编译器身份并重验修订证据。没有新的供应商请求，也不将此前 Anthropic 的 403 权限拒绝称为字段缺失或成功现场复现。
