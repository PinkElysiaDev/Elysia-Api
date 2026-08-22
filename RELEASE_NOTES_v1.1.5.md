# Elysia-API v1.1.5 发布报告

## 版本概述

`v1.1.5` 是一次正确性与稳定性集中修复版本，同时是**首个提供 macOS App（DMG）发布物的版本**：macOS 发布物由通用二进制 `elysia-api-macos.dmg` 取代此前的两个 darwin 裸二进制，Release 资产自此固定为 Windows exe / Linux / macOS DMG 三件套（附 SHA256SUMS）。其余修复覆盖跨协议工具调用、健康检查误禁、用量统计口径、数据写入安全与 WebUI 体验。

---

## 主要变更细节

### 1. macOS 发布物改为原生 App（DMG）

- 新增 `ElysiaApi.app` 原生壳应用：菜单栏常驻状态、面板自动登录、关窗后台运行、应用内一键自更新（下载 DMG 并校验 sha256 后原子替换）；
- 数据（config / SQLite / master-key / 日志）存放于 `~/Library/Application Support/ElysiaApi/`，更新不影响数据；
- 本地 `npm run build` 仍与主机无关地交叉编译全部四个平台二进制；DMG 只能在 macOS 上组装，由 CI 在发布时产出。

### 2. 跨协议工具调用链路修复

- OpenAI `role:"tool"` 消息内容正确包装为 ToolOutput：转 Claude/Gemini 时能生成 `tool_result`/`functionResponse`，不再因缺少工具结果被上游 400；
- Claude → OpenAI 方向 tool 消息紧跟 assistant 的 tool_calls、先于补充文本，符合 OpenAI 消息顺序硬性要求；
- Gemini `functionResponse` 的调用 ID 自动回填为同名 `functionCall` 的实际 ID（name 关联 → id 关结对齐）；
- thinking 块修复为始终位于 assistant 消息首位（Anthropic 要求），`[thinking, text]` 往返后顺序不乱；
- `max_tokens` 显式小值原样透传，不再被强制抬高到 65536。

### 3. 健康检查与模型列表防误伤

- Gemini 原生上游首次支持正确探测（`/v1beta/models/{model}:generateContent` + `x-goog-api-key`），不再必 404/401 被误禁；
- 每次探测独立限时，单个慢上游不再耗尽整轮预算连带误禁其他模型；
- 404/405 视为"端点不支持探测"而非上游故障；
- 模型源刷新遇到异常响应（解析出 0 个模型）时保留现有列表，不再清空该源。

### 4. 用量统计准确性与存储迁移

- usage_records 新增整型毫秒列 `started_ms`（含索引），修复 RFC3339 字符串字典序在整秒边界漏记录、同秒排序错乱的问题；旧库启动时自动迁移并分批回填（幂等、可断点续传）；
- 请求 ID 追加随机后缀，修复 Windows 时钟粒度下并发请求 ID 相同互相覆盖；
- `allTimeSummary` 真正查询全量；时间窗口按本地时钟对齐；全部候选失败时补记 usage；
- usage 持久化压缩重写改为内存拼接 + 原子替换，中途崩溃不再丢全部历史。

### 5. 可靠性与运行时配置

- 自定义协议非流式请求支持故障转移：连接失败 / 5xx / 429 时自动切换下一候选模型，而不是直接报错给客户端；
- 客户端断开连接后上游调用随 context 取消中止，不再空耗带宽与上游配额；
- `httpTimeout` 修改即时生效无需重启；"需要重启"状态真实上报（host/port/数据库路径变更后置位）；
- 拒绝设置空面板访问令牌，避免把管理面板锁死；
- config.json 改为原子写入 + 全程写锁，进程崩溃不留半截文件，并发保存不互相覆盖。

### 6. 协议兼容细节

- 终止原因在 OpenAI / Claude / Gemini 三协议间完整枚举归一化，未知值不再原样透传导致严格 SDK 解析失败；
- 音频输入转为裸 base64 + `mp3`/`wav` 短格式（剥离 data: URI、归一化 MIME）；
- SSE 流中未知字段行按规范忽略，不再破坏 JSON 解析或中止整条流；
- Anthropic 缓存写入 token 明细不再与总数双重计入；
- 自定义协议 `omitIfEmpty` 路径删除真正移除数组元素，不再留下 `null` 空洞。

### 7. WebUI 体验

- 修复暗色模式首帧闪白（主题脚本前置注入）；
- 用量页时间窗口每分钟自动推进，页面常开也能看到新记录；
- 记录删除/重置后分页自动收敛，不再停留在超界空页；
- 静态资源缓存策略优化（哈希资源 immutable、index.html no-cache），升级后不再白屏；
- 面板访问令牌留空提交 = 不修改；开发代理增加 `/debug`。

---

## 验证

- 后端 `go test ./...`（relay / server / storage / config）全部通过，含新增全项目 bug 审查回归测试（批次 A，A1–A9）与自定义协议故障转移端到端测试；
- 本地交叉编译四平台二进制（windows-amd64 / linux-amd64 / darwin-amd64 / darwin-arm64）通过；
- DMG 由 CI（macos-latest + Xcode 工具链）组装并随本 Release 发布。
