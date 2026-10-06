# Elysia-API 项目调查报告（2026-10-05）

> 一句话定位：**Elysia-API 是一个单进程 Go 的 LLM 网关——把「任意已声明协议的客户端请求」翻译成「任意已验证协议的上游调用」，再把回答（连同缓存用量）如实译回。** 协议本身是数据：四份内置协议与用户自定义协议都是同一套声明式 DSL，编译成双向编解码器；引擎对「无法等价表达」的语义一律显式诊断，绝不静默近似。进程内还住着一个通用 AI 助手（Agent），聊天即可管理这座网关。

基线：分支 `feat/protocol-v2-gateway` @ `bf264b2`，编译器 `2.0.0-dev.18`，协议引擎 dev.14→dev.18 四轮演进后的 v1.6.0 形态。本报告由四轮交叉审验（33 个审查/验证代理 + 人工逐行复核）与逐项代码实证支撑，关键结论带 file:line 或提交哈希。

心智模型是一条双向流水线，中间是唯一的语义真相：

```
客户端（Chat / Responses / Anthropic / Gemini / 自定义入口 /gateway/:id/*）
   │  路由：模型组 → 源（binding，离线组合验证放行）
   ▼
【入口解码】──► 语义模型（有序节点树 + 六类缓存意图 + usage 全 *Counter，缺失≠零）
                     │
                     ▼  能力门 CheckRequest（目标声明过才放行，否则 4xx）
              【上游编码】──► 上游（四协议自定义源）
                                    │ JSON / SSE / WebSocket / 异步任务
                                    ▼
                              【上游解码】（含原生帧暂存）
                                    │
              usage 结算落库（先于客户端编码，失败也落）
                                    ▼
【客户端编码】◄── 原生回放旁路（三重门：能力 + 编解码器双 hash + 帧 digest）
   │
   ▼
客户端
```

- **语义模型**是唯一真相：请求/响应/事件先译成有序节点树（溯源 Provenance 记录来源协议与方向），一切编解码、验证、回放判定、usage 统计都围绕它。
- **能力目录**是协议的承诺清单：声明了什么能力，就只被允许转换什么；没声明的组合在绑定期或请求期显式拒绝。
- **原生回放**是保真旁路：同线未修改的帧逐字节回放（省一次编解码），任何 hash/digest 变化立刻退回完整重编码或报错。

## 1. 目录地图

```
Elysia-Api/
├── backend/
│   ├── main.go                  入口：装配 config→storage→server
│   ├── config/                  config.json 加载/热重载（Reload 传播）/出站禁列
│   ├── protocol/                ★ 协议引擎（与传输无关的纯语义层）
│   │   ├── definition.go        Definition / CompilerVersion("2.0.0-dev.18")
│   │   ├── compiler.go          DSL→Compiled（双向 mapping，definitionHash=映射+ref闭包）
│   │   ├── capability.go        18 项能力目录 + CheckRequest 递归能力校验
│   │   ├── model.go             语义模型：Node 树 / CacheIntent / Usage(*Counter)
│   │   ├── runtime.go           EncodeRequest/Response/Event + preserveMappedNative
│   │   ├── native.go            原生载荷 + CanPreserveNative（同 family+wireVersion+方向）
│   │   ├── native_reconcile.go  三方合并（original/before/after，删除防复活）
│   │   ├── event_frame.go       SSE 帧编解码 + 回放门（encoder/decoder 双 hash + digest）
│   │   ├── event_replay.go      事件累计 + 晚到 usage（terminal 后仅 UsageUpdated）
│   │   ├── session*.go / job*.go 声明式 WebSocket 会话 / 持久异步生成任务
│   │   ├── verification*.go     激活前样例矩阵验证 + 组合验证（cacheBucketOmission）
│   │   ├── registry.go / revision.go  修订注册表与原子激活
│   │   └── builtin/             四协议内置模块（40+ 文件）
│   │       ├── module.go        TypedModule：module / after 变换 / ref 表达式
│   │       ├── request.go       请求侧：参数/system/tools/choice/六类缓存意图
│   │       ├── response.go      JSON 响应编解码
│   │       ├── stream*.go       SSE 帧解码/渲染/usage 合并/思考/扩展
│   │       ├── usage.go         ★ usage 编解码：计数契约 / 分桶 / 投影省略 / 诊断
│   │       ├── cache.go         ★ encodeCache（能力门统一写入）+ TTL 顺序警告
│   │       ├── definitions/     四份协议 JSON（chat/responses v2.2.0，anth/gem v2.1.0）
│   │       └── definition_upgrade.go  指纹白名单（IsPreviousDefinition）驱动预置升级
│   ├── relay/                   传输接入：protocol_transport / secure_dial(SSRF) / websocket / idle
│   ├── server/                  网关与全部 HTTP 面
│   │   ├── server.go            路由装配（四协议入口 + /gateway/:id + /api + /ui）
│   │   ├── gateway_route.go     binding 解析 + 组合验证 + 重试计划
│   │   ├── gateway_protocol.go  JSON 路径（结算:197 先于编码:207）
│   │   ├── gateway_stream.go    SSE 循环 + 失败 trailer（X-Elysia-Stream-Error）
│   │   ├── gateway_session/job  WS 会话与异步任务执行器
│   │   ├── gateway_usage.go     usageRecord 组装（三态掩码）
│   │   ├── agent*.go            AI 助手：引擎/工具/CLI/事件流
│   │   ├── remote_agent_service / a2a_server / mcp_server   远程三面
│   │   └── protocol_refresh.go  启动重验（CompilerVersion 不匹配→重算落盘）
│   ├── storage/                 SQLite：16 张表 / 迁移 / rollup / 保留期清理
│   └── agent/                   Agent 内核：轮次/暂停/压缩/脱敏/工具注册表
├── packages/webui/              React 控制台（构建后 embed 进二进制）
├── scripts/                     build-standalone(六平台) / smoke / publish-npm-binaries /
│                                verify-protocol(离线+live) / macos-app(DMG)
└── docs/                        本报告 + 契约/验证/审计文档群
```

依赖方向：`server → relay → protocol(builtin) → model`，storage 与 agent 只被 server 依赖；protocol 包不 import 任何 HTTP/存储——引擎可独立 fuzz。

## 2. 进程生命周期

1. **加载配置**：config.json（缺省有完整回退值），支持 `Reload` 热重载并传播（host/port/agentRemote/openBrowserOnStart 等）。
2. **存储就绪**：打开 SQLite → version-gated one-time 迁移（含 v1.6.0 的幂等 `ALTER TABLE usage_records ADD COLUMN cache_creation_tokens / usage_report_mask`）→ 覆盖索引按新列显式 DROP+CREATE → 自定义协议改名对账（`ReconcileCustomProtocolConfigIDs`，按行 ID 注册、坏行跳过）。
3. **协议运行时**：装配注册表 → 未被用户改动的预置经指纹白名单（`IsPreviousDefinition`）自动升级（chat/responses→v2.2.0）→ `refreshProtocolRuntime` 对 CompilerVersion 不匹配的 binding 重验重算落盘 → 路由层再校验。
4. **监听**：绑定端口，可选自动打开控制台；`/health` 上报版本（ldflags 注入的 AppVersion + commit 短哈希）。
5. **关停**：信号与 `POST /__shutdown` 统一经 `shutdownOnce`，`shutdownDone` 关闭后 `ListenAndServe` 真正返回（v1.5.1 修复进程驻留）。

## 3. 协议引擎（protocol-v2）

### 3.1 声明式 DSL 与编译

协议 = 一份 JSON：结构（请求/响应/事件形状）+ 映射（module 复用内置模块 / after 变换 / ref 引用命名表达式）。编译产物 `Compiled` 持有双向 mapping；`definitionHash = hash(mapping + 实际引用的 ref 闭包)`——同名 ref 不同实现必产生不同 hash，这是回放门的根基。编译器版本 `2.0.0-dev.18`；任何既有映射变更都升版本号，借启动重验机制让存量库自动失效重验，用户改动过的定义永不被覆盖。

### 3.2 能力目录（18 项，按方向声明）

`text / tools.function / tools.free_text / tools.server / native_extensions / images / reasoning / reasoning.signatures / reasoning.encrypted / cache.breakpoints / cache.keys / cache.retention / cache.resources / cache.options / cache.prewarm / usage …`。`CheckRequest`（runtime.go:42/:54 双跑）对节点树递归校验：断点/键/保留期/资源引用/模式，目标没声明就 `UnsupportedCapability` 4xx——**在任何上游 HTTP 调用之前**（cache_wire_matrix_test 断言「泄漏到上游即 fatal」）。

### 3.3 原生回放三重门（event_frame.go:66-110）

```
canReplay = 本方声明 native.preserve ∧ 帧带原生载荷 ∧ CanPreserveNative(同 family+wireVersion+方向)
  ├─ encoder/decoder 双 definitionHash 不等 → UnsupportedNative 硬错误（防绕过显式映射）
  ├─ scope 不匹配 → ResourceScopeMismatch
  ├─ 帧 digest 变化：单事件帧 → 降级重编码+reconcile（未知扩展保留）；compound 帧 → 硬错误
  └─ 全部通过 → 原帧逐字节回放
canReplay=false（跨协议/未声明）→ 逐事件完整重编码，无任何错误
```

四份内置定义全部声明 `native.preserve:true`，同协议 pin 流量走回放优化；JSON 路径无 hash 门但走重解码 + `reconcileNative` 三方合并（original/before/after 逐节点比较：相等原样保留、语义删除不复活、未知扩展不动）。

### 3.4 usage 契约（builtin/usage.go）

| 协议 | 读 | 写 | 总量 | 写分桶 |
|---|---|---|---|---|
| Chat | `usage.prompt_tokens_details.cached_tokens` | `…cache_write_tokens`（兼容旧顶层 `cache_creation_input_tokens`，同现异值报错） | `prompt_tokens`（已含缓存） | — |
| Responses | `input_tokens_details.cached_tokens` | `…cache_write_tokens`（同上兼容） | `input_tokens` | — |
| Anthropic | `cache_read_input_tokens` | `cache_creation_input_tokens` | 原始 `input_tokens`+读+写（归一） | `cache_creation.ephemeral_5m/1h_input_tokens` |
| Gemini | `cachedContentTokenCount` | 无生成侧等价字段 | `promptTokenCount` | — |

不变量：**缺失≠零**（语义层全 `*Counter`，nil=未报告）；负数/非整数/null/溢出/别名冲突显式报错；`Details` 不得占用规范键名（`input.cached_tokens`/`input.cache_write_tokens` 归规范化计数器）；同 Kind 重复意图四类一律报错。

## 4. 缓存语义专章（v1.6.0 现状与决策记录）

### 4.1 请求侧：六类意图与能力互斥

| 意图 Kind | 语义 | 内置声明方 | 跨协议现状 |
|---|---|---|---|
| breakpoint | Anthropic 块级断点（system/内容块/tools，≤4，TTL 5m/1h） | 仅 anthropic | →非 Anthropic 目标 4xx（显式拒绝，见 §12） |
| key / retention | OpenAI `prompt_cache_key` / 旧保留期（官方已标弃用，仍兼容解码） | 仅 chat/responses | Chat↔Responses 双向贯通 |
| options.ttl / mode | OpenAI 新 `prompt_cache_options{ttl 仅 "30m" 最低存活期, mode explicit/implicit}` | 仅 chat/responses（v2.2.0） | 同上 |
| prewarm | Responses 专属预热（true 时强制 `generate=false`，只写缓存不产出，标准写费率） | 仅 responses | 仅 Responses 目标可编码 |
| resource | Gemini `cachedContents` 资源引用 | 仅 gemini | 跨族引用显式拒绝 |

**结构化断点合成**（`5c89c55`+`4fe9c8b`）：源级 opt-in 设置（默认关闭），为无标记客户端向上游合成断点（tools 尾→system 尾→消息锚点）——「OpenAI 客户端→Anthropic 上游」方向的官方出口。

**TTL 顺序警告**（builtin/cache.go:53-89）：混合 TTL 断点要求 1h 在 5m 前（按上游处理序 tools→system→messages），违反出非阻断 warning 而非 4xx——四家参考实现均无此校验，属先行。

### 4.2 响应侧：跨协议投影（C28，`941518f`/`3995445`）

真实 Anthropic 缓存写入必带分桶；目标协议无等价字段时：**省略分桶 + warning 诊断**（路径 `/usage/details/ephemeral_5m_input_tokens` 或 Gemini 的 `/usage/cacheCreation`），**创建总量仍映射标准字段**；同协议 Anthropic 往返完整保留分桶；内部 `ProtocolUsage` 与落库始终保留完整分桶，省略只作用于线制输出；绑定期组合验证按 `cacheBucketOmission` 认可该投影。诊断按键名排序遍历（同输入同结果）并按请求去重，随成功/失败响应一并进入 `ConversionIssues` 落库。

### 4.3 决策记录（四轮审验终局）

| 决策 | 结论 | 依据 |
|---|---|---|
| 分桶跨协议省略投影 | ✅ 批准并已实现 | 修复「非 Anthropic 目标必 502 / 流式截断」 |
| Gemini 创建总量 | 省略+warning（原计划维持报错，实施时统一为投影，`3995445`） | 与投影语义一致 |
| Anthropic↔OpenAI 断点真映射 | ❌ 本轮不立项 | `mode=explicit` 会关闭隐式缓存而 tools/instructions 不可标——映射后覆盖可能**小于**直接丢弃；TTL 档无交集（5m/1h vs 仅 30m）。重启条件：OpenAI 支持 tools 级断点或混合语义，或站点支持度实测到位 |
| prewarm 建模 | ✅ 已实现（`6fcb36b`） | 官方语义核实充分（字段/行为/计费/Responses 专属） |
| 自动合成断点 | 默认关闭，仅 opt-in 源设置 | 「不自动启用缓存」边界 |

## 5. 网关执行路径（四条）

| 路径 | 主链 | 失败形态 |
|---|---|---|
| JSON | `gateway_protocol.go`：DecodeRequest→CheckRequest→EncodeRequest→Send→DecodeResponse→**结算(:197)**→EncodeResponse(:207) | 转换错→502+issues；上游 HTTP 错→按声明映射错误响应并结算 |
| SSE | `gateway_stream.go:36-81`：DecodeFrame→replay.Consume（晚到 usage）→EncodeFrame→emit | 已写字节后失败→`X-Elysia-Stream-Error` trailer + OperationFailed 帧 |
| WebSocket | `session_adapter`：同 encodeFrame 门；会话 usage 由 SessionReplay 收集 | 会话错误帧 |
| 异步任务 | job 持久化（崩溃/重启恢复）→ 结果回传走 JSON reconcile 门 | 任务态落库 |

usage 结算先于客户端编码——编码失败也完整落库（input/output/读/写/分桶）。

## 6. Agent 子系统

- **引擎**：轮次制（运行锁 per session）；审批卡/计划模式/`ask_user` 三类暂停型轮次；长对话在 user 边界压缩上下文；工具密钥全出口脱敏，安全工具并行执行（ToolMeta 声明）。
- **完成顺序**（`64d9d9b` 修）：defer 注册序 `close(events)`→`e.end`→`cancel`，LIFO 使 **EOF ⇒ 运行锁必然已释放**——四类消费面（WebUI SSE/REST/A2A/MCP）都以 EOF 判定轮次终态；panic/停止/审批续跑路径均安全。
- **工具面**：`bash`（内容感知门控探针）+ 会话原语（`update_title`/`ask_user` 等 22 项注册于 `agent/tools.go`）；管理能力经 **elysia CLI**（33 条命令、九大能力域）直达，与编辑器共用同一服务层。
- **会话体验**：IndexedDB 草稿、会话卡片五态、工具执行状态行、标题自动命名、图片缩略图灯箱。

## 7. 远程面

专用 **Agent 作用域 Key**（与网关调用 Key 完全分离，管理页独立启停）暴露三种远程指挥面：管理 REST（`/api/agent/*` + SSE 事件流）、MCP 端点（无状态 `elysia_cli` 单工具）、A2A 端点（双线制、审批可续、终态不可变）。远程对话/审批/提问与网页端实时同步。

## 8. 存储与统计

16 张表（models / model_groups / model_group_models / model_sources / api_tokens / custom_protocols + 协议修订 / agent_sessions / agent_messages / usage_records / usage_rollup_hour / usage_rollup_state / usage_asset_refs / system_logs / settings / schema_migrations）。

- **usage_records** 核心列：`input_tokens / output_tokens / total_tokens / cache_hit_tokens` + **v1.6.0 新增 `cache_creation_tokens`、`usage_report_mask`**（位掩码：Input/CacheHit/Creation 各一可信位，区分「上游未报告」与「报告为零」）；完整明细（含分桶、ConversionIssues、请求/响应快照）在 `record_json`。
- **usage_rollup_hour** 同步累积 `cc_tok / cc_rows`；小时 rollup 使全时间窗聚合毫秒级。
- 命中率只在「读与输入均可信」的同一集合上计算并显示覆盖率；读>输入的失真比率记异常排除而非钳制 100%。
- 媒体内容寻址外置去重（usage_asset_refs）；保留期清理与容量自适应批删。

## 9. 安全与运维

- **SSRF**：可配置 CIDR 禁止列表 `outbound.deniedIpRanges`（预置默认全内网段），运行配置页可编辑，Agent 也可代办（`update_outbound_policy`）。
- **密钥**：主密钥加密存储，所有出口（日志/UI/Agent 文本/CLI 回显）脱敏。
- **观测**：`/health` 版本上报；系统日志与用量日志分表保留期治理。

## 10. 配置面

config.json 全组：`host / port / panelAccessToken / databasePath / logLevel / httpTimeout / secretKeyPath / openBrowserOnStart / agentRemote{enabled,publicUrl} / outbound.deniedIpRanges / usageLog{persistEnabled,retentionDays,maxContentMB,maxRecords,bodyMaxKB,bodyOnErrorOnly,externalizeMedia,cleanupIntervalMinutes} / systemLog{…} / modelCatalog{enabled,url,proxy,syncIntervalMinutes}`。

WebUI 页面：总览（PageHeader+全宽网格）、用量统计（三态缓存展示+覆盖率）、用量日志（详情 Sheet+媒体灯箱）、模型源/模型组/API Key（含 Agent 专用 Key）、协议设计器（结构/映射分离+验证/预览/激活）、运行配置（出站策略编辑器）、系统日志、AI 助手（Claude 式 composer、确认卡接管、会话卡片五态）。

## 11. 构建与发布链

`scripts/build-standalone.mjs`：webui 构建→embed→**六平台交叉编译**（windows/linux/darwin × amd64/arm64）→`dist/standalone/`；版本经 ldflags 注入（最近 tag→AppVersion，无 tag=`dev`+commit 短哈希）。`smoke-standalone.mjs` 冒烟；`publish-npm-binaries.mjs` 发布 `elysia-api-<os>-<arch>` 平台二进制包（Koishi 插件瘦启动器按需下载，对齐上游=改 optionalDependencies 版本号）；`build-macos-app.mjs` 组装 DMG（发布由 CI 产出）；`verify-protocol.mjs` 分发离线套件与限额线上实验（cache-gaps / cache-followup，预算 96+4 清理预留，可恢复）。

## 12. 已知设计取舍

- **显式拒绝 vs 静默近似**（本项目最大的立场差异）：四家同类网关实测全部走静默路线——new-api 透传/剥离（缓存永不命中与严格上游 400 的 issue 在案）、axonhub 剥离+自动合成 `prompt_cache_key`+断点裁剪注入、cc-switch 静默剥离（防 GLM/Qwen 严格校验 400）、monoize extra_body 透传+可选合成。本项目的选择：**能力门显式 4xx + 诊断，合成仅 opt-in**——语义诚实换取开箱可用性，代价是 Anthropic 客户端→非 Anthropic 上游在 builtin 定义下无显式缓存（隐式缓存仍生效）。
- **缺失≠零**：语义层 `*Counter` → 存储层 mask 位 → UI 三态，全链路拒绝把「上游没报」当「命中为零」。
- **回放门硬化**：双 hash 门只在「双方声明 preserve 且同 family+wireVersion」时硬错误；跨协议自然降级完整重编码——严格但有出口。
- **协议是数据**：用户改过的定义永不被升级覆盖；指纹白名单只认「未改动的上一代原文」。
- **不自动合成断点**（默认）：断点写入 1.25×/2× 计费，替客户端决定花哪份钱不属于网关。

## 13. 当前状态与遗留

**Git**：`feat/protocol-v2-gateway` @ `bf264b2`（45 个本地提交未推送：C01–C27+追修 32 + v1.6.0 批次 13），deploy 另有 21 个未推送；全部提交 author/committer=PinkElysia（历史改写后的备份分支已删除；改写前 12 条中仅 2 条曾带 `Claude Fable 5.1` 尾注，已随 filter-branch 清除）。

**版本**：v1.6.0 CHANGELOG 已撰写（发版流程要求前置），尚未打 tag/发布；编译器 dev.18；预置 chat/responses v2.2.0。

**待办**（按优先级）：
1. C35 性能优化未做——旧别名修改路径 +14.4% CPU / +18% 分配回退仍在（方案已定：decodeUsage 打别名存在性位→encodeResponseUsage 早退→**此后**才可剥离 before 树 Native；顺序颠倒会复活 4dfa0d2 修过的别名残留）。
2. v1.6.0 批次 13 提交后的全包 `go test -race ./...` 复跑记录（race.mjs 专项已扩含 TestRunTurn_ConcurrencyGuard）。
3. 六平台构建与冒烟（dev.18 产物）。
4. C34 余项（长函数拆分、命名/嵌套收束——清单在案）。
5. 13 项线上证据缺口维持（Gemini 网关组非零读取、显式资源生命周期、1h/24h TTL、读后刷新、跨协议线上非零读取等；其中 Gemini 网关组存 wire 差异假说：合成 id 注入、tools 拆分、role 显式化、认证头差异可能影响中转站缓存路由）。
6. deploy 存量历史含 15 条 2026-02~07 的 `Co-Authored-By: Claude` 尾注且已在远端——是否重写已发布历史待决（需 force-push + 协作者协调）。

**发布就绪判定**：`protocol-release-evidence.json` 的 `releaseReady=false` 维持——代码保真层已闭合，站点行为 / 真实非零命中 / 时间行为三类证据仍开放。
