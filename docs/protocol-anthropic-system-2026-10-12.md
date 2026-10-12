# Anthropic 消息内 system 修复与验收

## 问题与依据

用户日志中的 Claude Code 版本线索为 `2.1.296.4d1`。请求同时含顶层 `system` 和第二条 `messages[]` 内的 `role:"system"`，后者带消息级 `output_config` 和内容块缓存。网关默认上提系统消息，在发送上游之前拒绝其元数据；编码器和最终角色检查也存在后续阻断。

此前将该角色称为“非标准 Anthropic 请求”的判断已撤回。已核对的官方 TypeScript SDK `0.132.1`、`0.133.0` 的 `MessageParam.role` 包含 `system`，同一源码中的旧说明却仍称不支持它。固定依据为 [官方 messages.ts，提交 2979ed5cef4b2f059b95af23e08728c01e588f86](https://github.com/anthropics/anthropic-sdk-typescript/blob/2979ed5cef4b2f059b95af23e08728c01e588f86/src/resources/messages/messages.ts)。类型声明和旧注释冲突不能成为继续误拒本次请求的理由；SDK 序列化通过也不能证明所有供应商渠道接受该结构。

## 修复边界

- 实际请求解码、编码均为 Anthropic 模块时，默认不启用系统指令上提。既有自定义 ID、旧模块定义、其他方向使用不同模块均不影响该判定。
- 利用节点原生来源区分顶层与消息内 system。保留容器、顺序、消息级配置、缓存及同协议扩展；模型名和正文的语义修改仍参与原生协调。没有改成直接返回原文的捷径。
- Anthropic 请求角色允许 user、assistant、system；响应仍只能为 assistant。最终输出校验继续运行，不接受其他角色或错误类型。
- 显式上提规则仍有效，跨协议未知字段、工具关联、签名和资源作用域的既有约束继续执行。`output_config` 不提升为全局参数，`<system-reminder>` 仍是普通文本。
- 上提拒绝归属 `conversion.request / encode_request`，包含目标协议、规则、策略哈希、修订及原始字段路径；预览和落库使用相同诊断，保留此前记录，不新增正文或字段值日志。
- 编译器语义版本提升为 `2.0.0-dev.65`，沿用自动重验，不改写用户定义、草稿和手动绑定。

## 脱敏与回归范围

`backend/protocol/builtin/testdata/claude-code-system-message.json` 是按原日志结构重建的合成夹具：正文、身份及工具业务内容均已替换，保留顶层与消息内系统指令、两处 `output_config`、thinking、context_management、缓存块、30 个工具及字段类型。测试不会读取用户日志或真实凭据。

| 层级 | 本地覆盖 |
| --- | --- |
| 编解码 | 完整夹具、兼容与严格模式、系统消息首中末位置及多条共存、模型路由改写、正文修改、大整数和未知同协议字段 |
| 自定义及策略 | 非预置 ID、旧定义、混合方向、显式上提、after 映射后的非法角色、响应角色不被放宽 |
| 证据 | 离线验证、实际预置组合验证、预览投影与真实网关一致 |
| 真实网关到模拟上游 | JSON/SSE、普通与并行同名不同 ID 工具第二轮；完整上游请求比较，仅允许模型名改写 |
| 失败诊断 | 跨协议元数据拒绝和严格作用域拒绝；零上游调用，原字段定位，预览与 SQLite 调用记录一致，早期诊断保留 |
| 审计脚本 | 原有同协议拒绝场景改为位置和字段保留检查；检测元数据缺失、消息移动及虚假上提；官方 SDK 序列化和完整消费 JSON/SSE |

## 执行记录

实施基线 `0f6e45e`，本地修复提交：

- `4029ecf`：同协议位置保真、请求角色契约、dev.65 和回归。
- `6313208`：请求阶段诊断、原路径、预览及落库一致性。
- `6c6215e`：审计脚本和 SDK 回归。

测试环境为 Windows amd64、Go `1.26.5`。已完成：

- `go test -json ./... -timeout 60m` 全量通过，服务端包耗时 1728.840 秒；包含子测试在内，2723 项通过、6 项按条件跳过、0 失败。覆盖四协议矩阵、启动恢复、连续十次重启、旧证据刷新及绑定/草稿保护。跳过项为两个真实模型入口、两个需单独指定隔离客户端安装路径的工具往返用例、一个需显式启用的负载测试，以及一个依赖 POSIX shell 的浏览器启动用例；均不作为已通过证据。
- `go vet ./...` 通过。
- `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` 通过。
- 协议及网关相关 `go test -race` 通过，Windows 使用 Zig 0.13.0 的 GNU 目标和 external linking。
- `npm.cmd test --prefix scripts/protocol-audit`：138 项通过，0 失败、0 跳过，包含实际执行的固定版本 SDK 测试。
- `node scripts/build-standalone.mjs` 通过：Windows、Linux、macOS 的 amd64/arm64 六目标；构建包含前端 TypeScript 检查及生产构建。macOS/Linux 仅为交叉编译，不代表原生运行验收。
- `node scripts/smoke-standalone.mjs` 通过：Windows amd64 产物在隔离新库启动，内嵌提交 `6c6215e`、编译器 `dev.65`，四个预置实际加载且运行时就绪，内嵌控制台可访问。该产物 SHA-256 为 `5dc2079644faed5193fa77893d4aef73e398791ac28ec07e5955d585f6efbf19`。

初次交付状态：**本地修复已验证，原环境待确认**。构建产物对应上述代码与测试提交；本文件及源码清单作为后续文档提交保存，不改变协议执行代码。

## 2026-10-12 真实渠道补验

用户追加授权后，于北京时间 17:58–18:02 使用 `config.local.json` 中的 `moyuu.cc / claude-haiku-4-5` 真实渠道执行专项。测试源码提交 `358ff82`、编译器 `dev.65`，使用隔离临时网关与数据库，关闭自动重试，沿用并发上限 32、单次最大输出 32768 tokens。仅发送合成内容；未发送用户原日志正文，未修改正式模型源、日常客户端或凭据配置。

本轮为 Anthropic 同协议专项，并未重跑四协议全部六组。共 34 次生成 HTTP 请求；返回历史在内存中原样用于续聊，落盘签名脱敏。工具第一轮未返回 thinking 块，因此这些用例不能证明真实思考签名在工具续传时被供应商接受。临时数据库在证据导出后删除。

| 测试 | 结果 |
| --- | --- |
| 默认绑定的 28 个用例 | 26 通过、2 个完整请求保真断言失败；两项失败也收到 HTTP 200，不能据此算保真成功 |
| 原 system 误拒结构 | 直连和网关 JSON/SSE 均通过；消息位置、消息级 `output_config`、内容块缓存均保留，无上提诊断 |
| 工具两轮 | 直连和网关 JSON/SSE 均完成实际工具调用与后续响应，保留真实返回历史；只回填合成工具结果，不执行任意模型指令 |
| 客户端 | `@anthropic-ai/sdk 0.132.1`、`@ai-sdk/anthropic 4.0.78` 标准 JSON/SSE 真实消费通过，流完整读取且无隐藏错误；另有官方 SDK 携带消息内 system 的请求通过 |
| 临时渠道能力覆盖对照 | 完整夹具 JSON/SSE 及官方 SDK 共 4 项通过，出站请求除模型名路由改写外完全一致 |

默认路径两项差异来自已有 `reasoning-controls`：内置能力将 `claude-haiku-4-5` 归为手动预算模型，将顶层 `thinking:{type:"adaptive"}` 投影为 `thinking:{type:"enabled",budget_tokens:16384}`，省略顶层 `output_config:{effort:"medium"}`。调用记录包含 `/reasoning/budget`、`/reasoning/effort` 的有损诊断；消息级 `output_config` 没有被删除。直连当前渠道接受原请求并返回 thinking/text，但这不证明供应商对 effort 的实际实现或所有同名模型都具有相同能力。

对照仅在另一临时实例中使用既有绑定 `reasoning` 覆盖：`kind:adaptive`、`levels:[medium]`、`defaultMode:auto`、`defaultEffort:medium`。它验证声明匹配时可以完整转发，不修改集中模型能力表，也不将原来的两项失败改记为通过。生产是否采用覆盖，仍应根据实际渠道能力决定；本次未变更协议执行代码。

两轮入口/上游修订均为 `feb70689b8723b8fa87ee43a8154871ec396ca0cee19475055124b6401c0f2b5`，有效策略哈希均为 `5da8e43fbc14be0819c10ddcaf291b71c045e251efe014249ec1316a063cd8a0`。第二轮另外记录完整能力覆盖及重新生成的绑定组合证据；策略哈希不包含该能力覆盖，不能单凭它判断两轮上下文相同。

本地脱敏报告及复现脚本位于以下被 Git 忽略的目录：

- `scripts/protocol-audit/results/anthropic-system-live-2026-10-12/`：默认绑定，`report.json`、HTTP、调用记录、事件顺序及运行时证据。
- `scripts/protocol-audit/results/anthropic-system-live-2026-10-12-capability/`：能力覆盖对照，包含相同类型的证据。

最终状态：**本地回归通过；原渠道的 system 专项通过；默认完整请求仍存在已记录的思考参数适配；原 Claude Code 客户端未复测。** 配置模型为 `claude-haiku-4-5`，原日志模型为 `claude-haiku-5-5`，不能把本轮结果写成原客户端与原模型故障已完全关闭。

## 2026-10-12 实际 VS Code 插件验收及新增缺陷

北京时间 19:12–19:38，使用已更新的 VS Code `1.141.0`（`2a59476c9bfcb90b3ddc372c36762471b7dfad1c`）和实际加载的 Claude Code 插件 `2.1.296`。出站 User-Agent 为 `claude-cli/2.1.296 (external, claude-vscode, agent-sdk/0.3.296)`。两轮使用独立配置、工作区、扩展目录和临时网关，目标为 `moyuu.cc / claude-haiku-4-5`，单次输出上限 32768，网关自动重试关闭。用户依次触发回复 OK、读取合成 `audit-note.txt`、沿用结果回复 marker；没有修改日常 VS Code 或正式模型配置。

首次测试（`a13e567`、dev.65）表面完成三步，但捕获到 7 个插件请求中的首个 HTTP 400：`thinking:{type:"adaptive",display:"updates"}` 被网关按稳定版枚举拒绝，日志还误标为 `decode_event / stream`。客户端随后移除 display 再发起请求，其余 6 个请求成功。因此这一轮记录为失败，不能用最终 UI 成功覆盖首次错误。

本地固定依赖 `@anthropic-ai/sdk 0.132.1` 的 `resources/beta/messages/messages.d.ts` 已声明 `BetaThinkingConfigAdaptive` 和 `BetaThinkingConfigEnabled` 的 `display` 包含 `updates`；Beta 名称为 `thinking-display-updates-2026-08-18`，稳定版类型尚不含此值。合成对照中，带相同字段的直连 JSON/SSE 均成功，网关均在调用上游前返回 400，确认是入口枚举缺项。

修复提交：

- `b9a319b`：识别 `updates`，同协议保留；跨协议沿用有诊断的投影，严格模式拒绝实际损失。编译器提升为 `2.0.0-dev.66`，通过已有机制重验；未知枚举和错误类型仍拒绝。
- `a624d37`：共享 display 校验，错误归属 `request.validation / decode_request`，保留 `/thinking/display` 及协议身份，增加零上游调用和 SQLite 诊断回归。此修复限定 display 校验，不宣称所有旧参数诊断已经整改。
- `5933722`：固定版本官方 Beta SDK 的 JSON/SSE 序列化和完整消费回归。

修复版临时网关基于 `a624d37`，未覆盖模型推理能力。实际插件第二轮捕获 **6 个请求全部 HTTP 200**（5 个用户对话/工具请求、1 个标题生成请求），每个网关重试计数为 0。首次及后续思考请求均保留 `display:"updates"`，没有发生先 400 再删字段的客户端回退。

该轮临时源 `plugin-audit` 绑定实际 `anthropic-messages` 预置，入口和上游修订为 `feb70689b8723b8fa87ee43a8154871ec396ca0cee19475055124b6401c0f2b5`，有效策略哈希为 `5da8e43fbc14be0819c10ddcaf291b71c045e251efe014249ec1316a063cd8a0`。首个成功插件请求为 `req_1791804891496005400_b18802b8`；完整绑定及组合证据随本地报告保存。修订和策略文本未改变，编译器 dev.66 的新语义通过证据版本区分。

| 检查 | 实际结果 |
| --- | --- |
| 用户界面 | OK、Glob/Read 成功、两次正确返回 `PLUGIN_AUDIT_7`，已核对实际窗口画面 |
| 消息容器 | 所有出站 `messages` 与顶层 `system` 均和入口一致，包括消息内 system、消息级 output_config、缓存块及工具历史 |
| 流契约 | 6 条插件流全部完整消费，首尾 usage、内容块生命周期、message_stop 合法，无 error 事件或落库失败 |
| 工具关联 | Glob、Read 的真实 tool_use ID 与下一轮 tool_result ID 对应，未按名称猜测关联 |
| 思考续传 | 工具和后续轮次带供应商实际返回的 thinking/signature，真实历史经原客户端回传且渠道成功响应；落盘签名脱敏，未用脱敏签名重新请求 |
| 模型能力 | 仍有已有预算/effort 适配诊断；同协议消息与 display 保留不等于默认所有顶层参数完全不变 |
| 真实合成对照 | 修复后 `summarized/updates × JSON/SSE × 直连/网关` 8 项通过，另有官方 Beta SDK JSON/SSE 2 项通过，SDK 自动重试关闭 |

签名结果只证明当前渠道接受本轮真实历史，不推断中转渠道内部一定执行了与原厂完全相同的签名验证。插件读取文件后曾用日语返回 marker；marker 内容正确，这属于生成内容表现，未计为协议转换失败。

本轮共记录 31 次入口生成请求：首次插件 7 次、修复前合成对照 8 次、修复后插件 6 次、修复后合成对照 8 次、修复后 Beta SDK 2 次。其中 3 次在网关本地拒绝，实际发起渠道生成 28 次；不包含前一节的 34 次 API 专项。所有临时网关在证据导出后停止，临时数据库已清理，隔离窗口保留供查看。52 份顶层 JSON 证据已扫描，配置中的供应商密钥匹配为零；本地配置、原始客户端资料和结果目录不提交 Git。

脱敏证据目录（Git 忽略）：

- `scripts/protocol-audit/results/claude-plugin-live-2026-10-12/`：首轮实际插件及修复前网关对照，保留失败报告。
- `scripts/protocol-audit/results/claude-display-live-2026-10-12/`：修复前直连/网关对照与本地验证日志。
- `scripts/protocol-audit/results/claude-plugin-fixed-2026-10-12/`：修复版实际插件、网关对照及 SDK 调用，含 `verified-summary.json` 和 `accepted-window.png`。
- `scripts/protocol-audit/results/claude-display-fixed-2026-10-12/`：修复后直连/网关、Beta SDK 报告及合成复现脚本。

本轮本地检查：Go `1.26.5` 下 `go test -json ./... -timeout 60m` **2740 项（含子测试）通过、6 项条件跳过、0 失败**，服务端包耗时 1779.463 秒。跳过范围与前一轮一致，不计为通过；本轮实际 VS Code 验收另行执行。协议包全量测试、关键网关回归、`go vet ./...`、固定版本 staticcheck 通过；相关 Windows race 通过；审计自测 **140 通过、0 跳过**。前端类型检查/构建和六平台产物构建完成，Vite 提示既有静态/动态导入共用模块的分包警告。Windows amd64 产物启动冒烟通过，四预置实际加载、运行时就绪，内嵌提交 `5933722`、编译器 dev.66，SHA-256 为 `10ad65abba74650e84602a08660ea184aebf33f0fca3d8c90a6681d3bc47ba4a`。其他平台只做交叉编译。

当前结论：**实际 Claude Code 的本次三步操作及新发现的 display 误拒已在配置渠道验证通过。** 验收模型仍为 `claude-haiku-4-5`，没有将原日志 `claude-haiku-5-5` 或未测试的全部 Beta 特性计为通过。
