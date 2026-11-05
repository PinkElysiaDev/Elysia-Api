# JSON 响应最终出站约束

四份 Spec 的 `client-response` 声明旧模块对实际出站 JSON 的校验。
Mapping 的 `encode_response.document` 选择 `response-candidate`，
`finalDocument` 选择 `client-response`：构造及原生协调继续遵守候选文档，
真实发送前的最终校验使用客户端文档。两者都参与修订身份和证据。
这保留了原生错误字符串可被解码、协调，但不能作为最终错误对象发送的原边界。

| 协议 | 声明的最终约束 |
|---|---|
| Chat | 对象类型、ID／模型、精确非负整数时间与索引、choices 和 assistant 消息 |
| Responses | 响应包络、输出项 ID、reasoning summary／content 的文本表示、消息 phase／角色／状态、output_text annotations |
| Anthropic | assistant message、thinking 完成块的文本与签名、usage 必需及可选计数 |
| Gemini | 可选 candidates／content、parts 的已识别载荷或 thoughtSignature；显式空文本仍有效 |

约束以有序断言表达，保留 `conversion_rejected / wire.output`、原静态原因和原文路径。
数组内按源顺序检查；整数必须可解码为 int64，不把 `1.0` 或 `1e0` 当作原模块接受的整数。
非空字符串不额外去除空白。未知可选字段及未知输出项类型仍沿用原接受范围。

Chat、Responses 和 Gemini 在非 null 的 error 对象存在时跳过成功包络检查。
Anthropic 只有 `type:error` 才跳过消息检查；仅携带 error 对象的普通响应仍校验消息和 usage。
这一区别显式写入配置，没有新增供应商专用 Go 执行分支。

`TestPackageFinalResponseContracts` 对四个旧模块和候选包调用同一最终校验入口，覆盖成功、
缺失／null／错误类型、整数拼写、多处同时错误、未知字段和错误包络提前返回。
比较接受结果及 issue 的错误码、方向、阶段、位置和原因；未宣称底层 schema 错误包装与旧模块具有相同错误树。
既有错误包络往返测试还对原生重放后的实际字节执行最终校验。

## 组件对照与激活证据

`executeVerificationSample` 只执行声明的组件方向，保留编码片段的原有接受边界。
`Verify` 在输出匹配、语义往返之后检查显式 `finalDocument`；通过后才计入方向和能力覆盖。
`VerifyCombination` 继续在转换策略、编码和 wire 处置之后检查最终输出，绑定门槛不变。
未新增跳过激活检查的样例开关。

历史 119 个 JSON 组件样例继续由迁移测试读取旧 Definition 并作完整对照。
其中 Anthropic 以下四个片段能编码，但旧模块最终交付也会拒绝，因此不能直接用作新包的交付证据：

| 旧组件样例（原 input／expected 不变） | 最终首错位置 | 新完整交付样例 |
|---|---|---|
| `tool-encode-response` | `/usage` | `completed-tool-encode-response` |
| `reasoning-response-encode` | `/content/0/signature` | `completed-reasoning-response-encode` |
| `signature-reasoning-response-encode` | `/usage` | `completed-signature-reasoning-response-encode` |
| `encrypted_content-reasoning-response-encode` | `/usage` | `completed-encrypted-reasoning-response-encode` |

四个新样例各自具有完整 usage，visible thinking 具有完成签名，并移除根 Native 以验证语义重建。
新语义输入显式携带 `attributes.anthropic_stop_sequence:null`，对应完整出站包络中的
`stop_sequence:null`，保持直接组件往返的缺失与 null 区分。
它们替换 Mapping.samples 中原片段的复制件；其余历史 JSON 样例保持原样，因此 JSON 证据仍为 119 个。
原组件差分中新增最终拒绝一致断言；JSONEvidence 测试要求四个新 ID 均存在且通过，
同时禁止四个旧 ID 被再次当作最终交付样例。原始大整数、字符串拼写及旧期望均未修改。
协议级测试还确认：组件输出与期望一致时，只要最终文档不接受，验证就不计入能力覆盖并阻止激活。

此切片只闭合 JSON 响应最终结构约束。请求工具关联、真实 SSE 生命周期、解码 metadata、
全量行为矩阵、性能及生产切换验收分别跟踪，不能由本文推断完成。
