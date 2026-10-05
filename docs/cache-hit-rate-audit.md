# 缓存命中率审计报告（C36）

[用量契约](cache-usage-contracts.md) · [本轮实测](cache-validation-2026-10-04.md) · [字段与自定义协议指南](cache-usage-contracts.md) · [证据索引](protocol-release-evidence.json)

本报告合并方案（C28—C36）五轮交叉审验的全部发现，并附四家引用实现的逐行对比。结论只使用方案事实基线与 C36 记录，不新增外部调研。

## 结论：不是计数 bug，是请求侧能力缺口与响应侧硬失败的叠加

「经过转换的协议缓存命中率为 0」自 v1.5.0 起出现。经四个实证子代理实跑、三个源码探索代理、参考实现对比与官方文档核实，确认：

1. **请求侧（主因）**：四份内置定义的缓存能力**互不相交**——Anthropic 仅 `cache.breakpoints`、Chat/Responses 仅 `cache.keys`+`cache.retention`、Gemini 仅 `cache.resources`；叠加 `request.go` 的四种 `Kind` 各只接受唯一目标协议，且**全仓无断点合成逻辑**。Claude Code（Anthropic 协议，断点挂在 system/tools/block）指向非 Anthropic 上游时，断点被能力门 4xx 拒绝或改用无断点形式，**缓存永不创建，命中率恒为 0**。
2. **响应侧（叠加伤害）**：真实 Anthropic 响应必带 `cache_creation.ephemeral_5m/1h` 分桶，非 Anthropic 目标在 `checkUsageDetails` 按**键存在**即拒 → 非流式 502、流式 200 + `X-Elysia-Stream-Error` trailer。
3. **同协议路径正常**，因此 codex 的 71 次线上证据全部通过——原 bug 标题的口径与实际缺陷错位。

## 主张—复现方法—结论—处置

| 主张 | 复现方法 | 结论 | 处置 |
| --- | --- | --- | --- |
| 分桶跨协议必失败 | 探针实测显式 0、单键、非零三种形态 | 确证：非流式 502、流式 trailer | C28 省略投影并产 warning（`6aa14c5`） |
| 诊断非确定 | 200 次同输入统计 | 确证：`ephemeral_5m` 171 / `ephemeral_1h` 29，根因 `usage.go` map 遍历序 | C28 按键名排序遍历（`6aa14c5`） |
| 落库不受影响 | 比较「更新 usage」早于「编码」的语句顺序 | 确证：`protocolUsage` 记录完整含分桶 | 保留完整分桶，省略只作用于线制输出 |
| `unsupported` 可被捕获 | 追踪 `module.go` 的 `IssuesError`（wrapped `ConversionError`） | 确证：网关 `errors.As` 命中 | 成功路径也 drain 诊断通道 |
| Details 覆盖可达性 | wire 不可达；自定义定义 `after` 表达式实测可达 | 确证：`20→99` 可穿到 wire | C29 拒绝 `input.cached_tokens`/`input.cache_write_tokens` 保留键 |
| `encodeCache` 无门 | 逐调用点检查 | 确证：块级与 Chat/Responses tools 无 adapter 判断；顶层与 Gemini tools 有门 | C29 在 `encodeCache` 内统一补门 |
| 能力门是真防线 | 内置定义下的节点级断点请求 | 确证：被 4xx 拒绝且上游零调用 | 断点合成默认关闭；目标须声明 `cache.breakpoints` |
| `Kind` 是裸 string | 检查 `model.go` 与 `request.go` 的 encode switch | 确证：加值不被编译器拦截；switch 无 default（静默丢弃） | C30 补 default 分支 |
| `CapabilityCatalog()` 是唯一真相 | 检查 `catalog.go` 与 `compiler.go` 校验 | 确证：定义能力必须在目录内；无测试断言完整性 | 新增 `cache.options`/`cache.prewarm` |
| 请求/响应能力分离有模板 | 检查 `capabilityApplies()` | 确证：`cache.*` 走 `isRequest` 分支 | 新能力按方向归属请求侧 |
| 编译器版本是自动失效开关 | 检查 `protocol_refresh.go` 与 `protocol_upgrade.go` | 确证：要求报告 `CompilerVersion` 匹配 | 每次改既有映射即升 `CompilerVersion` |
| 引用实现均无 TTL 排序校验 | 逐行核实四家 | 确证：「1h 须在 5m 前」无任何一家实现 | C29 补排序校验，我方先行 |
| `cache_control` 跨族去向 | 实测 | Anthropic→OpenAI 丢弃即正确；Anthropic→Gemini/Responses 按省略丢弃；真正有价值方向是 OpenAI→Anthropic | 断点合成 opt-in（默认关闭） |
| 存储无 `ConversionIssues` 列 | 检查 `UsageLogItem` | 确证：仅 error 路径经 `errors.As` 赋值 | 成功路径诊断落 `record.ConversionIssues` |
| 覆盖索引与 rollup 对存量库无效 | 检查 `migrate.go` 的 `CREATE TABLE IF NOT EXISTS` | 确证：存量库不补列、索引不因列清单变化重建 | C32 显式 `ALTER TABLE` 并 `DROP`+`CREATE` 索引 |
| 前端有独立消费点 | 清点渲染文件与 `types.ts` | 确证：6 渲染文件 + `types.ts` + `utils.ts` | C33 三态展示（未报告/零/非零） |

## 勘误记录

- **我方三处**：
  - 节点级泄漏——曾以为 `cache_control` 只在顶层请求泄漏；实测块级与工具路径同样无门（`encodeCache` 无 adapter 判断），据此 C29 把门收进 `encodeCache`。
  - Anthropic 顶层字段——对 Anthropic 顶层缓存控制字段的早期描述有误，已在字段目录与预置中补齐。
  - SSE 别名全丢——曾概括为流式下原生别名全丢；实际是特定合并路径的别名处理缺陷，已按合并语义修正。
- **glm 四处**：`retention` 合并建议（`retention` 是最大保留、`options.ttl` 是最小生命周期，语义独立，不合并）、`definition_upgrade` 描述（改定义必须同时追加指纹并放版本快照）、`equalValues` 规范化（`exactNumber`/`big.Rat` 已实现，不需再加）、`readArray`/SSE 合并过度概括（`null` 语义各异、SSE 各段格式不同，不可一概合并）。
- **Astra 两处**：`retention` 未弃用（官方已标 Deprecated，但仍解码兼容）、`encodeCache` 定性。
- **两份共有**：「已批准省略分桶」误述——省略分桶是 C28 新增能力，不是既有的已批准行为。

## 业界对比附录（四家逐行核实）

| 实现 | 合成策略 | 闸门 / 预算 | 分级 |
| --- | --- | --- | --- |
| axonhub | 清空重建；最后 tool + 最后 system 块 + ≤2 消息锚点 | 闸门 `countCacheControls>0`，零断点不注入 | 自动合成 |
| cc-switch | 预算 `4-existing`；保留调用方标记，`>4` 仅告警；锚点 tool 尾→system 尾→最新可缓存消息→次新 user | 保留调用方标记 | 自动合成 |
| monoize | 六个 `cache_*` 变换，需规则列表显式启用（默认全局列表为空）；每个 ≤1 断点、合计 ≤4，不改调用方标记、不重排 TTL、不碰 tools | 显式规则启用 | 显式可选合成 |
| new-api | 仅回写调用方原文（`to_oai_chat_req.go`），无任何合成代码 | 无 | 不合成 |

**结论修正**：并非「三家合成、一家不合成」——真实分级为**自动合成 2 家（axonhub、cc-switch）、显式可选合成 1 家（monoize）、不合成 1 家（new-api）**。本项目「从不合成」仍属异常项，但对照基准是这三级，而非单一结论。

**TTL 排序**：引用实现均无混合 TTL 排序校验——axonhub 全仓零 `5m`/`1h` 匹配且不读 `TTL`；cc-switch 曾实现 1h 后 `revert`（`6eb217b2`）移除；monoize 仅不做重排（spec ACAA-8 要求运营方自选）。我方「1h 须在 5m 前」的校验属先行。

**撤销**：先前事实基线与本节对 new-api issue #5335 的引用**无据**——该仓库无 CHANGELOG、全仓零 `5335` 命中、无任何缓存丢失相关 TODO/注释，已从方案移除，本报告不再引用。

## 证据边界与开放缺口

- 四类证据分别记录：**代码保真**（离线测试）、**站点行为**（线上探针）、**真实非零命中**、**时间行为**（TTL）。
- 13 项证据缺口维持，见[证据索引](protocol-release-evidence.json) 的 `remainingEvidenceGaps`。
- **跨协议线上非零读取仍为独立验证缺口**——代码测试不能替代；本地回放不能证明真实站点在某账号/模型下发生非零的跨协议命中。
- 迁移回滚边界：还原旧引擎需匹配的旧程序与兼容数据库备份，不能用新验证报告证明旧引擎通过。
