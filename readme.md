# Elysia-API for Koishi

> 受 [New-API](https://github.com/Calcium-Ion/new-API) 启发，为 Koishi 打造的 AI 模型网关与编排方案：一个 Go 高性能后端 + 内嵌 WebUI 控制台，统一对接 OpenAI / Claude / Gemini 等上游。

[![npm](https://img.shields.io/npm/v/koishi-plugin-elysia-api)](https://www.npmjs.com/package/koishi-plugin-elysia-api)
[![license](https://img.shields.io/npm/l/koishi-plugin-elysia-api)](#许可证)

## 简介

Elysia-API 是一个多上游 AI 模型网关。它把请求鉴权、模型组路由、负载均衡、OpenAI / Claude / Gemini 格式转换、流式转发、Usage 统计和 WebUI 管理能力集中在一个独立 Go 后端中。

Koishi 插件不是请求转发层，而是一个可选入口：它只负责写入 bootstrap 配置、启动 / 停止 / 重启后端进程、执行热重载，以及提供 WebUI 打开入口。因此，Elysia-API 可以作为 Koishi 插件使用，也可以完全不依赖 Koishi，在 Windows / Linux 等环境中独立部署。

## 项目架构

### 组件关系

- **Go 后端** (`backend/`)：网关本体。负责鉴权、模型组与负载均衡、多格式互转、流式转发、Usage 计费、限流、pprof 诊断、SQLite 持久化等。后端可独立于 Koishi 存活。
- **WebUI** (`packages/webui/`)：React + Vite 控制台，通过 `//go:embed` 打包进 Go 二进制。默认访问路径为 `/ui`，独立部署时不需要额外部署前端文件。
- **Koishi 插件** `koishi-plugin-elysia-api`：后端入口插件。只负责后端生命周期、bootstrap 配置写入和 WebUI 入口。本身不参与请求代理、维护模型组或聚合模型。

```text
┌──────────────────────────────────────────────┐
│  可选入口：Koishi + koishi-plugin-elysia-api │
│  生命周期 / 配置写入 / WebUI 入口            │
└──────────────────────┬───────────────────────┘
                       │ spawn / reload / shutdown
                       ▼
┌──────────────────────────────────────────────┐
│  Go 后端（网关本体）                         │
│  鉴权 · 模型组 / 负载均衡 · 多格式互转        │
│  流式转发 · Usage 计费 · 限流 · pprof         │
│  SQLite 持久化 · 内嵌 WebUI（//go:embed）     │
└──────┬──────────────┬──────────────┬─────────┘
       │              │              │
   ▼───▼          ▼───▼          ▼───▼
 OpenAI        Claude         Gemini   …
```

### 项目结构

```text
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

## 部署与启动

Elysia-API 支持三种部署方式：

| 方式 | 适用场景 | 启动入口 | WebUI |
| --- | --- | --- | --- |
| 独立后端部署 | 不依赖 Koishi，在 Windows / Linux 上单独运行网关 | `elysia-backend(.exe) --config config.json` | `http://<host>:<port>/ui/` |
| Koishi 入口插件部署 | 在 Koishi 中管理后端启动、停止、重载和 WebUI 入口 | `koishi-plugin-elysia-api` | 默认 `http://127.0.0.1:18765/ui` |
| 源码开发启动 | 开发后端、WebUI 或插件包 | `yarn build:backend` / `go build` / `yarn dev` | 内嵌 WebUI 或 Vite dev server |

### 独立后端部署（不依赖 Koishi）

独立部署只需要后端二进制和一个 bootstrap `config.json`。WebUI 已经嵌入后端二进制，不需要单独部署前端文件。后端启动后，模型源、模型组、Relay API Token 和 Usage 数据会写入 SQLite。

启动时建议显式传入 `--config config.json`。如果不传 `--config`，后端会读取当前工作目录下的 `config.json`。

源码构建后的二进制位于 `packages/elysia-api/assets/bin/`：

| 平台 | 二进制文件名 |
| --- | --- |
| Windows amd64 | `elysia-backend.exe` |
| Linux amd64 | `elysia-backend-linux` |
| macOS Intel | `elysia-backend-darwin-amd64` |
| macOS Apple Silicon | `elysia-backend-darwin-arm64` |

准备一个最小 `config.json`，并将它和二进制放在同一目录：

```json
{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "change-me",
  "databasePath": "elysia-api.sqlite3",
  "logLevel": "info",
  "httpTimeout": 120,
  "secretKeyPath": ".master-key"
}
```

#### Windows

```powershell
cd C:\elysia-api
.\elysia-backend.exe --config .\config.json
```

启动后访问：

```text
http://127.0.0.1:8765/ui/
```

使用 `config.json` 中的 `panelAccessToken` 登录。生产环境建议将进程交给 Windows 服务管理器托管，例如 NSSM、WinSW、计划任务或其他进程守护工具。仓库当前不提供 Windows service 安装脚本。

#### Linux

```bash
mkdir -p /opt/elysia-api
cp elysia-backend-linux /opt/elysia-api/
cp config.json /opt/elysia-api/
chmod +x /opt/elysia-api/elysia-backend-linux
cd /opt/elysia-api
./elysia-backend-linux --config ./config.json
```

启动后访问：

```text
http://127.0.0.1:8765/ui/
```

生产环境建议用 systemd、supervisord、Docker 或其他进程管理器托管。最小 systemd service 结构如下：

```ini
[Unit]
Description=Elysia-API Backend
After=network.target

[Service]
WorkingDirectory=/opt/elysia-api
ExecStart=/opt/elysia-api/elysia-backend-linux --config /opt/elysia-api/config.json
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

#### macOS

```bash
mkdir -p /opt/elysia-api
cp elysia-backend-darwin-arm64 /opt/elysia-api/
cp config.json /opt/elysia-api/
chmod +x /opt/elysia-api/elysia-backend-darwin-arm64
cd /opt/elysia-api
./elysia-backend-darwin-arm64 --config ./config.json
```

Intel 设备使用 `elysia-backend-darwin-amd64`。macOS 生产托管可使用 launchd 或其他进程管理工具。

### Koishi 入口插件部署

如果希望在 Koishi 中安装并管理后端入口，可在 Koishi 的插件市场中搜索 `koishi-plugin-elysia-api` 插件并安装。

在 Koishi 中安装插件：

```bash
koishi add elysia-api
```

或通过 npm 进行安装：

```bash
npm install koishi-plugin-elysia-api
```

> 旧版的双插件架构（`elysia-api-aggregator` + `elysia-api-orchestrator`）已合并为单一插件 `koishi-plugin-elysia-api`，请迁移至新包。

启用流程：

1. 在 Koishi 中启用 `elysia-api` 插件（默认 `autoStart`，Koishi ready 后自动拉起后端）。
2. 首次启动时若 `panelAccessToken` 留空，插件会自动生成一个并写入 bootstrap config，可在插件配置或 `configPath` 指向的 `config.json` 中查看。
3. 浏览器打开 WebUI（默认 `http://127.0.0.1:18765/ui`），用 panel access token 登录。
4. 在控制台添加 API 源、拉取模型、创建模型组。
5. 用 OpenAI 兼容端点调用模型组。

```bash
curl http://127.0.0.1:18765/v1/chat/completions \
  -H "Authorization: Bearer <你的-api-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"default","messages":[{"role":"user","content":"hi"}]}'
```

### 源码开发启动

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

## 配置与参数说明

### Bootstrap `config.json`

`config.json` 只保存后端启动所需的 bootstrap 字段。模型源、模型缓存、模型组、Relay API Token、Usage 记录和系统日志存储在 SQLite 中。

| 字段 | 默认值 / 示例 | 说明 |
| --- | --- | --- |
| `host` | `127.0.0.1` | 后端监听地址。仅本机访问用 `127.0.0.1`；需要局域网或反向代理访问时按实际环境配置。 |
| `port` | `8765` | 后端监听端口。独立后端示例使用 `8765`；Koishi 插件默认使用 `18765`。 |
| `panelAccessToken` | `change-me` | WebUI 和 `/api/admin/*` 管理 API 的访问令牌。 |
| `databasePath` | `elysia-api.sqlite3` | SQLite 数据库路径。相对路径会按 `config.json` 所在目录解析。 |
| `logLevel` | `info` | 日志级别，常用 `info` 或 `debug`。 |
| `httpTimeout` | `120` | 上游 HTTP 超时秒数，`0` 表示不限制。 |
| `secretKeyPath` | `.master-key` | SQLite 中敏感字段的加密主密钥文件路径。相对路径会按 `config.json` 所在目录解析。 |
| `webuiDir` | 空 | 可选。留空使用内嵌 WebUI；需要用自定义前端构建产物覆盖时，填入静态资源目录。 |
| `enablePprof` | `false` | 可选。启用后开放受 panel token 保护的 pprof 端点。 |
| `maxBodyBytes` | `33554432` | 可选。请求体大小上限。 |

也可以通过环境变量 `ELYSIA_API_MASTER_KEY` 提供主密钥。生产环境中，如果数据库目录会被整体备份或打包，建议把 `secretKeyPath` 放在单独受保护的位置，或使用 `ELYSIA_API_MASTER_KEY`。

### WebUI 首次配置

1. 启动后端。
2. 打开 `/ui/`，使用 `panelAccessToken` 登录。
3. 在 WebUI 中添加 API 源。
4. 拉取模型或填写手动模型。
5. 创建模型组，例如 `default`。
6. 创建 Relay API Token。
7. 用 OpenAI 兼容端点调用模型组。

调用示例：

```bash
curl http://127.0.0.1:8765/v1/chat/completions \
  -H "Authorization: Bearer <你的-api-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"default","messages":[{"role":"user","content":"hi"}]}'
```

`panelAccessToken` 只用于 WebUI 和管理 API。调用 `/v1/*` 时应使用 WebUI 中创建的 Relay API Token。

### Koishi 插件配置

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `enabled` | `true` | 启用独立后端入口插件 |
| `backendBinaryMode` | `bundled` | 后端二进制来源，可选择内置二进制或自定义路径 |
| `backendBinaryPath` | — | 自定义后端二进制路径，仅在使用自定义二进制时填写 |
| `configPath` | `data/elysia-api-standalone/config.json` | 独立后端 bootstrap config.json 路径 |
| `host` | `127.0.0.1` | 后端监听地址 |
| `port` | `18765` | 后端监听端口 |
| `panelAccessToken` | — | WebUI / 管理 API 访问令牌；留空时启动自动生成 |
| `httpTimeout` | `120` | 后端上游 HTTP 超时秒数，0 表示不限制 |
| `autoStart` | `true` | Koishi ready 后自动启动后端 |
| `restartOnConfigChange` | `true` | host/port/configPath/binary 变化时自动重启，否则仅写入配置 |
| `webuiOpenCommand` | — | 打开 WebUI 的命令，如 `xdg-open` / `open` / `start`；留空仅返回 URL |

### Koishi CLI 命令

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

## 运维与数据管理

### 热重载、关停与健康检查

- `GET /health`：公开健康检查端点，会检查 SQLite 状态，可用于负载均衡、systemd watchdog 或容器探针。
- `GET /api/admin/health`：管理健康检查端点，需要 `Authorization: Bearer <panelAccessToken>`，会返回基本内存指标。
- `POST /api/admin/reload`：管理侧热重载端点，需要 panel token。
- `POST /__reload`：本机 loopback 热重载端点，仅允许本机调用。
- `POST /__shutdown`：本机 loopback 优雅关停端点，仅允许本机调用。

修改 `host`、`port`、`databasePath` 或 `enablePprof` 后通常需要重启。模型源、模型组、Relay API Token、Usage 等运行时数据写入 SQLite，可通过 WebUI 修改，不需要编辑 `config.json`。

### SQLite、WAL 与备份

后端在 `databasePath` 指定位置保存运行时数据。启动时会应用：

- `PRAGMA journal_mode=WAL`
- `PRAGMA busy_timeout=5000`
- `PRAGMA foreign_keys=ON`
- `PRAGMA synchronous=NORMAL`

运行后通常会看到：

- `elysia-api.sqlite3`
- `elysia-api.sqlite3-wal`
- `elysia-api.sqlite3-shm`

备份时应使用 SQLite backup 工具，或先停止后端，再同时复制上述数据库文件。若启用了密钥文件，也必须备份并保护 `secretKeyPath` 指向的 `.master-key`。丢失主密钥后，SQLite 中加密保存的上游 API key 和 Relay API Token 无法解密。

### Panel Token 重置

如果忘记 `panelAccessToken`：

1. 停止后端。
2. 编辑 bootstrap `config.json` 中的 `panelAccessToken`。
3. 重启后端。

Relay API Token 存储在 SQLite 中。恢复 panel access 后，可通过 WebUI 或 `/api/admin/api-tokens` 修改。

### 旧配置迁移

旧配置中包含的 `tokens` 和 `modelGroups` 会作为兼容数据在启动时导入 SQLite。新安装只应在 `config.json` 中保留 bootstrap 字段，例如 host、port、database path、panel access token、日志和诊断配置。

后端不再依赖 Koishi heartbeat 自行退出。是否随系统启动、崩溃后是否重启，应交给 OS、服务管理器、Docker 或可选的 Koishi 入口插件处理。

## HTTP 端点与鉴权

请求鉴权支持：`Authorization: Bearer <token>`、`x-api-key`、`x-goog-api-key`、`?key=`，以及 panel access token 场景下的 `panel_access_token` cookie。

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

新安装推荐通过 WebUI 写入模型源、模型缓存、模型组和 API Token。上述 `modelGroups` 示例用于说明配置结构和兼容行为；旧配置中的 `tokens`、`modelGroups` 会在启动时导入 SQLite。

## Usage 统计字段

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

- `/debug/pprof/`：索引页
- `/debug/pprof/heap`、`/debug/pprof/goroutine`、`/debug/pprof/allocs`、`/debug/pprof/block`、`/debug/pprof/mutex`、`/debug/pprof/threadcreate`：各类采样
- `/debug/pprof/profile`、`/debug/pprof/trace`：CPU profile / 执行追踪
- `/debug/pprof/cmdline`、`/debug/pprof/symbol`

## 许可证

MIT
