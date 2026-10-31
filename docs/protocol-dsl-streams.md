# DSL 流式契约与执行状态

本文描述已经接入 v3 编译入口的能力及剩余缺口，不代表四预置已完成迁移或通过性能验收。总进度见 `protocol-dsl-completion-2026-10-30.md`。

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

动作 `when` 是只读原始帧的布尔表达式，不访问 Mapping 状态。没有动作命中的帧被拒绝。`responseId`、`itemId` 可在动作上覆盖；`kind` 区分 text、reasoning、refusal。`observe` 仅观察已开始响应，`control` 不创建响应，适用于独立心跳。

`inheritResponseId:true` 允许省略已建立的身份；默认仍拒绝显式 null、空字符串及身份变化。`optionalResponseId:true` 明确允许缺失、null、空字符串，直到首次非空身份；之后仍禁止变化。

`itemIdIsIndex:true` 使用非负 int64 整数字面量作为身份。可用 `itemIdValue` 纯表达式声明归一化，例如缺失/null 索引为 0；归一化后仍检查身份类型，`itemId` 指针继续用于错误定位。未声明时不自动补 0。

`sequence` 和 usage 计数要求非负 int64 整数字面量，不接受小数或指数拼写。缺失 usage 字段不补零；必需字段由 schema/assertions 声明。只有 usage 可以出现在成功终态之后，EOF 仍检查终态。

`tool_start`、`tool_delta`、`tool_finish` 复用核心工具关联及 JSON 完成校验。普通 `item_finish` 关闭既有类型，`tool_finish` 额外要求工具项。关联表及保留字节受原有限额约束。

## Mapping 状态与事务

事件方向 `state` 声明固定名称槽位，每个槽位包含 `schema`、可选 `initial` 以及 `update` 表达式。`read from:state path:/name/...` 在编译时绑定槽位。

`initial` 缺失与 JSON null 不同。当前帧所有槽位更新读取同一旧快照；输出、来源或核心校验失败不提交。只有外层帧成功才统一提交。

状态属于一个 `EvaluationState` 中的已编译方向和 scope，不跨请求共享。持久槽位拥有独立字符串，临时 `JSONOutput` 不进入下一帧。Native 重放同样执行 effects、observe 及槽位更新。

`eventBatch:true` 表示一个输入生成多个核心事件。空批次默认拒绝；有实际 state/indexes 的 `deferEvents:true` 方向才可推迟到后续事件。独立 wire 校验仍执行，空批次不能隐藏非法生命周期或提前 EOF。

## 稀疏索引与增量原语

`indexes` 声明 keyMode（string、integer、raw_json）、重复策略（first、last、reject）及值 schema。`remember`/`recall` 优先使用词法 `with_index`，否则使用声明的持久表。事务仅暂存本帧变化的条目，不复制整表。

普通表保存独立 JSON，不携带私有 Native/Source 来源凭据；给普通对象添加 `native` 字段不能获得重放权限。动态条目限额不扣除固定槽位和表的声明数量。

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

已接通 Anthropic source 四份既有文本、工具、reasoning、加密块样例，对照完整核心事件及文本/工具的 Chat 输出。Anthropic target 初始样例由独立迁移测试验证。新增身份归一化测试单独核对；空事件批次 nil/[] 属于内部表示差异，事件数量和内容继续逐项比较。

这不代表四协议 SSE 完成。Chat 结构帧、数组动作、隐式开始及稀疏工具身份，Anthropic 签名片段、晚到缓存 usage、未知字段精确路径，以及 Responses/Gemini 生命周期仍须逐项闭合。原生前缀后修改语义增量的旧失败边界单独记录，未获批准前不作为预置新增接受行为。
