# Elysia-API 项目调查报告（2026-10-09）

> 一句话定位：**Elysia-API 是一个单进程、零外部依赖的 Go 自部署 AI 网关——把「任意已声明协议的客户端请求」经统一语义模型翻译成「任意已验证协议的上游调用」，再把回答（连同缓存与用量口径）如实译回。** 协议本身是数据：四份预置与用户自定义协议共用一套声明式定义语言，编译成双向编解码器；无法等价表达的语义一律显式诊断，绝不静默近似。进程内还住着一个通用管理 Agent，聊天即可维护这座网关。

基线：分支 `deploy` @ `27c8e68`，工作树干净，领先 `origin/deploy` 12 个提交未推送。编译器 `2.0.0-dev.21`，语义模型 v1，定义语言 v2；CHANGELOG 最新版本 v1.6.0（2026-10-05）。后端 224 个非测试 Go 文件约 5.0 万行，204 个测试文件 / 737 个 Test + 5 个 Fuzz 目标（`server/_snapshot` 下另有等量快照副本，统计时勿重复计入）；前端 React 18 + TypeScript 5.4。本报告由五路并行子系统调查（协议引擎 / Agent / server+relay / storage+config / webui）交叉汇总，关键结论带 file:line（相对仓库根）。

心智模型是一条双向流水线，中间是唯一的语义真相：

```
客户端（Chat / Responses / Anthropic / Gemini / 自定义入口 /gateway/:id/*）
   │  鉴权（Bearer / x-api-key / x-goog-api-key / ?key / cookie）→ 模型组 → 源
   ▼
【入口解码】──► 语义模型（有序节点树 + 七类缓存意图 + usage 全 *Counter，缺失≠零）
                     │
                     ▼  能力门 CheckRequest + 转换策略（五阶段规则，可带续传载体）
              【上游编码】（同族同版本时原生回放 + 三方调和回填）
                                    │ JSON / SSE / NDJSON / WebSocket / 异步任务
                                    ▼
                              【上游解码】（含原生帧暂存）
                                    │
              usage 结算落库（先于客户端编码，失败也落）
                                    ▼
【客户端编码】◄── 保真边界：能表达的不丢，不能表达的出诊断（code/stage/path）
   │
   ▼
客户端
```

- **语义模型是唯一真相**：请求/响应/事件先译成有序节点树（每节点带 `Provenance` 溯源与可选 `Native` 原文快照），一切编解码、验证、透传判定、用量统计都围绕它。
- **能力目录是协议的承诺清单**：22 项能力，声明了什么才允许转换什么；没声明的组合在绑定期或请求期显式拒绝。
- **验证先于启用**：样例证据 + 组合验证 + 三重哈希绑定，激活的必须是当前引擎下可复现的报告。

## 1. 目录地图

```
Elysia-Api/
├── backend/                      Go 后端（唯一编译单元，go 1.25 + gin + modernc.org/sqlite 纯 Go 无 CGO）
│   ├── main.go                   薄入口：config.init 装配 → server.New → ListenAndServe；父进程 stdin EOF 触发关停
│   ├── protocol/                 协议引擎（约 60 个文件，见 §3）
│   │   └── builtin/              四个内置适配器 + 四份预置定义 JSON（24 个测试文件含 3 组基准）
│   ├── relay/                    传输与出站安全：共享 HTTP 客户端、SSRF 拨号、WebSocket 桥、流空闲超时
│   ├── server/                   HTTP 服务（81 个文件）：路由、网关编排、管理面、Agent 宿主、预置刷新
│   │   ├── presets/              旧格式（v1）预置种子 JSON，服务迁移链
│   │   └── _snapshot/            elyisia code 工具与发布快照的源码副本（下划线目录不参与编译）
│   ├── storage/                  SQLite 持久化（27 个文件）：32 张表、迁移、加密、用量聚合
│   ├── config/                   config.json 解析、首启自举、原子写、热重载
│   ├── agent/                    Agent 执行引擎（领域无关，不认识任何线格式）
│   └── webui/                    //go:embed all:dist（构建脚本同步，仓库只跟踪 .gitkeep）
├── packages/webui/               React 管理界面（见 §13）
├── scripts/                      六平台构建 / 冒烟 / macOS DMG / npm 二进制分发 / 协议 E2E / 模型目录快照
├── docs/                         40+ 篇中英双语文档（指南、定义参考、审计、验证清单）
└── config.json.example           带注释的配置样例（含 18 个默认出站禁段）
```

依赖方向：`server → relay/protocol/agent → storage → config`；agent 引擎通过 `StreamCaller` 接口与协议层解耦（agent/model.go:12）。

## 2. 进程生命周期

配置在 `config` 包 `init()` 完成（flag `-config`；首次运行 `EnsureConfig` 自动生成默认配置与随机 `elysia-` 面板令牌，config/bootstrap.go:17）。`server.New()` 装配顺序（server/server.go:159-253）：

1. gin ReleaseMode + Recovery（debug 才加 Logger）；
2. 打开 SQLite（`OpenWithKey` + 主密钥；失败记 startupErr 而非崩溃）→ 密钥完整性探测（试解一行密文，storage/crypto.go:112）→ rollup 回填 → 协议升级备份；
3. 一次性迁移链：`importLegacyConfig` → `migrateLegacyCustomProtocols` → `migratePresetProtocolRenames`（六对旧 ID 改名，server/protocol_presets.go:208）→ `reconcileCustomProtocolConfigIDs` → `stripGeminiModelIDPrefixes` → `seedPresetProtocols`；
4. 后台模型目录轮询启动、出站禁段下发到拨号层；
5. `initializeProtocolRuntime`：注册表迁移 + 原子 Reload（预置无条件跟进 shipped 版本并重验）。

`ListenAndServe()`（server.go:1006-1062）：initLifecycle → setupRoutes → usage 写入器 → 网关任务轮询 → 健康检查/保留期清理 → 先 `net.Listen` 绑定再 `Serve`（端口占用在监听期报错而非启动期）。信号、`/__shutdown`（仅回环）、父进程 stdin EOF 共用同一优雅关停序列。

## 3. 协议引擎（backend/protocol）

### 3.1 语义模型（model.go）

| 类型 | 关键字段 | 设计意图 |
| --- | --- | --- |
| `Request` | Model、Content []Node、Tools、ToolChoice、**Parameters（命名语义参数，非透传容器）**、Cache、Resources、Native、ClientOutput | 唯一有序历史；未知 wire 扩展不自动混入 |
| `Node`（11 种 Kind） | Role/ID/CallID/Name/Payload、Input、**Children（工具调用/结果保持原位）**、Cache、Resources、Attributes、**Source \*Provenance、Native** | 消息树；每节点可溯源 |
| `CacheIntent`（7 种 Kind） | breakpoint/key/retention/resource/mode/options.ttl/prewarm；TTL 单一属主（缺省移除、显式 null 保留 null） | 缓存机制语义化，不与字段名绑定 |
| `Counter` / `Usage` | Count + **Origin（observed/inferred）**；Input/Output/Total/CacheRead/CacheCreation + Details | 缺失、明确零、推断值三态分离 |
| `Resource` / `Scope` | Kind/ID + Provider/Account/Model/Session | 签名、文件、缓存等供应商侧资源的归属 |
| `Value`（value.go） | 不可变 JSON | 零值=缺失；null/false/0/""/[]/{} 互不相同；数字不重编码 |

引擎限额默认：深度 64 / 节点 10 万 / 变更 1024 / 状态 4096 / 缓冲 8MB；定义只能收窄（native.go:20）。

### 3.2 定义语言与编译

一个协议 `Definition`（schemaVersion 2）= 身份（id/family/wireVersion）+ 能力声明 + **八个独立方向**（decode/encode × request/response/event/upstream_event）+ HTTP 操作（方法/路径/传输/鉴权位置/输入约束）+ 样例证据。每个方向三选一（definition.go:75）：

| 映射方式 | 语义 |
| --- | --- |
| `module` | 选注册的内置 Go 适配器（`openai-chat` / `responses` / `anthropic` / `gemini`），可配 `after` 后映射（input=模块结果，root=原输入） |
| `transform` | 纯声明式表达式（28 个操作：read/object/array/map/flatmap/filter/choose/if/enum/cast/merge/concat/join/sort/associate/exists/…），循环只遍历输入，禁止脚本与递归 |
| `rules` | 事件帧规则（when/emit），未知帧默认拒绝，需忽略的帧必须显式映射为空数组 |

`Initial` 声明首个非空事件帧前的一次性前缀（服务省略 item-start 帧的供应商）。编译器（compiler.go）做严格解码（拒绝未知键）、类型推断、输出 schema 检查、样例结构校验，产出不可变 `Compiled`（带内容哈希）。

### 3.3 预置协议

| 预置 ID | 版本 | family |
| --- | --- | --- |
| `openai-chat-completions` | 2.2.5 | openai_chat |
| `openai-responses` | 2.2.4 | openai_responses |
| `anthropic-messages` | 2.1.4 | claude |
| `google-generate-content` | 2.1.4 | gemini |

定义内嵌于 builtin/definitions/（引擎 v2 格式）；server/presets/ 另存旧格式（v1）种子服务迁移链。改名映射把六个历史 ID（含 `chat-completions-api`、`anthropic-api` 等）直接指向现行 ID，避免每次重启触发无谓刷新（server/protocol_presets.go:208）。预置只读（保存/激活/删除三处闸门，registry.go:73/156/224）；每次启动无条件跟进 shipped 版本，hash 变化即重验并记入升级计划，本地修改被回归时在系统日志留审计行（server/protocol_refresh.go:44）。历史归档 `preset_replaced` 行三层拒绝物理删除（storage/protocol_history.go:284）。

## 4. 一次转发的完整路径

入口分派（server/gateway_protocol.go:39）：按 protocolId 固定活跃修订 → 异步任务短路 → WebSocket 会话短路 → 常规请求进 `prepareGatewayPlan`（gateway_route.go:45）：

1. `ingress.DecodeRequest`：wire → 语义模型（盖 Source 身份与网关侧作用域，`CheckRequest` 能力门）；
2. 候选循环（模型组 → 源 → key 策略展开）：选绑定 → `resolveGatewayConversion` 解析转换策略（默认策略 + 上下文匹配的活跃策略，rank 0 通配 / 1 精确，歧义报错；source/model 级覆盖）→ 请求相转换 → `matchGatewayCombination` 用离线组合验证报告匹配能力契约；
3. `forwardGateway`（gateway_protocol.go:151）：出站 IP 预校验 → `EncodeRequest`（同族同 wire 版本时走原生回放 + `reconcileNative` 三方调和回填未映射字段）→ 可选 wire 相位再解码自检 → 发上游；
4. 响应反向：`DecodeResponse` → 响应相转换 → `ingress.EncodeResponse` → wire 回验 → 附加续传载体 → 200；用量结算先于客户端编码落库，失败也落。

可重试错误按候选退避重试（`canRetryGeneration`）；流式走 `forwardGatewayStream`（gateway_stream.go:16）：逐帧 DecodeFrame → 事件相转换 → EncodeFrame → WriteFrame+Flush，帧超限防护，失败经 `X-Elysia-Stream-Error` trailer + 失败事件帧报告。

**转换策略层（2026-10-09 新增，本批未推送提交的主体）**：`ConversionPolicy/ConversionRule` 五阶段（ingress/request/response/event/wire）规则引擎（protocol/conversion_policy.go），默认策略仅对预置协议开启少量具名规则；`ContinuationCodec` 提供 HMAC 封装的跨协议续传载体（签名/加密内容经载体往返，protocol/continuation.go）；`ParseClientOutput` 把客户端呈现偏好（`stream_options.include_usage` 等）与生成分离（protocol/client_output.go）。配套存储（策略草稿/修订/激活、续传缓存与统计、上游证据表）、管理 API（`/api/admin/protocols/conversion-policies/*` 含签名探针）与 WebUI 管理页。权威说明见 [conversion-implementation.md](conversion-implementation.md)。

**异步任务**：协议可声明 submit/status/result/cancel 操作；`JobCoordinator` 先持久预留任务 ID 再做上游 I/O（幂等冲突显式报错），提交结果不确定进入不确定态且永不自动重发，租约轮询 + outbox 结算（protocol/job_coordinator.go:40）。

**WebSocket 会话**：`SessionAdapter` 固定双端编译产物 + 绑定契约做双向翻译；有界队列背压、显式握手字段过滤凭据、优雅关闭带时间预算，不重连不重放（protocol/session_transport.go:135）。

## 5. 保真边界：能力门、原生保留与诊断

- **同族无损**：`CanPreserveNative` 要求 family + wireVersion + 方向 + 资源作用域全匹配才允许原样回放（protocol/native.go:59）；跨协议必须走声明的语义映射。同源透传由真实客户端语料测试锁定（server/passthrough_test.go、stream_passthrough_test.go）。
- **能力即承诺**：22 项能力（text、tools.function/free_text/server、native_extensions、images/audio/video/documents、reasoning.signatures/encrypted、cache.breakpoints/keys/retention/resources/options/prewarm、sessions、media.realtime、tasks.async、usage）。文本专用协议拒绝工具数据而非剥离（protocol/capability.go:51）。
- **显式诊断**：一切不可表达产出 `ConversionIssue`（稳定 code + direction + stage + capability + 语义字段路径）；预置行为示例——目标无法表达 Anthropic 缓存创建分桶时省略明细并产 warning，创建总量仍进用量（v1.6.0）。
- **不承诺**：模型回答相同、未知机制自动兼容、跨供应商缓存资源互用（docs/protocol-guide.md「无损的范围」）。

四协议两两行为保证矩阵（16 对）与显式拒绝清单单列于 [protocol-pair-guarantees.md](protocol-pair-guarantees.md)，由真实客户端语料（Claude Code / Codex / Gemini CLI）测试锁定。

## 6. 验证与激活体系

| 层 | 机制 |
| --- | --- |
| 样例证据 | 五类：`Sample`（方向+能力+期望输出或期望诊断）、`SessionSample`（双 lane 交错）、`TaskSample`、`ModelSample`（模型发现）、`AgentSample`；HTTP 200 不能替代断言 |
| 离线验证 `Verify` | 无网络执行全部样例；强制「每方向至少一个通过样本 / 事件方向完整序列 / 声明能力有语义证据 / 工具覆盖定义-调用-结果-多轮关联」；输出三重哈希绑定（定义+编译器+样例集）的验证报告 |
| 往返验证 | `verifyRoundTrip` 语义往返；`verifyNativeExtension` 同 wire 未映射字段保留 |
| 组合验证 | `VerifyCombination`：入口协议样本 → 转换策略 → 上游编码 → 上游再解码 roundtrip；绑定组合额外限定能力合同，越界样本记 Skipped；绑定档案把证据绑到完整能力合同（含显式 false），失败时按受限 profile 拆分复验且证据不跨档案拼接 |
| 激活门槛 | `CanActivate`：报告通过 + 有检查项 + 无 error + 三哈希与当前引擎一致；Service 侧激活前重新加载验证（registry.go:250）；激活/回滚/比较共用同一验证服务 |

编辑器、Agent CLI（`elysia protocol …`）与 REST 三个入口共用上述服务；保存不等于启用，草稿带乐观并发检查。

## 7. Agent 系统

**引擎（backend/agent）领域无关**：模型调用抽象为 `StreamCaller`（流式 + 增量回调），对线格式一无所知。轮次脱离 HTTP 上下文在后台 goroutine 运行，断连不终止轮次。

- **主循环**（agent/agent.go:582）：方案陈旧计数 → 上下文微压缩 → 流式调用 → 无工具调用即终稿 → `executeCalls`；模型调用上限默认 30，热重载 + 会话级覆盖。
- **工具面**：引擎注册 3 个工具（`elysia_cli` / `update_plan` / `ask_user`，server/agent_routes.go:66）；`elysia_cli` 内部按 9 组命令表分发——协议工作流全套（draft/preview/validate/verify/diagnose/diff/activate/rollback…）、模型源/模型/模型组/Key/用量/日志运维、出站策略、`elysia code` 源码参考（读内嵌快照）、内部命令 `update_title`。写操作经引擎门控（判定阶梯 agent.go:895）+ 探针按会话审批。
- **并行与预算**：连续 `ConcurrentSafe` 非门控调用聚合并行（errgroup 上限 4，会话快照隔离）；每工具独立 120s 硬超时 + 子步骤进度心跳；结果按 48KB 钳制落库。
- **上下文压缩**：两级——水位 75% 微压缩（旧工具结果换占位，只改发送副本）；轮首 90% 且 ≥8000 token 且 ≥8 条消息触发摘要压缩，切点回退到最近 user 边界，`BoundarySeq` 落库回放（agent/context.go:76）。
- **暂停型交互**：`ask_user` 与 `update_plan(ready_for_approval)` 把整批剩余调用封进 `PendingAction`，会话置 `waiting_approval`，前端确认卡接管输入框；`/approve` 恢复时作答合成工具结果、同批其余调用合成取消，保证 tool_calls 配对完整。
- **脱敏**：四层——输入键名打码、CLI 命令行 flag 打码、按值精确打码（工具声明 `SecretValues`）、审批快照脱敏；明文仅存在一次调用现场。
- **对外**：管理面 `/api/admin/agent/*`（会话 CRUD、消息 SSE、approve、stop、还原草稿）+ 远程面 `/api/agent/*`（agent 作用域 key 鉴权）+ MCP（Streamable HTTP，无状态 CLI）+ A2A（JSON-RPC + SSE，双版本）+ agent-card。SSE 14 种事件类型 + 15s 注释帧心跳。
- **会话状态**：后端三态 `idle/running/waiting_approval`；前端会话卡片五态（待确认/进行中/有未发送内容/已结束/空对话）为展示口径（叠加草稿与消息数）。未发送输入框草稿存浏览器 IndexedDB；协议工作草稿存后端（draft_config + 单槽还原点）。
- **模型就绪度**：逐模型原因码（未绑定/协议未启用/未声明工具能力…）；未声明工具能力的模型可发带 nonce 的真实函数探针，通过后单事务启用能力标记与模型级绑定，失败零副作用。

## 8. 出站安全与传输层（backend/relay）

- **SSRF 防护**：默认 18 个禁止网段（含 `169.254.0.0/16` 云元数据、`198.18.0.0/15` fake-ip、IPv6 本地/链路/组播），IPv6 过渡地址（6to4/NAT64/Teredo）解包后递归判定（relay/secure_dial.go:161）。双层校验：拨号期 `net.Dialer.Control` 回调（封 DNS rebinding TOCTOU）+ server 预校验。配置语义：nil=默认禁段、显式空列表=全放行；运行时可改带回滚。
- **超时**：非流式总超时 `httpTimeout`（可热替换，relay/timeout_client.go）；流式默认 120s 空闲超时（仅限静默时长，操作可收窄至 1ms—120s）；超时取消整个请求而非半个帧。
- **传输细节**：SSE 多行 data / event / id / retry、EOF 前未闭合事件、NDJSON 逐行（protocol/transport_http.go:128）；`[DONE]` 只由需要它的目标生成；错误信息出站前脱敏 URL。

## 9. 存储与配置

**SQLite（modernc 纯 Go，无 CGO），WAL + 单连接 + PRAGMA 调优。32 张表分五组（逐表逐字段分析见 [storage-schema-analysis-2026-10-09.md](storage-schema-analysis-2026-10-09.md)）：**

| 组 | 表 |
| --- | --- |
| 核心业务 | settings、api_tokens、model_sources、models、model_groups(+引用)、usage_records、system_logs、custom_protocols、agent_sessions、agent_messages |
| 协议体系 | protocol_revisions / drafts / activations / verification_reports / bindings / history、generation_jobs(+结算) |
| 转换策略（新） | conversion_policy_drafts / revisions / activations、conversion_provider_evidence / conversion_policy_evidence、protocol_continuations、conversion_generation、continuation_totals |
| 用量聚合 | usage_rollup_hour（小时×模型×组×key×状态码桶）、usage_rollup_state、usage_assets / usage_asset_refs（外置媒体） |
| 元 | schema_migrations（版本化迁移标记） |

迁移三层混合：全量 `CREATE IF NOT EXISTS` + 幂等 `ALTER`；昂贵步骤走版本化标签；一次性回填分批事务。缺表容忍恢复有真实案例支撑（拷库丢 WAL 后按基线跳过并告警，storage/protocol_upgrade.go:88）。

**主密钥**：env `ELYSIA_API_MASTER_KEY` > 密钥文件 > 旧 `.db-key` > 自动生成；AES-256-GCM，`enc:v1:` 前缀幂等；加密覆盖 token/key/任务载荷/agent 测试 key；行级解密失败清空留行告警。

**配置（config/config.go）**：顶层 host/port（默认 8765）/databasePath/secretKeyPath/webuiDir + 分组 `outbound`（deniedIpRanges）、`agentRemote`（REST/MCP/A2A 暴露面）、`agent`、`usageLog`（8 个指针字段区分未配置与显式 0）、`systemLog`、`modelCatalog`（models.dev 目录）、`responses`、`healthCheck`。无独立 relay/storage 分组。热重载全量拷贝；原子写落盘。

## 10. 多 Key 调度与模型发现

- key 列表整列加密存 `model_sources.api_keys`；每条带 `FetchedModels`（自动拉取的发现集）与 `AllowedModels`（手动分配集），二者互斥。
- 装配期 `KeyAllowsModel` 过滤：无 key 可服务的模型整组剔除（含历史残留与手动模型，server/route_cache.go:174）；请求期策略展开：priority（按序多候选）/ round-robin（进程内游标）/ random / single。
- 刷新并发按 key 拉模型 → `CommitSourceRefresh` 回写各 key 发现集并合并模型表（乐观校验防并发改源）；上游消失的 fetched 行删除并同步清组内引用；删源级联清理模型/组引用/协议绑定。
- 模型目录：models.dev 快照 + 后台轮询同步（可代理），用于模型元数据补全。

## 11. WebUI（packages/webui）

React 18.3 + TypeScript 5.4 + Vite(SWC) + Tailwind 3.4 + Radix 原语 + SWR + Recharts；HashRouter 全页面懒加载 + 空闲预热下一跳 chunk。

| 页面 | 职责 |
| --- | --- |
| 总览 | KPI（渐变数字）、趋势、实时脉冲 |
| 调用日志 | 列表 + 四段正文详情（入站/转发/上游/下游） |
| 模型源 / 模型组 / 令牌 | 源与 key、组、访问令牌（含 agent 作用域） |
| 用量统计 | 图表（按日/模型/脉冲） |
| 协议设计器 | 预置复制/自定义协议编辑（Raw JSON 唯一事实源 + 八个结构化 Tab：基本信息/方向映射/传输操作/样例分区/转换预览/能力验证/版本回滚/完整 JSON）、`canActivate` 门槛（不脏+已验证+编译器版本一致）、转换策略管理页、协议历史页（归档/恢复/引用阻断） |
| AI 助手 | 会话总览卡片网格 ⇄ 工作区三列（轮数条 w-11 / 聊天列 / 任务资料栏 260—560px 可拖拽，45% 钳制保障聊天列下限，<1200px 退化为抽屉） |
| 运行配置 / 系统日志 / 诊断 / 登录 | 运行时配置（含出站禁段编辑器、AI 助手默认与快捷键——自首页迁入）、日志、诊断、动效登录页 |

设计语言「瓷梅刻印 2.0」：明暗 HSL 语义 token + 品牌效果 token（src/index.css）；扁平非卡片（消息直接浮在背景上、会话卡片有线无底）；Claude 式 composer（有线无底、hover 填充）；确认卡（提问/方案/审批）整块接管输入框位；工具行状态动词；`no-scrollbar` 工具类。Agent 未发送草稿存 IndexedDB（`elysia-agent` 库），后端协议工作草稿走服务端还原点。

测试：11 个 Playwright 规格（chromium + webkit，登录动效独立单 worker 工程）；ESLint `--max-warnings 0` 与 `tsc --noEmit` 当前全绿。

## 12. 构建、分发与工程现状

- **六平台交叉编译**（scripts/build-standalone.mjs）：windows/linux/darwin × amd64/arm64，产出 `dist/standalone` + SHA256；`smoke-standalone.mjs` 冒烟；DMG 仅 macOS/CI 组装；`publish-npm-binaries.mjs` 发布平台二进制 npm 包（Koishi 启动器插件按 optionalDependencies 版本对齐消费，该插件在独立仓库）。
- **测试规模**：后端 204 个测试文件 / 737 个 Test 函数 + 5 个 Fuzz 目标（server 108 文件 395 函数、protocol 32/114、builtin 24/53、storage 24/103、agent 3/36、relay 9/22、config 4/20；另有 3 组基准）；前端 11 个 E2E 规格（无单元测试）。
- **git 现状**：`deploy` @ `27c8e68`，领先远端 12 个提交——主体为跨协议转换功能九连提交（`d2b0a1b..7ab8276`：策略引擎/续传载体/typed client_output/pipeline+管理 API/存储持久化/预置别名修复/WebUI 管理页/历史过滤测试/实施文档）+ README 横幅、确定性测试与 WebUI hooks 修复。
- **文档**：40+ 篇中英双语，含四份 dated 审计（缓存保真/命中率/流式/工具）、协议定义参考、迁移与发布清单；`elysia code` 工具让 Agent 读到内嵌的当前源码与文档快照。

## 13. 已知设计取舍

- **不支持任意运行时脚本/WASM**：映射必须可静态验证；表达不了的语义只能显式报错，不能删字段迁就目标。
- **同协议透传保留未知字段与数组顺序，但不承诺字节相同**（JSON 排版可变）；跨协议只保双方可等价表达的部分。
- **单连接 SQLite**：写吞吐有上限；小时级 rollup 预聚合（与原始行同事务 UPSERT、可重建）缓解统计查询压力。
- **配置热重载只影响后续请求**；协议启用固定到不可变修订，热更新走「保存草稿→离线验证→激活新修订」链。
- **WebSocket 会话不转码、不自动续接不支持恢复的会话**；异步任务提交结果不确定时永不自动重发（宁可让人来定夺）。
- **流式空闲超时只限静默时长不限总时长**；帧处理有界（超限即报错）。
- **Agent 明文密钥仅存在于一次调用的现场**：工具结果、事件、审批快照、CLI 回显四路全部脱敏后出引擎。
- **预置协议只读且随版本自动回归本地修改**（留审计行）；定制必须复制为新 ID。
- **`server/_snapshot/` 存在源码快照副本**（`elysia code` 与发布快照用途），不参与编译——全局检索时需注意区分主源码。
