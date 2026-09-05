<h1 align="center">Elysia-API</h1>

<p align="center">
  <a href="https://github.com/PinkElysiaDev/Elysia-Api/tags"><img src="https://img.shields.io/github/v/tag/PinkElysiaDev/Elysia-Api?sort=semver&amp;style=flat-square&amp;label=tag" alt="最新 tag"></a>
  <a href="https://github.com/PinkElysiaDev/Elysia-Api/stargazers"><img src="https://img.shields.io/github/stars/PinkElysiaDev/Elysia-Api?style=flat-square&amp;logo=github" alt="GitHub stars"></a>
  <a href="./backend/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/PinkElysiaDev/Elysia-Api?filename=backend%2Fgo.mod&amp;style=flat-square" alt="Go 版本"></a>
</p>

<p align="center">
  简体中文 · <a href="./README.md">English</a>
</p>

Elysia-API 是一个可独立部署的多上游 AI 模型网关。它把请求鉴权、模型组路由、负载均衡、OpenAI / Claude / Gemini 格式转换、流式转发、Usage 统计和 WebUI 管理能力集中在一个 Go 后端中。

WebUI 通过 `//go:embed` 嵌入后端二进制，默认在 `/ui/` 提供。运行时配置使用 bootstrap `config.json`，模型源、模型组、Relay API Token、Usage 和系统日志存储在 SQLite 中。

## 项目结构

```text
elysia-api/
├── backend/                # Go 后端（网关本体）
│   ├── config/             # 配置加载 / 热重载 / 加密密钥
│   ├── relay/              # 上游转发 / 格式转换 / Maheshvara 核心协议
│   ├── server/             # HTTP 路由 / 鉴权中间件 / 管理 API / Usage 记录
│   ├── storage/            # SQLite 持久化
│   └── webui/              # 内嵌 WebUI 静态资源（//go:embed all:dist）
├── packages/webui/         # React + Vite 控制台源码
├── docs/                   # 部署、WebUI API、数据模型等文档
├── scripts/                # 独立后端发行构建脚本
└── config.json.example     # 最小 bootstrap 配置模板
```

## 功能特性

- 模型组与负载均衡：支持轮询、顺序、随机策略和模型组级权限。
- 多格式互转：以 Maheshvara Request / Response / Usage 为唯一核心，在 OpenAI Chat Completions、OpenAI Responses、Claude Messages、Gemini GenerateContent 之间转换。
- Responses API：`/v1/responses` 可原生转发，也可转换到 Chat / Claude / Gemini 上游。
- 流式响应：四种内建协议和自定义协议均通过 Maheshvara 状态化 decoder / renderer 转换 SSE。
- 同协议透传：四种协议（Chat Completions、Responses、Claude Messages、Gemini GenerateContent）在客户端与上游线路协议同源时自动零转换透传，仅改写 model 名，其余字段原样保留。
- Usage 统计：记录缓存命中、推理 token、多模态 token、内置工具调用、请求/响应摘要和重试事件。
- 流量限制：支持模型组级并发和每日请求/token 限制。
- 安全加固：敏感字段加密存储、SSRF 防护、常量时间 token 比较。
- 运维诊断：内置健康检查、系统日志、pprof、WebUI 用量面板和热重载端点。
- 多 Key 调度：一个模型源可配置多个 API Key，按轮询 / 随机 / 优先级策略调度；**逐 Key 权限自动发现**——拉取时每个 Key 独立请求模型列表，自动得到各自分组的可用模型集（可在面板按 Key 勾选启停），调度时保证不会切到无权限的 Key。
- 模型能力目录：内置 models.dev 快照（零配置开箱即用），后台定期在线更新并落盘缓存（models.dev 不可达时自动回退 jsDelivr 镜像）；拉取模型时自动回填视觉 / 工具 / 结构化输出 / 思考模式 / 上下文长度等能力。模型组按成员推导能力并实际生效（不支持视觉的组自动剥离图片，不支持工具的组拒绝工具请求）。
- 异步后台拉取：模型拉取为后台任务，发起即返回、页面不阻塞，进度与结果实时轮询展示；拉取中的源自动锁定相关操作防止误冲突。
- 自定义拉取地址：模型列表端点与请求转发端点不同源（域名 / 端口 / 协议不一致）的站点可单独配置拉取地址。
- 模型级管理：拉取的模型支持单个编辑 / 启停 / 删除与检索，刷新采用保留式合并（手动模型与用户编辑永不丢失）。

## 快速开始

预编译二进制通过 [GitHub Releases](https://github.com/PinkElysiaDev/Elysia-Api/releases/latest) 发布。下载对应平台的程序和 `SHA256SUMS`；如需从源码重建，参考下文「构建」一节。

| 平台 | Release 文件 |
| --- | --- |
| Windows amd64 | `elysia-api-windows-amd64.exe` |
| Windows arm64 | `elysia-api-windows-arm64.exe` |
| Linux amd64 | `elysia-api-linux-amd64` |
| Linux arm64 | `elysia-api-linux-arm64` |
| macOS App（Intel / Apple Silicon 通用） | `elysia-api-macos.dmg` |

### 通用配置

首次启动时若二进制同目录下没有 `config.json`，后端会自动写入一份默认配置，其中 `panelAccessToken` 用 `crypto/rand` 随机生成（非 `change-me` 占位符），生成的 token 与配置路径会打印在启动日志里——用它登录面板后请及时轮换。无需手工建文件，下方的模板仅供参考。

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

如果 `config.json` 与 exe 位于同一目录，也可以直接双击 exe；未传 `--config` 时程序会读取当前目录下的 `config.json`。首次启动若该文件不存在会自动创建（带随机 `panelAccessToken`，见启动日志）。

### Linux

将 `elysia-api-linux-amd64` 和 `config.json` 放在同一目录后运行：

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

### macOS

从 Release 下载 `elysia-api-macos.dmg`，双击打开后将 `ElysiaApi` 拖入 `Applications` 快捷方式即完成安装，之后从启动台双击运行：
- 首次启动自动生成配置与随机 `panelAccessToken`，数据统一保存在 `~/Library/Application Support/ElysiaApi/`（config.json、SQLite、`.master-key`、`elysia-api.log`）。
- 支持应用内更新：有新版本时窗口左下角出现「立即更新」按钮，一键完成下载（DMG）、sha256 校验、提取与替换；也可通过菜单栏「检查更新…」手动触发。更新只替换内嵌后端，配置与数据不受影响。
- 默认端口 `8765`，若被占用会自动改用 `8799` 起的空闲端口（实际端口见菜单栏状态行与面板地址）。

> CI 产物为 ad-hoc 签名。首次打开若被 Gatekeeper 拦截，执行
> `xattr -d com.apple.quarantine /Applications/ElysiaApi.app` 后再打开即可。

### Docker

先在仓库根目录构建镜像：

```bash
docker build -t elysia-api:local .
```

最小运行命令：

```bash
docker run -d \
  -p 8765:8765 \
  -v elysia-data:/data \
  -e ELYSIA_API_HOST=0.0.0.0 \
  elysia-api:local
```

首次启动时，如果配置文件不存在，后端会在数据卷中生成配置和随机 `panelAccessToken`。访问 `http://127.0.0.1:8765/ui/`，使用启动日志中的 token 登录。

如需公网访问请配置：`ELYSIA_API_HOST=0.0.0.0`，默认`127.0.0.1`。

如需通过环境变量提供数据库主密钥，在运行命令中增加 `-e ELYSIA_API_MASTER_KEY=...`。

推荐的 Compose 配置：

```yaml
services:
  elysia-api:
    image: elysia-api:local
    container_name: elysia-api
    restart: unless-stopped # 自启动
    init: true
    read_only: true
    tmpfs:
      - /tmp:size=64m,mode=1777
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    ports:
      - "${ELYSIA_HTTP_PORT:-8765}:8765"
    environment:
      ELYSIA_API_HOST: 0.0.0.0
      # 使用外部数据库主密钥时取消注释，并通过安全的环境管理方式注入：
      # ELYSIA_API_MASTER_KEY: your-master-key
    volumes:
      - elysia-data:/data

volumes:
  elysia-data:
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
npm install
```

> 若网络无法访问 `proxy.golang.org`，构建前先设置 Go 模块代理：`export GOPROXY=https://goproxy.cn,direct`

构建 WebUI、同步嵌入资源并交叉编译全平台独立二进制（与构建主机无关）：

```bash
npm run build
```

本地构建产物位于 `dist/standalone/`。该目录不会提交到 Git；正式版本通过 GitHub Releases 分发，发布物如下：

| 平台 | 发布物 |
| --- | --- |
| Windows amd64 | `elysia-api-windows-amd64.exe` |
| Windows arm64 | `elysia-api-windows-arm64.exe` |
| Linux amd64 | `elysia-api-linux-amd64` |
| Linux arm64 | `elysia-api-linux-arm64` |
| macOS（Intel / Apple Silicon 通用） | `elysia-api-macos.dmg` |

> DMG 只能在 macOS 上组装（依赖 swiftc / lipo / codesign / hdiutil），由 CI 在发布时产出：推 `v*` tag 自动发布，或在 Actions 页面手动触发（`workflow_dispatch`）后下载产物。两个 darwin 裸二进制只是 DMG 的组装输入，命令行场景仍可直接使用。

在 macOS 上（需要 Xcode Command Line Tools）可从 `npm run build` 产出的 darwin 二进制单独组装 `ElysiaApi.app` 与 DMG：

```bash
npm run build:macos-app
```

开发 WebUI：

```bash
cd packages/webui
npm run dev
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

### Maheshvara 与自定义协议

跨协议转换统一经过 Maheshvara 核心请求/响应模型：OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 和 Gemini GenerateContent 都先解析为 Maheshvara，再按上游协议渲染。模型源的 `platform` 可以写成 `custom:<协议ID>`（WebUI 可直接选择并填写 ID），并在 bootstrap `config.json` 的 `customProtocols` 中声明安全的 JSON body 模板。自定义协议源使用手动模型列表，不执行自动模型发现。例如：

```json
{
  "customProtocols": [
    {
      "id": "vendor-json",
      "request": {
        "method": "POST",
        "path": "/v2/generate/{{maheshvara.model}}",
        "headers": {"X-Model": "{{maheshvara.model}}"},
        "bodyTemplate": "{\"model\":{{maheshvara.model | json}},\"messages\":{{maheshvara.messages}},\"temperature\":{{maheshvara.temperature | default:0.2}}}",
        "omitIfEmpty": ["temperature"]
      },
      "response": {
        "textPath": "answer.text",
        "usagePath": "usage",
        "finishReasonPath": "finish"
      }
    }
  ]
}
```

模板只读 `maheshvara.*`（同时兼容 `request.*`），支持字符串插值、原生 JSON 值、`json`、`default:` 和 `omitIfEmpty`；不执行任意代码。常用字段包括 `model`、`instructions`、`messages`、`tools`、`tool_choice`、生成参数、`reasoning`、`metadata`、`stream` 和 `raw_extra`。非流式响应和 SSE / NDJSON 流都可映射回 Maheshvara，再渲染为客户端所请求的四种协议之一。

完整字段模型、四协议映射矩阵、reasoning 安全约定、Gemini Part 不变量和自定义协议配置说明见 [`docs/maheshvara-protocol.md`](docs/maheshvara-protocol.md)。

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
| `GET /api/admin/*` | 管理 API | Panel Token |
| `GET /debug/pprof/*` | pprof 性能分析（需启用） | Panel Token |
| `GET /health` | 健康检查 | 无 |

请求鉴权支持 `Authorization: Bearer <token>`、`x-api-key`、`x-goog-api-key` 和 `?key=`。Panel token 场景还支持 `panel_access_token` cookie。

## 旧配置迁移

旧配置中包含的 `tokens` 和 `modelGroups` 会作为兼容数据在启动时导入 SQLite。新安装只应在 `config.json` 中保留 bootstrap 字段，例如 host、port、database path、panel access token、日志和诊断配置。
