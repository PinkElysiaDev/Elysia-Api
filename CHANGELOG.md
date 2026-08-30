# Changelog

本文件记录 Elysia-API 每个版本的完整发布说明，新版本在前。每个版本小节由
两部分组成：`<!-- release-details -->` 标记之前是面向使用者的简明发布正文
（发版时由 `.github/workflows/release.yml` 提取为 GitHub Release 正文），
标记之后是完整技术细节。发版前必须先在此写下该版本的小节，否则发布流程
会失败（这是有意为之，避免发布空说明）。

v1.1.0 及更早版本的说明先于本文件存在，未收录于此；自 v1.1.1 起的全部
发布说明已合并进来，根目录不再保留按版本拆散的 `RELEASE_NOTES_v*.md`。

## v1.2.0 - 2026-08-31

这是 v1.1.5 以来最大的一次更新，主题是「看得更清楚、跑得更快、更稳」。

### 全新的用量分析

- 总览页新增「实时脉搏」：每分钟请求量、平均与最慢响应时间，一张图看清
  网关当下的状态；还有「模型调用日分布」，最近 7/30 天每个模型每天被调了
  多少次一目了然。
- 统计与调用日志页支持按「模型源」筛选，不同源里的同名模型也能准确区分。

### 数据再多也不卡

- 过去把时间窗切到「30 天 / 全部时间」要等几十秒，现在几乎秒开——新版会
  提前把汇总数据算好。
- **升级后第一次启动会自动升级数据库**：历史数据多的话需要几分钟，期间可
  能暂时没有新日志，属正常现象、不是卡死；完成后以后的启动都会很快。

### 不同 AI 接口之间的翻译更可靠

- 思考过程、引用来源、工具调用等内容在 OpenAI / Claude / Gemini 三种协议
  之间来回转换时不再丢失或变形；流式回复的结束方式更规范，用量统计的口径
  更准。

### 修了一批稳定性问题

- 健康检查误报导致的「好模型被误禁」、Claude 源探测路径错误、重复记录导
  致统计翻倍、高并发下的写入竞态等。

### 界面更细致

- 页面按需加载，登录后首屏明显变快；模型源健康列表放不下时才滚动；热门
  模型支持 24 小时 / 7 天 / 30 天 / 全部快速切换。
- 大屏幕上的字号与间距做了收敛调优：只在很宽的屏幕上轻微放大，整体观感与
  旧版接近。

老数据全部自动迁移，无需任何手工操作。完整技术变更见仓库
[CHANGELOG.md](https://github.com/PinkElysiaDev/Elysia-Api/blob/deploy/CHANGELOG.md)。

<!-- release-details -->

自 v1.1.5 以来合入 deploy 的完整技术变更：

- **用量分析全量重建**（PR #12）：usage 记录持久化模型源 ID（`source_id`
  列 + 增量迁移），模型源筛选按源精确匹配，同名模型跨源不再串数据；
  总览页新增实时用量脉搏（RPM / 平均时延 / P95 / 瞬时吞吐）与模型日调用
  堆叠图；新增 `GET /api/admin/usage/pulse`、`/usage/by-model-daily`、
  `/usage/seq` 接口；路由懒加载 + chunk 拆分优化首屏。
- **用量写入生命周期加固**：reset 与异步落库的 generation 竞态、writer
  关停/入队并发、优雅关停幂等（usage writer 全链路上锁 + 防死锁测试）；
  同 request_id 重复落库不再使 rollup 双计数；坏时间戳（`started_ms<=0`）
  行不再落成 1970 日桶；rollup 回填随 Store 关闭可取消。
- **一次性迁移可见性与版本门控**：大库上覆盖索引构建与标签改写两步一次
  性迁移现在会打印进度/耗时日志（升级后首启不再「看起来卡住」）；标签迁移
  挂 `schema_migrations` 版本标记，已迁移的库每次启动零成本跳过全表扫描。
- **健康检查路径统一**：探测端点按 `NormalizeAPIFormat` 归一化构造，
  claude 源探测从 `/messages` 修正为 `/v1/messages`（与 ClaudeAdapter
  一致），Responses 平台支持原生 `/responses` 探测。
- **协议转换强化（六批次）**：推理链闭环（maheshvara-reasoning-v2 私信
  封）、严格流式终态语义、三协议 citations 与 grounding 保真、往返保真、
  用量计量保真、遗留 function calling 与 `system_fingerprint`。
- **全项目 review 修复（四批次）**：稳定性与安全（2×P1）、协议正确性
  （3×P1）、rollup 卫生与重试取消、路由元数据与可访问性。
- **大库用量窗口性能优化（两阶段）**：切窗查询索引友好化 + 小时级
  rollup 预聚合，全部时间窗口毫秒级返回；修复筛选命中单列索引退化陷阱；
  筛选工具栏无卡片重设计，空交集显式空结果。
- **WebUI 卡片化重设计（PR #10）**：总览与用量页重建，Gemini 风格设计
  语言与十项 UX 修复；模型源健康按实测溢出滚动、热门模型时间窗快捷切换。
- **异步后台模型刷新任务**与 usage 聚合覆盖索引。
- **代码质量 pass（六批次）**：死代码清理（约 -2300 行）、兜底审计、重复
  helper 提取、`migrate()` 拆分、命名与魔法值常量化、遗留 usage 面板下线。
- **核心转换协议命名统一**：Canonical → Maheshvara（大自在天），数据库
  历史标签自动迁移。
- **排版收敛**：大屏流式字号改为 1536px 起步、18px 封顶（原 1280px 起步、
  20px 封顶），侧栏/表头/品牌/抽屉标签的过松字距收紧，KPI 大数字上限调低；
  图表刻度字重阶梯相应简化。
- **发布说明机制改造**：散落的 `RELEASE_NOTES_v*.md` 合并为本 Changelog，
  Release 正文改由本文件提取。

## v1.1.5 - 2026-08-23

`v1.1.5` 是一次正确性与稳定性集中修复版本，同时是**首个提供 macOS App
（DMG）发布物的版本**：macOS 发布物由通用二进制 `elysia-api-macos.dmg`
取代此前的两个 darwin 裸二进制，Release 资产自此固定为 Windows exe /
Linux / macOS DMG 三件套（附 SHA256SUMS）。其余修复覆盖跨协议工具调用、
健康检查误禁、用量统计口径、数据写入安全与 WebUI 体验。

### macOS 发布物改为原生 App（DMG）

- 新增 `ElysiaApi.app` 原生壳应用：菜单栏常驻状态、面板自动登录、关窗
  后台运行、应用内一键自更新（下载 DMG 并校验 sha256 后原子替换）；
- 数据（config / SQLite / master-key / 日志）存放于
  `~/Library/Application Support/ElysiaApi/`，更新不影响数据；
- 本地 `npm run build` 仍与主机无关地交叉编译全部四个平台二进制；DMG
  只能在 macOS 上组装，由 CI 在发布时产出。

### 跨协议工具调用链路修复

- OpenAI `role:"tool"` 消息内容正确包装为 ToolOutput：转 Claude/Gemini
  时能生成 `tool_result`/`functionResponse`，不再因缺少工具结果被上游
  400；
- Claude → OpenAI 方向 tool 消息紧跟 assistant 的 tool_calls、先于补充
  文本，符合 OpenAI 消息顺序硬性要求；
- Gemini `functionResponse` 的调用 ID 自动回填为同名 `functionCall` 的
  实际 ID（name 关联 → id 关结对齐）；
- thinking 块修复为始终位于 assistant 消息首位（Anthropic 要求），
  `[thinking, text]` 往返后顺序不乱；
- `max_tokens` 显式小值原样透传，不再被强制抬高到 65536。

### 健康检查与模型列表防误伤

- Gemini 原生上游首次支持正确探测（
  `/v1beta/models/{model}:generateContent` + `x-goog-api-key`），不再必
  404/401 被误禁；
- 每次探测独立限时，单个慢上游不再耗尽整轮预算连带误禁其他模型；
- 404/405 视为"端点不支持探测"而非上游故障；
- 模型源刷新遇到异常响应（解析出 0 个模型）时保留现有列表，不再清空
  该源。

### 用量统计准确性与存储迁移

- usage_records 新增整型毫秒列 `started_ms`（含索引），修复 RFC3339
  字符串字典序在整秒边界漏记录、同秒排序错乱的问题；旧库启动时自动
  迁移并分批回填（幂等、可断点续传）；
- 请求 ID 追加随机后缀，修复 Windows 时钟粒度下并发请求 ID 相同互相
  覆盖；
- `allTimeSummary` 真正查询全量；时间窗口按本地时钟对齐；全部候选失败
  时补记 usage；
- usage 持久化压缩重写改为内存拼接 + 原子替换，中途崩溃不再丢全部
  历史。

### 可靠性与运行时配置

- 自定义协议非流式请求支持故障转移：连接失败 / 5xx / 429 时自动切换
  下一候选模型，而不是直接报错给客户端；
- 客户端断开连接后上游调用随 context 取消中止，不再空耗带宽与上游
  配额；
- `httpTimeout` 修改即时生效无需重启；"需要重启"状态真实上报
  （host/port/数据库路径变更后置位）；
- 拒绝设置空面板访问令牌，避免把管理面板锁死；
- config.json 改为原子写入 + 全程写锁，进程崩溃不留半截文件，并发保存
  不互相覆盖。

### 协议兼容细节

- 终止原因在 OpenAI / Claude / Gemini 三协议间完整枚举归一化，未知值
  不再原样透传导致严格 SDK 解析失败；
- 音频输入转为裸 base64 + `mp3`/`wav` 短格式（剥离 data: URI、归一化
  MIME）；
- SSE 流中未知字段行按规范忽略，不再破坏 JSON 解析或中止整条流；
- Anthropic 缓存写入 token 明细不再与总数双重计入；
- 自定义协议 `omitIfEmpty` 路径删除真正移除数组元素，不再留下 `null`
  空洞。

### WebUI 体验

- 修复暗色模式首帧闪白（主题脚本前置注入）；
- 用量页时间窗口每分钟自动推进，页面常开也能看到新记录；
- 记录删除/重置后分页自动收敛，不再停留在超界空页；
- 静态资源缓存策略优化（哈希资源 immutable、index.html no-cache），
  升级后不再白屏；
- 面板访问令牌留空提交 = 不修改；开发代理增加 `/debug`。

### 验证

- 后端 `go test ./...`（relay / server / storage / config）全部通过，
  含新增全项目 bug 审查回归测试（批次 A，A1–A9）与自定义协议故障转移
  端到端测试；
- 本地交叉编译四平台二进制（windows-amd64 / linux-amd64 /
  darwin-amd64 / darwin-arm64）通过；
- DMG 由 CI（macos-latest + Xcode 工具链）组装并随本 Release 发布。

## v1.1.4 - 2026-08-18

`v1.1.4` 修复 Anthropic Messages 转到 OpenAI Chat Completions 时
`tool_choice` 形态不合法的问题。Claude Code 默认发送的 `{"type":"auto"}`
不再原样进入 Chat 上游。

### 修复 Anthropic → Chat 的 `tool_choice` 转换

- Anthropic 对象形态现在映射为 Chat Completions 合法值：
  - `{"type":"auto"}` → `"auto"`
  - `{"type":"any"}` → `"required"`
  - `{"type":"none"}` → `"none"`
  - `{"type":"tool","name":"X"}` → `{"type":"function","function":{"name":"X"}}`
- Responses 扁平的 `{"type":"function","name":"X"}` 也会补成 Chat 的
  `function` 嵌套对象；
- 不再把 `{"type":"auto"}` 原样发给 Chat 上游，消除
  `Expected field function in tool_choice`。

### 对齐并行工具调用开关

- Claude `disable_parallel_tool_use` 写入 `parallel_tool_calls`，再由
  Chat 请求发出；
- Chat → Claude 时，若 `parallel_tool_calls=false`，会在 Claude
  `tool_choice` 对象上补 `disable_parallel_tool_use: true`。

### 修复 Chat → Claude 时 `tool_choice` 被原始值覆盖

- 去掉 Claude 写出路径里用原始 `req.ToolChoice` 覆盖转换结果的逻辑；
- Chat 的 `"auto"` / `{"type":"function",...}` 现在稳定渲染为 Claude 的
  `{"type":"auto"}` / `{"type":"tool","name":...}`。

### 验证

- 后端：`go test ./relay` 通过；
- 新增 `tool_choice` 表驱动测试与 Claude ↔ Chat 往返回归。

## v1.1.3 - 2026-08-13

`v1.1.3` 是修复版本，聚焦三类稳定性缺陷：模型组删除导致的后台整页
空白、工具调用 `id` 缺失导致的上游拒绝、以及 Responses 同协议透传丢失
`reasoning_text`。

### 修复删除被使用的模型组导致管理面板整页空白

- 零可见模型的模型组现在返回 `"models":[]` 而非 `null`，消除前端
  `group.models.slice` 崩溃的根源；
- `DeleteGroup` 改为事务化操作，级联移除所有 API Key `allowedGroups`
  中的组名引用，同时完整保留 usage 历史；
- 删除后同步清理该组的限流、轮询游标与粘滞路由运行时状态，避免残留
  内存；
- WebUI 增加数据归一化与根部 ErrorBoundary，异常时展示可重试界面而非
  白屏。

### 修复工具调用 id 缺失导致的 `messages[N]: missing field id`

- Claude、OpenAI Chat、Responses 输入中缺失的工具调用 `id` 会生成确定性
  的 `call_<消息序号>_<调用序号>`；
- assistant 的 `tool_calls[].id` 与后续 `role:"tool"` 的 `tool_call_id`
  保持一致；
- OpenAI 直通请求仅在确实缺少 id 时做最小修补，其余字节原样透传；流式
  渲染同步兜底。

### 修复 Responses 同协议透传丢失 `reasoning_text`

- Responses→Responses 与 OpenAI 系同协议流改为原始 SSE 逐行转发，不再
  经 Maheshvara 重渲染；
- `response.reasoning_text.*` 等 provider 私有事件完整到达下游，多轮
  思考模式续传不再报 `reasoning_text must be passed back`；
- usage 统计、首字节耗时与错误处理逻辑保持不变。

### 验证

- 后端：`go test ./...` 与 `go vet ./...` 全部通过；
- 前端：TypeScript 编译与 Vite 构建通过；
- 新增存储、协议转换、流式透传回归测试覆盖上述场景。

## v1.1.2 - 2026-08-13

`v1.1.2` 是一个修复与优化版本，聚焦于四类同协议透传的稳定化、工具调用
格式转换修复、模型管理一致性，以及发布产物体积回归。

### 四种协议同协议透传稳定化

- 将 Chat Completions、Responses、Claude Messages、Gemini
  GenerateContent 四类协议的同源透传改为**无条件默认启用**（不再依赖
  `relay.passthrough` 开关，该字段标记为弃用）；
- 彻底修复 codex 多轮 Responses 请求中 `reasoning_text` 等富字段被有损
  重建导致上游报错（`reasoning_text ... must be passed back`）的隐患。

### Anthropic → Chat 工具调用格式转换修复

- 修复 Claude `tool_result` 块被错误渲染为 `user` 文本消息的问题；
- 现在 `tool_result` 会正确转换为 OpenAI `role:"tool"` 消息并携带匹配的
  `tool_call_id`，消除上游 `insufficient tool messages` 报错。

### 模型源手动模型同步

- 手动添加的模型改为保存时**同步写入**模型缓存，消除前端 revalidate
  竞态导致的「手动模型不更新模型组」问题。

### 模型源删除/停用后的模型组清理

- 删除模型源时事务内同步清理 `model_group_models` 引用；
- 模型组列表查询按源 `enabled` 状态过滤，停用/删除源后不再残留旧模型
  选中。

### 发布产物体积回归

- 交叉编译脚本补回 `-ldflags "-s -w"`，剥离符号表与 DWARF 调试信息；
- 四平台二进制由约 22MB 回落至约 16MB。

### 独立交叉编译产物

本次发布的独立运行包包含静态 WebUI 嵌入，无需任何外部 Node.js / 前端
环境依赖：

| 平台架构 | 可执行文件名 |
| :--- | :--- |
| **Windows AMD64** | `dist/standalone/elysia-api-windows-amd64.exe` |
| **Linux AMD64** | `dist/standalone/elysia-api-linux-amd64` |
| **macOS Intel (AMD64)** | `dist/standalone/elysia-api-darwin-amd64` |
| **macOS Apple Silicon (ARM64)** | `dist/standalone/elysia-api-darwin-arm64` |

## v1.1.1 - 2026-08-09

`v1.1.1` 是一个重要修复与功能增强版本，主要聚焦于 **WebUI 页面交互与
缓存性能修复**，以及 **同类 API 协议默认透传模式 (Passthrough Mode)**
的功能升级。

### WebUI 交互体验与性能优化

- **Token 累计分布图 Hover 精准触发**：将 `UsageStatsPage` 中甜甜圈
  图表（输入 Token 拆分为缓存命中/未命中环）的 hover 触发区域限制在
  圆环真实半径范围内；避免鼠标仅划过图表卡片空白边缘或图例时产生误
  触发，交互体验更加自然流畅。
- **Usage 统计与日志页面秒开 (0ms 延迟)**：引入 `normalizedNow` 工具
  函数将查询终止时间戳向下舍入至 **1 分钟粒度**，解决毫秒级时间戳导致
  SWR Cache Key 频繁变动而反复触发骨架屏 (Skeleton) 的问题；开启
  `keepPreviousData: true` 及 5 秒请求去重，实现多页面间来回切换时上一
  次统计数据的**瞬间呈现与无感后台静默更新**。
- **模型源停用后的路由隔离与配置拦截**：修复停用某个模型源后，模型组
  编辑弹窗 (`group-form.tsx`) 依然可以选中该源下模型的 bug；后端路由
  缓存 (`route_cache.go`) 与数据库模型列表 (`queries.go`) 增加了对模型
  源 `enabled` 状态的同步校验，确保已停用源的模型无法在运行时被请求
  路由匹配或调用。

### 同类 API 默认启用透传模式

- **转发引擎透传策略升级**：将 `config.go` 中
  `RelayConfig.Passthrough` 的默认值由 `false` 修改为 `true`；当下游
  客户端请求协议与上游目标模型源协议同属一类（如 OpenAI Chat ->
  OpenAI, Claude Messages -> Anthropic, Responses -> OpenAI Responses）
  时，代理层默认自动采用 **原生 Payload 透传模式**；在跳过冗余格式
  转换开销的同时，完整保真客户端发送的第三方扩展字段与高级特性（如
  `cache_control`、`thinking` 等）。

### 独立交叉编译产物

本次发布的独立运行包包含静态 WebUI 嵌入，无需任何外部 Node.js / 前端
环境依赖：

| 平台架构 | 可执行文件名 |
| :--- | :--- |
| **Windows AMD64** | `dist/standalone/elysia-api-windows-amd64.exe` |
| **Linux AMD64** | `dist/standalone/elysia-api-linux-amd64` |
| **macOS Intel (AMD64)** | `dist/standalone/elysia-api-darwin-amd64` |
| **macOS Apple Silicon (ARM64)** | `dist/standalone/elysia-api-darwin-arm64` |
