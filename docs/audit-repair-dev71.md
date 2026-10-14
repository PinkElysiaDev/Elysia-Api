# 2026-10-14 审计修复与 dev.71 验收

本轮以 `87782c9` / `2.0.0-dev.70` 为基线，实现缺失 Chat 模型的限定兼容、`new_api_error` 分类与投影、流空闲超时归因及审计记录修复。最终代码提交为 `2a1fd1a`，编译器为 `2.0.0-dev.71`。后续文档提交不改变实现。默认仅本地提交，没有推送，也没有调用真实收费渠道。

## 实现及提交

| 提交 | 内容 |
|---|---|
| `58a1975` | `chat_response_model` 动作、实际出站模型来源、网关／收集器／预览／组合验证与 dev.71 |
| `6d3f56b` | 精确识别 `new_api_error`，保留供应商包装和详情，按明确 code／实际非成功 HTTP 分类 |
| `15a525a` | 模型别名只替换合法非空字符串，保留非法上游字段供最终校验；限定来源归因 |
| `e63d1c8` | 可沿错误链识别的流空闲超时，调用日志保留阶段和次级故障 |
| `f94f382` | 审计主次分类、独立恢复步骤、持久化检查和扩展覆盖记录 |
| `2a1fd1a` | 保留 `new_api_error` 的 `model_not_found`／`param:"model"` 组合详情 |

没有新增端点、数据库表或通用兼容框架。dev.71 沿用现有协议和绑定证据重验机制；没有批量重写用户定义、草稿、绑定或手动能力。

### Chat 非流式模型补齐

- 稳定规则 ID 为 `chat-response-model`，动作 `chat_response_model` 位于 `response` 阶段。按实际内置 Chat 请求／响应模块选择默认规则，包含复用这些模块的自定义协议；不按 family 或名字推断手写映射。
- 只处理成功非流式响应中原始和语义模型均缺失的情况。补齐来源是本次最终实际发出的请求体；不使用模型组别名或其他请求的状态，不增加生成次数。
- 兼容模式记录 `/model`、源协议、`decode_response`、规则来源、策略哈希及 `lossy_compatible`。严格模式拒绝，禁用规则后不补齐。
- 原始字段为 null、空串、非字符串时不补齐。源映射已经给出模型时不覆盖，映射删掉原有模型时不掩盖。只有合法非空模型才参与已有客户端别名替换。
- 原始响应证据和 Native 快照仍保留缺字段形态。大整数、工具参数、未知同协议字段和原始 usage 保留；补齐值不代表供应商确认了实际模型，也不参与改写路由或计费。
- Agent 收集器仅复用所选策略的模型修复，不额外执行客户端封装。预览的 `responseModel` 明确描述本次出站模型及源字段存在性；组合验证夹具通过 `upstreamRequest` 提供实际请求上下文。
- 模型最终校验失败只有在源值、转换后语义值及最终线格式的存在性和值均一致时，才标为 `upstream_contract_violation / decode_response`。映射删除或修改导致的错误继续归属目标校验。其他未经证明的错误不统一改成上游责任。

### 供应商失败与传输失败

- 只识别精确的 `new_api_error`，不接受其他未知包装类型或任意对象扩展。分类先看已有明确 code，再看实际非成功 HTTP；HTTP 200 的失败事件仍是失败。
- Chat／Responses 保留可表达的结构化 type、code、param；同协议 Native 保留原形态。Anthropic／Gemini 兼容模式把不能结构化表示的合法字符串详情追加到错误消息并诊断，严格模式拒绝这种结构变化。
- `input_tokens_out_of_range` 作为供应商失败细分码保留，不据其名称推断请求实际过长，不自动调整历史或预算。`model_not_found` 的包装参数也保留，不套用原有规范化将其删去。
- `StreamIdleError` 沿错误链传递。`transportFailure.kind=upstream_idle_timeout`，阶段分别为 `awaiting_response`、`reading_body`。前者只表示尚未获得 HTTP 响应，不进一步猜测连接、TLS 或源站处理。
- 默认时长、HTTP 状态策略、重试策略保持原状。已有 HTTP 失败／供应商失败保留主因，后续超时作为次级证据；客户端取消单独归类。没有增加已开始生成的重试。
- 原有 `chatcmpl-0`／created 为 0 且缺少 model 的不完整启动组合继续拒绝。正文后身份变化、第三种 ID、Responses 时间戳变化、非法尾帧和未完成 EOF 的保护继续执行。

### 审计报告

- 分开记录实际上游 HTTP、客户端 HTTP、逻辑调用状态、主失败、次级转换诊断及传输阶段。SDK 失败标签不会抢占已存在的结构化上游证据。
- `failureCounts.primary` 汇总主分类；`failureCounts.secondary` 单独计数混合故障中的转换／契约／传输问题，允许与主分类重叠，不能直接相加成独立失败总数。
- 非法 JSON 的预期拒绝和恢复生成分开。持久化测试分开配置保存、禁用、启用、重启状态、令牌创建／撤销及各次恢复生成；重启核对不依赖供应商生成成功，撤销不依赖前一次令牌生成成功。
- 覆盖记录分别记录扩展是否出现、规则是否触发、用例是否完整消费成功。未知源证据标为 null；普通成功不冒充扩展验收。规则告警也不能代替完整消费或第二轮成功。
- 直连仍按供应商原生合规验收；网关兼容成功保持独立。没有原地改写历史报告，也没有记录轮次状态原值。

## 回归范围与证据

新增夹具只含合成文本、模型、工具与错误内容，保留缺失／null／错误类型、原始字段位置和大整数。没有提交完整用户请求或真实供应商身份信息。

| 范围 | 回归证据 |
|---|---|
| 模型修复 | 缺失、null、空串、数字、对象、合法值；兼容／严格／禁用；模型组别名不同；源映射提供／删除；自定义模块和手写映射排除 |
| 跨层 | 实际网关 mock 上游、Agent 收集器、管理预览和组合验证；完整响应正文、工具参数、usage 及原始证据 |
| 错误投影 | Chat／Responses 两源、四目标、HTTP 200／400／401／429／500、JSON／SSE；严格和禁用、未知类型／扩展、明确 code 优先及参数保留 |
| 超时 | 实际本地 HTTP 响应前／响应体停滞，HTTP 错误体后停滞，供应商失败立即终止，取消；错误链中供应商主因加次级超时，日志 JSON 保留阶段 |
| 审计 | 混合故障分类、SDK 优先级、扩展未出现／出现但消费失败、恢复步骤和不依赖生成成功的存储检查 |
| 原有保护 | 冻结 dev.69 差分资产未重写；历史比较仅剥离新增默认规则导致的策略身份变化，再比较实际输出、诊断和组合结果 |

最小回归先确认实际故障：缺失模型原先返回 502；非法模型被别名覆盖原先错误返回 200；包装类型原先触发 `/wire:openai_chat/type`；包装参数原先被移除。修复后的回归结果与这些复现分开记录。

本轮人工审读集中于上述修改及相邻模型映射、错误投影、传输取消和审计调度调用链；静态检查与测试覆盖不等于逐行人工审读整个 release 差异，不据此宣布此前全量审查台账已完成。

## 同一最终代码提交的工程验收

环境：Windows，Go 1.26.5，Node 24.18.0。最终验证日志位于 `%TEMP%\elysia-audit-dev71\accepted-final\`；`verification-summary.json` 保存代码提交、命令、开始／结束时间、退出码和日志 SHA-256。

| 检查 | 结果 |
|---|---|
| Go 全量 `go test -json ./... -count=1 -timeout=45m` | 3,067 项测试及子测试通过，0 失败，6 项条件跳过；server 包 1882.391 秒 |
| `go vet ./...` | 通过 |
| staticcheck `v0.6.1` | 通过 |
| 相关 race | protocol、builtin、relay、server 选定路径通过；server 275.772 秒 |
| 前端类型／lint／生产构建 | 通过 |
| 审计自测 | 161 通过，0 失败，0 跳过 |
| 源码快照边界 | 4 通过 |
| 本地后端 Playwright | Chromium 16 通过；包括实际协议服务用例及页面路由 mock 用例，不把全部描述为真实供应商端到端测试 |
| 六目标构建 | Windows／Linux／macOS 的 amd64、arm64 全部通过 |

最终 Go 全量执行时间为北京时间 2026-10-14 23:40:15 至 2026-10-15 00:11:50，退出码 0。日志 `go-test.jsonl` 的 SHA-256 为 `ca6fabf7d4c11763f0921f5ccbed1e722cd232a6526f8f94c1192531d53e5993`。两项无测试文件的包不计入下面六项条件跳过。

条件跳过分别为 `TestCLILivePromptTasks`（需要真实模型配置）、`TestConsoleBrowserEnvironmentOverride`（需要 POSIX shell）、`TestConversionClientToolRoundTrip/openai` 与 `/claude`（需要独立客户端安装）、`TestProtocolLive`（未开启真实供应商测试）、`TestProtocolLoad`（未开启本地负载测试）。它们不构成通过或真实环境验收。

主要工程命令从仓库根目录执行，Go 命令从 `backend/` 执行：

```text
go test -json ./... -count=1 -timeout=45m
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...
npm.cmd test --prefix scripts/protocol-audit
node node_modules/typescript/bin/tsc --noEmit -p packages/webui/tsconfig.json
npm.cmd run lint --workspace @root/webui
node --test scripts/source-snapshot.test.mjs
node scripts/build-standalone.mjs
node scripts/test-protocol-e2e.mjs protocol-v2.spec.ts conversion-policy.spec.ts protocol-history.spec.ts
```

race 使用本机 llvm-mingw、`CGO_ENABLED=1`、`GORACE=halt_on_error=1`，覆盖新模型规则、失败投影、流空闲超时、收集器、Chat 启动、轮次状态、HTTP 失败、取消和关闭路径。完整筛选表达式及所有日志摘要保存在 `verification-summary.json`。

在最终提交前的复核发现包装参数遗漏，早一轮全量测试被主动中止。其日志保留在 `accepted/`，不计入最终通过记录。另一次新测试的 mock 服务未读请求体，导致 Close 等待；修正 mock 后定向回归通过，未改变生产超时来规避该失败。

### 本地后端的故障注入

四种合成上游均固定返回 Cloudflare 524，实际运行持久化审计：33 项准备／配置／重启／授权检查通过，20 次生成或恢复调用保持 `upstream_http_timeout` 失败，0 blocked／skipped。整体报告仍为 failed；这证明故障分类和独立存储检查行为，不代表上游可用性通过。最终同代码复测记录见 `accepted-final/persistence-outage/`。

另外用同一提交的真实本地后端和合成 Chat 上游验证缺模型响应：OpenAI SDK `7.31.0`、Vercel `@ai-sdk/openai 4.0.91` 均完整消费成功。两次调用均实际触发 `chat-response-model`，上游证据仍缺 model，出站请求为 `sent-model`，客户端返回 `client-group`，原始输入／输出用量为 7／3。第三次将上游 model 改为 null，网关保持 502；总共只有三次上游请求。证据见 `accepted-final/sdk-model.json`。这验证了本地 SDK 路径，不是原 VS Code／Claude Code 客户端验收。

## 交付边界

- 本地修复：定向回归及同一最终代码提交的工程验收已通过；六项条件跳过如上单列。
- 真实渠道：本轮没有调用，待另行按并发 1 定位、再以原参数完整复测。
- 实际 VS Code／Claude Code／Codex 客户端：本轮未验收。不能由 Go、浏览器或模拟 SDK 测试替代。
- 轮次状态：此前真实报告没有捕获该扩展，本轮也不将普通路径通过标成真实扩展兼容通过。既有 OpenAI SDK 对 `response.metadata` 的限制没有被本轮改写或掩盖。
- 渠道异常：真实 524、明确供应商失败、空闲超时、不完整占位启动帧和后续身份／时间变化仍是失败。本轮未证明渠道已恢复，也未扩大兼容规则来消除失败计数。
- macOS 原生生命周期／下载／更新／回滚未运行；本轮无 Swift 修改，交叉编译不替代原生验收。
