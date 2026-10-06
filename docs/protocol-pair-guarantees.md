# 四协议点对点转换保证

[契约](cache-usage-contracts.md) · [调查报告](project-investigation-2026-10-05.md)

本表定义 chat-completions（C）/ responses（R）/ anthropic（A）/ gemini（G）四个协议 **12 个跨族有向对 + 4 个同族对**的行为保证：真实客户端形态**正确转换**，目标无法表达的形态**显式诊断**（错误或 warning），不再有语义惊喜。行为由测试矩阵锁定：`protocol/builtin/real_client_matrix_test.go`（真实客户端语料）、`protocol/builtin/module_test.go` 4×4、`relay/protocol_module_test.go`、`server/cache_wire_matrix_test.go`（HTTP 级全对）、`server/cross_protocol_tools_e2e_test.go`。

## 本轮打通的主干道（此前 4xx）

| 形状 | 此前行为 | 现行为 |
|---|---|---|
| A→A：会话中段 system 消息（Claude Code system-reminder）+ 消息级元数据 | 硬错 `/content/N/attributes` | **按位输出** `{role:"system"}`，消息级扩展 wire 保真，位置不变（缓存前缀保真） |
| R→C/A/G：消息 item 的 id/status（Codex 恒带） | 硬错 | 转换 + warning（同族经原生回放保真） |
| G→C/R/A：对象工具结果（Gemini 标准） | 硬错（与 C→G 双向死锁） | **JSON 字符串序列化**，往返可逆 |
| C→G：JSON 可解析的字符串工具结果 | 硬错 | 解析为对象；纯文本仍显式拒绝 |
| A→C：并行多 tool_result 同消息 / 数组 content / is_error / thinking+签名 / strict / 消息级 cache_control / metadata.user_id / disable_parallel=false | 各类硬错 | 拆分多条 tool 消息；拼接为文本；`[Tool error]` 文本标记+warning；签名丢弃+warning 转 reasoning_content；strict/消息级扩展剥离+warning；user_id 映射 user 字段；false 视为无操作 |
| G→X：RECITATION/OTHER finish | 全目标硬错 | 映射最近语义（content_filter/refusal/end_turn/stop），文档钉死 |
| 流式收集（Agent/探针）遇未映射帧扩展 | 硬错 | 保真保留至 `Attributes["wire:stream"]`（`a135094`） |

## 16 对保证矩阵（请求方向；响应方向同类原则）

| 目标→ | C | R | A | G |
|---|---|---|---|---|
| **C 源** | 直通 | 转换（openai 族） | 转换†1 | 转换†2 |
| **R 源** | 转换（item 身份丢弃+warning） | 直通 | 转换†1（item 身份 warning） | 转换†2（item 身份 warning） |
| **A 源** | 转换（本表 A→C 行全部适配） | 转换（同 A→C 语义） | 直通（含中段 system 按位保真） | 转换（中段 system/块断点→显式拒绝） |
| **G 源** | 转换（对象结果 JSON 序列化；thoughtSignature 丢弃+warning） | 同左 | 同左（CacheCreation 省略+warning） | 直通 |

†1 显式拒绝保留：hosted/server 工具、refusal→A、参数族（logprobs/seed/response_format/top_k/anthropic_thinking/gemini_* 等无映射项）、块级 cache_control（目标未声明 breakpoints）、文件/视频媒体跨族、encrypted reasoning 跨族。
†2 显式拒绝保留：纯文本工具结果、thoughtsTokenCount（→A）、googleSearch 等 hosted 工具、块级 cache_control。

## 已知降级（转换但语义变化，均有 warning 或文档记录）

- Anthropic/Gemini 目标把**会话前段**的 system 消息提升合并进顶层 system 数组（既有行为）；中段 system 对 A 按位、对 G 拒绝。
- 跨族流式：Anthropic 客户端的 message_start 不带 usage（仅尾帧）；Responses sequence_number 为本地重编；Chat 流式 refusal→G 退化为文本。
- 缓存计数投影：分桶/TTL 明细与 Gemini 创建总量跨族省略+warning（`cache-usage-contracts.md`）。
- chat 旧版 `"role":"function"` 历史消息未识别（透传后上游 400、本层无诊断）——遗留项，待下轮补显式诊断。

## 原则

- **同族直通**优先走原生回放：未修改请求逐字节保真，一切跨族适配不适用。
- **转换优先于拒绝**：真实客户端恒带字段（item id、strict、disable_parallel=false、metadata.user_id）一律适配。
- **拒绝必须显式**：不可表达形态返回带路径的 `unsupported_capability`，绝不静默丢弃计数或身份。
- **warning 记录降级**：剥离/丢弃类适配全部进入该次请求的 ConversionIssues（用量日志可查）。
