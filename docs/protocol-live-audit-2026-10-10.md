# 四协议真实行为审计与修复记录（2026-10-10）

后续 dev.27 修改及再次暂停真实测试的状态见 [dev.27 本地修复记录](protocol-audit-dev27-local-2026-10-10.md)。下文保留本轮当时的版本与证据，不能作为后续代码的验收结果。

本轮从 `c2ed84a` 开始，按独立根因提交本地，未推送。用户要求调整 API，因此不启动下一轮真实渠道测试。这里区分已执行的旧版真实测试、最终修复的本地回归和仍未完成的客户端验收。

## 实际执行范围

- 渠道：用户配置的 moyuu.cc。Chat、Responses 使用 `gpt-6.1-sol`，Anthropic 使用 `claude-haiku-4-5`，Gemini 使用 `gemini-3.8-flash`。
- 真实测试保持并发 **32**、最大输出 **32768 tokens**，禁用自动重试。未降低参数来规避超时。
- 完整审计包含 daily、protocol、sdk、errors、persistence、code 六组；另执行四渠道直连基线和一次 protocol 专项。
- 运行环境：Windows amd64、Go 1.26.5、Node 24.18.0。固定依赖为 OpenAI 7.31.0、Anthropic 0.132.1、Google GenAI 2.28.0、Vercel OpenAI 4.0.91、Vercel Anthropic 4.0.78。
- 凭据仅存在被忽略的本地配置中。临时网关使用独立数据库；导出脱敏调用证据后清理。
- 用户确认没有使用过 Cherry Studio，故不将 Cherry Studio 版本或复测作为本轮用户验收前提。

## 已有真实证据，不能当作最终代码验收

| 结果目录 | 启动网关代码 | 实际结果 |
|---|---|---|
| `direct-baseline-20261010` | 直连，无网关 | 四渠道发现、JSON/SSE 文本，12/12 通过 |
| `live-initial-20261010` | `7a0cc01` | protocol 矩阵 137 通过、123 失败；整轮未通过 |
| `protocol-fixed-20261010` | `bbb974e` | protocol 矩阵 180 通过、80 失败 |
| `live-fixed-20261010` | `f79ad66` | protocol 矩阵 206 通过、54 失败；六组汇总 339 通过、85 失败，整轮未通过 |

目录均在 `scripts/protocol-audit/results/`，未提交原始结果文件。protocol 矩阵每轮包含 260 项：四条直连路径和 16 条网关路径，各覆盖发现及 JSON/SSE 场景。上述数字不能解释为 16 个方向已全部通过。主报告中的 protocol 汇总行不重复计入总数。

最后一轮的代码检查发生在持续开发的工作区，捕获了新工具流回归的失败，不能将其归为启动二进制 `f79ad66` 的本地测试结果。最后代码另行执行本地回归，不改写历史失败报告。渠道请求在用户暂停下一轮真实测试前已发出，本轮后续只完成已有测试进程的本地检查。

在 `f79ad66` 的 daily 专项中，Gemini 上游到 Anthropic 的 JSON/SSE usage，以及 Responses 的 include 缺失/null/空/思考选择项、store 缺失/null/true 均通过；非法 include/store 的预期拒绝也通过。这是脚本的真实渠道证据，不能替代实际插件消费，更不代表其他 Responses 上游方向已修复。

该轮临时实例实际 `runtimeReady=true`，四个加载修订为：

| 固定预置 ID | 修订 SHA-256 |
|---|---|
| `openai-chat-completions` | `1b10698ed82f7bece36ad346e1e2345505e8f397123edc0c15b2f8160bf3a07f` |
| `openai-responses` | `635735cc85a4105fe70640ecb04b71266a796975b9151937ea9c2a8f2e1b2df1` |
| `anthropic-messages` | `1116d3558f6bb00fd340ddd208c527aaf4e67ff72ae66155960c714434997151` |
| `google-generate-content` | `5a8568d57e9111e3279d8408ed4ee300f0d53323cf1ed44551f8b4a692c8e26d` |

就绪证据为该轮 `evidence/0006-startup-runtime.json`。每条调用用 `X-Elysia-Request-Id` 关联持久记录；记录实际入口/上游修订、完整生效策略哈希、原始用量、转换诊断，以及 SDK 实际序列化的 include/store。无调用 ID 的请求明确标为不可关联，不按时间猜测。事件摘要仅包含事件类型、序号及索引。

## 根因与本地修复

| 根因 | 修复及保留的边界 | 本地提交 |
|---|---|---|
| 单 choice 的扩展映射写入 nil map | 初始化容器，避免元数据使正常响应 panic | `c5ba63a` |
| Responses 仅含状态/元数据的事件冒充 usage 更新 | 独立元数据事件；不制造空用量 | `1f242f6` |
| 已知空状态与活动上下文混淆 | 仅规范化确定的空形态，非空状态仍受保护 | `47967b0` |
| Anthropic thinking 明细未识别 | 映射 `output_tokens_details.thinking_tokens`，不再加到已包含思考的总输出上 | `eb61403` |
| Responses assistant 历史错误编码为 input_text | 改为 output_text，保留轮次和工具关联 | `488795e` |
| Chat 流在每段参数增量重复工具 ID/name | 在工具开始时发送身份，后续只发增量；同步修复旧生成样例，保留原生回放断言 | `c214b3d`、`a689ad9`、`d80096b` |
| Gemini 单项强制工具选择被当成未知配置 | 将 ANY+唯一 allowedFunctionName 规范化为指定工具；不扩展到多名称或 AUTO | `deedfc1` |
| Gemini 终止帧空 text 生成虚假内容项 | 仅忽略无附加状态的终止占位；签名及注解不丢弃 | `bbb974e` |
| Chat 缓存创建计数别名、choice 结束说明未识别 | 别名计数不累加、冲突报错；`native_finish_reason` 单独归属 choice，不覆盖标准结束原因 | `f0bd751`、`5e7ccd2` |
| 工具消息空正文与直接工具来源被误认为额外语义 | Chat 空正文不构造文本节点；Anthropic `caller.type=direct` 归为客户端函数调用，程序工具仍受保护 | `030e247`、`f79ad66` |
| 中段 system 上提后又对原始非法消息做原生重编码 | 发生结构变更时重建请求；合法顶层 system、普通 reminder 文本维持原语义 | `b625219` |
| Chat reasoning_content:null/空串成为虚假思考节点 | 不构造空思考；原生回放保留原字段；非空可见思考不能冒充 Responses summary | `6bf506b` |
| 工具 JSON 最终快照被压缩，误判为已经发送的内容缩短 | 完整 JSON 按 token 比较，允许空格/转义/精确数值等价写法；拒绝类型、值、重复键结构、顺序或不完整前缀改写；保留原字节用于续传 | `3c03bbd` |
| Gemini 只有输入、思考和总量，Chat 缺 completion_tokens | 仅在总量精确证明其他非负分量为零时推导总输出；持久记录区分 inferred 与 observed，不估算、不把未知补成零 | `9f13007` |
| 自动模型发现只用更新时间检测并发修改 | 事务内核对完整模型源配置，防止同一时钟刻度的旧刷新覆盖新密钥、停用状态或地址 | `316966a` |

最后一个问题来自已有记录：`promptTokenCount=17`、`thoughtsTokenCount=202`、`totalTokenCount=219`，无 `candidatesTokenCount`。归一化输出为 **202 inferred**，思考明细仍为 **202 observed**，总量仍为 **219 observed**。存在无法归属的正余量时不作该推导。流式支持总量晚到、重复快照及后来出现的真实候选输出覆盖推导值；JSON/SSE 网关测试均查询实际落库记录。

编译器语义版本为 `2.0.0-dev.26`，沿用原激活和绑定证据重验。没有将未知厂商字段全部放行，没有改写用户配置来掩盖失败，也没有恢复旧的错误执行分支。

模型发现并发缺陷由最后的全量测试发现，独立重复 20 次有 3 次复现。旧存储测试曾通过人为推进时间戳避开该情况；现改为强制保持相同时间戳，验证旧快照仍被拒绝。修复后原服务端用例连续 20 次通过，相关存储/网关测试重复三轮通过。该修复不修改协议语义版本，也没有操作用户正在调整的真实渠道配置。

## 系统消息与插件结论

人工样例确实暴露 A→A 路径的一个缺陷：语义上提已发生，原生重编码却仍使用未上提的中段 system。已增加实际预置回归并修复。兼容规则关闭或严格模式下仍拒绝实际范围变化；消息级缓存、资源等无法迁移时不清空字段绕过检查。

`<system-reminder>` 作为 user/assistant 文本原样保留，不从标签推断权限。合法 Anthropic 顶层 system 及合法缓存块不被上提规则改写。非标准 `messages[].role=system` 不被宣称为 Anthropic 标准，也不把合成夹具称为 Claude Code 线上流量。

已准备隔离插件取证工具 `scripts/protocol-audit/plugin.mjs`，包括原字节代理、trailer 转发、精确调用关联和退出前等待导出。登记安装版本为 **Claude Code 2.1.292**。实际窗口被 VS Code 更新锁阻止，用户要求先做其他测试；**未采集到插件调用，客户端验收未完成**。启动进程成功不能证明窗口已打开。本地代理自测通过也不能替代插件实测。

## 本地验证

先前全量曾通过；下一次发现上述模型发现并发缺陷，保留了失败日志。修复后在 `316966a` 上重新执行 `go test ./... -count=1`，所有包通过（server 用时 413.511 秒）。已完成：

- 审计自测 **84/84**，无跳过；全部 16 项 SDK 用例实际执行。固定依赖通过 `npm ci --ignore-scripts --prefix scripts/protocol-audit` 安装。
- `go vet ./...`；工具快照、空思考、原生样例保护、网关工具流、思考用量和模型发现并发的定向 race；事件回放 fuzz 15 秒通过。
- 前端类型检查、lint 和生产构建通过。
- Chromium/WebKit 共 **20** 项页面回归通过，未跳过。协议创建、预览、验证、启用、回滚和 Agent 草稿测试连接真实临时后端；历史页面其他用例为模拟 API，不能混称全部真实后端。
- Windows/Linux/macOS 的 amd64、arm64 六平台产物已构建，位于 `dist/standalone/`；交付前同步最终源码及文档快照。

本地最终日志在被忽略的 `.tmp-dev/`：`audit-verified-go-tests.log`、`audit-verified-vet.log`、`audit-final-race.log`、`audit-current-race.log`、`audit-refresh-race.log`、`audit-final-fuzz.log`、`audit-selftests-local.log`、`audit-final-browser.log`、`audit-delivery-builds.log`。这些是本地验证记录，不代表最终代码已完成真实渠道测试。

## 未关闭的问题及下一步

1. 真实渠道返回的 Responses `access_programs`、输出项 `phase`、`tool_usage`、`usage.attribution` 等仍阻断部分跨协议路径。涉及状态/计量，不能按字段名一律当成普通可丢元数据。需要逐个固定真实结构、核对含义及目标映射，保留严格模式和未知扩展保护。**完整真实矩阵尚未通过。**
2. 存在 provider 524、超时及 SDK 中断，保持失败，不算转换成功。部分渠道将 Gemini 错误包成 OpenAI 格式，错误转换可能遮挡原始原因；原始供应商响应仍在持久证据，后续需单独修复错误表达。
3. 工具 JSON 快照、空思考、中段 system 重建、精确思考用量推导均完成本地复现与回归，尚未用最终代码重跑真实渠道。
4. 旧审计脱敏会整体重新序列化 JSON，可能改空格或舍入大整数。`001d1c4` 改为敏感字段范围替换；旧日志不能证明原始字节。原始签名只在请求轮次内用于续传，脱敏签名不代表供应商验签通过。
5. 用户调整 API 期间，不启动新真实测试、不改变其配置。收到配置已就绪后再执行直连基线、受影响方向及完整六组；待 VS Code 更新恢复，再做隔离插件的普通对话、工具和第二轮验证。

交付状态：本轮列出的修复已提交本地，本地回归通过；真实渠道总体验收未通过，最终代码真实重测暂停，实际插件未验收。原故障未整体关闭。
