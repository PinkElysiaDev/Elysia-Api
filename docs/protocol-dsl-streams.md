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

本阶段没有跨帧 Mapping 寄存器、工具参数片段、父子消息容器、复合帧、metadata、签名、
源续传去重、WebSocket、任务或 Agent 接入。四个供应商的实际流规则须逐项迁移并差分。
六处转换包络新建Response已在 `05cf4117` 绑定所属事件版本；旧零版本独立helper调用保留
core1兼容，已提供Response的版本不被修正。现有服务器Target、源流接入和HTTP失败响应
仍须在生产激活前逐项接通，不能只把本页示例替换进预置后发布。
