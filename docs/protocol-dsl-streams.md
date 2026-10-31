# DSL 流式契约与执行状态

本文描述已经接入 v3 编译入口的能力及剩余缺口，不代表四预置已完成迁移或通过性能验收。总进度见 [本轮闭合记录](protocol-dsl-closure-2026-10-31.md)。

## Spec 独立校验

`spec.json` 的 `documents.<name>.stream` 描述原始帧生命周期。Mapping 的事件方向通过 `document` 引用它。校验器直接读取原始帧，不依赖 Mapping 是否保留字段，避免非法输入经投影后消失。

`events` 的每个标签对应有序动作数组。单帧的序号只检查一次，动作按声明顺序执行。

```json
{
  "discriminator": "/kind",
  "responseId": "/response_id",
  "itemId": "/item_id",
  "sequence": "/seq",
  "events": {
    "started": [{"action": "response_start"}],
    "text_start": [{"action": "item_start"}],
    "text_delta": [{"action": "text_delta", "text": "/text"}],
    "text_end": [{"action": "item_finish"}],
    "usage": [{"action": "usage", "usage": {"input": "/tokens/in", "output": "/tokens/out"}}],
    "done": [{"action": "response_finish"}],
    "failed": [{"action": "failure", "error": "/error"}],
    "cancelled": [{"action": "cancel"}]
  }
}
```

动作 `when` 是只读原始帧的布尔表达式，不访问 Mapping 状态。没有动作命中的帧被拒绝。`responseId`、`itemId` 可在动作上覆盖；`kind` 区分 text、reasoning、refusal。`observe` 默认要求响应已经开始；`control` 不创建响应，适用于独立心跳。

没有固定标签的帧可用 `discriminatorValue` 从原文结构选择已声明的动作组。动作中的
`foreach` / `actions` 有序遍历原始数组，可嵌套：`input` 是当前成员，`root` 始终是整帧，
错误路径附真实数组索引。缺失数组为零次动作，显式 null 和非数组仍拒绝；条件在选中
数组前检查。帧序号只检查一次，不因多 choice/tool 成员重复推进。

`implicit:true` 显式声明首次出现时建立 response/item，后续出现观察已有身份；
`sparseIdentity:true` 允许工具名称和 call ID 晚到，冲突及终态完整 JSON 仍检查。
`closeItemsOnFinish:true` 用于没有逐项 stop 的协议，成功终态关闭已建立项并完成参数
校验。`allowBeforeStart:true` 允许 failure/cancel、usage 或 observe 先于 response_start，默认仍拒绝；它不允许 observe 出现在终态之后。
`usage.allowEmpty:true` 允许声明的无主计数辅助帧，不补零、不建立响应；未声明仍拒绝。

`inheritResponseId:true` 允许省略已建立的身份；默认仍拒绝显式 null、空字符串及身份变化。`optionalResponseId:true` 明确允许缺失、null、空字符串，直到首次非空身份；之后仍禁止变化。

`responseIdValue` 可用纯表达式选择当前帧的响应身份，始终读取原始根帧，不访问 Mapping 状态。`responseId` 保留真实字段位置用于诊断。例如 Anthropic 只在 `message_start` 读取 `/message/id`，其他帧返回 `omit` 并按声明继承；顶层未知扩展 `response_id` 不参与身份校验。动作显式覆盖 `responseId` 时仍使用该动作的局部选择器。

`itemIdIsIndex:true` 使用非负 int64 整数字面量作为身份。可用 `itemIdValue` 纯表达式声明归一化，例如缺失/null 索引为 0；归一化后仍检查身份类型，`itemId` 指针继续用于错误定位。未声明时不自动补 0。

`sequence` 和 usage 计数要求非负 int64 整数字面量，不接受小数或指数拼写。缺失 usage 字段不补零；必需字段由 schema/assertions 声明。只有 usage 可以出现在成功终态之后，EOF 仍检查终态。

`tool_start`、`tool_delta`、`tool_finish` 复用核心工具关联及 JSON 完成校验。普通 `item_finish` 关闭既有类型，`tool_finish` 额外要求工具项。关联表及保留字节受原有限额约束。
Wire 观察器的身份、必要诊断路径及未完成工具参数共同消耗原 BufferBytes。身份自身超限
保留原 `identity_bytes` 诊断及错误链，只有组合超限才报告实际 `state_bytes`。

## Mapping 状态与事务

事件方向 `state` 声明固定名称槽位，每个槽位包含 `schema`、可选 `initial` 以及 `update` 表达式。`read from:state path:/name/...` 在编译时绑定槽位。

`initial` 缺失与 JSON null 不同。当前帧所有槽位更新读取同一旧快照；输出、来源或核心校验失败不提交。只有外层帧成功才统一提交。

状态属于一个 `EvaluationState` 中的已编译方向和 scope，不跨请求共享。持久槽位拥有独立字符串，临时 `JSONOutput` 不进入下一帧。Native 重放同样执行 effects、observe 及槽位更新。

`eventBatch:true` 表示一个输入生成多个核心事件。空批次默认拒绝；有实际 state/indexes 的 `deferEvents:true` 方向才可推迟到后续事件。独立 wire 校验仍执行，空批次不能隐藏非法生命周期或提前 EOF。

## 稀疏索引与增量原语

`indexes` 声明 keyMode（string、integer、raw_json）、重复策略（first、last、reject）及值 schema。`remember`/`recall` 优先使用词法 `with_index`，否则使用声明的持久表。事务仅暂存本帧变化的条目，不复制整表。

普通表保存独立 JSON，不携带私有 Native/Source 来源凭据；给普通对象添加 `native` 字段不能获得重放权限。动态条目限额不扣除固定槽位和表的声明数量。

事件解码表可显式设置 `retainSource:true`，只保留 `capture_source` 或
`capture_native.source` 已产生的真实来源凭据。存储先独立拥有编码后的 JSON，再在其
视图上恢复已存在的 Source 标记；不保存初始整帧，不恢复 Native 包络权限。
同名普通 JSON 覆盖会清掉旧凭据，不能借历史有效项伪造来源。scope、编译修订和释放
仍由原流事务约束。额外 Go 视图容器属于物理占用，不将逻辑字节计量写成精确堆占用。

`index_values` 按键确定性枚举；需要注册顺序时显式按条目中的数值 index 排序。`sort` 比较实际数字，不把 10 排在 2 前面。

| 原语 | 用途 |
|---|---|
| `resolve_item` | 关联 source 中 itemId/callId/index/parentId，返回稳定 key 及 parent；沿用 ItemIdentities 冲突规则 |
| `track_text` | name 为通道、source 为稳定 key、body 为文本；delta 追加，snapshot 返回未见后缀 |
| `track_tool` | name 为通道、source 为稳定 key；body 声明 kind/callId/text/snapshot/done/emit，保存未完成或未交付参数 |

`track_tool` 首次必须指定 json/text kind，后续可补 callId，不能改变身份。snapshot、done、emit 为显式布尔值。`emit:false` 暂存，随后 `emit:true` 返回尚未发送内容。JSON 完成时验证一次，成功发送后释放正文；自由文本不解析成 JSON。

普通文本增量只更新摘要，不复制完整前缀或 hash。JSON 快照、参数完成及首次发送积压内容才物化累计内容。失败事务不改变摘要、别名或缓存前缀。

每方向最多声明 8 个字面量 tracker 通道，由编译期检查。body/signature/arguments 不另计为三个动态 item；同一稳定身份仍算一个 item。槽位、索引、工具正文、身份别名、tracker 键及工具 callId 共同消耗已编译的 `BufferBytes`，不会分别获得一份完整额度，也不使用表达式临时工作预算的放大系数。这是逻辑预算，不是物理堆占用估计。

## 输出、原生观察及释放

`frameBatch:true` 使 `EncodeFrames` 直接物化数组成员，避免先编码整个数组再解析；`EncodeEvent` 保留数组接口。一个原始帧由 EventFrame 保存一次，满足身份、scope、映射指纹等重放条件时只发送一次。

`observe` 是原生重放时的目标状态更新程序，与 program 共用准备表达式，不通过“渲染后丢弃”代替。只有 observer 能读取 `runtime /nativeFrame/...`，以保存真实 wire 索引。普通渲染和 effects 无权读取该私有输入。使用持久写入且允许 Native 重放的事件编码器必须声明 observer。

`finalize` 返回尾帧数组，可以读取状态和 runtime，不能读取已结束的 input/root。仅在观察到真实语义终态且实际渲染过时执行。Native-only 流及已交付终态流不补终态，usage 尾帧可保留至 `FinishEvents`。

目标 runtime `/stream/responseId`、`/stream/sequence`、`/stream/started`、`/stream/terminal` 来自已验证交付状态。`CommitDelivery` 仍在实际 Write/Flush 成功后调用，Mapping 不提前推进它。

真实流生命周期结束时调用 `EvaluationState.Release()`。共享 source/target 状态不能在 source EOF 提前释放。网关、Agent 和双向会话在所属顶层生命周期结束处释放；`SessionAdapter.Release()` 保留重放计量契约。

## 当前验证范围

Anthropic source 四份既有文本、工具、reasoning、加密块样例对照完整核心事件；target
五份样例及晚到工具身份、文本快照、自动关闭、metadata、最终 wire 边界均有对照。
Chat source 四份样例及 metadata/logprobs、晚到 usage/工具身份已验证；target 三份
样例分别验证四种 ClientOutput 意图，四条同协议流逐帧原字节只重放一次。
空事件批次 nil/[] 是内部表示差异；嵌入 Native 按现有容器规则规范化，顶层原帧独立保留，
不通过删除数字、字段或来源断言消除差异。

这不代表四协议 SSE 完成。Chat target 的 choice/message metadata、Anthropic source
签名片段与完整 metadata、未知字段精确路径，以及 Responses/Gemini 生命周期仍须闭合。
原生前缀后修改语义增量的旧失败边界单独记录，未获批准前不作为预置新增接受行为。

## Responses JSON 工具输入切片

最初的工具切片接入既有 `stream-tool-decode` 的六帧生命周期：响应开始、JSON 工具开始、参数增量、参数快照、工具完成和响应完成。配置复用 JSON 解码表达式，`with_input` 只切换所选 `/response` 输入，`root` 仍为原帧。尚未接入 Responses 事件编码器，不据此宣布同协议原帧重放通过。

初始文档显式声明 `itemKinds:["tool_call"]`，只缩小 `decode_event` 方向的能力，协议全局及 JSON 方向的 text 能力保留。省略 `itemKinds` 时仍要求原有文本生命周期，工具动作不能代替文本动作；显式类型必须与实际动作及方向能力一致。下述文本切片已同步补动作和恢复该方向的 text 能力。

来源差异采用明确边界核对：旧终态工具节点的 `Source.Path`、`Native.Source.Path` 是相对包络的 `/output/0`，新值为原帧位置 `/response/output/0`。旧节点的 Native 值保留片段缩进，新节点沿用 `capture_native` 的 ConfigStd 容器规范化；测试对原片段使用相同规范化后比较全部字节，不解码为 `any`，不忽略数字、字段或路径。顶层 `Response.Native` 在旧、新两边均缺失；整帧由 EventFrame 独立保存。

Spec 的 `optionalSequence` 只允许缺失序号，不允许 null 或倒退，缺失帧不会重置已建立的水位。`tool_snapshot` 复用既有 JSON 参数前缀规则。带 `key` 的身份声明把输出索引和 wire ID 关联起来；`comparison:raw_json` 保留旧的原始字符串拼写比较，`unique:true` 拒绝相同原始 ID 被另一个索引复用，关联项与现有 wire 状态共用保留预算。

这组六帧不能代替文本父子项、reasoning、自由文本工具、辅助 metadata、turn-state、心跳特殊处置和 Responses 目标累计缓冲的验收。文本输入的后续进展如下。

## Responses 文本输入切片

`decode_event` 现声明 `tool_call`、`message`、`text`、`refusal`，并恢复该方向的 text 能力。普通文本和 refusal 共用消息／内容片段关系，工具分支继续保留；没有用缩小全局能力的方式通过测试。

独立 Spec 合同增加以下通用声明，供应商标签和字段仍只在配置中出现：

- `message` 是无自身 delta 的容器，只要求 start／finish。`wireId`、`role`、`status` 选择其身份字段；`parentId`／`parentIdValue` 关联子项，`ordinal`／`ordinalValue` 约束子项顺序。
- `text` 选择初始正文或完整快照。`text_snapshot` 可用 `sealText:true` 封口；封口后不再接受 delta 或第二次封口，随后完成快照必须与摘要完全一致。普通内容片段不强制要求单独的 text.done。
- `item_verify` 校验已经完成的内容，不能再次推进 EventReplay。`verifyCompleted:true` 仅用于容器完成：已完成容器必须一致，并且不再完成第二次。预置只在 response 终态使用它，显式 output_item.done 仍拒绝重复。
- `childrenCount`／`childrenPath` 核对声明快照的子项数量以及本帧是否逐一校验；终态 `requireItems:["message"]` 要求覆盖所有已注册消息，包括此前已显式完成的消息。
- `properties`／`propertiesPath` 选择需要保存与冻结的 wire 属性，不包含 Source 或 Native。完成前合并已观察字段，完成后拒绝变更；子项核验使用完整属性集合，不能悄悄删除已完成的 annotations。
- `foreach` 的 `as`、`indexAs` 为子动作绑定当前成员和位置；嵌套仍使用真实数组路径，根帧不变。条件和选择器不访问 Mapping 可变状态。

新增状态只持有独立身份、属性、父子关联和文本摘要，不保存已发送文本前缀。属性、身份和未完成工具正文共用原 `BufferBytes`；组合超限报告 `state_bytes`。普通 delta 只检查封口状态，不复制属性快照或更新其索引。

Mapping 复用 JSON 消息解码、已命名的消息完成表达式和有界索引。共用 `decode-message` 补齐 phase 与已知 turn tag metadata；不认识的 tag 继续留在 wire 扩展中。首次消息来源通过 `retainSource` 保存，后续完成节点不再持有初始 Native 正文；终态正文来自当前终态帧。

`TestResponsesPackageTextSourceStream` 复用三消息、双内容片段、空消息的既有 phase 夹具，比较 phase 在 start、done、terminal 到达以及 refusal 对应序列的逐帧核心结果。表示差异明确限定为 core v2、真实 `/item`／`/response/output/...` 来源与既有 ConfigStd Native 片段编码；正文、消息顺序、usage、身份及其余字段继续完整比较。通用合同测试覆盖封口、重复完成、子顺序、终态遗漏／改写和共享保留预算。

这仍不是 Responses SSE 全部闭合：annotation.added、reasoning、完整 logprobs 累积、in_progress 的特殊分支、辅助 metadata／turn-state、心跳和目标编码器尚未接通。未知扩展当前保留原始帧证据，其与旧虚拟扩展路径的完整差分、混合错误首错顺序、异常 part 类型接受边界仍需独立核对；不能用当前正常序列的成功替代这些验收。未接入事件编码器，因此仍不宣称 Responses 同协议原帧重放已经通过。

## Responses 普通进度与 usage 尾帧

普通 `response.in_progress` 复用 JSON 的模型 usage、托管工具 usage 和 metadata 表达式；不把普通进度更新送入完整响应解码，因此不会因为其中存在 `output` 或 `error` 就改变事件含义。帧只输出本次实际观察到的 usage，内部原始计数账本用于发现跨帧算术冲突，不用累计快照替换当前更新。只有普通进度的模型 usage 更新这个账本；created、completed 以及独立特殊分支不为它填入初值。

原始 usage 的合并先以本次更新的键集合排除旧同名成员，再组装保留成员和更新成员；同名对象也按这个顺序完成一层浅合并。新增的局部索引只记录键，不同时保存两份大值。不能先拼接旧、新全部成员再去重，也不能直接把旧、新对象交给累计操作数大小的 `merge`，否则被覆盖的值仍会重复占用临时预算，错误拒绝原本可容纳的更新。Spec 与 Mapping 使用相同声明结构，仍各自持有状态，通用操作的预算没有放宽。

Spec 的 `state` 与 Mapping 的同名槽位分别编译、分别持有。`assert` 和 `state_update` 按帧内声明顺序执行，序号在动作前检查；一次帧至多更新一次 Spec 状态。所有槽位更新读取同一旧快照，后续断言能读取暂存的新快照，整帧失败不提交。Spec 不得访问 Mapping 索引、Native 或目标运行时；槽位、身份、容器快照和未完成工具正文共同使用原有 `BufferBytes`，不是各占一份额度。固定槽位数量不扣除动态条目容量。

`check_usage` 检查已有核心计数结构和算术，返回原输出值，不重新编码或补默认计数。明确的检查拒绝保留其业务诊断；其他表达式错误仍归到当前断言。来源 identity 保留 `decode.event` 归因，普通计数错误保持旧 runtime 路径。

无 usage 的普通进度按实际载荷产生 metadata 事件或空事件批次，既不建立响应，也不产生终态。metadata 可以先于 created，但不能跟随终态；usage 尾帧可以携带 metadata。空进度控制帧可以位于终态之后，EOF 仍要求真实终态。测试的旧侧使用解码器和来源 EventReplay，区分解码拒绝与后置生命周期拒绝。

Spec 的 observe/control 只需区分“还有载荷”和“空控制帧”，不用再次构造 metadata 数组。计数与形状检查通过且没有 usage 后，投影移除 `id/model/status/usage/object/created_at/output/error/incomplete_details`，剩余字段必然属于 metadata 或未映射载荷。`created` 不在这个排除表中：它在普通进度里仍是未知扩展，不能套用完整 JSON 响应的时间别名。`content_filters:null` 是 metadata，非 null 是不透明扩展，两者都属于载荷；无计数的 `tool_usage` 空容器与未知残余也分别保留。`output_text` 的特殊验证在此前执行，不能因这一存在性判断绕过。实际 metadata 解码、原文位置和排序继续由 Mapping 的共享 JSON 表达式决定。

完整 JSON 解码的空 `content` 保持公开旧路径的 `[]`；直接流终态响应保持旧流中的 `null`。这项差异在事件装配处表达，不修改共享 JSON helper。事件批次的测试版本归一必须保留原 slice 的 nil 性，不能由测试克隆方法制造差异，再反向修改生产结果。

当前特殊分支仍明确拒绝并单列待迁移：`safety_buffering`、需要完整响应验证的 `output_text`、`linapi_cache_billing` 和 `attribution` 的已识别／未识别形态。它们不能通过省略字段或伪造默认值绕过。独立 Spec 中的普通模型计数检查、累计检查和托管工具检查按旧顺序运行；文档级 index/item 断言与 keyed identity 的完整首错顺序仍需后续闭合，当前定向用例不能代表全部多故障输入。

Spec 仍展开独立模型计数与托管工具检查；不能引用 Mapping 的可变状态来缩短配置。observe/control 已改用现有投影表达式，避免为存在性判断重复构造 metadata。其余共享不可变帧读取结果的优化仍需测量；此处没有最新性能无回退结论，也没有默认切换预置。

## 从已交付文本按需接续

`read_delivered_item` 的 `source` 是 Mapping 明确选出的 wire item ID 字符串，返回 `kind`、`isFinished`；仅在 Spec 的 `itemIdIsIndex` 为 true 时附带 `index`。未绑定匹配的输出校验器或未找到该项时返回 missing；有尚未交付的校验结果或失败帧时拒绝读取。协议身份、映射指纹和 scope 继续由绑定入口限定。

`resume_text` 用 `name` 指定固定文本通道，`source` 为目标 tracker key，`body` 为已交付 wire item ID。它仅在实际事件编码程序中可用，不能在原生观察或 finalizer 中登记。成功时只复制摘要及长度，后续追加不会修改校验器；事务失败不保留副本。它不消费或代替最终 wire 校验，也不能恢复工具参数或供应商私有状态。

`runtime /delivered/maxItemIndex` 返回已交付数字身份的最大值；未绑定或尚无数字身份时为 missing。它与 item lookup 属于已有交付事实，不触发读取动态响应序号时的逐帧重渲染。索引加一仍由 Mapping 在实际需要生成新块时决定。

Anthropic 将 `item:` 身份与 wire index 的关系写在 Mapping 中，只在首次修改已转发文本、目标尚无对应渲染项时接续；纯原生流不再另建逐项渲染状态。不绑定输出校验器的独立编码调用保留原拒绝，不能凭未经确认发送的帧假装已有文本。工具和 thinking 的混合接续尚未启用。
