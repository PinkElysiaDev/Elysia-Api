# Responses 规范化诊断与上下文边界验收

基于 ce52956 实施，编译器语义版本升级至 2.0.0-dev.24。未修改远端配置，也未新增完整响应历史存储。

## 行为

- include 跨协议分离为客户端输出偏好时，记录 conversion_normalized、info、preserved。这不代表载体已经生成；后续恢复或交付问题独立记录。缺失 include、原生未改变路径不制造降级。
- 诊断收集与调用日志使用一致的完整内容去重，同字段不同阶段、策略、修订及结果不会互相覆盖。
- truncation 和 previous_response_id 在入口及用户映射后的标准语义处校验。数字、对象、非法枚举和空字符串返回 invalid_input 及准确字段路径。
- responses_context 规则只将跨协议的 null 上一轮引用转为无引用；可关闭、可覆盖。原生保留原形态；关闭后目标编码器仍拒绝不支持的参数。
- 非空上一轮引用及未实现的截断约束继续拒绝。实际 Responses 预置未声明会话能力，原生引用也明确说明限制，不以打开 sessions 掩盖能力和验证缺口。
- 预置及旧自定义内置模块共用规则，不改写用户协议、草稿或手动配置。纯手写目标编码器不根据 family 继承删除行为。

## 验收记录

新增覆盖实际四预置、旧自定义 after 映射、类型边界、规则关闭、严格模式、组合证据、预览只读及诊断持久化。

| 检查 | 结果 |
|---|---|
| 协议完整测试与网关定向回归 | 通过；同时断言 include 规范化与后续载体降级实际落库 |
| Go 全量回归 | go test ./... -count=1 全部通过，server 包 390.893 秒；之后补充的空语义请求保护与保真分类断言通过定向回归 |
| go vet ./... | 通过 |
| 关键 -race | protocol、builtin、server 通过，含转换、网关、并发读取及草稿检查 |
| TypeScript | tsc --noEmit 通过 |
| Playwright + 真实后端 | conversion-policy.spec.ts 的 Chromium 4 项通过；覆盖 schema 表单、预览、禁用、严格拒绝和只读 |
| 历史二进制升级 | Windows amd64 的 dev.23→dev.24 通过；旧自定义原修订、未完成草稿保留，新证据自动生成且继承新规则 |
| 十次重启 | 协议列表、激活、当前报告、草稿、配置及备份集合保持一致 |

协议、网关和浏览器检查不调用真实供应商。浏览器首次新增测试把无诊断响应误期望为 []，按现有接口的 null 表示修正后重跑通过，没有改变接口空集合语义。

复现入口：后端执行 `go test ./... -count=1`、`go vet ./...`；根目录执行 `node scripts/test-protocol-e2e.mjs conversion-policy.spec.ts` 和 `node scripts/smoke-projection-upgrade.mjs <dev.23旧二进制> <dev.24新二进制>`。关键并发使用现有 verification/toolchain.mjs 的 Windows 工具链运行 `go test -race ./protocol/... ./server -run 'TestResponsesContext|TestResponsesIncludeProjection|TestDiagnosticDeduplication|TestGatewayResponsesContext|TestUsageRecordDiagnostic|TestGatewayIncludeCarrierDeliveryModes|TestProtocolConcurrentReaders|TestProtocolDraftConcurrent' -count=1`。

## 原环境待确认

尚无原环境的脱敏请求与事件序列、实际协议修订／绑定／策略、Cherry Studio 版本及供应商复测结果，不能宣称原故障关闭。构造回归不是远端原请求重放。

复测材料只保留相关字段的存在性、类型、允许公开的选项值、节点关联及帧顺序；提示词替换为最小合成文本，凭据不进入材料，签名内容以测试值替换。脱敏签名仅验证结构，不证明供应商接受真实签名。先以脱敏结构固化回归，再在原客户端与供应商执行最小请求及工具第二轮，并记录实际版本与结果。

后续完整自动截断及跨协议 previous_response_id 支持需要独立的上下文预算、历史生命周期、权限隔离及工具关联设计，不属于这次边界修复。
