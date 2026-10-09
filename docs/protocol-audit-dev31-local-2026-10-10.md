# dev.31 异常流修复与本地验收（2026-10-10）

本轮从 `abdb800` 开始，协议编译器提升为 `2.0.0-dev.31`。真实 API 测试仍按用户要求暂停；没有读取或修改 `scripts/protocol-audit/config.local.json`。本报告只记录模拟上游、实际本地网关及固定 SDK 回放，不宣称原渠道或原客户端问题已关闭。

## 已复现并修复

### 原生 Responses 回放后，本地错误事件从序号 0 重新开始

原生 `EventFrame` 保留路径跳过语义编码器，目标编码器不知道之前已经输出的序号。前一帧序号为 49 时，本地 `operation.failed` / `operation.cancelled` 仍生成 0，最终线格式校验拒绝发送该错误帧。

共享协议层增加原生帧观察接口，只同步编码状态；Responses 保存最近的序号，不重建正文或未知扩展。语义编码及原生回放使用同一个请求内编码器状态，后续错误事件生成 50。非零起点和序号间隔不能被帧计数取代。纯原生路径不会触发语义编码器的 finalizer，因此不多发终止事件。

回归覆盖实际预置、使用同一模块的自定义 ID、失败与取消、请求状态隔离、正常原生完成及整数上限。序号耗尽时明确拒绝生成，不回绕为负数或 0；错误投递失败保留原始原因，并继续使用 trailer。

### Responses 错误外壳混入语义错误

原解码器仅删除顶层 `type`，把 `sequence_number` 和嵌套 `error` 一并当成未知错误扩展；平铺 `message` 还会被通用用量扫描误当成 Anthropic 消息对象。最小测试确认：合法错误消息进入公共模型后消失，跨协议不能正确表达。

现在分别识别嵌套和平铺错误，只将错误载荷传入错误语义模型。帧序号仍属于帧状态，消息用量扫描限定到 Anthropic。入口及最终 Responses 错误帧检查消息、类型和可空字段；同时出现嵌套和平铺载荷时明确拒绝歧义。未识别的 `vendor`、`billing:null` 等扩展继续保留来源边界，不能被跨协议静默删除。

回归验证错误原因可以通过语义转换表达给四个目标，原生错误 JSON 保持原文。

### 上游已发送错误事件，调用记录仍可能成功

`EventReplay.Finish()` 表示事件序列具有合法终止，并不等于生成成功。原网关仅检查序列是否结束，没有把独立的 `operation.failed` / `operation.cancelled` 作为失败结果记录。

网关现在分别处理事件完整性和生成结果：完整消费并验证已收到的错误及用量尾帧，然后记录失败；不再追加第二个错误事件或合成成功标记。错误前已经发出的 HTTP 200 不修改为 500，失败通过 SSE、trailer 和持久调用记录表达。测试将最大重试数设为 3，仍要求实际上游调用数为 1。

## 验证记录

| 检查 | 本轮结果 |
|---|---|
| `go test ./... -count=1` | 全部通过；server 包 455.501 秒 |
| `go vet ./...` | 通过 |
| 协议及网关关键 race | 通过；包括原生序号、错误外壳、迟发错误及 16 方向文本矩阵 |
| EventReplay fuzz | 15 秒目标运行通过；38,627 次执行 |
| 审计脚本自测 | 109/109 通过，0 跳过 |
| 实际网关输出的正常 SDK 回放 | 32/32，56 次 SDK 消费通过 |
| 实际网关输出的异常 SDK 回放 | 4/4，8 次 SDK 消费通过 |
| Chromium / WebKit | 20/20；协议工作流连接隔离后端，部分历史 UI 用例仍使用接口 mock |
| 前端 lint、TypeScript、构建 | 通过 |
| 六目标构建 | Windows / Linux / macOS，各 amd64 / arm64，通过 |
| Windows amd64 产物启动 | 隔离新库连续 10 次启动；四个预置加载、运行时就绪、激活和草稿稳定；0 次上游调用 |

正常 SDK 回放继续检查完整正文与流，包含 Responses 多内容块；异常回放单独检查截断、非法终止快照、上游嵌套错误及平铺错误。固定版本 `openai 7.31.0`、`@ai-sdk/openai 4.0.91` 均须保留错误前正文、报告一次包含原始原因的错误，并且只请求一次。成功 EOF、无关 SDK 格式异常不能算作错误投递成功。

脚本用法见 `scripts/protocol-audit/README.md`，新增 `replay-errors.mjs` 只允许本地回放。原始合成捕获及日志留在被忽略的 `.tmp-dev/`：

- `audit-dev31-go-all.log`、`audit-dev31-vet.log`、`audit-dev31-race.log`、`audit-dev31-fuzz.log`
- `audit-dev31-gateway-failure.log`、`audit-dev31-provider-failure-before.log`
- `audit-dev31-selftests.log`、`audit-dev31-sdk-matrix.json`、`audit-dev31-sdk-errors.json`
- `audit-dev31-browser.log`、`audit-dev31-lint.log`、`audit-dev31-builds.log`
- `audit-dev31-delivery-smoke.json`、`audit-dev31-wire/`

## 本地提交及产物

1. `75b49ad`：共享原生回放序号状态及回归，编译器 dev.31。
2. `51597d4`：Responses 错误外壳解码、最终检查及边界回归。
3. `358b978`：网关失败结果、错误投递诊断及实际 HTTP 回归。
4. `9a59c4f`：异常流双 SDK 回放、自测与工具用法。

六平台产物来自代码提交 `9a59c4f`，位于 `dist/standalone/`；本报告随后单独提交。所有提交仅在本地，没有推送。

## 尚未完成的验收

- 等用户明确通知 API 调整完成后，再恢复真实渠道审计。
- 本轮未执行原渠道重放、供应商真实签名续传、原数据库升级或 Claude Code 插件交互。新库十次启动不能替代原数据库升级验收。
- 用户已说明没有使用过 Cherry Studio，不能要求或声称完成其 Cherry Studio 验收。Claude Code VS Code 测试仍受用户更新锁影响。
- 普通 Chat 原生流完全缺失 assistant role 的终局检查、复杂思考跨协议投影、尚未声明的供应商扩展及完整 Responses 历史恢复仍属于后续范围。

交付状态：本轮本地修复已验证；四协议整体真实渠道及原环境验收仍未完成。
