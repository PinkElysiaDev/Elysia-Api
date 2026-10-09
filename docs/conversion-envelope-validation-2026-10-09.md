# Anthropic 输出外壳与 Responses 保存语义修复验收

基于 `a455eb0` 工作区实施，编译器语义版本为 `2.0.0-dev.23`。本地修复已验证，用户原环境及远端原故障仍待确认。没有修改远端配置。

## 实现结果

- Gemini、Chat、Responses 上游转 Anthropic 时，同一帧中已知的用量先提供给首帧。首帧、终止帧和最终 JSON 的必填计数缺失时，兼容策略只在客户端副本补零并记录 `anthropic_usage_envelope` 诊断；严格策略拒绝。计数晚到按快照替换，不重复相加。
- 响应 ID 在本轮内稳定。使用实际模块和映射决定默认策略，不因自定义名称、ID 或 family 相同跳过修复。合法原生帧继续保留，最终必填字段检查不能被原生回放、`after` 或线格式规则绕过。
- `responses_storage` 校验布尔／null 类型。原生 Responses 路径保留请求及上游结果；向不支持响应对象保存的已知目标转换时，false 允许无状态调用，true／null／缺省在兼容模式诊断降级、严格模式拒绝。发生降级的 JSON、`response.created` 和终止对象返回 `store:false`。
- `previous_response_id` 等上下文参数不随 `store` 一起清空。未实现映射、未知厂商扩展及生成约束仍保留原有拒绝边界。
- 内部计量及持久记录只接收原始上游用量。新增的 `placeholder` 来源仅用于客户端表达；上游契约拒绝将该来源冒充真实观测。
- 运行、预览和组合验证采用相同规则。严格策略对缺失计数／保存意图的条件拒绝标记为 `checks[].rejected`，不会被当作成功或能力证据；独立的正向请求、响应和完整流仍是启用条件。

## 验证结果

| 检查 | 结果与覆盖 |
|---|---|
| `go test ./... -count=1` | 全部通过；最终 server 包 260.365 秒 |
| `go vet ./...` | 通过 |
| 关键 `-race` | protocol、builtin、server 通过；覆盖策略、占位计数、流式关联、网关及持久恢复 |
| TypeScript | `tsc -p packages/webui/tsconfig.json --noEmit` 通过 |
| Playwright + 真实后端 | Chromium 7 项通过；包含两项规则的表单与预览、严格拒绝、只读预览、策略激活及并发冲突 |
| 两版 Anthropic SDK | 2.0.33、4.0.78 均完整消费真实网关 SSE；检查每个事件并禁止出现 `error`，覆盖首帧用量、晚到、缺失、真实零及部分计数 |
| 工具往返 | OpenAI SDK 7.30.1、Claude Code 2.1.295 与本地模拟 Gemini 完成工具第二轮；HTTP 回归覆盖关闭数据库、重建注册表后的无载体恢复 |
| 持久计量 | 网关测试结束后直接读取实际落库记录；缺失用量保持缺失，部分计数不被补齐，Gemini 思考明细完整保留 |
| `store` | 普通及流式、原生保留、四种合法形态与错误类型、严格及用户覆盖；非法输入和无法表达的上下文请求不调用上游 |
| 历史升级 | 历史 `dev.21` 二进制创建数据库，`dev.23` 接管；旧 Responses／Anthropic 自定义原修订和草稿保留并重验，预览继承新规则 |
| 十次重启 | 协议列表、修订报告、激活、草稿、配置原文及协议升级备份集合保持一致 |
| 构建 | Windows、Linux、macOS 各 amd64／arm64 共六个目标；Windows amd64 执行实际升级启动测试，其他五目标为编译验证 |

修正了原有模拟 Anthropic 响应中缺少必填消息字段和用量的正向夹具。新增明确的首帧用量、无状态请求正向样例；未批量翻转保护断言。`vendor.long`、`billing:null` 等扩展边界测试保持不变。

## 复现入口

后端目录：

```powershell
go test ./... -count=1
go vet ./...
go test ./protocol/... ./server -run 'TestEnvelope|TestDeliveryFrame|TestResponsesStorage|TestAnthropicUsageEnvelope|TestStrictEnvelope|TestFinalWirePolicy|TestProviderCannotMint|TestGatewayAnthropicEnvelopeAndClients|TestGatewayResponsesStorage' -count=1
```

真实 SDK 测试设置 `ELYSIA_TEST_ANTHROPIC_MODULES`，指向安装了 `@ai-sdk/anthropic@4.0.78` 和别名 `@ai-sdk/anthropic-v2@npm:@ai-sdk/anthropic@2.0.33` 的隔离目录。工具往返测试另用 `ELYSIA_TEST_OPENAI_MODULE`、`ELYSIA_TEST_CLAUDE_BIN`。未设置可选客户端路径时，不把基础 Go 测试当作真实 SDK 验收。

仓库根目录：

```powershell
node scripts/test-protocol-e2e.mjs
node scripts/build-standalone.mjs
node scripts/smoke-projection-upgrade.mjs <历史二进制> dist/standalone/elysia-api-windows-amd64.exe
```

关键并发检查在 Windows 使用 `scripts/verification/toolchain.mjs` 提供的固定 LLVM MinGW 工具链，设置 `CGO_ENABLED=1` 后运行相应测试的 `-race` 版本。

## 验收边界

旧版 Anthropic SDK 2.0.33 在首帧输入未知、尾帧已知为 3 的实验中仍展示 0；4.0.78 展示 3。这是已确认的客户端差异，不影响内部上游计量。未新增本地估算或整轮等待来掩盖差异。

Claude Code 使用隔离的 `--bare --restricted` 环境、Read 工具、短系统提示词，并关闭提示缓存／thinking；测试显式配置了 metadata／output_config 的具名兼容规则。此结果不代表其所有默认配置都已支持。

没有用户当前 macOS Cherry Studio 的版本与运行环境，也没有本次远端 Responses 请求的实际 `store` 值。因此尚未执行该 Cherry Studio 版本和真实供应商的复测，不能标记原环境故障已经关闭。网关成功只表示上游调用与下游格式检查完成，无法证明无回执的客户端已经成功消费。
