# DSL 能力状态（dev.157）

当前默认、网关、Agent、探测、预览和验证入口使用 Spec／Mapping 包与通用 DSL。旧 codec 不再执行或作为测试 oracle。大自在天 core2 是中心，每份 Mapping 只连接自身 wire 与核心。包／DSL／核心版本为 4／2／2，转换策略版本为 2。

**第一阶段整体验收尚未完成。** 最新状态见[切换记录](protocol-three-part-cutover-2026-11-14.md)及逐项测试退役清单。[11 月 5 日性能记录](backend-optimization-2026-11-05.md)描述旧默认链仍在运行的历史状态，不能作为本轮切换通过证据。

| 能力 | 当前实现和证据 | 边界 |
|---|---|---|
| 四协议 JSON／SSE | 独立 Spec 和双向 Mapping；四包样例、16 方向组合、真实网关文本与工具回归 | 复杂组合仍需独立证明 |
| 计数与客户端显示 | Usage／ClientOutput，部分 TTL、思考计数、缺失／零及未知余项回归 | 原始计量独立，资源限制不提高 |
| 签名与加密续传 | 来源所有者、宿主认证、声明恢复；JSON／SSE 两轮工具回归 | digest、叶子顺序、篡改和禁用反例保留 |
| 历史与输出布局 | 目标调用通用分组／展开／拆分／包装 | 保留 reasoning／text／tool／text 顺序、父所有者和 Native 边界 |
| 来源开场规范化 | sourcePrelude 单帧有界暂存；原临时 ID 输入已恢复 | 固定 decoder／route，Spec、EOF／取消、原始帧和预算独立检查 |
| 请求、缓存、工具 | 核心参数／CacheIntent／ToolInput／ToolResult | 无 Native 重建、冲突、strict／禁用与作用域验证 |
| 元数据与失败 | 来源所有者、只读失败观察、最终 Spec | null／空 logprobs、错误信封及成功元数据分离、实际 HTTP 状态 |
| 目录及输入计数 | Spec 操作及 ModelPage／TokenCount 映射 | 不扩大未声明 API 或复杂组合 |
| 编写与管理 | 双源编辑、完整修订、不可变激活快照 | 隔离真实后端浏览器 22 项通过、无跳过；不迁移用户配置 |
| 组合证明 | 完整报告、独立能力子集、可见思考及传输限制 | JSON 专用证明不能授权流式，拒绝／跳过不算正向覆盖 |

通用 Go 宿主保留权限、路由、Scope、Seal/Open、I/O、计量、取消、重试和实际交付确认。供应商字段校验与转换位于其自身 JSON；新增执行能力必须有供应商无关的 DSL 合同。旧 Module、after、Codec 自动规则和专用 renderer 不作失败兜底。

protocol、builtin、relay 完整回归及浏览器已通过。服务端完整回归、其余 race、退役审查、发行构建和本轮性能证据仍在收口，不能用历史测试数量或旧性能结果宣称本轮完成。
