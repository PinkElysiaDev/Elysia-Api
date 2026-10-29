# 声明式缓存意图处置

此能力仅接入独立编译的 v3 包。四个现有预置、生产加载入口及编译器版本未改变，`historyRetention` 尚未加入核心。

目标 `encode_request` 可以声明 `scope: "cache_intents"` 的 `drop` 或 `default` effect。`path`、`target` 必须为空，不接受 aggregate layout。`when` 必须存在，使用当前 `CacheIntent` 作为 `input`／`root`；不能读取请求上下文。引用表达式在编译时解析，运行时不重新编译。

- `drop` 必须明确 `severity: "info"` 或 `"warning"`，并提供静态原因。兼容模式记录 `conversion_degraded / lossy_compatible`；严格模式拒绝。
- `default` 首批只接受 `default: null`。只有 kind 为 key、retention 或 breakpoint，且 value 为显式 null、TTL 缺失、没有 Resource 时，才可能命中其 `when`。匹配后记录 `conversion_normalized / preserved`。它不为缺失输入添加默认值，也不能用于证明任意目标默认行为。
- provider cache resource 或带 Resource 的 intent 不能通过本批处置省略。未知 kind 不能因宽泛条件被自动删除。具体非空 policy、TTL 和未知对象成员是否可省略，必须由作者条件明确限定。

编译器将目标声明生成小型不可变规则配置，包含目标身份、映射哈希和实际引用的表达式。它进入现有 `request-projection`（顺序 130），仍按默认、全局、协议对和绑定策略的同 ID 完整替换机制处理。禁用或替换规则有效；编码器只拒绝仍命中的未处置意图，不补做删除。显式编写的独立 `remove` 规则保持原行为。

处置只访问根请求、工具及节点的核心 CacheIntent，包括 children 和 reasoningContent。工具参数、工具结果与用户 JSON 中同名的 cache 字段不参与处置。候选使用独享容器，原请求与资源作用域不被修改。无缓存意图时不构造局部执行计划或中间 JSON。

Native 保真仍是单独边界。首批没有证明缓存字段可从同 wire 的原生快照删除；存在恢复风险的组合明确拒绝，不清空 Native，也不在最终校验前再次删除字段。

回归入口为 `backend/protocol/package_cache_projection_test.go`。其中原型条件完整限定 ephemeral policy、允许的 TTL 及 Resource 缺失；带额外字段或未知 TTL 的值保留到目标能力检查。测试覆盖 strict／compatible、禁用／替换、直调与 raw 编码、空提示、各核心 owner、资源、Native 和输入隔离。当前没有完整 HTTP 性能收益声明。
