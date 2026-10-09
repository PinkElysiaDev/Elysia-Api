# dev.33 SDK 工具两轮转换本地验收（2026-10-10）

本轮从 `b25d168` 开始，协议编译器提升至 `2.0.0-dev.33`。按用户最新要求，真实 API 测试继续暂停，没有读取或修改 `scripts/protocol-audit/config.local.json`。以下结果来自固定版本 SDK、实际隔离网关、回环模拟上游和合成历史，不代表原渠道或插件验收完成。

## 验收缺口与两个产品根因

已有 Go 工具矩阵主要由网关自己的解码器读取结果，SDK 回放则主要覆盖普通文本。因此，“转换成功且工具参数正确”仍可能漏掉 SDK 汇总响应及生成第二轮历史时才出现的错误。本轮增加实际 SDK 的两轮矩阵，模拟上游独立检查第二轮收到的调用 ID、参数和结果关联，避免仅用同一套转换代码证明自身正确。

### Chat 工具索引被普通内容占用

Chat 编码器此前使用通用内容位置生成 `delta.tool_calls[].index`。正文、思考或拒绝节点也占用这个位置，使两个工具可能取得索引 1、3，SDK 建立的 `tool_calls` 数组出现空洞。经校正测试工具后，修复前矩阵为 29/32：Responses、Anthropic、Gemini 上游返回给 Chat 流式客户端的三个场景均在 OpenAI SDK 汇总时失败，报 `Cannot read properties of undefined (reading 'id')`。

现在只由工具调用推进 Chat 的工具计数器，普通内容不消耗工具索引；工具参数分片仍按原调用关联。最终线格式检查同时拒绝缺失、越界或负数索引，并在成功结束时检查工具索引集合从零连续。允许索引 1 先于索引 0 到达，只要最终集合完整，不按分片到达顺序猜测调用关联。该检查沿用共享的最终输出检查位置，不仅保护默认编码器，也检查原生回放和用户映射后的帧。

回归覆盖工具前的正文／思考／拒绝、两个工具之间的正文、逆序完成参数，以及最终仍有空洞时拒绝。其他协议的输出项目顺序不因此改变。

### `refusal:null` 被误解为实际拒绝内容

索引修复后，SDK 得以进入第二轮，进一步暴露标准 Chat 历史中的 `refusal:null` 问题。原解码器只检查字段是否存在，因而凭空创建拒绝节点；Chat 客户端向 Anthropic、Gemini 上游发送第二轮时分别触发“不支持 refusal 内容”和“不支持此内容类型”。

现在缺失或 null 表示没有拒绝内容；非 null 必须是字符串，其他类型返回定位到该字段的 `invalid_input`。真实字符串拒绝内容（包括空字符串）保持原有能力检查，不能被静默删除或改称正文。同协议快照仍保留 null 的字段存在性，修改其他正文后也不会丢掉该原始字段。

新增测试覆盖四目标的兼容／严格模式、JSON 响应及请求原生保留、非法类型和实际拒绝内容保护。没有全局放宽未知扩展或内容类型比较。

## 审计工具修正与新增覆盖

新增 `scripts/protocol-audit/local-tools.mjs` 和 `src/sdk-tools.mjs`。本地矩阵编译当前后端，创建隔离数据库，通过管理 API 建立四个仅指向回环地址的模型源，运行 16 个方向的 JSON/SSE 两轮并行工具对话。工具名称相同、调用 ID 分别为 c1/c2，参数分别为 `{n:1}`、`{n:2}`；夹具包含正文与工具混合、逆序完成参数。模拟上游按 ID 检查第二轮完整历史和工具结果，逐次查询持久调用记录并保存修订、策略及诊断证据。配置关闭重试，实际调用次数也必须精确匹配。

最初测试工具将 SDK 的内部解析辅助字段当作线格式历史发回网关，产生了额外失败。它们不能计作产品缺陷：

- Responses 使用固定版本 SDK 自带的 `toResponseInputItems` 构造历史，并断言没有省略输出项。
- Chat 仅去除 SDK 添加的 `parsed` / `parsed_arguments` 辅助属性，保留实际消息字段。
- 没有修改网关去无条件吞掉未知扩展，也没有放宽 SDK 错误断言。

该矩阵使用 `openai 7.31.0`、`@anthropic-ai/sdk 0.132.1`、`@google/genai 2.28.0`、`@ai-sdk/openai 4.0.91`、`@ai-sdk/anthropic 4.0.78`。Vercel 路径完整消费流并检查隐藏错误及工具参数增量，不以看到 finish 作为唯一成功依据。

本轮 SDK 用例采用安全整数参数；不能据此声称 JavaScript SDK 保留任意大整数。已有 Go 工具回归继续单独检查大整数原文。合成夹具及新增 12 项脚本自测纳入版本库，本地配置、捕获和产物保持忽略。

## 本地验证

| 检查 | 结果 |
|---|---|
| `go test ./... -count=1` | 全部通过，server 包 458.380 秒 |
| `go vet ./...` | 通过 |
| 相关 race | builtin 6.754 秒、server 22.677 秒，通过 |
| EventReplay fuzz | 15 秒目标运行通过，43,925 次执行 |
| 审计脚本自测 | 123/123，0 跳过 |
| 实际本地网关 SDK 工具两轮矩阵 | 32/32 场景、56 个 SDK 变体、112 次模拟上游调用，通过；持久记录均成功 |
| Chromium / WebKit | 20/20；协议工作流连接隔离真实后端，部分历史 UI 用例使用接口 mock |
| 前端 lint | 通过 |
| TypeScript、前端构建及六平台产物 | 通过；Windows / Linux / macOS，各 amd64 / arm64，来自 `e4a4936` |
| Windows amd64 产物十次启动 | 通过；四个预置就绪，加载修订、激活及草稿稳定，0 次上游调用 |

关键证据保留在忽略目录 `.tmp-dev/`：

- `audit-tool-index-before.log`、`audit-null-refusal-before.log`：产品缺陷的修复前单元复现。
- `audit-sdk-tools-confirmed.json`：校正测试历史构造后的修复前矩阵，29/32。
- `audit-sdk-tools-fixed.json`：索引修复后暴露 null refusal 的矩阵，30/32。
- `audit-dev33-sdk-tools.json`：两项修复后的最终 SDK 工具矩阵，32/32。
- `audit-dev33-selftests.log`、`audit-dev33-go-all.log`、`audit-dev33-vet.log`、`audit-dev33-race.log`、`audit-dev33-fuzz.log`。
- `audit-dev33-browser.log`、`audit-dev33-lint.log`、`audit-dev33-builds.log`。
- `audit-dev33-delivery-smoke.log`、`audit-dev33-delivery-smoke.json`：实际 Windows 产物版本及十次启动结果。

早期 `audit-sdk-tools-before.json`、`audit-sdk-tools-after.json` 含上述测试工具自身的问题，不作为产品失败数量或最终通过证据。

## 本地提交与边界

1. `189dc34`：Chat 工具独立连续索引、最终检查、回归及 dev.33。
2. `c7af1be`：正确处理 Chat nullable refusal，保留真实拒绝和原生字段存在性。
3. `e4a4936`：实际本地网关 SDK 两轮工具矩阵、合成夹具、脚本自测及用法。

六平台产物位于 `dist/standalone/`，来自代码提交 `e4a4936`；实际 Windows 产物确认编译器为 `2.0.0-dev.33`。本报告随后单独提交；所有提交仅在本地，不推送。编译器版本变化通过既有机制触发证据重验，本轮无需改写预置定义、用户草稿或手动能力。

本轮不重复声称 dev.32 的普通文本／异常回放为 dev.33 新执行结果。未执行真实渠道、供应商签名续传、用户原数据库升级或 Claude Code 插件交互；Windows 十次启动检查使用隔离新库，也不能替代旧库升级验收。用户已说明没有使用 Cherry Studio，Claude Code VS Code 测试仍因更新锁延后。

交付状态：本轮本地修复已验证。真实渠道继续等待用户调整 API 后明确通知；四协议整体真实渠道及原环境验收仍未完成。
