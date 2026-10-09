# dev.29 消息阶段修复与本地验收

接续 [dev.28](protocol-audit-dev28-local-2026-10-10.md)。遵照用户要求，本轮真实 API 测试保持暂停，没有读取或修改 `scripts/protocol-audit/config.local.json`，没有请求用户渠道。所有模型响应均为本地合成夹具，所有提交只在本地。

## 修复内容

| 提交 | 内容 |
|---|---|
| `6d1d683` | Responses 消息 `phase` 的 JSON、历史和 SSE 归属、晚到元数据、最终快照校验；编译器语义版本升至 `2.0.0-dev.29`。 |
| `edac203` | 实际四预置网关矩阵、固定 SDK 消费、旧自定义协议自动重验及十次刷新回归。 |
| `8cf42a4` | 修正两个旧 Responses 夹具中的首尾消息 ID 冲突和缺失内容结束事件，保留原有用量及取消断言。 |
| `508fc6b` | 增加网关实际输出的本地 SDK 回放命令；修正 Chat 正向样例缺失 assistant role 的问题。 |

`phase` 是消息字段，接受缺失、null、`commentary`、`final_answer`。同格式 JSON 重建和下一轮 assistant 历史保留其原形态。流式解析按输出索引记录消息所属阶段，内容结束后只保留文本摘要用于核对，待消息结束或完整终止快照到达后再结束语义项。终止快照不得改写已完成消息的阶段、正文或 ID。

目标 Responses 的阶段字段写回消息外壳，不写到 `output_text`，也不转移给相邻消息。空消息可以在 Responses 路径保留。收集器和往返比较继承消息阶段，不再因展开内容节点而忽略它。最终请求、响应及事件检查继续拦截非法阶段值。

其他协议通过既有 `response_metadata` 规则处理：null 的规范化记录信息级诊断；非空阶段无等价映射时，兼容模式记录损失、严格模式拒绝；关闭规则后编码器仍拒绝未经处理的字段。预置及相同模块的旧自定义协议共用该逻辑，包含请求历史和 `after` 映射后的语义。

生命周期修复改变了两个旧内置解码样例的预期事件时机。升级仅修正输入和预期指纹都精确匹配的旧样例，经完整验证后发布新修订；任意手写断言或自定义解码映射不猜测改写。原修订和用户草稿保留，十次无变化刷新不重复写入。

## 验收方法与结果

本轮先运行了全量回归，发现两个旧夹具把开始事件的 `msg1` 在终止快照改为 `item_0`，并省略内容结束事件。修正的是上游模拟结构，没有放宽生产校验，也没有修改用量、499 取消状态或错误类型断言。修正后定向回归、race 和全量回归分别执行，首轮失败日志保留。

另将 `TestGatewayAuditTextMatrix` 生成的 32 份实际网关输出送入固定 SDK。第一次发现 Chat 原生正向夹具整轮缺少角色，OpenAI SDK 报 `missing role for choice 0`。为正向输入补上标准 assistant role 后重新经过网关生成输出，再原样回放；没有修改捕获的响应来制造成功。

| 检查 | 本地结果 |
|---|---|
| Go 全量及 vet | 交付代码执行 `go test ./... -count=1`、`go vet ./...`；结果见下列交付日志。 |
| 重点 race | 消息阶段、组合证据、旧自定义重验、实际网关矩阵、用量及取消场景通过。 |
| 事件回放 fuzz | 15 秒通过。 |
| 审计自测 | 99/99，无跳过。 |
| 实际网关输出 SDK 回放 | 16 方向 × JSON/SSE，32/32；56 次固定 SDK 消费通过，流完整读取，检查隐藏 error。 |
| 浏览器 | Chromium/WebKit 20/20。协议工作流用例连接隔离真实后端，部分历史页面用例使用模拟 API。 |
| 前端 | lint 通过；类型检查与生产构建包含在六平台构建中。 |
| 交付构建 | Windows/Linux/macOS × amd64/arm64，使用 `scripts/build-standalone.mjs`。 |
| Windows 产物启动 | 隔离新库十次启动，检查当前引擎版本、四预置实际加载、runtimeReady 及激活/草稿稳定。新库冒烟不替代原环境升级验收。 |

可重复的本地回放命令见 [审计脚本说明](../scripts/protocol-audit/README.md#暂停真实渠道时的本地-sdk-回放)。该命令不读取真实配置，并阻止外部 fetch。固定依赖来自审计脚本 lockfile。

证据在被忽略的 `.tmp-dev/`，不提交原始运行日志：

- `audit-dev29-go-all.log`：首轮失败；`audit-dev29-go-final.log`：修正 Responses 夹具后的全量；`audit-dev29-go-delivery.log`：最终交付版本全量。
- `audit-dev29-vet-delivery.log`、`audit-dev29-race.log`、`audit-dev29-race-final.log`、`audit-dev29-fuzz.log`。
- `audit-dev29-phase-final.log`、`audit-dev29-upgrade.log`、`audit-dev29-selftests-final.log`、`audit-dev29-sdk-matrix.json`。
- `audit-dev29-browser.log`、`audit-dev29-lint.log`、`audit-dev29-delivery-builds.log`、`audit-dev29-delivery-smoke.json`。产物版本和实际提交号以最后一份冒烟记录为准。

## 仍未关闭的边界

- 原生帧保留原有消息边界。通用语义流重建仍可能把同一消息的多个内容项拆成多个输出消息；本轮验证了阶段随对应内容保留，不将其称作完整消息边界保真。
- Chat 正向夹具补角色不等于已经实现“任意上游整轮缺角色”的自动修复或完整拒绝检查。各帧字段检查仍不足以证明所有跨帧客户端约束，新增 SDK 回放对此提供独立防线。
- 空消息向其他目标、复杂多段思考、Responses `access_programs` / `tool_usage` / `usage.attribution`、完整历史恢复及尚未识别的供应商错误格式仍需各自的能力证据；不因普通文本通过扩大支持声明。
- 真实 16 方向工具多轮、供应商签名、原渠道及 Claude Code 插件验收仍未关闭。用户未使用 Cherry Studio，不把它作为用户验收前提。

本轮状态限于本地修复与验收。真实渠道继续暂停，待用户确认 API 调整完成后再恢复；现有配置文件和旧目标文本不构成恢复通知。
