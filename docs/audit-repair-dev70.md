# 2026-10-14 审计失败修复验收（dev.70）

本轮完成 HTTP 失败主因保护、限定 Responses 轮次状态适配、日志脱敏和审计分类修复。验证使用本地模拟上游与合成载荷，未调用真实收费渠道，也未推送。真实渠道兼容和原客户端验收仍待执行；本地 SDK 补充测试保留一项明确失败，不能将本轮写成所有客户端均通过。

## 实现与提交

基线为 `d8d67ae`，最终代码提交为 `c32c3d9`，编译器为 `2.0.0-dev.70`。后续仅追加本验收文档，工程检查对应同一个代码提交。

| 提交 | 内容 |
|---|---|
| `e3ee5f7` | 后端调用日志、审计对象／嵌套 JSON 字符串／SSE／截断片段的轮次状态脱敏，合成回归夹具 |
| `212bed4` | 网关与 Agent 共用非成功 HTTP 解码入口，保留实际状态和次级诊断 |
| `5de6622` | 专用轮次状态语义、限定转换规则、网关／收集器／预览／组合验证及 dev.70 |
| `406dfd1` | 审计主次分类、直连与兼容验收区分、序号诊断和恢复子用例 |
| `c32c3d9` | 修正源码快照清单，并对仓库实际清单增加边界验证 |

### HTTP 失败

- 先执行协议声明的错误映射；解码失败后以实际 HTTP 拒绝为主因，原解码问题保留在错误链及诊断中。网关和 Agent 使用相同入口。
- 仅在实际 HTTP 524、`cloudflare_error=true`、数值 `status=524`、`error_name=origin_response_timeout` 一致时，显示“上游源站响应超时（HTTP 524）”。冲突、HTML、空体及其他未知错误体使用安全的通用 HTTP 失败说明，不回显整个供应商响应。
- 自定义错误映射、供应商合法错误投影及既有重试策略保留；没有新增生成重试。读取错误体失败也不能将已观察到的 524 改成生成成功或覆盖为次级超时。
- 审计分别记录上游 HTTP、客户端 HTTP、调用逻辑状态、主因和次级问题，524 归为 `upstream_http_timeout`。重复 `HTTP 524:` 前缀被消除。

### Responses 轮次状态

- `ResponsesTurnState` 由 `metadata.updated` 承载，保留精确原始帧、来源、作用域及原帧序号。它不进入正文、用量、工具结果或思考签名，也不进入 Agent 的最终生成结果。
- 仅识别 `response.metadata`、唯一的大小写不敏感 `x-codex-turn-state` 头及可选非负整数序号。重复键／大小写别名、额外字段／头、空值、非字符串、控制字符和资源超限均拒绝。
- 控制帧可以在创建事件前或生成期间出现；不建立身份，不补序号，不生成正文。终止后到达、仅控制帧后 EOF、序号回退仍失败。取消不会伪造成功终止。
- 默认规则 `responses-turn-state`／动作 `responses_turn_state` 按实际内置编解码模块选择，覆盖复用模块的自定义协议。手写映射不因名称或 family 自动启用。
- 同协议原位置保留原帧；跨协议兼容模式限定省略并记录 `conversion_degraded / lossy_compatible`。严格模式或禁用规则后的跨协议交付继续拒绝。最终 after 映射校验、来源作用域保护继续执行。
- 诊断包含来源协议、`decode_event`、`conversion.event`、规则／策略 hash、原帧序号及 `/headers/x-codex-turn-state`，不包含状态值。
- 只修改日志副本，实际交付保持原值；脱敏先于日志截断，保留大整数。历史报告不改写。没有自动回填请求头、跨请求状态缓存或会话黏性，不能据此宣称 Codex 有状态续传已恢复。

### 审计结果解释

直连按供应商原生契约验收；网关仅对已知控制形态使用兼容验收，不能把扩展流的兼容成功改成供应商原生合规。非法／缺失序号不覆盖上一合法序号，下一条合法帧不再产生连锁误报。`wrong-key`、`unknown-model` 与各自恢复请求分别计数。

## 同一最终代码提交的工程检查

环境：Windows、Go 1.26.5、Node 24.18.0。日志目录为 `%TEMP%\elysia-audit-dev70\accepted\`，不纳入源码包。

| 检查 | 结果 |
|---|---|
| `go test -json ./... -count=1 -timeout=45m` | 通过；2928 项测试及子测试通过、6 项条件跳过、0 失败；server 包 1855.009 秒 |
| `go vet ./...` | 通过 |
| `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` | 通过 |
| 相关 race | protocol 全部子包和 server 的轮次状态、524、失败投影、收集器、Chat 启动、取消与关闭路径通过 |
| 前端类型／lint | 通过 |
| 前端生产构建 | 通过 |
| 审计全部自测 | 158 通过、0 跳过 |
| 源码快照边界 | 4 通过，包含实际仓库清单及合成敏感配置／越界路径测试 |
| 本地后端 Playwright | Chromium 16 通过；套件包含真实后端创建／验证／预览／激活／回滚和使用路由模拟的页面测试，不将全部用例描述为端到端真实服务调用 |
| 六目标构建 | Windows／Linux／macOS 的 amd64、arm64 均通过；产物在 `dist/standalone/` |

主要命令从仓库根目录执行，Go 命令从 `backend/` 执行：

```text
npm.cmd test --prefix scripts/protocol-audit
node node_modules/typescript/bin/tsc --noEmit -p packages/webui/tsconfig.json
npm.cmd run lint --workspace @root/webui
node --test scripts/source-snapshot.test.mjs
node scripts/build-standalone.mjs
node scripts/test-protocol-e2e.mjs protocol-v2.spec.ts conversion-policy.spec.ts protocol-history.spec.ts
```

race 使用本机 llvm-mingw、`CGO_ENABLED=1`、`GORACE=halt_on_error=1`，执行 `go test -race ./protocol/... ./server`，筛选 `ResponsesTurnState`、`GatewayResponsesTurnState`、`HTTP524`、`HTTPFailureKeeps`、`CustomHTTPFailures`、`GatewayUnmappedFailure`、`TurnStateRedaction`、`CollectorAppliesStartupConversion`、`GatewayChatStartup`、`EventReplay`、`ResponseCollector`、`AgentCallerRetryAndCancel`、`Close` 测试，超时 20 分钟。

第一次构建发现源码快照清单误加入不属于允许范围的审计脚本；已修正并重新执行最终检查。该轮被停止的 Go 全量进程不计入最终验收。保留 dev.69 的 736 项冻结差分摘要，没有重新生成期望值；新默认策略与旧策略的既有样本比较仅排除预期改变的策略身份字段。

Go 全量执行时间为北京时间 2026-10-14 19:26:28—19:57:25，进程退出码为 0。`go-test.jsonl` 的 SHA-256 为 `33a96984f25f6b83b147b3c7ad0086fe6d377b4c71e34874418ce1c9e4a9d53c`；各检查日志摘要另存于验收目录的 `verification-summary.json`。

Go 的条件跳过分别为 `TestCLILivePromptTasks`（未提供真实模型配置）、`TestConsoleBrowserEnvironmentOverride`（需要 POSIX shell）、`TestConversionClientToolRoundTrip/openai` 和 `/claude`（未提供独立客户端安装）、`TestProtocolLive`（未授权真实供应商）、`TestProtocolLoad`（未显式启用本地负载）。这些覆盖不能由普通单元测试替代。另有两个包无测试文件，不计入这六项条件跳过。

## 本地完整流与 SDK 补充验证

补充验证启动独立真实后端，四上游均为 localhost 模拟服务，并加载测试网络限制；没有真实凭据和业务请求。原始合成报告保留失败状态，以下仅划分本轮目标断言与独立问题。

| 场景 | 实际结果 |
|---|---|
| Responses 上游含首帧和流中轮次状态，四种客户端 × 文本／工具 | 8/8 完整消费通过；四个工具用例均发送第二轮并收到最终文本／用量 |
| 同一合成扩展流直连 | 两项保持 `provider_control_extension` 失败，符合原生合规与兼容成功分离原则 |
| 四上游 SDK 组（含准备） | 32 通过、1 失败；失败为 `sdk/synthetic-responses/responses-sse/openai` |
| wrong-key／unknown-model 与恢复 | 预期拒绝各自通过；模拟 524 的恢复请求各自失败，并归类 `upstream_http_timeout` |
| 整个补充协议组（含准备，去除矩阵汇总） | 39 通过、6 失败，不能宣布整组通过 |
| 整个补充错误组（含准备） | 25 通过、8 失败，包含刻意注入的 524 和瞬时完成模拟流不满足取消窗口 |

合成工具完成事件采用现有已支持结构：名称位于工具项目，`response.function_call_arguments.done` 不附加顶层 `name`。首次使用原 mock 时，该额外字段使三个跨协议工具路径在 `/name` 拒绝；不含轮次状态的对照也复现相同拒绝。未修改生产校验或原 mock 来掩盖它。

其余协议组失败来自原 mock 的 Chat 工具 `extra_content` 外部签名，以及 Anthropic→Gemini 累计思考前缀边界。不含控制帧的同结构对照仍有这些失败。本轮没有放宽这些字段及生命周期保护。

### 已确认的 SDK 限制

OpenAI SDK `7.31.0` 的 `responses.stream(...).finalResponse()` 对 `response.metadata` 抛出 `Unhandled response stream event: unknown`。独立直连对照共三次请求：无控制帧通过；加入带 SSE 事件名的控制帧失败；加入不带事件名的控制帧同样失败。SDK 自身的响应累积器未包含该事件，不是网关省略正文或本地审计断言误报。

Vercel Responses 和本轮跨协议 SDK 路径通过；不能将它们替代上述官方 SDK 的失败。按用户确定的同协议保留策略，不删除控制帧来使 SDK 报告变绿。实际 Codex／VS Code 客户端可能使用不同消费方式，仍需原客户端验证。

### 签名续传的独立边界

工具第二轮成功不等于所有跨协议签名均恢复。合成场景中 Chat／Anthropic 的第二轮仍出现 `continuation_unavailable`，理由为历史节点、位置及父级不再匹配。带控制帧与无控制帧逐项对照的状态、轮次数及续传告警一致；四条网关工具续聊中仅同协议请求包含原合成签名，不能宣称 4/4 签名续传通过。

本轮回归确认控制事件不会使既有收集器报“continuation stream collection unavailable”，Agent 结果不含状态；上面的独立续传问题保留为后续调查项，不以删除签名保护解决。

本地补充证据位于验收日志目录下的 `synthetic-confirmed/`、`synthetic-canonical-baseline/`、`synthetic-baseline/`、`synthetic-scope-comparison.json` 和 `sdk-control-direct.json`。临时补充脚本曾把所有工具继续执行误等同于所有签名恢复，其断言失败已保留；对照结果按上述实际范围记录，没有将该失败改写为完整验收通过。

对含控制帧的两次补充运行全部 JSON／Markdown 证据再检索，未发现合成轮次状态原值。SDK 失败及次级诊断仍保留在报告中。

## 完成边界与后续真实验收

- 本地实现与针对性回归：已完成；同一代码提交的工程检查通过，补充 SDK 与独立续传边界保留上述非通过记录。
- 真实扩展兼容：本轮未执行。后续先并发 1，再按原并发 32、32768 tokens 运行六组，实际捕获轮次状态并消费完整流／工具后续才能确认。
- 原客户端：未执行；官方 OpenAI SDK 原生 Responses 路径目前有明确限制。
- 平台原生：本轮未修改 Swift 行为。macOS 原生生命周期／下载／更新／回滚验收未执行，六目标交叉构建不替代它。
- 真实 Cloudflare 524、Responses `created_at` 增加 19 秒及后续身份变化仍是失败。没有时间容差、覆盖已建立身份、伪造成功终止或新增自动重试。
- dev.70 沿用既有协议和绑定证据重验；不新增端点／数据库表，不重写用户定义、草稿、绑定或手动能力。
