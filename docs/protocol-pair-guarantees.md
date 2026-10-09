# 四协议点对点转换保证

[契约](cache-usage-contracts.md) · [调查报告](project-investigation-2026-10-05.md)

**当前转换策略说明见 [可配置转换](conversion-implementation.md)。** 本页早期“转换”标签不是任意内容无损的承诺。实际调用以双方修订、完整模型契约和最终策略 hash 对应的组合报告为准；报告区分保留、兼容和拒绝。客户端载体生成成功也不等于真实 Claude Code 或其他客户端一定保留它。

本表记录 chat-completions（C）/ responses（R）/ anthropic（A）/ gemini（G）四个协议 **12 个跨族有向对 + 4 个同族对**的已测行为。覆盖范围以具体语料、能力契约和策略为准，不代表所有客户端及所有字段均可转换。对应测试：`protocol/builtin/real_client_matrix_test.go`（客户端语料）、`protocol/builtin/module_test.go` 4×4、`relay/protocol_module_test.go`、`server/cache_wire_matrix_test.go`（HTTP 级全对）、`server/cross_protocol_tools_e2e_test.go` 和 `server/gateway_conversion_test.go`。

## 本轮打通的主干道（此前 4xx）

| 形状 | 此前行为 | 现行为 |
|---|---|---|
| 目标 A/G：会话中段 system/developer | 曾输出 Anthropic 不接受的 messages[].role=system | 具名规则按顺序上提；作用域或层级改变有诊断，严格模式拒绝；资源及缓存边界不随意删除 |
| R→C/A/G：消息 item 的 id/status（Codex 恒带） | 硬错 | 转换 + warning（同族经原生回放保真） |
| G→C/R/A：对象工具结果（Gemini 标准） | 编码器隐式序列化导致组合验证不一致 | 语义阶段具名 JSON 文本投影；保留数字精度，但载荷类型改变，严格模式拒绝；不会自动把 JSON 字符串解析回对象 |
| C/A→G：字符串工具结果 | 缺少对象映射 | 默认 `gemini-tool-results` 明确包装为对象并诊断；可配置字段名或禁用，严格模式拒绝 |
| A→C：并行多 tool_result 同消息 / 数组 content / is_error / thinking+签名 / strict / 消息级 cache_control / metadata.user_id / disable_parallel=false | 各类硬错 | 拆分多条 tool 消息；拼接为文本；`[Tool error]` 文本标记+warning；签名由具名策略保存或诊断损失，thinking 转 reasoning_content；strict/消息级扩展剥离+warning；user_id 映射 user 字段；false 视为无操作 |
| G→X：RECITATION/OTHER finish | 全目标硬错 | 映射最近语义（content_filter/refusal/end_turn/stop），文档钉死 |
| 流式收集（Agent/探针）遇未映射帧扩展 | 硬错 | 保真保留至 `Attributes["wire:stream"]`（`a135094`） |

## 16 对保证矩阵（请求方向；响应方向同类原则）

| 目标→ | C | R | A | G |
|---|---|---|---|---|
| **C 源** | 直通 | 转换（openai 族） | 转换†1 | 转换†2 |
| **R 源** | 转换（item 身份丢弃+warning） | 直通 | 转换†1（item 身份 warning） | 转换†2（item 身份 warning） |
| **A 源** | 转换（仍受具体能力约束） | 转换（仍受具体能力约束） | 合法原生请求保留；非法中段 system 不原样输出 | system/developer 经规则上提；无法映射的缓存断点明确拒绝 |
| **G 源** | 签名经配置的客户端载体／加密副本保存后投影；无法保存则诊断或严格拒绝 | 同左 | 同左；绝不把 Gemini 原签名冒充 Anthropic 原生签名 | 同作用域的原生保留 |

†1 显式拒绝保留：hosted/server 工具、refusal→A、参数族（logprobs/seed/response_format/top_k/anthropic_thinking/gemini_* 等无映射项）、块级 cache_control（目标未声明 breakpoints）、文件/视频媒体跨族、encrypted reasoning 跨族。
†2 未配置的语义仍明确拒绝，包括 googleSearch 等 hosted 工具和不支持的块级 cache_control。工具结果包装与签名恢复是独立具名规则，不能用于清空其他资源。

Chat→Gemini 的 `stream_options.include_usage` 属于客户端输出偏好，不进入 Gemini 生成参数。上游真实响应按照原始模型绑定验证，目标表达限制在转换后检查，不再通过收窄模型绑定把合法签名误判为上游违约。

自 `dev.22` 起，上述默认规则按实际方向模块适用于预置和旧自定义协议。Responses→Gemini 的 `include: ["reasoning.encrypted_content"]` 由客户端输出选择动作处理，复用 Elysia 认证续传；不代表供应商原生 encrypted reasoning 可以任意互译。未知选择项跨协议拒绝，类型错误返回 `invalid_input`。同线格式保留原生选择。

自 `dev.24` 起，输出选择分离和跨协议空上一轮引用规范化有 `conversion_normalized / info / preserved` 记录，不等同于已完成签名包装。`responses_context` 可关闭，只将 `previous_response_id:null` 转为无引用；非空引用和截断要求仍需对应能力。实际 Responses 预置的非空原生引用尚未通过会话能力声明与验证，本轮明确拒绝，不宣称已经支持。类型校验覆盖入口及用户映射后的标准字段；同路径不同阶段的诊断分别保留。

## 已知降级（转换但语义变化，均有 warning 或文档记录）

- Anthropic/Gemini 目标将 system/developer 提升合并进顶层系统要求。前置 system 可等价表达；中段指令作用域和 developer 层级变化属于有损兼容，严格模式拒绝。
- 跨族流式：Anthropic 首帧包含必填 usage，同帧已知计数优先；兼容模式为未知必填计数补客户端专用 0 并诊断，严格模式拒绝。尾帧使用最终已知用量，内部计量不受占位影响。部分旧 SDK 不会修正首帧输入计数。Responses sequence_number 为本地重编；Chat 流式 refusal→G 退化为文本。
- Responses 保存语义：`store:false` 可无损转为无状态调用；true／缺省／null 向不支持响应对象保存的目标转换时，兼容模式诊断降级并返回 `store:false`，严格模式拒绝。状态关联参数不会一起丢弃。
- 用量投影：具名规则在兼容模式省略目标不可表达的 TTL 桶、Gemini 创建总量、Anthropic 思考明细及非 Gemini 工具提示明细；严格模式拒绝。原始计量和既有总量保留（`cache-usage-contracts.md`）。
- chat 旧版 `"role":"function"` 历史消息未识别（透传后上游 400、本层无诊断）——遗留项，待下轮补显式诊断。

## 原则

- **原生回放有条件**：相同线格式、作用域满足且内容未修改时优先保留；规则改变内容后须重新编码和检查。
- **兼容行为须有依据**：已实现映射的客户端字段按映射处理；未知语义不因兼容模式而自动忽略。
- **拒绝必须显式**：不可表达形态返回带路径的诊断；严格策略无法保留时返回 `conversion_rejected`，目标限制不冒充上游违约。
- **具名规则记录降级**：本轮元数据、工具对象、系统上提和输出形态投影进入该次请求的 ConversionIssues。其他历史适配仍须按具体样例核验，不能据此宣称全部字段无损。

## dev.25 验收边界

实际四个预置的 16 个方向分别覆盖 JSON/SSE 普通文本、单工具两轮，以及正文混合两个同名不同 ID 工具的两轮调用。测试经过 HTTP 网关，保存完整回复历史后提交工具结果；对象中的大整数必须完整到达模拟上游。固定 SDK 回放检验网关实际输出，完整消费流并检查错误事件，不能替代真实供应商和 Cherry Studio 验收。详见 [修复与验收记录](four-protocol-repair-dev25.md)。

非空 Chat↔Responses 公共 URL 引用和 logprobs 有明确映射；文件引用、Gemini grounding、非 URL 引用和非空上下文管理状态没有通用跨协议映射，继续明确拒绝。流式引用跨越不同文本项交错形成的不连续区间时拒绝，避免把引用绑定到错误正文。
