# Elysia API WebUI

Elysia-API 的可视化管理控制台。纯前端单页应用，所有数据来自后端 `/api/admin/*` REST API。

技术栈：React 18 + Vite + TypeScript + TailwindCSS + Radix UI + SWR + Recharts + React Router（Hash 模式）。
主题支持日间（粉 / 白）与夜间（粉 / 黑），跟随系统并可手动切换，选择持久化在 localStorage。

## 功能

控制台围绕「配置上游、组装模型、对外发 Key、观测用量」四件事组织，对应以下页面：

- **概览**：后端运行状态（服务状态、SQLite、内存、GC）、模型源 / 模型 / 模型组规模，以及近 7 天用量速览（成功率、Token 总量与缓存命中率、平均耗时与首字、平均吞吐 TPM/RPM）。
- **模型源**：管理上游供应商（base URL、API Key、协议类型）。支持自动拉取模型或手动登记模型，保存后自动刷新一次。
- **模型缓存**：查看各源拉取到的全部模型及其能力（类型、最大 token、视觉 / 工具 / 结构化输出、思考模式、可用性）。
- **模型组**：把多个模型组合成对客户端暴露的逻辑模型，配置路由策略（轮询 / 顺序 / 随机）、重试、并发与每日限额。
- **API Tokens**：管理业务调用方的访问令牌，可限定每个 Token 可访问的模型组；令牌默认脱敏，按需查看明文。
- **Usage 统计**：按时间范围与模型组 / 模型 / 调用方多选条件汇总请求与 token 用量。卡片展示成功率、Token 总量与缓存命中率、平均耗时与首字、平均吞吐；图表展示成功 / 失败分布与「输入 / 输出 / 缓存命中」的累计 token 分布。
- **Usage 日志**：逐条请求明细，支持按时间范围、模型组、模型、调用方（均为带搜索的多选）与状态码筛选。详情弹窗展示完整链路（下游请求 → 后端转发 → 上游回传 → 返回下游）与 token 用量，可导出完整 JSON。
- **系统日志**：刷新、错误等后端事件，按级别筛选。
- **运行配置**：查看与调整运行参数（日志级别、HTTP 超时等）；改 host / port 会提示需要重启。
- **诊断**：内存指标与 pprof 入口。

交互约定：所有破坏性操作（删除模型源 / 模型组 / Token、重置 Usage）均二次确认；所有列表均有加载 / 空 / 错误三态；密钥类输入保存后即清空明文。

## 开发

```bash
# 先启动后端（默认 127.0.0.1:8765）
npm run dev          # Vite dev server，端口 5273，已代理 /api /v1 /health 到后端
```

登录使用后端 bootstrap `config.json` 中的 `panelAccessToken`。

## 构建

```bash
npm run build        # 产物输出到 dist/，base 为 /ui/
```

将 `dist/` 部署为后端 `webuiDir`，后端通过 `gin.Static("/ui", webuiDir)` 提供服务，
访问 `http://<host>:<port>/ui/`。后端无 history fallback，故前端使用 HashRouter。
