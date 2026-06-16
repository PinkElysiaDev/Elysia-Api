# Elysia-API 代码审查报告

- **日期**：2026-06-15
- **范围**：`backend/`（Go：relay / server / storage / config）与 `packages/webui/`（React + TypeScript）
- **方法**：分子系统逐文件审查，结论按严重度排序。每条给出位置、影响与修复建议。
- **代码量**：后端约 13.9k 行（不含测试）+ 前端约 20 个页面/组件。

## 修复进度（2026-06-16 更新）

全部 **Critical + High** 项已修复并补回归测试；顺带修了相关的 Medium（M1/M2/M3、S5/S7）：

| 条目 | 状态 | 修复要点 |
|---|---|---|
| C1 Responses 无故障转移 | ✅ | `responses()` 改用 `buildCandidates`+尝试循环，两处空组返回 500；新增 `TestResponsesFailoverToHealthyModel`/`...EmptyGroupReturns500` |
| H1 失败请求占用 token 配额 | ✅ | release 闭包统一退还预留，`adjustTokenUsage` 只加实际值；新增 `TestAcquireRateLimitRefundsReservationOnRelease` |
| H2 SSRF DNS rebinding | ✅ | 新增 `relay/secure_dial.go`：`net.Dialer.Control` 连接时校验实际 IP；新增 `secure_dial_test.go` |
| H3 保留网段不全 | ✅ | `IsPrivateOrRestrictedIP` 补 TEST-NET/基准段/240.0.0.0/4 |
| H4 异步入队浅拷贝 | ✅ | `enqueueUsageRecord` 深拷贝切片字段、置空 downstream 指针 |
| H5 退出不冲刷 usage | ✅ | `shutdown` 在 `httpServer.Shutdown` 后 `stopUsageWriter()`+`healthChecker.shutdown()` |
| M1 429 不记 usage | ✅ | `chatCompletions` 限流路径补 `recordUsage` |
| M2 重试计数 off-by-one | ✅ | `RetryCount = attempt`（0 次重试=首次） |
| M3 午夜重置清零 Active | ✅ | 日期翻转只重置 Requests/Tokens，不动 Active |
| R1 Chat→Responses 丢工具调用 | ✅ | 跟踪 function_call 项，补 `output_item.added`/`arguments.done`/纳入 `completed.output`；新增 `...EmitsFunctionCallItem` |
| R2 Claude→OpenAI 流丢 usage | ✅ | 从 `message_start`/`message_delta` 取 usage，`[DONE]` 前补 usage chunk |
| R3 Claude/Gemini 不检查状态码 | ✅ | Responses 流路径对 Claude/Gemini 非 200 走 `connFail`（可故障转移） |
| R4 多模态图片丢失 | ✅ | canonical 解析/渲染补 image（Claude source / Gemini inlineData/fileData / OpenAI data URI）；新增 `multimodal_test.go` |
| S1/S2 加密密钥同目录/路径不一致 | ✅ | `GetDBEncryptionKey` 改用 `SecretKeyPath`，保留 `.db-key` 兼容回退，同目录时告警 |
| S3 `GetGroups/GetTokens` 返回内部切片 | ✅ | 返回副本，消除锁外别名 |
| S5 `DeleteSource` 非事务 | ✅ | 包进事务 |
| S7 cacheHitRate 可能 >1 | ✅ | 钳到 `[0,1]` |
| W1 多选嵌套交互元素 | ✅ | trigger 改 `div[role=combobox]`，clear 为同级 `<button>`，补 ARIA/键盘（W3） |

未处理项（按优先级保留在下文）：R5–R13 的剩余 Medium/Low、M4（纯工具流空响应误判，与 R1 改动有交集需再评估）、W2/W4–W10、各 Low/风格项。

## 摘要

整体工程质量较高：路由热路径有内存缓存、SSE 头时机与 `Transfer-Encoding` 冲突处理得当、SQL 全部走参数占位符（无注入）、面板令牌用常量时间比较、SQLite 单连接下的回填死锁有正确规避。本次新增的「同源透传」也已落地并通过测试。

但存在若干**值得优先处理**的问题，集中在四类：

1. **可靠性不一致**：`/v1/responses` 入口无故障转移、空模型组会误报 403（C1）。
2. **计量正确性**：失败请求永久占用每日 token 配额（H1）；`/chat/completions` 的 429 不记 usage（M1）；优雅退出不冲刷 usage 队列，重启即丢账（H5）。
3. **安全**：SSRF 校验存在 DNS rebinding TOCTOU 窗口（H2）；at-rest 加密的密钥默认与数据库同目录，威胁模型基本落空（S1/S2）。
4. **转换保真度**：经 unified/流式路径时，工具调用、多模态图片、usage token 在多个方向被丢弃（R1–R4）。新增的「同源透传」恰好绕开了其中一部分，但跨协议转换仍受影响。

下面按子系统展开。严重度：**Critical** 影响核心功能/数据正确性、**High** 显著影响可靠性或安全、**Medium** 局部正确性/可观测性、**Low** 次要/风格。

---

## 0. 本次新增：三种 API 的同源透传（已实现并验证）

针对需求「完善 responses 之外另三种 API 的透传」，已实现：

- `relay.PassthroughBody`（`backend/relay/canonical_convert.go`）：以原始请求字节为基底，仅改写 `model`、按需补 `stream`/`stream_options.include_usage`，其余字段原样保留，把 Responses 的零转换透传推广到 chat_completions / claude / gemini。
- `relay.FormatMatchesPlatform`（`backend/relay/converter.go`）：判定输入格式与上游线路 API 是否同源（Claude↔Anthropic、Gemini↔Gemini、OpenAI/DeepSeek↔OpenAI/DeepSeek/Azure）。
- `chatCompletions`（`backend/server/server.go`）：同源且未触发 vision 过滤时走透传，否则回退转换；用 `RelayConfig.Passthrough`（默认开）可关闭。
- 测试：`relay/passthrough_test.go` + `server/passthrough_test.go`，覆盖未知字段保真、model 改写、stream 处理、关闭后回退转换。

**审查结论（来自 server 审查 M4）**：vision 过滤的 `!filtered` 守卫正确——被过滤的图片只改了 `unifiedReq` 而非原始字节，透传会把图片漏给上游，因此过滤后必须回退转换。Gemini 不改写体内 `model`（model 在 URL）也正确。**小问题**：`record.RelayMode` 只在 body 构造成功后设置，构造失败的记录看不到 passthrough/transform 标记——建议在构造前按 `usePassthrough` 预设。

---

## 1. server 包

### Critical

**C1. `/v1/responses` 无故障转移，且空模型组误报 403**
`server/responses.go:82`、`server/server.go:1211-1217`
`responses()` 用 `selectModel(group)` 只取单个候选；若 `buildCandidates` 为空则返回零值 `ModelRef{}`，随后 `validateOutboundBaseURL("")` 以 "missing host" 返回 403，掩盖真实原因（组内无可用模型）。更关键的是：与 `chatCompletions` 不同，Responses 路径**完全没有故障转移**——首个模型 5xx/连接错误即直接返回客户端，无视 `MaxRetries` 与多候选。
- **影响**：两大入口可靠性不一致；瞬时上游抖动在 `/chat/completions` 能恢复，在 `/responses` 直接失败；空组误报 403。
- **修复**：先判空（仿 `server.go:636` 返回 500「no available models」）；理想做法是让 Responses 复用 `buildCandidates` + 尝试循环。

### High

**H1. 失败请求永久占用每日 token 配额**
`server/server.go:657-662`、`1292-1340`
请求开始 `acquireRateLimit` 预留 `estimatedTokens` 到每日 `state.Tokens`。成功路径用 `adjustTokenUsage` 把差额补到实际值；但 `defer releaseLimiter(estimatedTokens)` 传入的是 estimate，`+= estimated - estimated = 0`（空操作）。当请求**整体失败**（不 commit、不 adjust）时，预留的 `estimatedTokens` 从未被回退——失败请求永久消耗每日 token 预算。
- **影响**：`DailyLimitMaxTokens` 单调虚高；大量失败可在零成功 token 的情况下耗尽配额。
- **修复**：把「预留→实际」的对账统一到一处。让 `releaseLimiter` 在未记录实际用量时减去原预留（失败传 0），并移除 `adjustTokenUsage` 对 `Tokens` 的二次修改。

**H2. SSRF 校验存在 DNS rebinding TOCTOU**
`server/server.go:1361-1399`
`validateOutboundBaseURL` 先 `net.LookupIP` 校验解析到的 IP，但真正发起请求时 adapter 会**再次解析**域名。攻击者控制 DNS 即可在校验时返回公网 IP、连接时返回 `127.0.0.1`/`169.254.169.254`（经典 rebinding），且校验通过的 IP 未与拨号器绑定。
- **影响**：尽管有 SSRF 检查，仍可打到云元数据端点/内网服务。这是主要防线却可被绕过。
- **修复**：解析一次并钉死——用自定义 `DialContext`（`net.Dialer.Control` 回调）在**连接时**校验实际目标 IP，拒绝私网/保留地址；或直连预校验过的 IP 并保留 Host 头。

**H3. SSRF 保留网段清单不完整**
`server/server.go:1401-1441`
手写的 `isPrivateOrRestrictedIP` 依赖 `IsPrivate()` 覆盖 RFC1918，但 `192.0.2.0/24`、`198.18.0.0/15`（基准测试段）等未显式处理；IPv4-mapped IPv6 经 `To4()` 多数被 `IsLoopback`/`IsPrivate` 覆盖但边界依赖实现细节。
- **修复**：配合 H2 的连接时校验后此项大多自然消解；如保留静态清单，建议用成熟 bogon 列表并统一走 `To16()`。

**H4. 异步 usage 入队为浅拷贝，切片/指针跨 goroutine 别名**
`server/usage_writer.go:44`
`copied := *record` 是浅拷贝，`RetryEvents`/`ConversionChain`/`RequestWarnings` 切片与 `*downstreamCaptureWriter` 指针仍与原记录共享底层。当前未在入队后改写这些切片，但 streaming defer 顺序微妙，一旦引入「入队后 append」即触发数据竞争。
- **修复**：入队时深拷贝切片字段，并把 `copied.downstream` 置 nil（其内容已在 `recordUsage` 物化）；或显式约束「`recordUsage` 后不得再碰 `record`」。

**H5. 优雅退出不冲刷 usage 队列、不停健康检查——重启即丢账 + goroutine 泄漏**
`server/server.go:1600-1611`、`usage_writer.go:28`、`health_checker.go:67`
`stopUsageWriter` 与 `healthChecker.shutdown` 仅在测试里被调用。`/__shutdown` 只 `httpServer.Shutdown(ctx)`，不 drain `usageQueue`（最多缓冲 1024 条）也不停健康检查 goroutine。
- **影响**：每次优雅重启（Koishi 重启流程依赖 `/__shutdown`）静默丢失缓冲中的 usage/计费记录；goroutine 泄漏至进程退出。
- **修复**：`shutdown` 在 `httpServer.Shutdown` 后调用 `s.stopUsageWriter()` 与 `s.healthChecker.shutdown()`。

### Medium

**M1. `/chat/completions` 限流 429 不记 usage**
`server/server.go:657-661`
`acquireRateLimit` 出错时直接 `c.JSON(429)` 返回，**未** `recordUsage`，也未设状态码/时间戳。其他早返回（403/404/500）都记了；Responses 路径的 429 也记了（`responses.go:122-130`）。
- **影响**：限流事件（往往最值得关注）在主入口的 usage 日志/统计中完全不可见。
- **修复**：设 `record.StatusCode=429`/`Error`/时间戳并 `s.recordUsage(record)` 后再返回。

**M2. `appendRetryEvent` 把首次尝试也算作重试（off-by-one）**
`server/relay_retry.go:26`
`RetryCount = len(RetryEvents) + 1` 后再 append，首个失败尝试就得到 `RetryCount=1`，把初次尝试计成重试。
- **修复**：append 后取 `RetryCount = len(RetryEvents)`，或只统计 attempt>0。

**M3. 每日重置把 `Active` 并发计数清零，跨午夜可绕过 `MaxConcurrency`**
`server/server.go:1309-1323`
日期翻转时 `state.Active = 0`。跨午夜仍在途的请求其 `releaseLimiter` 会对刚清零的计数器 `--`（有 `>0` 保护不会变负），导致并发计数错乱，午夜窗口内 `MaxConcurrency` 可被突破。
- **修复**：日期翻转只重置 `Requests`/`Tokens`，不要动 `Active`（它跟踪在途请求而非每日配额）。

**M4. 流式「空响应」判定会误伤纯工具调用流**
`server/server.go:1108-1116`、`responses.go:413`
`streamYieldedNothing` 在「无输出文本且无 usage token」时把 200 改判 502。只发 tool_calls/function_call delta、且上游省略 usage chunk 的合法流会被误判为「空响应」降级为 502，尽管客户端已收到有效 200。
- **修复**：把捕获到的 tool-call/function-call delta 也视为非空（检查事件而非仅 `responseText`），或仅在「零条 SSE data 行」时判空。

### Low / 风格

- **L1** `extractAccessToken` 支持 `?key=` 查询参数传 key（`server.go:444`），易随访问日志泄漏；建议仅限 `/v1beta` Gemini 兼容并文档化。
- **L2** `redactJSON`（`usage.go:195`）仅按 key 名脱敏，上游错误体里值内嵌的 `Bearer sk-...` 不会被脱敏；建议对捕获的上游 body 增加值级 `sk-`/`Bearer` 清洗。
- **L3** 健康检查每轮每模型新建 `&http.Client{}`（`health_checker.go`），无连接复用，且共享 H2 的 SSRF 风险。
- **L4** 非 store 回退路径在持锁 `usageMu` 时做同步文件 IO（`usage_persist.go`），高负载下串行化所有请求；store 路径已异步，回退路径未跟进。
- **L5** `RequestID = fmt.Sprintf("req_%d", UnixNano())`（`usage.go:168`）在 Windows 粗粒度计时器下同纳秒可能撞号；建议加随机后缀或原子计数。
- **L6** `loopbackOnly` 仅看 `RemoteAddr`（`server.go:508`）；若日后置于反代之后，`/__reload`、`/__shutdown` 会对所有人可达。建议文档化「不得前置反代」或额外要求 dashboard token。

**确认正确**：SSE 头延后到上游 200、不手设 `Transfer-Encoding`（避免 TE+Content-Length 冲突）；`roundRobinIndex` 有锁保护且 `% modelCount` 钳制消除越界 panic；dashboard token 常量时间比较。

<!-- PLACEHOLDER_RELAY -->

---

## 2. relay 包（格式转换 / 流式 / 适配器）

> 注意：本次新增的「同源透传」对**同协议**方向（Claude→Claude 等）绕开了下述多数转换缺陷；但**跨协议**转换（如 Claude 客户端 → OpenAI 上游）仍走 unified/流式路径，以下问题依然成立。

### Critical / High

**R1. Chat→Responses 流式转换丢失工具调用**
`relay/responses_stream.go:229-298`
该转换发出 `response.function_call_arguments.delta`，但从不发 `response.output_item.added`（function_call 项）、不发 `function_call_arguments.done`，且终态 `output_item.done`/`response.completed` 的 `output` 里**没有** function_call 项；`output_index` 还被硬编码为 `1`。
- **影响**：最常见的网关方向（Chat→Responses，codex 用）工具调用静默损坏——客户端收到指向「从未声明的项」的参数 delta，且完成响应里没有 function_call。
- **修复**：按 index 跟踪工具调用项，首见时发 `output_item.added`（含 `call_id`/`name`），累积参数后发 `function_call_arguments.done`，并把 function_call 项纳入 `output_item.done`/`completed.output`，`output_index` 递增。

**R2. Claude→OpenAI 流式转换丢失 usage token**
`relay/canonical.go:1086-1093`
`message_delta` 分支读了 `stop_reason` 却忽略 `event["usage"]`（Claude 在此带 `output_tokens`、`message_start` 带 `input_tokens`）。产出的 OpenAI chunk 从无 `usage`，导致每个 Claude→OpenAI 流式请求计费为零 token。
- **修复**：从 `message_start.message.usage` 取 `input_tokens`、从 `message_delta.usage` 取 `output_tokens`，在末尾发一个带 `usage` 的 `chat.completion.chunk`。

**R3. Claude/Gemini 适配器不检查上游状态码**
`relay/claude.go:37-53`、`relay/gemini.go:37-59`
两者返回原始 `*http.Response` 而不看 `StatusCode`（OpenAI adapter 则检查了）。上游 4xx/5xx 时 body 是 JSON 错误而非 SSE，流式转换器扫不到 `data:` 行，于是发出一个伪造的空内容流（带捏造的 `message_start`/`STOP`），错误被吞、无法故障转移。
- **影响**：上游错误变成「静默的空成功响应」，无故障转移、无状态码传播。
- **修复**：两个 adapter 检查 `resp.StatusCode != 200`，读+关 body，返回携带状态码的错误（仿 OpenAI adapter）。

**R4. 多模态图片在多数转换中被丢弃**
`relay/canonical_convert.go:515-516`、`786-791`、`824-837`；`relay/canonical.go:398-401`
`parseClaudeMessages` 把 Claude `image` 块存为 `Raw` 却不抽取 URL/base64/media_type；`canonicalMessagesToClaude`/`ToGemini` 的 switch 不含 image case，图片整段丢弃；`contentPartsToInterface` 仅在 `ImageURL != ""` 时输出图片，base64/file 图无输出。
- **影响**：任何经 canonical 模型路由的 vision 请求都会丢图。
- **修复**：解析时填充图片字段，并在各 `canonicalMessagesTo*` 中补 image/file case（Claude `source`、Gemini `inlineData`/`fileData`）。

### Medium

**R5. `ForwardOpenAIStream` 破坏 SSE 分帧** — `relay/openai.go:522-523`：对每行追加 `\n\n`，把多行事件拆成多个畸形事件。应忠实转发 `line + "\n"`，仅在原本的空行处加分隔（仿 `ForwardStreamRaw`）。

**R6. `ForwardStreamResponse` 丢弃非 `data:` 的 SSE 字段** — `relay/openai.go:494-512`：只转发 `data:` 行，丢掉 `event:`/`id:`/`retry:`。应逐行原样转发。

**R7. `prompt_cache_retention` 类型断言恒失败** — `relay/canonical_convert.go:79`：对 `map[string]any` 值断言 `.(json.RawMessage)` 永远失败，retention 静默丢失。应 `json.Marshal` 该值再塞入。

**R8. `GeminiToUnified` 丢弃同消息内先出现的部件与所有工具部件** — `relay/converter.go:407-419`：文本部件处 `contentParts = nil` 会清空已累积的 `executableCode`；`functionCall`/`functionResponse` 解析后从不转换。

**R9. `temperature: 0` 等「零但有效」参数被静默丢弃** — `relay/converter.go:379-384`(Gemini)、`477-482`(Claude)：`> 0` 把合法的 `temperature=0`（确定性）当未设。应解码为 `*float64` 并判 `!= nil`。

**R10. Claude/Gemini→Responses 丢工具调用并误标 reasoning** — `relay/responses_stream.go:320-491`：把 Claude `delta.thinking` 混入 `output_text`（推理变成可见正文），且忽略 tool-use/functionCall。应分别发 `reasoning` 与 `function_call` 项。

**R11. 16 MB scanner 行上限可能中途截断流** — 各 `bufio.NewScanner` 处：超长 SSE 行（大 base64 图片事件）触发 `ErrTooLong`，已发的部分流无 `[DONE]`/`response.completed`，客户端看到截断且原因不透明。建议改 `bufio.Reader.ReadString('\n')` 或抬高上限并在出错时发终止事件。

**R12. SSRF：base URL 完全用户可控、无 scheme/host 限制（relay 层）** — `relay/openai.go:28-51` 等：`baseUrl` 直接进 `http.NewRequest`。与 server 层 H2/H3 同源，修复应集中在 server 的连接时校验。

**R13. 停止原因丢失 `content_filter`/拒答信号** — `relay/gemini.go:96-99`、`relay/claude.go:104`：Gemini `SAFETY`/`RECITATION` 全塌缩为 `stop`，调用方无法区分被安全拦截。应映射为 `content_filter`。

### Low

- `ConvertOpenAIStreamToClaudeStream` 按 delta 自增 `outputTokens`（`canonical.go:1239`）是无意义计数却作为 Claude usage 上报。
- `newCanonicalResponseID`/`RequestID` 用 `UnixNano()`，高并发下可能撞号。
- `ConvertGeminiResponseToOpenAI` 把 `Model` 置空（`gemini.go:136`）。
- 上游错误体 verbatim 进 `fmt.Errorf`（`openai.go:320`），建议截断（API key 未被记录，正确）。

**测试缺口**：无测试覆盖 Chat→Responses / Claude·Gemini→Responses 的工具调用保真（R1/R10）、Claude→OpenAI 流式 usage（R2）、Claude/Gemini adapter 非 200 处理（R3）、多模态往返（R4）、`temperature=0`（R9）、超长 SSE 行（R11）。

<!-- PLACEHOLDER_STORAGE -->

---

## 3. storage & config 包

### High

**S1. 加密主密钥默认与数据库同目录，威胁模型基本落空**
`config/config.go:346`（`GetDBEncryptionKey`）
`crypto.go:32` 声称「拿到 `.sqlite3` 但没有主密钥则无法恢复 secret」，但默认密钥自动生成并写到与库**同目录**的 `.db-key`。任何备份/卷快照/`cp -r` 都会同时带走密文与密钥，最常见的外泄场景下 at-rest 加密形同虚设。
- **修复**：默认把密钥放到库目录之外（或强制显式指定），优先 `ELYSIA_API_MASTER_KEY` 环境变量；回退到同目录密钥时打印醒目警告。

**S2. `SecretKeyPath` 配置被计算却从不使用，真实密钥路径是另一个硬编码名**
`config/config.go:170-174` vs `346`
`SecretKeyPath`（默认 `.master-key`）被规范化，但 `GetDBEncryptionKey` 完全忽略它、读写 `.db-key`。运维设置 `secretKeyPath` 指向受保护路径会被静默忽略。
- **修复**：让 `GetDBEncryptionKey` 尊重 `c.SecretKeyPath`，或删除该字段——二选一，单一真相源。

**S3. `GetGroups()`/`GetTokens()` 在锁内返回内部切片，调用方锁外读取（数据竞争）**
`config/config.go:234-238`、`253-257`
直接返回 `c.Groups`/`c.Tokens`（切片头别名内部底层数组），返回即释放锁。若运行时这些字段被并发重写（legacy import/admin 路径），与遍历结果竞争。注释声称 RWMutex 提供保护，但返回别名破坏了该保证。
- **修复**：返回副本（`append([]T(nil), c.X...)`），与 `GetGroupByName` 已有的拷贝做法一致。

### Medium

- **S4** `GetDBEncryptionKey` 在 `rand.Read`/`WriteFile` 失败时返回 nil → 静默降级为明文存储，仅 `log.Printf`（`rand.Read` 失败甚至无日志）。应升级为硬错误或持久化系统日志。`config.go:354-365`
- **S5** `DeleteSource` 非事务（`storage/store.go:373-380`）：先删 source 再删 models，第二步失败留下孤儿模型；`models` 无 FK 级联。应包事务或加 `ON DELETE CASCADE`。
- **S6** `findModel` 对裸 model id 用 `ORDER BY source_name LIMIT 1` 任取一源（`queries.go:42`），重复 id 跨源时可能绑错上游。应尽量要求复合 `sourceId:modelId`，歧义时报错而非猜测。
- **S7** `cacheHitRate = cacheHit / input`（`queries.go:335-337`）：缓存命中 token 通常是 input 的子集却独立求和，可能 >1.0；迁移前历史行 `cache_hit_tokens=0` 会拉低窗口命中率且无标注。建议钳到 `[0,1]` 并考虑排除迁移前行。

### Low

- `parseTime` 解析失败静默返回零时（`store.go:177-185`），与「真缺失」不可区分。
- `SaveUsageRecordJSON` 用 `INSERT OR REPLACE`（`queries.go:210`），建议改 `ON CONFLICT DO UPDATE` 更明确。
- `hashToken` 是无盐 SHA-256（`crypto.go:22`）——依赖 token 高熵，建议加注释说明。
- `encrypt` 以 `enc:v1:` 前缀判幂等（`crypto.go:73`），明文恰以此前缀开头会被误判（随机 token 下概率极低）。

**确认正确（重要）**：
- **SQL 注入**：`usageWhere`/`usageInClause`（`queries.go:251-305`）安全——列名为硬编码字面量，所有用户值走 `?` 占位符，IN 子句只按参数个数生成 `?, ?, ...`。
- **回填死锁规避**：`store.go:124-161` 在 `SetMaxOpenConns(1)` 下先把待回填行读入内存并 `rows.Close()` 再做 `UPDATE`，正确避免单连接自死锁。
- **GCM nonce**：`crypto/rand` 96-bit 随机 nonce 前置密文，本规模安全。
- **Reload 锁**：逐字段拷贝而非整体复制（避免拷贝内嵌锁），`Tokens`/`Groups`(json:"-") 不在 reload 覆盖，一致。

<!-- PLACEHOLDER_WEBUI -->

---

## 4. webui 前端（React + TypeScript）

### High

**W1. 多选组件存在嵌套交互元素（trigger 按钮内嵌 clear 按钮）**
`components/ui/multi-select.tsx:87-115`
「清空选择」是嵌在外层 `<button>` 里的 `<span role="button">`。嵌套交互元素是非法 HTML，跨浏览器事件/焦点行为不一致（并触发 React hydration 警告），点 clear 可能误触 toggle，且键盘不可达（`tabIndex={-1}`）。
- **修复**：trigger 改为 `<div role="combobox">` 包裹，clear 作为同级真实 `<button>`；或把 clear 放到 trigger 按钮之外。

### Medium

**W2. 面板令牌存于 `localStorage`，任意注入脚本可读** — `lib/auth.ts:4,12,19`：完整管理凭据持久化在 localStorage 并作为 Bearer 附加到每个请求（`lib/api.ts:65`），XSS/恶意依赖即可外泄，且无过期。当前已做对的：不进 URL（HashRouter）、401 即清除。建议：优先后端下发 httpOnly cookie；否则加客户端空闲超时并文档化 XSS 暴露面。

**W3. 多选组件缺 combobox/listbox 语义与键盘导航** — `multi-select.tsx:85-167`：无 `aria-expanded`/`aria-haspopup`、选项无 `role="option"`/`aria-selected`、无方向键/Enter 选择。点击外部/Esc/打开聚焦/受控状态都正确，仅缺 ARIA 与键盘层。建议补 roles + `aria-activedescendant` + Arrow/Enter 处理。

**W4. 空页时分页控件消失，可能把用户困住** — `pages/usage-logs.tsx:197-267`、`components/ui/states.tsx:103`：分页按钮渲染在 `AsyncState` children 内，仅在 `data.length>0` 时出现。若某查询在 `page>0` 时返回 0 条（数据变动或 offset 越界），空状态替换整个表含分页器，无法回到第 0 页（`system-logs.tsx` 同样）。筛选变更会 `resetFilters()`→page0 缓解但脆弱。建议把分页器渲染到 `AsyncState` 外，或在 `total` 收缩时用 effect 钳制 `page`。

### Low

- **W5** `serializeUsage`（`lib/api.ts:170-186`）先设标量 `keyName` 再被数组 `...{keyName: keyNames}` 覆盖，依赖 spread 顺序，脆弱；既然各页都用数组形式，建议删掉标量行。数组→重复参数的 `buildUrl` 序列化正确。
- **W6** SWR key 的 `to: toRFC3339(new Date())` 每次渲染都变，靠 `useMemo` 冻结避免无限重拉（`overview.tsx:39-42` 等，注释已记录此坑）。当前正确——提示勿删这些 `useMemo`。长会话下 `to` 会偏旧，可加手动刷新。
- **W7** `revealToken` 把明文 API key 写剪贴板无自动清除（`tokens.tsx:161-163`），且非安全上下文（纯 HTTP）`navigator.clipboard` 抛错仅显示通用「复制失败」。建议提示安全上下文要求并考虑定时清空。
- **W8** 概览 `rpm`/`tpm` 按整个 7 天窗口平均（`overview.tsx:50-51`），读作长期均值而非当前吞吐；`usage-stats.tsx:59-62` 已用 `firstUsedAt`/`lastUsedAt` 改善，建议概览也对齐。
- **W9** 可变列表用数组索引作 key（可删除的 manual-models：`sources/source-form.tsx:229`），删除中间项会错配输入状态；建议用稳定 id。
- **W10** 跨标签页不同步：登出/登录无 `storage` 事件监听（`App.tsx:18-20`、`api.ts:80-83`），其余 401 集中处理正确。

**确认正确**：无 `dangerouslySetInnerHTML`/`eval`；日志/usage 内容作为 React 文本子节点渲染（`<pre>` 内），转义到位；编辑弹窗从不回填明文 secret，创建表单保存后清空；除/0 守卫齐全（`percent`/`ratePerMinute`/`compactNumber`）；effect 清理（监听器/定时器/async guard）到位。

---

## 5. 建议的修复优先级

| 优先级 | 条目 | 一句话 |
|---|---|---|
| P0 | C1 | Responses 入口补故障转移 + 空组判空 |
| P0 | H1 | 失败请求回退每日 token 预留 |
| P0 | H5 | `/__shutdown` 冲刷 usage 队列、停健康检查 |
| P0 | H2 | SSRF 改连接时校验（DialContext.Control），消除 rebinding |
| P1 | M1 | `/chat/completions` 429 记 usage |
| P1 | R1/R2/R3 | 跨协议流式：工具调用、usage、上游状态码 |
| P1 | S1/S2 | 加密密钥默认移出库目录 + 统一密钥路径 |
| P2 | R4/R10 | 多模态与 reasoning/工具在 canonical 路径保真 |
| P2 | M3/M4 | 并发计数午夜重置、纯工具流空响应误判 |
| P2 | H4 | 异步 usage 入队深拷贝 |
| P3 | W1/W3/W4 | 多选组件 a11y/嵌套按钮、分页器位置 |
| P3 | 其余 Low | 见各节 |

## 6. 总评

架构清晰、热路径优化与 SSE 细节处理到位，新增透传实现正确且有测试覆盖。最值得立即投入的是**计量正确性**（H1/H5/M1，直接影响计费与配额）、**Responses 入口可靠性对齐**（C1）与**SSRF 连接时校验**（H2）。转换保真问题（R1–R4）主要影响跨协议场景——本次透传已显著降低同协议场景的暴露面，是正确的方向。

