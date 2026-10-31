# 请求最终 wire 结构迁移

四份 Mapping 的 `encode_request.document` 保持 `generation-request`，
新增 `finalDocument:upstream-request`。前者继续约束构造及 Native 协调；
后者检查真实出站 Value。最终校验、预览和组合验证选择相同文档，
离线激活验证在正例输出匹配和往返之后检查最终文档，通过才计入覆盖。
没有增加完整请求重解码或重新执行 CheckRequest 的兜底。

## 本切片的结构约束

| 协议 | 已声明 |
|---|---|
| Chat | 非空 model、messages 数组、消息对象预检、角色枚举、孤立 thinking 拒绝 |
| Responses | 非空 model；input 的字符串／数组／前序响应形式；消息角色；assistant 数组内容不得使用 input_text |
| Anthropic | 非空 model、messages 数组与 user／assistant／system 角色；末尾保留 thinking／output_config 局部断言 |
| Gemini | contents 数组及消息对象；role 可缺失，存在时仅 user／model |

字符串不额外 trim，转义字符串按原解码值比较。错误按声明顺序产生，
Chat 先做原邻接扫描中的消息对象预检，再校验角色；这保留无工具错误干扰时的原首错顺序。
Anthropic 局部 controls 保留 `request.validation` 阶段及旧路径，
包括 `/parameters/anthropic_thinking`、`/parameters/anthropic_output_config`。

Anthropic 的 `max_tokens` 属于独立 operation input 门槛：新旧 generate／stream 均要求它。
旧模块直接最终检查允许缺失，但完整调用依次检查正文预算、最终 wire 和 operation input，仍拒绝缺失。
现有候选 generation 合同更严格，继续保留；最终合同不复制它的根 schema、model 和消息数组检查。
只有必要的七条 thinking／output_config 局部断言在最终文档中显式声明，未新增引用框架或运行两份整文档。

## 尚未闭合

这不是完整请求最终校验替代，不能据此切换生产预置：

- Chat outstanding calls 的紧邻结果及末尾排空、调用 ID 原文比较，以及包含工具错误时的首错顺序。
- 四协议全树调用／结果关联、重复 ID 和结果、工具必需字段及关联节点预算。
- Gemini 无 ID 调用／结果的隐式身份和同名歧义处理。
- 旧最终重解码承担的其余参数、工具 JSON、reasoning、媒体、cache 和 metadata 动态约束。

`TestPackageFinalRequestRemainingConstraints` 明确登记四个尚未迁移的拒绝边界：
旧模块拒绝而结构文档暂时接受。它不是差异忽略列表；将来补齐时必须审查并关闭相应记录。
字段结构已迁移的测试使用旧／新 `ValidateWireOutput(EncodeRequest)` 同一入口，
比较接受结果、错误码、方向、阶段、位置及原因。
后置修改测试从有效 wire 经 ApplyMutations 改坏 thinking、effort 或角色，确认检查实际最终结果及原首错。
operation 门槛单独验证，未通过扩大往返忽略范围或修改旧样例期望消除差异。

本切片尚无性能实测结论。声明式校验仍有共享读取和重复计量的优化空间，
关联及深层迁移必须复用不可变结果并保持有界状态，不能重新构造完整 Request／Native／媒体树。
