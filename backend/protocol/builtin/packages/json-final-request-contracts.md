# 请求最终 wire 结构迁移

四份 Mapping 的 `encode_request.document` 保持 `generation-request`，
新增 `finalDocument:upstream-request`。前者继续约束构造及 Native 协调；
后者检查真实出站 Value。最终校验、预览和组合验证选择相同文档，
离线激活验证在正例输出匹配和往返之后检查最终文档，通过才计入覆盖。
没有增加完整请求重解码或重新执行 CheckRequest 的兜底。

## 本切片的结构约束

| 协议 | 已声明 |
|---|---|
| Chat | 非空 model、messages 数组、相邻工具调用／结果序列、角色枚举、孤立 thinking 拒绝、stream_options 与 max-token 别名冲突 |
| Responses | 非空 model；input 的字符串／数组／前序响应形式；消息角色；assistant 数组内容不得使用 input_text |
| Anthropic | 非空 model、messages 数组与 user／assistant／system 角色；末尾保留 thinking／output_config 局部断言 |
| Gemini | contents 数组及消息对象；role 可缺失，存在时仅 user／model |

字符串不额外 trim，转义字符串按原解码值比较。错误按声明顺序产生，
Chat 先完成工具邻接扫描，再校验角色；消息对象、未完成调用、工具结果、调用数组、
调用方角色和调用 ID 按旧扫描的实际顺序检查，不能先把所有字段按 schema 校验完再检查关系。
Anthropic 局部 controls 保留 `request.validation` 阶段及旧路径，
包括 `/parameters/anthropic_thinking`、`/parameters/anthropic_output_config`。

Anthropic 的 `max_tokens` 属于独立 operation input 门槛：新旧 generate／stream 均要求它。
旧模块直接最终检查允许缺失，但完整调用依次检查正文预算、最终 wire 和 operation input，仍拒绝缺失。
现有候选 generation 合同更严格，继续保留；最终合同不复制它的根 schema、model 和消息数组检查。
只有必要的七条 thinking／output_config 局部断言在最终文档中显式声明，未新增引用框架或运行两份整文档。

## Chat 工具邻接声明

`upstream-request` 的消息数组断言使用现有 `with_index`、`remember`、`recall`，
以及通用的 lexical-only `forget`、`index_size`。供应商字段、角色及诊断全部留在 Spec。
一个临时索引保存当前待完成的原始调用 ID，另一个只有一项，保存最后调用数组的诊断路径。
检查按消息／调用数组顺序执行，map 的每项结果均省略，不积累副本或编码完整历史。
成功和失败都会退出临时作用域；没有跨请求状态、额外全请求解码或逐项扫描整个索引。

在任意非 tool 消息之前，待完成集合必须为空；tool 消息必须消费一个待完成 ID；
非空 tool_calls 只能属于 assistant。调用 ID 必须是非空字符串，且不能在待完成集合中重复。
最终集合非空时，按原实现报告最后调用数组路径。并行调用的结果可交换顺序。

索引使用 `raw_json` 比较方式，保留旧邻接检查的 ID 写法边界：`"c"` 与 `"\u0063"`
不能互相消费，但完全相同的转义写法可以匹配。后续全树关联使用解码后的 ID，
所以两轮分别匹配、但两轮 ID 仅写法不同的历史，仍必须由下一层检查拒绝。
此差别没有被归一化或新的通用索引语义掩盖。

消息／调用非对象和 tool_calls 非数组的错误说明直接对应旧 `Value.ReadObject/ReadArray`
的固定文本，保留 `invalid_input / runtime / /`；邻接关系错误保持
`invalid_association / wire.output` 及动态消息／调用位置。
Wire check 内明确声明的 reject 保留自己的诊断与错误链；普通断言失败仍使用原结构错误。
`TestPackageFinalRequestStructure/openai-chat` 对照旧模块核对这些边界和多故障首错，
包括 pending 打断、重复／孤立结果、并行顺序、空列表、工具字段类型及原始 ID 拼写。

## Chat 根请求控制

结构、邻接和角色检查通过后，按旧 `builtin/request.go` 的顺序检查：

1. `stream_options` 缺失或 null 均允许；对象的未知成员仍保留原接受范围。
2. `include_usage` 若存在，必须是非 null 的布尔值；不把 false 当成缺失。
3. `max_tokens` 与 `max_completion_tokens` 不能同时存在，两个值即使都是 null 仍冲突。

前两项保留旧 `ParseClientOutput` 的 `invalid_input / decode_request / decode` 归因，
后者保留裸解码错误经请求边界附加的 `invalid_input / encode_request / runtime / /`。
没有在本切片调整这些既有诊断，也不增加单个 max-token 字段的数值约束。
检查使用当前不可变最终 Value，仅选择根字段；没有重新解码 messages、构建 Request 或调用供应商代码。

`TestChatPackageFinalRequestControls` 复用旧／新最终校验对照，确认缺失／null／false、
未知 stream_options 成员、单个 limit 和同时存在的 limit 均保持原边界。
首错组合覆盖 options 优先于 max 冲突，原角色／pending 优先于 options，
以及 options 优先于后面的内容解码错误。旧样例及期望未修改。

## 尚未闭合

这不是完整请求最终校验替代，不能据此切换生产预置：

- Chat 全树调用 ID 的解码字符串去重；它不同于已迁移的待完成集合原文比较。
- 四协议全树调用／结果关联、重复 ID 和结果、工具必需字段及关联节点预算。
- Gemini 无 ID 调用／结果的隐式身份和同名歧义处理。
- 旧最终重解码承担的其余参数、工具 JSON、reasoning、媒体、cache 和 metadata 动态约束。

`TestPackageFinalRequestRemainingConstraints` 明确登记四个尚未迁移的拒绝边界：
旧模块拒绝而结构文档暂时接受。它不是差异忽略列表；将来补齐时必须审查并关闭相应记录。
Chat 原“末尾有待完成调用”已经迁移并移入接受／拒绝对照，当前剩余例是两轮调用分别有结果、
但解码后复用了同一 ID，旧版在 `/content/2/children/0/callId` 拒绝。
字段结构已迁移的测试使用旧／新 `ValidateWireOutput(EncodeRequest)` 同一入口，
比较接受结果、错误码、方向、阶段、位置及原因。
后置修改测试从有效 wire 经 ApplyMutations 改坏 thinking、effort 或角色，确认检查实际最终结果及原首错。
operation 门槛单独验证，未通过扩大往返忽略范围或修改旧样例期望消除差异。

本切片尚无性能实测结论。声明式校验仍有共享读取和重复计量的优化空间，
关联及深层迁移必须复用不可变结果并保持有界状态，不能重新构造完整 Request／Native／媒体树。
