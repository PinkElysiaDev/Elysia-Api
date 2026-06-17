# Elysia-API for Koishi

> 受 [New-API](https://github.com/Calcium-Ion/new-API) 启发，为 Koishi 打造的 AI 模型网关与编排方案：一个 Go 高性能后端 + 内嵌 WebUI 控制台，统一对接 OpenAI / Claude / Gemini 等上游。

[![npm](https://img.shields.io/npm/v/koishi-plugin-elysia-api)](https://www.npmjs.com/package/koishi-plugin-elysia-api)
[![license](https://img.shields.io/npm/l/koishi-plugin-elysia-api)](#许可证)

## 简介

Elysia-API 把“多上游 AI 模型网关”做成一个独立的 Go 后端进程，再由一个轻量 Koishi 插件负责它的生命周期与配置写入。整体由三部分组成：

- **Koishi 插件** `koishi-plugin-elysia-api`：独立后端入口插件。只负责后端的启动 / 停止 / 重启 / 热重载、bootstrap 配置写入，以及 WebUI 入口。本身不参与请求转发，可绕过插件手动控制后端。
- **Go 后端** (`backend/`)：真正的模型网关。负责鉴权、模型组与负载均衡、多格式互转、流式转发、Usage 计费、限流、pprof 诊断等。已 daemon 化，独立于 Koishi 存活。
- **WebUI** (`packages/webui/`)：React + Vite 控制台，通过 `//go:embed` 打包进 Go 二进制，开箱即用，零额外配置即可在 `/ui` 提供。

## 功能特性

- **模型组与负载均衡**：把模型组织成自定义组，支持轮询 / 顺序 / 随机策略，支持模型组级权限。
- **多格式互转**：以 Canonical Request / Response / Usage 为中间表示，在 OpenAI Chat Completions、OpenAI Responses、Claude Messages、Gemini GenerateContent 之间自动转换。
- **Responses API**：`/v1/responses` 可原生转发，也可经 Canonical 中间层转换到 Chat / Claude / Gemini 上游。
- **流式响应**：完整支持流式输出，并支持 Chat / Claude / Gemini 流转换为 Responses SSE 事件。
- **同源直发透传**：Claude / Gemini / Chat 同源请求可零损耗透传，避免不必要的格式往返。
- **Token 计费**：跟踪缓存命中、推理 token、多模态 token、内置工具调用等用量明细。
- **流量限制**：可选的请求频率控制与并发限制。
- **安全加固**：密钥加密存储、SSRF 防护、常量时间 token 比较防时序侧信道。
- **运维诊断**：WebUI 内置健康检查、内存指标、pprof 性能分析；后端支持热重载与 daemon 化。
- **独立 Usage 仪表盘**：`/usage` 提供无需登录控制台即可查看的用量统计页面（同样需要 panel access token）。

## 安装

在 Koishi 中安装插件：

```bash
koishi add elysia-api
```

或通过 npm：

```bash
npm install koishi-plugin-elysia-api
```

> 旧版的双插件架构（`elysia-api-aggregator` + `elysia-api-orchestrator`）已合并为单一插件 `koishi-plugin-elysia-api`，请迁移至新包。

## 快速开始

1. 在 Koishi 中启用 `elysia-api` 插件（默认 `autoStart`，Koishi ready 后自动拉起后端）。
2. 首次启动时若 `panelAccessToken` 留空，插件会自动生成一个并写入 bootstrap config，可在插件配置或 `configPath` 指向的 `config.json` 中查看。
3. 浏览器打开 WebUI（默认 `http://127.0.0.1:18765/ui`），用 panel access token 登录。
4. 在控制台添加 API 源、拉取模型、创建模型组。
5. 用 OpenAI 兼容端点调用模型组：

```bash
curl http://127.0.0.1:18765/v1/chat/completions \
  -H "Authorization: Bearer <你的-api-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"default","messages":[{"role":"user","content":"hi"}]}'
```

## 插件配置

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `enabled` | `true` | 启用独立后端入口插件 |
| `backendBinaryPath` | — | 自定义后端二进制路径（留空使用内置） |
| `configPath` | `data/elysia-api-standalone/config.json` | 独立后端 bootstrap config.json 路径 |
| `host` | `127.0.0.1` | 后端监听地址 |
| `port` | `18765` | 后端监听端口 |
| `panelAccessToken` | — | WebUI / 管理 API 访问令牌；留空时启动自动生成 |
| `httpTimeout` | `120` | 后端上游 HTTP 超时秒数，0 表示不限制 |
| `autoStart` | `true` | Koishi ready 后自动启动后端 |
| `restartOnConfigChange` | `true` | host/port/configPath/binary 变化时自动重启，否则仅写入配置 |
| `webuiOpenCommand` | — | 打开 WebUI 的命令，如 `xdg-open` / `open` / `start`；留空仅返回 URL |

## CLI 命令

所有命令需 authority ≥ 3。

```bash
# 后端生命周期
elysia-api.backend.start     # 启动独立后端
elysia-api.backend.stop      # 停止独立后端
elysia-api.backend.restart   # 重启独立后端
elysia-api.backend.status    # 查询后端状态 / 健康检查
elysia-api.backend.reload    # 写入 bootstrap config 并热重载（必要时重启）

# WebUI
elysia-api.webui.url         # 显示 WebUI 地址
elysia-api.webui.open        # 按配置命令打开 WebUI
```

## HTTP 端点

| 端点 | 说明 | 鉴权 |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions 入口 | API Key |
| `POST /v1/responses` | OpenAI Responses API 入口 | API Key |
| `POST /v1/messages` | Claude Messages 原生入口 | API Key |
| `POST /v1/messages/count_tokens` | Claude 兼容 token 统计 | API Key |
| `GET /v1/models` | 列出可用模型 | API Key |
| `GET /v1beta/models` / `POST /v1beta/models/*` | Gemini 兼容入口 | API Key |
| `GET /ui` | WebUI 控制台 | — |
| `GET /usage` | 独立 Usage 仪表盘 | Panel Token |
| `GET /__usage/*` | Usage 统计 / 日志 API | Panel Token |
| `GET /api/admin/*` | 管理 API（模型源、模型组、Token、运行时配置等） | Panel Token |
| `GET /debug/pprof/*` | pprof 性能分析（需启用） | Panel Token |
| `GET /health` | 健康检查 | — |

请求鉴权支持：`Authorization: Bearer <token>`、`x-api-key`、`x-goog-api-key`、`?key=`，以及 panel access token 场景下的 `panel_access_token` cookie。

## API 格式与 Responses 支持

后端以 Canonical Request / Response / Usage 作为中间表示，支持在以下格式之间转换：

| 输入 / 输出格式 | 非流式 | 流式 | 工具调用 | Usage 提取 |
| --- | --- | --- | --- | --- |
| OpenAI Chat Completions (`/v1/chat/completions`) | ✅ | ✅ | ✅ function tools | ✅ prompt / completion / cached / reasoning |
| OpenAI Responses (`/v1/responses`) | ✅ | ✅ | ✅ function 与部分 builtin tools | ✅ input / output / cached / reasoning |
| Claude Messages (`/v1/messages`) | ✅ | ✅ 转 Responses SSE | ✅ tool_use / tool_result | ✅ input / output / cache read / cache creation |
| Gemini GenerateContent | ✅ | ✅ 转 Responses SSE | ✅ functionCall / functionResponse | ✅ prompt / candidates / thoughts / cached / modality details |

`/v1/responses` 可根据模型端点能力和配置选择处理方式，默认推荐 `upstreamMode: "auto"`：

- **native**：上游原生支持 Responses 时直接请求上游 `/responses`；显式设置该模式会严格要求上游声明 Responses 支持。
- **transform**：上游不支持 Responses 时，将请求转换为目标上游格式，再将响应转换回 Responses 格式。
- **auto**：优先使用原生 Responses；否则根据模型 `endpoints.responses` / `endpoints.chatCompletions` / `endpoints.claudeMessages` / `endpoints.geminiGenerateContent` 自动选择转换目标。

Claude-only 上游可配置 `platform: "anthropic"` 或 `endpoints.claudeMessages: true`，Codex 等只发送 Responses 请求的客户端会自动转换到 Claude Messages。

配置示例：

```json
{
  "responses": {
    "enabled": true,
    "upstreamMode": "auto",
    "transformUnsupportedBehavior": "error",
    "passThroughUnknownFields": true
  },
  "usage": {
    "estimateWhenMissing": true,
    "charsPerToken": 4,
    "defaultOutputTokenEstimate": 1024,
    "imageInputTokenEstimate": 300,
    "fileInputTokenEstimatePerKB": 128
  },
  "modelGroups": [
    {
      "id": "default",
      "name": "default",
      "models": [
        {
          "id": "gpt",
          "name": "gpt-4.1",
          "platform": "openai",
          "baseUrl": "https://api.openai.com/v1",
          "endpoints": { "chatCompletions": true, "responses": true }
        }
      ]
    }
  ]
}
```

### Usage 统计字段

用量记录会尽量保留各平台返回的原始 token 语义，并统一输出到 dashboard / logs：

- `inputTokens` / `outputTokens` / `totalTokens`
- `cacheHitTokens`
- `usageDetail.cachedInputTokens` / `usageDetail.cacheCreationInputTokens`
- `usageDetail.reasoningTokens`
- `usageDetail.textInputTokens` / `usageDetail.textOutputTokens`
- `usageDetail.imageInputTokens` / `usageDetail.imageOutputTokens`
- `usageDetail.audioInputTokens` / `usageDetail.audioOutputTokens`
- `usageDetail.toolUseTokens`
- `builtinToolUsage.webSearchCalls` / `fileSearchCalls` / `imageGenerationCalls`
- `usageSource`
- `estimated` / `estimatedTokens`

当上游没有返回 usage 且开启 `usage.estimateWhenMissing` 时，后端会基于 Canonical 请求内容进行估算。估算值会标记为 `estimated=true`，并单独计入 `estimatedTokens`，不会污染 provider 返回的真实 token 总量。

## pprof 性能分析

在 WebUI「诊断」页开启 pprof 并重启后端后，以下端点可用（需 panel access token，浏览器从 WebUI 跳转时通过 `panel_access_token` cookie 自动携带）：

- `/debug/pprof/` — 索引页
- `/debug/pprof/heap`、`/debug/pprof/goroutine`、`/debug/pprof/allocs`、`/debug/pprof/block`、`/debug/pprof/mutex`、`/debug/pprof/threadcreate` — 各类采样
- `/debug/pprof/profile`、`/debug/pprof/trace` — CPU profile / 执行追踪
- `/debug/pprof/cmdline`、`/debug/pprof/symbol`

## 架构

```
┌──────────────────────────────────────────────┐
│  Koishi  +  koishi-plugin-elysia-api         │
│  （生命周期 / 配置写入 / WebUI 入口）        │
└──────────────────────┬───────────────────────┘
                       │ spawn / 热重载
                       ▼
┌──────────────────────────────────────────────┐
│  Go 后端（daemon，独立于 Koishi 存活）       │
│  鉴权 · 模型组 / 负载均衡 · 多格式互转        │
│  流式转发 · Usage 计费 · 限流 · pprof         │
│  内嵌 WebUI（//go:embed → /ui）              │
└──────┬──────────────┬──────────────┬─────────┘
       │              │              │
   ▼───▼          ▼───▼          ▼───▼
 OpenAI        Claude         Gemini   …
```

## 项目结构

```
elysia-api/
├── backend/                # Go 后端（网关本体）
│   ├── config/             # 配置加载 / 热重载 / 加密密钥
│   ├── relay/              # 上游转发 / 格式转换 / Canonical 中间表示
│   ├── server/             # HTTP 路由 / 鉴权中间件 / Usage 仪表盘 / 管理 API
│   ├── storage/            # 持久化
│   └── webui/              # 内嵌 WebUI 静态资源（//go:embed all:dist）
├── packages/
│   ├── elysia-api/         # Koishi 插件（后端入口 / 生命周期 / CLI）
│   └── webui/              # React + Vite 控制台源码
├── docs/                   # 部署、WebUI 规范、数据模型等文档
└── scripts/                # 后端 / WebUI 构建脚本
```

## 开发与构建

```bash
# 构建 WebUI 并同步到后端内嵌目录，再编译各平台 Go 二进制
yarn build:backend

# 构建所有 Koishi 插件包
yarn build

# 仅开发 WebUI（Vite dev server）
cd packages/webui && yarn dev

# Lint
yarn lint
```

WebUI 源码在 `packages/webui`，构建产物会被 `scripts/build-backend.sh` 拷贝到 `backend/webui/dist`，再由 `//go:embed all:dist` 打包进二进制。也可通过配置 `webuiDir` 指向外部目录覆盖内嵌资源，便于开发期热替换。

## 许可证

MIT
