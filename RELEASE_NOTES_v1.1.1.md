# Elysia-API v1.1.1 发布报告

## 📌 版本概述
`v1.1.1` 是一个重要修复与功能增强版本，主要聚焦于 **WebUI 页面交互与缓存性能修复**，以及 **同类 API 协议默认透传模式 (Passthrough Mode)** 的功能升级。

---

## 🛠️ 主要变更细节

### 1. WebUI 交互体验与性能优化 (WebUI Fixes & Enhancements)
- **Token 累计分布图 Hover 精准触发**：
  - 将 `UsageStatsPage` 中甜甜圈图表（输入 Token 拆分为缓存命中/未命中环）的 hover 触发区域限制在圆环真实半径范围内；
  - 避免鼠标仅划过图表卡片空白边缘或图例时产生误触发，交互体验更加自然流畅。
- **Usage 统计与日志页面秒开 (0ms 延迟)**：
  - 引入 `normalizedNow` 工具函数将查询终止时间戳向下舍入至 **1 分钟粒度**，解决毫秒级时间戳导致 SWR Cache Key 频繁变动而反复触发骨架屏 (Skeleton) 的问题；
  - 开启 `keepPreviousData: true` 及 5 秒请求去重，实现多页面间来回切换时上一次统计数据的**瞬间呈现与无感后台静默更新**。
- **模型源停用后的路由隔离与配置拦截**：
  - 修复停用某个模型源后，模型组编辑弹窗 (`group-form.tsx`) 依然可以选中该源下模型的 bug；
  - 后端路由缓存 (`route_cache.go`) 与数据库模型列表 (`queries.go`) 增加了对模型源 `enabled` 状态的同步校验，确保已停用源的模型无法在运行时被请求路由匹配或调用。

### 2. 同类 API 默认启用透传模式 (Default Passthrough Mode)
- **转发引擎透传策略升级**：
  - 将 `config.go` 中 `RelayConfig.Passthrough` 的默认值由 `false` 修改为 `true`；
  - 当下游客户端请求协议与上游目标模型源协议同属一类（如 OpenAI Chat -> OpenAI, Claude Messages -> Anthropic, Responses -> OpenAI Responses）时，代理层默认自动采用 **原生 Payload 透传模式**；
  - 在跳过冗余格式转换开销的同时，完整保真客户端发送的第三方扩展字段与高级特性（如 `cache_control`、`thinking` 等）。

---

## 📦 独立交叉编译产物 (Standalone Release Assets)
本次发布的独立运行包包含静态 WebUI 嵌入，无需任何外部 Node.js / 前端环境依赖：

| 平台架构 | 可执行文件名 |
| :--- | :--- |
| **Windows AMD64** | `dist/standalone/elysia-api-windows-amd64.exe` |
| **Linux AMD64** | `dist/standalone/elysia-api-linux-amd64` |
| **macOS Intel (AMD64)** | `dist/standalone/elysia-api-darwin-amd64` |
| **macOS Apple Silicon (ARM64)** | `dist/standalone/elysia-api-darwin-arm64` |
