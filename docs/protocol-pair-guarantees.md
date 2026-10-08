# 四协议点对点转换保证

[契约](cache-usage-contracts.md) · [调查报告](project-investigation-2026-10-05.md)

**当前转换策略说明见 [可配置转换](conversion-implementation.md)。** 本页早期“转换”标签不是任意内容无损的承诺。实际调用以双方修订、完整模型契约和最终策略 hash 对应的组合报告为准；报告区分保留、兼容和拒绝。客户端载体生成成功也不等于真实 Claude Code 或其他客户端一定保留它。

本表记录 chat-completions（C）/ responses（R）/ anthropic（A）/ gemini（G）四个协议 **12 个跨族有向对 + 4 个同族对**的已测行为。覆盖范围以具体语料、能力契约和策略为准，不代表所有客户端及所有字段均可转换。对应测试：`protocol/builtin/real_client_matrix_test.go`（客户端语料）、`protocol/builtin/module_test.go` 4×4、`relay/protocol_module_test.go`、`server/cache_wire_matrix_test.go`（HTTP 级全对）、`server/cross_protocol_tools_e2e_test.go` 和 `server/gateway_conversion_test.go`。

## 本轮打通的主干道（此前 4xx）

| 形状 | 此前行为 | 现行为 |
|---|---|---|
| A→A：会话中段 system 消息（Claude Code system-reminder）+ 消息级元数据 | 硬错 `/content/N/attributes` | **按位输出** `{role:"system"}`，消息级扩展 wire 保真，位置不变（缓存前缀保真） |
| R→C/A/G：消息 item 的 id/status（Codex 恒带） | 硬错 | 转换 + warning（同族经原生回放保真） |
| G→C/R/A：对象工具结果（Gemini 标准） | 硬错（与 C→G 双向死锁） | **JSON 字符串序列化**，往返可逆 |
| C/A→G：字符串工具结果 | 缺少对象映射 | 默认 `gemini-tool-results` 明确包装为对象并诊断；可配置字段名或禁用，严格模式拒绝 |
| A→C：并行多 tool_result 同消息 / 数组 content / is_error / thinking+签名 / strict / 消息级 cache_control / metadata.user_id / disable_parallel=false | 各类硬错 | 拆分多条 tool 消息；拼接为文本；`[Tool error]` 文本标记+warning；签名由具名策略保存或诊断损失，thinking 转 reasoning_content；strict/消息级扩展剥离+warning；user_id 映射 user 字段；false 视为无操作 |
| G→X：RECITATION/OTHER finish | 全目标硬错 | 映射最近语义（content_filter/refusal/end_turn/stop），文档钉死 |
| 流式收集（Agent/探针）遇未映射帧扩展 | 硬错 | 保真保留至 `Attributes["wire:stream"]`（`a135094`） |

## 16 对保证矩阵（请求方向；响应方向同类原则）

| 目标→ | C | R | A | G |
|---|---|---|---|---|
| **C 源** | 直通 | 转换（openai 族） | 转换†1 | 转换†2 |
| **R 源** | 转换（item 身份丢弃+warning） | 直通 | 转换†1（item 身份 warning） | 转换†2（item 身份 warning） |
| **A 源** | 转换（本表 A→C 行全部适配） | 转换（同 A→C 语义） | 直通（含中段 system 按位保真） | 转换（中段 system/块断点→显式拒绝） |
| **G 源** | 签名经配置的客户端载体／加密副本保存后投影；无法保存则诊断或严格拒绝 | 同左 | 同左；绝不把 Gemini 原签名冒充 Anthropic 原生签名 | 同作用域的原生保留 |

†1 显式拒绝保留：hosted/server 工具、refusal→A、参数族（logprobs/seed/response_format/top_k/anthropic_thinking/gemini_* 等无映射项）、块级 cache_control（目标未声明 breakpoints）、文件/视频媒体跨族、encrypted reasoning 跨族。
†2 未配置的语义仍明确拒绝，包括 googleSearch 等 hosted 工具和不支持的块级 cache_control。工具结果包装与签名恢复是独立具名规则，不能用于清空其他资源。

Chat→Gemini 的 `stream_options.include_usage` 属于客户端输出偏好，不进入 Gemini 生成参数。上游真实响应按照原始模型绑定验证，目标表达限制在转换后检查，不再通过收窄模型绑定把合法签名误判为上游违约。

## 已知降级（转换但语义变化，均有 warning 或文档记录）

- Anthropic/Gemini 目标把**会话前段**的 system 消息提升合并进顶层 system 数组（既有行为）；中段 system 对 A 按位、对 G 拒绝。
- 跨族流式：Anthropic 客户端的 message_start 不带 usage（仅尾帧）；Responses sequence_number 为本地重编；Chat 流式 refusal→G 退化为文本。
- 缓存计数投影：分桶/TTL 明细与 Gemini 创建总量跨族省略+warning（`cache-usage-contracts.md`）。
- chat 旧版 `"role":"function"` 历史消息未识别（透传后上游 400、本层无诊断）——遗留项，待下轮补显式诊断。

## 原则

- **原生回放有条件**：相同线格式、作用域满足且内容未修改时优先保留；规则改变内容后须重新编码和检查。
- **兼容行为须有依据**：已实现映射的客户端字段按映射处理；未知语义不因兼容模式而自动忽略。
- **拒绝必须显式**：不可表达形态返回带路径的诊断；严格策略无法保留时返回 `conversion_rejected`，目标限制不冒充上游违约。
- **warning 记录降级**：剥离/丢弃类适配全部进入该次请求的 ConversionIssues（用量日志可查）。
