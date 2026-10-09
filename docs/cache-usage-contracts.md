# 缓存用量契约与定向验证

[English](cache-usage-contracts.en.md) · [本轮实测报告](cache-validation-2026-10-04.md)

缓存是否被使用、上游是否报告计数、网关是否正确转发计数，是三个独立问题。缺失值不能当零；上游接受缓存字段不证明发生命中，更不证明某个 TTL 的时间行为。

## 标准计数

| 协议 | 读取 | 创建 | 输入总量 |
| --- | --- | --- | --- |
| Chat | `usage.prompt_tokens_details.cached_tokens` | `usage.prompt_tokens_details.cache_write_tokens` | `usage.prompt_tokens`，已经包含缓存部分 |
| Responses | `usage.input_tokens_details.cached_tokens` | `usage.input_tokens_details.cache_write_tokens` | `usage.input_tokens`，已经包含缓存部分 |
| Anthropic | `usage.cache_read_input_tokens` | `usage.cache_creation_input_tokens` | 原始 `input_tokens` + 读取 + 创建 |
| Gemini | `usageMetadata.cachedContentTokenCount` | 无生成响应等价字段 | `usageMetadata.promptTokenCount` |

Chat/Responses 继续接受旧顶层 `cache_creation_input_tokens`。与嵌套创建字段同时出现且数值不同会报契约错误；负数、非整数类型、null 与溢出也会被拒绝。创建值缺失保持缺失，零保持零。跨协议生成的新 usage 使用标准嵌套字段，同源未修改的原生响应保留原形。修改或删除语义计数时，已知旧别名同步协调，不允许旧 Raw 恢复已删除的计数。

Gemini 资源创建响应中的 token 数表示资源容量，不能充当一次生成的 `CacheCreation`。目标没有可表达的创建计数或明细字段时，转换器给出诊断并省略该细节，既不隐藏、也不为让转换通过而改动计数——见下节。

## 跨协议缓存创建分桶投影

Anthropic 上游在每次写出缓存的响应里都附带 `cache_creation.ephemeral_5m_input_tokens` 与 `ephemeral_1h_input_tokens` 两个 TTL 分桶。这是上游的记账明细，不是调用方请求的能力，而目标协议（Chat、Responses、Gemini）没有对应字段。

- **具名兼容投影**：默认兼容策略通过 `usage_projection` 省略目标无法表达的分桶，**创建总量仍映射到 `cache_write_tokens`**（Chat/Responses）。严格模式拒绝信息损失；禁用规则后编码器也会拒绝未经投影的字段。
- **可见省略**：省略在 `/usage/details/ephemeral_5m_input_tokens`（或 `ephemeral_1h_input_tokens`）上产一条 `SeverityWarning` 诊断，随成功响应一并进入该次记录的 ConversionIssues，故是「显式省略」而非静默丢弃。
- **确定性**：明细按键名排序遍历，同一输入每次都报告同一条路径；此前被拒绝的键取决于 map 遍历序，同输入在不同运行间会报 `ephemeral_5m` 或 `ephemeral_1h`。
- **同协议保留**：Anthropic→Anthropic 的未修改往返完整保留两个分桶，工具、system 与消息位置都不重排。
- **Gemini 创建总量**：Gemini 响应只报告读取，没有创建计数字段；创建总量同样被省略并产 warning（路径 `/usage/cacheCreation`），读取仍走 `cachedContentTokenCount` 正常跨过。

自 `dev.22` 起，组合验证比较具名规则执行后的语义，不再按协议家族跳过计数比较。Gemini 创建计数省略前先检查原始算术关系，同时从客户端副本移除依赖创建计数的未缓存子计数。`ProtocolUsage` 始终保留完整原始计数，流式客户端投影不会回写计量。

**开放验证缺口**：跨协议线上非零缓存读取尚未在真实站点取得，本地测试不能替代该项，详见[本轮实测报告](cache-validation-2026-10-04.md)。

## 声明站点别名

[映射补丁](examples/cache-usage-alias.mapping.json) 和 [离线样例](examples/cache-usage-alias.sample.json) 演示人工约定的 `usage.provider_metrics.write_tokens`。它们不是完整协议，也不是对 moyuu 私有计费字段的解释；只有供应商契约及实际证据确认其含义后，才应替换路径使用。

1. 在编辑器复制 Chat 定义，使用新 ID；保留能力、四方向、操作、原有样例和扩展。
2. 将补丁对应的 `decode_response.after`、`decode_event.after` 合入定义。如果已有 `after`，应显式组合变换并增加样例，不能覆盖原变换。
3. 将样例追加到 `samples`，补充实际站点的 JSON、流式、零值、缺失和冲突样例。该示例约定标准语义字段优先，包括明确零值；只有标准创建计数缺失才读取别名。
4. 执行验证、预览、保存草稿、启用确切修订，然后验证模型绑定及实际转发。

Agent 使用同一服务层：`elysia protocol draft`、`validate`、`verify`、`preview --direction decode_response --sample ...`、`save`、`activate --id ... --hash ...`。定义编辑后旧验证报告失效；编译器版本变化由现有启动重验机制处理。预置协议只读且随版本自动更新（服务层拒绝在预置 ID 上保存/激活），用户定制一律在协议设计器「复制为新协议」后编辑副本。

旧样例如果把 `cache_write_tokens` 预期为未知扩展，需要按新解析结果更新这一字段的断言：`expected.usage.cacheCreation={"count":原值,"origin":"observed"}`，包括零值；输入总量仍使用原来的总输入。只调整该已识别字段的旧扩展路径，保留其他扩展和所有样例。先用预览检查完整语义差异，再重验启用；不要删除失败样例来绕过版本验证。

`module` 先解析基础协议；`after` 的 `input` 是语义对象，`root` 是原始响应或事件帧。事件变换逐项处理本帧事件，不把后续尾帧提前当作最终计数。原生回放同时要求源/目标编码器和解码器兼容，包括引用表达式的实际实现。相同协议族或相同 ref 名称不足以授权回放。不同定义间不能表达的扩展继续返回 `unsupported_native`，不能通过清空扩展取得成功。

`TestCacheAliasEditorAgentAndForwardingShareContract` 检查编辑器管理接口、Agent、预览、启用、JSON/SSE 和 SQLite 的一致性。这是离线服务层集成测试，不代表浏览器 E2E 或真实供应商别名已验证。

## 有限额的线上实验

测试入口：`node scripts/verify-protocol.mjs live --suite=cache-gaps`。按目标设置模型及凭据环境变量名；秘密值只传给子进程，避免粘贴到命令参数或文件。新账本最多 96 次、预留 4 次清理、两小时窗口，不修改旧 256 次账本。失败调用、预检和资源操作也计数。

恢复入口：`live --suite=cache-gaps --resume=<证据目录>`。检查点绑定模型、编译器和协议修订。已开始但结果不确定的付费请求不重放；已预热的 TTL 组继续按原计划时间观察。最终版本补验用 `live --suite=cache-followup --parent=<目录>`，等待父实验退出，共用原账本且最多消费一次 20 次预留额度。

Anthropic TTL 是读取后刷新的最短生命周期；安静等待超过 TTL 后仍命中，结论是未观察到失效。Gemini 显式资源按服务端 `expireTime` 检查，创建、查询、引用、到期和清理由显式启用的测试程序执行，只清理本轮资源。接口拒绝时停止该资源实验，不改为内联全文重试。

报告保存原始 usage、计费扩展、选定契约、客户端与 SQLite 快照、聚合统计、请求/响应摘要和实际时间。生产正文捕获默认设置未改变。完整后端/race、fuzz 与本地性能测试不消耗真实调用预算。
