# dev.52 四协议渠道审计与修复（2026-10-10）

**本地修复与产物验证通过，真实渠道审计尚未全绿，原故障不整体关闭。** 已确认的成功响应转换缺陷均有独立修复和回归；当前剩余失败及未验收范围见下文。

本轮使用用户更新的配置；四个入口均指向 moyuu，Chat/Responses 模型为 `gpt-6.1-sol`，Anthropic 为 `claude-haiku-4-5`，Gemini 为 `gemini-3.8-flash`。真实 R5 审计代码基线 `917604f`，最终代码 `d8738ba`，编译器同为 `2.0.0-dev.52`。两者差异仅为已失败流的原因保留和避免重复错误终止；没有改变请求、响应投影或成功路径。凭据、本地配置、原始调用记录及数据库不入库；只提交代码、合成回归和脱敏结论，不推送。

## 本轮缺陷及修复

| 根因 | 修复及边界 | 本地提交 |
|---|---|---|
| Responses `access_programs` 被当作未声明扩展 | 对已安装 SDK 明确定义的 `cyber` 枚举建立类型化元数据，原生保留，兼容投影诊断，严格拒绝实际损失 | `fad75ab` |
| `tool_usage` 独立计数阻断转换 | 枚举 web search 和 image generation 的八个计数，保存到原始用量明细，独立检查算术，不加入模型 token 总量；尾帧缺用量不覆盖已报告值 | `a1f893b` |
| Responses SSE 的 `obfuscation` 未识别 | 按具体增量事件识别填充，不把它当作响应顶层字段；兼容模式明确诊断转换后无法维持原始长度混淆，严格模式拒绝损失 | `bf6ff0e` |
| 消息关联注释被当作未知字段 | 精确识别 `metadata.turn_id` 和 `internal_chat_message_metadata_passthrough`，保留所属消息；未知键继续受扩展边界约束 | `02db86d` |
| SSE 填充进入内容收集器，终止快照比较失败 | 事件传输元数据不归入持久内容，原生增量仍保留其原字段 | `67d34fa` |
| OpenAI 已识别的 `server_error` 类型仍被判为未知扩展 | 接受该明确别名，使上游错误能按目标协议表达；不放行未知分类 | `4e4ca2d` |
| `usage.attribution` 拦截正常结果 | 明确 item/request field 计数桶结构，保留 int64 精度、快照合并和原始记录；不假设桶覆盖全部输入，不重复累计 | `784f48e` |
| 渠道的缓存计费说明阻断响应 | 精确识别 `linapi_cache_billing` 披露结构，分开保存观测值和渠道计费值；不覆写普通缓存计数或本地价格 | `0f46e50` |
| 关联注释还会附着于思考和工具输出项 | 扩展到这些明确项目，保存所属项和分数时间原文；JSON/SSE 使用同一识别及投影 | `467ca00` |
| 空思考输出到 Gemini 成为无数据 part | 转换动作把空可见思考规范化为显式空文本；最终格式检查拒绝空对象、仅 thought 标记及 null text | `25b03a3` |
| 服务档位快照从 auto 更新成 default 时，来源路径不同被误认为两份字段 | 响应及用量元数据以协议、所属层级、字段名识别同一快照；保留最新路径用于诊断。不同 choice 不合并，真正的目标字段冲突仍拒绝。原生 Responses 重建同样覆盖 | `59da0d3` |
| 空思考项转 Anthropic JSON 缺少 thinking 字符串 | 与 Gemini 共用具名规范化，明确输出空字符串，不虚构思考或供应商签名 | `79fd4b2` |
| 长等待中的 Responses keepalive 被误当作不支持的内容 | 仅识别 type/sequence_number 的无载荷心跳，继续验证序号；原生回放保留，跨协议不生成虚假内容。任何附加字段仍受未知扩展边界保护 | `917604f` |
| 上游已发过载错误，后续非法用量尾帧覆盖原原因并触发第二次错误发送 | 保留原始生成失败和尾帧拒绝两项原因，已发送错误后只更新 trailer，不再追加终止事件；仍拒绝未知字段。即使允许三次重试，也只调用一次上游 | `d8738ba` |

新增计量字段先进入原始 `ProtocolUsage`，随后仅对客户端副本投影；落库回归验证投影没有污染模型总量、工具用量及披露信息。未知字段、非法计数和未知嵌套扩展仍拒绝或要求显式规则。各语义修复递增编译器版本，沿用当前证据重验机制。

审计分类修复 `b9bb052` 区分 `upstream_contract_violation` 与转换错误：只有前者时标记上游契约失败，存在混合转换错误时仍标记网关转换失败。它只修正归因，不改变失败状态、SDK 断言或旧报告。旧 R4 报告继续保留当时的分类，复核摘要另行记录。

## 验证记录

| 检查 | 已完成结果 |
|---|---|
| 固定版本代码组 | `917604f` 的 Go 全量、vet、TypeScript、lint、前端构建 5/5 |
| 最终独立 Go 全量 | `d8738ba` 所有包通过，server 470.008 秒；独立 vet 通过 |
| 相关 race | dev.49 的 protocol、builtin、server 回归通过；dev.52 新增元数据快照、心跳、空思考、计量落库和失败尾帧回归通过 |
| dev.52 EventReplay fuzz | 20 秒目标，30,444 次执行，通过 |
| 浏览器 | Chromium/WebKit 20/20，流程连接隔离真实后端；部分历史展示用例使用 mock |
| 固定 SDK 两轮工具 | 32/32 场景、56 SDK 变体、112 模拟上游调用，核对第二轮实际出站工具关联和持久记录 |
| 六目标产物 | Windows/Linux/macOS 的 amd64/arm64 全部构建，SHA-256 位于 `dist/standalone/SHA256SUMS` |
| Windows 新库 | 实际产物十次启动，四预置加载、激活和草稿状态稳定 |
| Windows 旧库升级 | dev.33 二进制经管理 API 建库后升级到 dev.52，保留自定义修订和用户草稿；注入预置缺失、同哈希损坏、证据损坏后恢复，连续十次重启状态和备份稳定 |
| 真实四渠道直连基线 | 模型发现及 JSON/SSE 文本 12/12，通过时刻 14:49—14:51；不代表之后渠道持续可用 |

SDK 固定版本为 `openai 7.31.0`、`@anthropic-ai/sdk 0.132.1`、`@google/genai 2.28.0`、`@ai-sdk/openai 4.0.91`、`@ai-sdk/anthropic 4.0.78`。dev.52 脚本自测 **132 项通过，零跳过**，SDK 依赖实际执行。

探索轮 R1 基于 dev.39，421 通过/51 失败；R2 定向 Responses 基于 dev.41，31/44；R3 各组编译版本不同，419/53。这些结果保留为发现缺陷的证据，不作为 dev.52 验收。

dev.49 R4 在干净 `25b03a3` 上执行，保持并发 32、最大输出 32768、180000ms 请求超时及禁用自动重试。后续修改在独立工作区完成；最后一组已加载固定二进制后才合入源码，不改变其运行时版本。

R4 最终 **395 通过、76 失败、1 阻塞、零跳过**。其中协议矩阵 240/300；外层协议组另有 5 项启动和配置检查。7 个矩阵失败和 1 个 SDK 失败来自本轮末尾三处已修复的网关缺陷；其他失败包含 Cloudflare 524、上游长时间无数据、渠道声明模型不可用及模型未回复预期文本。SDK Anthropic→Chat 的文本断言失败经实际调用记录核对，上游和下游正文完全一致。Chat 持久化首次调用 524，对应重启用例依赖未满足，明确标为阻塞。

dev.52 对 R4 六份完整失败响应的脱敏结构及一份插入合法序号心跳的结构，经过真实隔离网关转换到四个客户端，共 **28/28**；这只证明结构转换。keepalive 后因旧网关中止而未取得完整供应商尾帧，所以使用完整捕获流插入心跳，明确不冒称原供应商完整流重放。

R4 后对三份 Chat 专项的实际出站请求进行直连，保持原字节并记录 SHA-256。混合工具 JSON 成功；混合工具 SSE 和思考片段 SSE 均在没有网关的情况下返回 `server_error`，消息为供应商服务器过载。结果位于 `results/dev49-chat-special-direct-replay/`。这证明重放时存在上游失败，不将历史超时自动改写为成功。

R5 在北京时间 15:32—16:09 执行，仍使用并发 32、最大输出 32768 tokens、180000ms 请求超时，不自动重试。六组均在干净 `917604f` 上启动；最终失败收尾修复在最后一组已加载测试二进制后合入，重启沿用同一二进制。

R5 六组最终结果为 **418 通过、54 失败、0 阻塞、0 跳过**。这些计数包含启动和路由配置检查，不等同于模型生成次数。

| 组 | 通过 | 失败 | 阻塞 | 跳过 |
|---|---:|---:|---:|---:|
| daily | 79 | 4 | 0 | 0 |
| protocol | 265 | 40 | 0 | 0 |
| sdk | 29 | 8 | 0 | 0 |
| errors | 19 | 2 | 0 | 0 |
| persistence | 21 | 0 | 0 | 0 |
| code | 5 | 0 | 0 | 0 |

SDK 组 8 个失败均为 524 或流空闲超时：6 个 Chat 上游请求、Responses→Anthropic JSON 和 Responses→Gemini SSE；没有发现新的 SDK 格式缺陷。不能将这些失败改记为通过。

异常组的两个失败也是 Chat 渠道超时：无效模型检查后的正常恢复调用返回 524，取消场景在收到可取消内容前发生空闲超时。持久化组四渠道的调用、重启、配置修改、禁用及令牌撤销检查全部通过。

R5 的协议矩阵共 **260/300 通过**，没有跳过。按上游分组：

| 上游 | 直连 | 经网关四客户端 JSON/SSE | 说明 |
|---|---|---|---|
| Chat | 9/15 | 33/60 | 524、长等待超时、过载及渠道自定义错误；部分错误后追加未声明用量扩展 |
| Responses | 9/15 | 59/60 | 网关唯一失败是原生 Responses 的 system JSON 请求返回 524；三个跨协议目标均 15/15 |
| Anthropic | 15/15 | 60/60 | 本轮矩阵范围全部通过 |
| Gemini | 15/15 | 60/60 | 本轮矩阵范围全部通过 |

本轮直连的 12 个失败全部返回 HTTP 524，Chat、Responses 各 6 个，部分响应明确说明服务器过载。这些直连请求绕过 Elysia；它们证明本轮上游也存在失败，不据此排除所有网关缺陷。

对全部 54 个失败逐项读取实际 HTTP 及持久记录，按 524、空闲超时、过载的优先顺序互斥归类：32 个 HTTP 524、14 个空闲超时、7 个明确过载，以及 1 个渠道 `upstream_error`。最后一项的实际上游帧仅包含 `error`，说明请求处理失败并建议重试，没有成功回答被网关误当作错误。过载流中仍存在非法尾帧，错误收尾修复只保留完整原因，不会使失败的供应商生成变为成功。脱敏逐项复核索引保存在 `.tmp-dev/dev52-remaining-failure-review.json`。

本轮没有放宽 Chat 渠道 `upstream_error` 未声明错误类型、错误后用量中的 `claude_cache_creation_*` 和重复 `input_tokens/output_tokens` 等未知扩展边界。已看到的相关记录本身已是上游生成失败；这些字段不能据此被当作成功生成的可用性证据。后续若要支持这种渠道扩展，应单独明确其计数含义与重复计数一致性，不能直接删除。

dev.52 本地证据位于 `.tmp-dev/dev52-final-go-all.log`、`dev52-final-vet.log`、`dev52-new-race.log`、`dev52-fuzz.log`、`dev52-browser.log`、`dev52-final-builds.log`、`dev52-final-delivery-smoke.log`、`dev52-final-upgrade-smoke.log`、`audit-dev52-sdk-tools.json`、`dev52-audit-selftests.log`、`dev52-structural-replay.json`。R4 完整报告位于忽略目录 `scripts/protocol-audit/results/refreshed-credentials-dev49-r4/`；R5 位于 `refreshed-credentials-dev52-r5/`。各自记录实际修订、绑定、策略、SDK、请求轮次和事件顺序，不能混算版本。原始业务数据不随文档提交。

## 原故障及运行证据

R5 的 Gemini→Anthropic JSON/SSE 用量专项实际观察到 `output.reasoning_tokens`，原始持久记录保留该明细；首尾帧结构检查通过。Responses `include` 缺失、null、空数组、加密思考选择，以及 `store` 缺失、null、true 的 JSON/SSE 专项均通过，非法类型按预期拒绝。`include` 规范化的 `conversion_normalized/info/preserved` 诊断实际进入调用记录。

同轮签名诊断必须分别解读：上述 JSON 样例有 `recoverable_wrapped`，SSE 样例记录 `lossy_compatible`；不能把后者宣称为完整签名恢复成功。实际供应商续传接受范围仍以对应多轮用例为准。

启动证据显示 `runtimeReady=true`，四个加载修订均匹配激活：

| 协议 | 实际修订 |
|---|---|
| anthropic-messages | `190cdfe534411a307ec85194a2d43cb9b278fa72f95d1bc7f51af3831183a6d1` |
| google-generate-content | `5a8568d57e9111e3279d8408ed4ee300f0d53323cf1ed44551f8b4a692c8e26d` |
| openai-chat-completions | `6d6cbe620976c7294b301008f3988c5d818eb955ac4f3fd7f56de8329379f16f` |
| openai-responses | `592c9315ba3d6ae8a54e2c36619108d3d0c1539338ac947799c7de8a2e9a01d9` |

原故障 SSE 的有效策略哈希：

- Anthropic 入口 / Gemini 上游：`5ed390950d045ca3322a3ae7f51ea2c688561f01fa4f3bb1027d5e35d5b569e1`。
- Responses 入口 / Gemini 上游：`b7431db96652e90a70f48b6b9c0858d7c9eee0ee651b24692f61493030761d7a`。

对应证据为 R5 的 `daily/evidence/0196-http.json`、`0197-call-record.json`、`0204-http.json`、`0205-call-record.json`，包含事件顺序及实际绑定；这些本地文件不提交。顶层 system/cache、Chat/Responses 中段系统指令上提、普通 reminder 文本及工具第二轮，Anthropic 和 Gemini 上游对应专项均通过。它们不证明 VS Code 插件实际操作已完成。

## 使用边界

实际 Claude Code VS Code 插件因用户明确推迟更新而未操作；用户确认未使用 Cherry Studio，不将其列为待用户复测的原客户端。真实脚本与 SDK 消费结果不替代插件交互。

特殊媒体、完整服务端会话恢复、摘要和带状态的交错思考等未经本轮验收的能力不扩称支持；脱敏签名结构回放也不等同于供应商验签。

最终六平台产物对应代码 `d8738ba`，后续文档提交不改变二进制。Windows amd64 文件为 `dist/standalone/elysia-api-windows-amd64.exe`，SHA-256 为 `9440a164e30767633f992b4d5571c7c0ad690e60fd4486a7aa1f3fb596c6f292`。全部平台的校验值见同目录 `SHA256SUMS`。下一次真实审计应在用户确认渠道恢复或调整配置后执行；本轮不通过重复全量请求来碰运气获取全绿结果。
