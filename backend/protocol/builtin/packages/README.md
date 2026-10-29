# Anthropic 声明迁移草案

`anthropic.draft.json` 是未启用、未取得激活证据的 v3 Spec/Mapping 候选。
生产加载器仍只读取 `definitions/*.json`；本目录没有加入 embed 或加载流程。
文件保持真实 ProtocolPackage 格式，没有测试封套、隐藏开关或迁移拒绝兜底。

迁移基线是现有 dev.78 Anthropic codec，目标是保持当前接受行为、核心语义和原生保真。
使用现有样例和合成边界做差分；不读取私人请求，不调用供应商。
核心 schemaVersion 1→2 是声明格式迁移的显式差异，不是业务期望值修改。

## 已写入候选的内容

- 请求参数字段改名，顶层 system、消息、文本、图片、文档、工具定义和工具选择。
- 缓存意图的 value/TTL 分区，以及根 key、retention、options、prewarm 字段保存。
- thinking、签名、加密内容、工具结果的显式递归；只沿所选 content/children 数组遍历。
- `capture_native` 从原始 read 位置捕获消息、工具及内容块；不根据变换后的下标拼路径。
- 请求内容用 `with_index` 保存有序引用：原始 wire 键在容器转义前记入／查询，
  子数组构造前固定当前结果名称；未命中保持缺失，不扫描工具 arguments。
- 请求渲染的对应字段，以及响应主体、终止原因、精确 int64 usage 加减的初稿。

这些内容尚不能组成一个完整替代协议。不得把文件存在、编译通过或部分样例通过写成迁移完成。

## 未闭合的边界

| 范围 | 当前阻塞 |
|---|---|
| 请求解码 | 完整 reasoning 控制规范化与 interleaved 可信上下文 |
| 请求渲染 | server/opaque 原生节点回放；带标注及原生 inline system 的位置规则；独立 assistant 历史分组 |
| 节点保护 | metadata/citations、scope、结构化拒绝及告警；多缓存、多资源和未映射核心字段的逐 owner 拒绝 |
| 扩展 | 递归冲突合并、跨族扩展拒绝及稳定诊断；媒体 data URI 的无转码解析 |
| 响应 | 错误包络、metadata、完整未知 finish reason 与 fallback；输出分组、完整 usage 扩展与反向校验 |
| 原生保真 | 移除根 Native 后的节点回放；原生保存禁用时独立的来源证据；映射变更、scope 和修订矩阵 |
| 声明证据 | 动态 owner 的 emit/aggregate 证明尚未完成；现有效果声明不代表完整字段覆盖 |
| 其他能力 | SSE、模型发现、WebSocket、任务和 Agent 能力没有在本草案中迁移 |

当前没有加入 mapping.samples 激活证据。迁移测试直接使用现有定义中的请求/响应样例，
分别记录已闭合与未闭合结果；不通过修改原样例期望值掩盖差异。

## 当前差分记录

`TestAnthropicPackageDraftMigration` 直接编译并执行候选文件，无测试分支或供应商回调。
30 个既有 JSON 样例中，29 个完整比较相等；另有 12 个边界场景，8 个相等。
比较包含所有公开核心字段、Native、输出字节及完整诊断对象；仅显式适配核心 schemaVersion。

工具历史和数组形式工具结果的 name 差异已闭合。新增原始 `"<"` 与 `"\u003c"`
引用键差分，确认没有在容器规范化后错误匹配；递归内容和 Native 仍完整比较。

以下 5 个场景仍未闭合，测试打印 `NOT CLOSED`，不是跳过：

- `hosted-tool-encode`：尚无可信 server-tool 回放，当前明确拒绝。
- 非法 caller、非法 is_error：两边均拒绝，但阶段／归因等诊断尚未等价。
- thinking 控制：仍保留 legacy 参数桶，尚未生成完整的规范 ReasoningIntent。
- 移除根 Native 后的 inline system：候选错误地提到顶层，原生位置规则待迁移。

台账检查通过只表示没有出现未登记差异；**不表示这 5 个场景通过了行为等价验收**。
任何新增差异都会失败，已登记差异消失也要求显式更新台账。未运行本草案的性能验收。

后续先完成请求方向的关联、保护和原生边界，再补全响应及其 wire contract。
四方向及原有能力完成回归、证据和性能验收后，才考虑接入生产预置加载。
