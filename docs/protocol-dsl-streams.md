# 声明式流契约的首批边界

v3 包现在可以独立编译 `decode_event` 和 `encode_event`，但生产加载器及四个预置尚未切换。
本阶段验证单响应、文本项目、单帧单事件；不能据此宣称已迁移供应商 SSE。

## Spec 和 Mapping 各自负责什么

Spec 描述线格式及其生命周期：什么标签表示开始，身份及计数在哪里，什么时候允许结束。
Mapping 的 DSL 程序负责真正构造核心事件与目标帧。两者没有共享的可变执行状态。
最终校验不会重新执行 Mapping，也不会用 Mapping 的输出解释原始输入是否正确。

以下是一个合成协议的 `spec.stream`，仅展示流契约。完整包还需要逐方向的
`wireContracts`、Mapping 程序、effects 和验证样例。

```json
{
  "discriminator": "/kind",
  "responseId": "/response_id",
  "itemId": "/item_id",
  "sequence": "/seq",
  "events": {
    "started": {"action": "response_start"},
    "text_start": {"action": "item_start"},
    "text_delta": {"action": "text_delta", "text": "/text"},
    "text_end": {"action": "item_finish"},
    "usage": {"action": "usage", "usage": {"input": "/tokens/in", "output": "/tokens/out", "total": "/tokens/all"}},
    "done": {"action": "response_finish"},
    "failed": {"action": "failure", "error": "/error"},
    "cancelled": {"action": "cancel"}
  }
}
```

`events` 的键是 wire 标签，不是自动生成的核心事件类型。例如把 `text_delta` 映射成
`item.delta`、把 `/text` 映射成核心 `/delta`，仍须写在 Mapping 中。
通用状态机不认识供应商字段，也不决定跨协议的降级取舍。

## 校验与持有量

- 正常生命周期要求响应开始、文本项目开始、增量、项目结束、usage 和响应结束六类动作的声明。
  failure/cancel 可选；未知标签和不支持的动作在声明或运行边界拒绝。
- 响应和项目身份必须是非空字符串。一个实例只接受一个响应，所有帧必须使用同一响应 ID。
  响应开始之前不接受其他动作；响应结束要求所有文本项目闭合。
- 如果声明 sequence，则每帧必须提供非负 int64 整数字面量并严格递增。
  本阶段拒绝重复序号，不把相同内容当作续传重放。
- usage 选择器读取累积快照。缺失计数表示本帧未观察，显式零仍是零；至少一个已声明计数必须出现。
  已出现值必须是非负 int64 整数字面量，保留既有 usage 算术检查。
  必填字段由 wire schema/断言规定；不把缺失 input/output 补成零。
- 成功、失败或取消都是真实终态。失败/取消可以结束未完成项目；终态后只接受 usage。
  EOF 必须由 `Finish` 确认已有终态，不能补造成功帧。
- 单帧继续受 wire 限额约束；跨帧保留项目身份、文本摘要及累积 usage。
  项目数量和身份总字节有界，不保存原始帧数组、已转发全文或额外 Mapping 状态。

此处的“整数字面量”不包含 `1.0` 或 `1e1`。需要其他数值拼写的协议必须显式扩展契约，
不能把本阶段的读取规则等同于全部 JSON Schema integer 表示。

## 执行和证据

`NewWireInputStreamValidation` 检查原始源帧，要求独立 package 流契约；
`NewWireStreamValidation` 检查经过映射及最终修改的目标帧。
调用方逐帧 `Consume`，在真实 EOF 调用 `Finish`。普通 `DecodeEvents`/`EncodeEvent`
仍只负责当前帧，单独调用不构成完整流验收。

核心 schemaVersion 由包固定。事件、嵌套 Request/Response 以及收集结果必须保持该版本，
不能靠重写版本字段跨过验证。`Compiled.Target` 为回放和收集提供正确的版本及独立能力集合。

现有离线序列验证复用这两种 wire 校验：源 Mapping 隐藏序号或补造终态、目标 Mapping
破坏序号或删除终态，均不能取得通过结果。Stream 声明进入包哈希和事件映射哈希，
修改契约不能继续使用原生帧的旧映射身份。

## 后续迁移所需工作

本阶段没有跨帧可写 Mapping 寄存器、工具参数片段、父子消息容器、复合帧、metadata、签名、
源续传去重、WebSocket、任务或 Agent 接入。四个供应商的实际流规则须逐项迁移并差分。
六处转换包络新建Response已在 `05cf4117` 绑定所属事件版本；旧零版本独立helper调用保留
core1兼容，已提供Response的版本不被修正。现有服务器Target、源流接入和HTTP失败响应
仍须在生产激活前逐项接通，不能只把本页示例替换进预置后发布。

## 只读目标流位置

`EvaluationContext.WithOutputStream` 可以绑定实际目标 `WireStreamValidation`。只有 v3
`EncodeEvent` 的程序和 effects 可以读取以下固定叶子；Spec、请求及解码程序不能读取。

| runtime 路径 | 含义 |
|---|---|
| `/stream/responseId` | 已成功交给本地传输的目标响应 ID，尚未开始时缺失 |
| `/stream/sequence` | 最近交付的序号，尚未观察到时缺失，不补零 |
| `/stream/started` | 是否已交付响应开始帧 |
| `/stream/terminal` | 是否已交付真实终态 |

绑定要求完整编译身份、事件映射身份、EncodeEvent方向和精确scope匹配。未绑定或不匹配时，
真正读取会报错，不能被解释成一个空流；未执行分支不会因为携带别的绑定而失败。
`required` 的缺失语义保持，声明可用现有 `integer(add)` 产生后续序号，不增加供应商原语。

校验成功的位置与已交付位置分开。`CommitDelivery` 只在本批所有Write及原有Flush成功后
提交，不检查context，也不改变Flush次数；校验／Write／Flush失败不提交交付位置。
位置来自全部wire修改后的实际帧，复用Spec已解析结果，不另扫一遍帧，不保留整份正文。
Native原始帧也经过相同emit观察。这里的交付只表示本地传输调用成功，不等于客户端已消费。
离线EncodeEvent Sequence明确模拟完整批次验证成功后的交付，裸EncodeEvent不自动绑定。

修复前的反例已保留：目标第一帧改成41后，三个Native入口把声明应生成的42还原为源序号2；
两个快回放入口还绕过了编码effects。当前普通Native快路径执行共享effects检查，仍可保留
原始帧。程序可能读取流状态时进入一次现有编码路径；若实际读取且还需自动协调Native，
明确报 `UnsupportedNative`，不还原错误值，也不删除未知字段。effects的条件读取只负责
允许／拒绝，不算输出程序依赖。状态相关Native字段的可证明协调属于后续能力。

本地非Native错误分支已能沿实际交付的ID、41→42序号渲染，并在真实emit的最终Spec检查、
Write与Flush中验证。尚未开始、已终止和序号耗尽没有默认兜底。最终生命周期校验器仍非
事务式：失败前replay内部可能已改变，不能据位置快照未推进宣称整条流可继续使用。
未完成carrier批次可能已有字节离开进程，也不能依据旧交付位置安全重发。

绑定／序号／所有权、Native反例和服务器交付定向测试通过。原始证据位于
`.tmp-dev/output-stream-native-2026-10-30/`。实测结构尺寸为 evaluationBudget 64→72字节、
evaluationRuntime 24→32字节；尚未量化总分配或延迟，不能称无成本。
组合验证等尚未绑定的入口仍待接入；生产加载器和供应商完整流尚未切换。
