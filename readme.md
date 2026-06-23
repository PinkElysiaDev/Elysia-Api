# Elysia-API

Elysia-API 是一个可独立部署的多上游 AI 模型网关。它把请求鉴权、模型组路由、负载均衡、OpenAI / Claude / Gemini 格式转换、流式转发、Usage 统计和 WebUI 管理能力集中在一个 Go 后端中。

WebUI 通过 `//go:embed` 嵌入后端二进制，默认在 `/ui/` 提供。运行时配置使用 bootstrap `config.json`，模型源、模型组、Relay API Token、Usage 和系统日志存储在 SQLite 中。

## 项目结构

```text
elysia-api/
├── backend/                # Go 后端（网关本体）
│   ├── config/             # 配置加载 / 热重载 / 加密密钥
│   ├── relay/              # 上游转发 / 格式转换 / Canonical 中间表示
│   ├── server/             # HTTP 路由 / 鉴权中间件 / Usage 仪表盘 / 管理 API
│   ├── storage/            # SQLite 持久化
│   └── webui/              # 内嵌 WebUI 静态资源（//go:embed all:dist）
├── packages/webui/         # React + Vite 控制台源码
├── docs/                   # 部署、WebUI API、数据模型等文档
├── scripts/                # 独立后端发行构建脚本
└── config.json.example     # 最小 bootstrap 配置模板
```

## 功能特性

- 模型组与负载均衡：支持轮询、顺序、随机策略和模型组级权限。
- 多格式互转：以 Canonical Request / Response / Usage 为中间表示，在 OpenAI Chat Completions、OpenAI Responses、Claude Messages、Gemini GenerateContent 之间转换。
- Responses API：`/v1/responses` 可原生转发，也可转换到 Chat / Claude / Gemini 上游。
- 流式响应：支持流式转发，并支持 Chat / Claude / Gemini 流转换为 Responses SSE 事件。
- 同源直发透传：当客户端输入格式与上游格式一致时，可跳过不必要的中间转换。
- Usage 统计：记录缓存命中、推理 token、多模态 token、内置工具调用、请求/响应摘要和重试事件。
- 流量限制：支持模型组级并发和每日请求/token 限制。
- 安全加固：敏感字段加密存储、SSRF 防护、常量时间 token 比较。
- 运维诊断：内置健康检查、系统日志、pprof、Usage 仪表盘和热重载端点。

## 快速开始

### 通用配置

从仓库根目录的 `config.json.example` 创建运行时配置，至少修改 `panelAccessToken`：

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

`databasePath` 和 `secretKeyPath` 使用相对路径时，会按 `config.json` 所在目录解析。

### Windows

将 `elysia-api-windows-amd64.exe` 和 `config.json` 放在同一目录后运行：

```powershell
.\elysia-api-windows-amd64.exe --config .\config.json
```

如果 `config.json` 与 exe 位于同一目录，也可以直接双击 exe；未传 `--config` 时程序会读取当前目录下的 `config.json`。

### Linux

将 `elysia-api-linux-amd64` 和 `config.json` 放在同一目录后运行：

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

### macOS

Apple Silicon 使用 `elysia-api-darwin-arm64`，Intel 设备使用 `elysia-api-darwin-amd64`：

```bash
chmod +x ./elysia-api-darwin-arm64
./elysia-api-darwin-arm64 --config ./config.json
```

### WebUI 初始化

后端启动后打开 WebUI：

```text
http://127.0.0.1:8765/ui/
```

使用 `panelAccessToken` 登录后，在 WebUI 中添加模型源、拉取模型、创建模型组并创建 Relay API Token。随后可通过 OpenAI 兼容端点调用模型组：

```bash
curl http://127.0.0.1:8765/v1/chat/completions \
  -H "Authorization: Bearer <你的-relay-api-token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"default","messages":[{"role":"user","content":"hi"}]}'
```

## 构建

首次构建或依赖变更后先安装依赖：

```bash
yarn install
```

构建 WebUI、同步嵌入资源并生成多平台独立发行物：

```bash
yarn build
```

二进制产物位于 `dist/standalone/`：

| 平台 | 文件 |
| --- | --- |
| Windows amd64 | `elysia-api-windows-amd64.exe` |
| Linux amd64 | `elysia-api-linux-amd64` |
| macOS Intel | `elysia-api-darwin-amd64` |
| macOS Apple Silicon | `elysia-api-darwin-arm64` |

开发 WebUI：

```bash
cd packages/webui
yarn dev
```

Vite dev server 默认代理到 `http://127.0.0.1:8765`。

## 配置

`config.json` 只保存启动所需 bootstrap 字段。模型源、模型组、Relay API Token、Usage 和系统日志存储在 SQLite。

| 字段 | 说明 |
| --- | --- |
| `host` | 后端监听地址。仅本机访问使用 `127.0.0.1`。 |
| `port` | 后端监听端口，默认示例为 `8765`。 |
| `panelAccessToken` | WebUI 和 `/api/admin/*` 管理 API 的访问令牌。 |
| `databasePath` | SQLite 数据库路径。相对路径按 `config.json` 所在目录解析。 |
| `logLevel` | 日志级别，常用 `info` 或 `debug`。 |
| `httpTimeout` | 上游 HTTP 超时秒数，`0` 表示不限制。 |
| `secretKeyPath` | SQLite 敏感字段加密主密钥文件路径。相对路径按 `config.json` 所在目录解析。 |
| `webuiDir` | 可选。留空使用内嵌 WebUI；填写后用外部静态资源目录覆盖。 |
| `enablePprof` | 可选。启用受 panel token 保护的 pprof 端点。 |
| `maxBodyBytes` | 可选。请求体大小上限。 |

也可以通过环境变量 `ELYSIA_API_MASTER_KEY` 提供主密钥。生产环境中，如果数据库目录会被整体备份或打包，建议把 `secretKeyPath` 放在单独受保护的位置，或使用环境变量注入。

## 运维

- `GET /health`：公开健康检查端点。
- `GET /api/admin/health`：管理健康检查端点，需要 panel token。
- `POST /api/admin/reload`：管理侧热重载端点，需要 panel token。
- `POST /__reload`：本机 loopback 热重载端点。
- `POST /__shutdown`：本机 loopback 优雅关停端点。

修改 `host`、`port`、`databasePath` 或 `enablePprof` 后通常需要重启。生产环境建议使用 systemd、Windows 服务管理器、supervisord、Docker 或其他进程管理器托管后端。

## 数据备份

SQLite 数据库使用 WAL 模式。运行后通常会看到：

- `elysia-api.sqlite3`
- `elysia-api.sqlite3-wal`
- `elysia-api.sqlite3-shm`

备份时应使用 SQLite backup 工具，或先停止后端，再同时复制上述数据库文件。若启用了密钥文件，也必须备份并保护 `secretKeyPath` 指向的 `.master-key`。丢失主密钥后，SQLite 中加密保存的上游 API key 和 Relay API Token 无法解密。

## HTTP 端点

| 端点 | 说明 | 鉴权 |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions 入口 | Relay API Token |
| `POST /v1/responses` | OpenAI Responses API 入口 | Relay API Token |
| `POST /v1/messages` | Claude Messages 原生入口 | Relay API Token |
| `POST /v1/messages/count_tokens` | Claude 兼容 token 统计 | Relay API Token |
| `GET /v1/models` | 列出可用模型组 | Relay API Token |
| `GET /v1beta/models` / `POST /v1beta/models/*` | Gemini 兼容入口 | Relay API Token |
| `GET /ui/` | WebUI 控制台 | 页面登录 |
| `GET /usage` | 独立 Usage 仪表盘 | Panel Token |
| `GET /__usage/*` | Usage 统计 / 日志 API | Panel Token |
| `GET /api/admin/*` | 管理 API | Panel Token |
| `GET /debug/pprof/*` | pprof 性能分析（需启用） | Panel Token |
| `GET /health` | 健康检查 | 无 |

请求鉴权支持 `Authorization: Bearer <token>`、`x-api-key`、`x-goog-api-key` 和 `?key=`。Panel token 场景还支持 `panel_access_token` cookie。

## 旧配置迁移

旧配置中包含的 `tokens` 和 `modelGroups` 会作为兼容数据在启动时导入 SQLite。新安装只应在 `config.json` 中保留 bootstrap 字段，例如 host、port、database path、panel access token、日志和诊断配置。
