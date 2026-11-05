# 真实Responses流的后续迁移依据

本页是源码与既有样例审查，尚未实施新身份契约，也没有通过新增实测宣布真实Responses流已迁移。
当前通用文本／JSON工具Spec的成功不能替代下面这些边界。

## 现有八帧样例需要保留什么

预置`stream-text-decode`依次包含created、output_item.added、content_part.added、text.delta、
text.done、content_part.done、output_item.done、completed。created/completed从response.id取身份，
中间六帧没有response_id。消息owner由output_index关联item.id／item_id，内容part还需要content_index；
不能直接把多个part共用的item_id当成每个文本项的主键。

text.done是完整文本快照，尚不关闭part；content_part.done复核并关闭part；output_item.done
要求子项闭合，再复核消息；terminal还核对完整output和usage。将done帧当无内容事件跳过会
丢失“结束快照改写已输出正文”的拒绝能力。工具arguments.done与item.done也不能合并成一次普通关闭。

## 源接受与最终输出约束不同

| 情况 | 旧源decoder | 旧最终wire校验 |
|---|---|---|
| 后续响应身份缺失 | 保留既有绑定 | 保留既有绑定 |
| 嵌套response.id为null、空串、错误类型或改变 | 拒绝 | 拒绝 |
| 普通帧顶层response_id为null或另一ID | 普通源路径未检查 | 拒绝 |
| 首次created缺response.id | 核心ResponseID和Response.ID保持缺失 | Responses最终包络拒绝 |

源还存在前置item、等价ID不同拼写及重复created的兼容行为；不得将当前严格Spec的start要求
直接套上。响应身份比较使用解码字符串，item别名的旧比较仍用Value原拼写。
上述依据在`builtin/stream_source_contract.go`、`stream_responses.go`、`stream_message.go`及
`wire_stream_contract.go`。当前方向共享、单层、每帧强制非空ID的Spec不能表达这一组差异。

默认同协议原生对不安装event-envelope，不会偷偷修复缺ID；最终Responses校验可拒绝。
默认跨协议和其他非原生对通过已有交付规则使用本次attempt的`resp_ + crypto/rand.Text()`。
直接编码器不生成ID。v3的Codec为空，不能假定已自动继承legacy默认交付规则；源Spec不应生成ID。
后续目标声明需要显式交付身份事实或交付投影，不能用首帧前为空的已交付ID冒充生成器。

## 源事实的同步授权边界

不能把可长期携带的WithOutputStream直接开放给Decode。后续宜采用很薄的同步入口：
Consume当前帧成功后，私有绑定已验证源事实，再执行原DecodeFrame一次；不返回可复用源context，
不记录帧哈希或保留整帧。原Mapping预算、静态schema、effects和来源检查仍需保留。

server源流和离线解码序列已经Consume→Decode，可原地接入。组合验证的目标反解应读取当前
accepted状态，然后才CommitDelivery；不能读取上一批delivered。gateway的wire规则反解目前
在最终Consume/carrier之前，v3需安排到实际最终帧验证处，不能为了绑定再执行一次Mapping。
旧模块顺序保持。运行态可复用一个位置指针，源与目标方向及完整编译身份／scope必须明确授权。

源ID还必须保留原Value表示：旧stream.id可以继承`"\u0072"`，StringValue(decodedString)会将其
改成`"r"`。单个独立拥有的sourceResponseID Value是可考虑的最小持有；不能保存借用的子串
拖住整帧。它会增加16字节字段及原token存储，不能称零成本；现identityBytes按解码长度计量的
口径不能悄改，额外raw token的有界保留需单列。重复创建／身份更新的旧时点也需保持，不能
未经核对就永远使用第一帧拼写。

下一批设计需要同时处理分方向约束、显式继承／别名、消息与part层级、快照和多动作帧。
只有新增ID读取API仍不足以完成真实八帧迁移，更不构成生产加载、Agent或最终性能验收。
