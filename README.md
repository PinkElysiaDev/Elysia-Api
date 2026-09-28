<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/banner.svg">
  <img src="docs/assets/banner-light.svg" alt="Elysia API">
</picture>

**一款轻量级自部署 AI 网关（单二进制，内置 WebUI），内置管理 agent 协助网关运维。可依托无代码 DSL 快速接入模型协议，支持通过 REST / MCP / A2A 接口远程调度。**

*A lightweight, self-hosted AI gateway in a single binary with a built-in WebUI, featuring an embedded management agent to streamline gateway operations. It enables rapid integration of model protocols via a no-code DSL and supports remote orchestration via REST, MCP, and A2A interfaces.*

<p>
  <a href="https://github.com/PinkElysiaDev/Elysia-Api/tags"><img src="https://img.shields.io/github/v/tag/PinkElysiaDev/Elysia-Api?sort=semver&amp;style=flat-square&amp;label=tag" alt="最新 tag"></a>
  <a href="https://github.com/PinkElysiaDev/Elysia-Api/stargazers"><img src="https://img.shields.io/github/stars/PinkElysiaDev/Elysia-Api?style=flat-square&amp;logo=github" alt="GitHub stars"></a>
  <a href="./backend/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/PinkElysiaDev/Elysia-Api?filename=backend%2Fgo.mod&amp;style=flat-square" alt="Go 版本"></a>
</p>

简体中文 · <a href="./README.en.md">English</a>

</div>

---

## <img src="docs/assets/icons/icon-features.svg" width="20" alt=""> 特性

WebUI 通过 `//go:embed` 嵌入后端二进制，默认在 `/ui/` 提供；运行时配置使用 bootstrap `config.json`，模型源、模型组、Relay API Token、Usage 与系统日志存储在 SQLite 中。

### <img src="docs/assets/icons/icon-gateway.svg" width="16" alt=""> 网关核心

- 模型组与负载均衡：支持轮询、顺序、随机策略和模型组级权限。
- 多格式互转：以 Maheshvara Request / Response / Usage 为唯一核心表示，在 OpenAI Chat Completions、OpenAI Responses、Claude Messages、Gemini GenerateContent 之间转换。
- Responses API：`/v1/responses` 可原生转发，也可转换到 Chat / Claude / Gemini 上游。
- 流式响应：四种内建协议和自定义协议均通过 Maheshvara 状态化 decoder / renderer 转换 SSE。
- 同协议透传：四种协议在客户端与上游线路协议同源时自动零转换透传，仅改写 model 名，其余字段原样保留。
- Usage 统计：记录缓存命中、推理 token、多模态 token、内置工具调用、请求/响应摘要和重试事件。
- 流量限制：支持模型组级并发和每日请求/token 限制。
- 运维诊断：内置健康检查、系统日志、pprof、WebUI 用量面板和热重载端点。

### <img src="docs/assets/icons/icon-agent.svg" width="16" alt=""> 管理智能体

- WebUI「AI 助手」页内置通用智能体：上传 API 文档即可接入协议（草稿 → 离线自检 → 经审批真实测试 → 保存），也能新增/修改模型源与模型组、查询用量并生成图表、下钻分析失败请求、维护出站安全策略。
- 写操作与出站请求均需审批（审批卡 + 计划模式暂停点），会话历史可追溯，模型与思考等级可调，Agent 用量计入统计页。
- `elysia` CLI：在终端以同一引擎驱动网关运维（见 [docs/agent-cli.md](docs/agent-cli.md)）。

### <img src="docs/assets/icons/icon-dsl.svg" width="16" alt=""> 无代码协议 DSL

- 协议设计器：请求体/返回体逐字段声明与 Maheshvara 字段的对应关系，可视化构建、保存即热生效。
- 预置协议：四大标准协议本身也是同源的数据定义，可在设计器中查看、复制、编辑。
- AI 生成：把 API 文档交给 AI 助手，直接产出协议草稿并走完验证流程。
- 模型发现：自定义协议可声明模型列表端点，声明后引用该协议的模型源即可开启自动拉取。

### <img src="docs/assets/icons/icon-remote.svg" width="16" alt=""> 远程调度

- REST：`/api/agent/*` 管理面（会话与消息管理，SSE 流式响应）。
- MCP：`POST /mcp` 只暴露无状态的 `elysia_cli`，直接执行网关运维命令。
- A2A：`POST /a2a` 消息端点 + `GET /.well-known/agent-card.json` 标准 Agent Card。
- 专用远程 Key：agent 作用域 Bearer Key 可驱动内置助手（`/api/agent`、`/a2a`），或通过 `/mcp` 的 `elysia_cli` 直接运维，不能调用 `/v1` 推理接口；`config.agentRemote` 总开关（默认启用）。

### <img src="docs/assets/icons/icon-support.svg" width="16" alt=""> 支撑能力

- 多 Key 调度：一个模型源可配置多个 API Key，按轮询 / 随机 / 优先级策略调度；**逐 Key 权限自动发现**——拉取时每个 Key 独立请求模型列表，自动得到各自分组的可用模型集（可在面板按 Key 勾选启停），调度时保证不会切到无权限的 Key。
- 模型能力目录：内置 models.dev 快照（零配置开箱即用），后台定期在线更新并落盘缓存（models.dev 不可达时自动回退 jsDelivr 镜像）；拉取模型时自动回填视觉 / 工具 / 结构化输出 / 思考模式 / 上下文长度等能力。模型组按成员推导能力并实际生效（不支持视觉的组自动剥离图片，不支持工具的组拒绝工具请求）。
- 异步后台拉取：模型拉取为后台任务，发起即返回、页面不阻塞，进度与结果实时轮询展示；拉取中的源自动锁定相关操作防止误冲突。
- 自定义拉取地址：模型列表端点与请求转发端点不同源（域名 / 端口 / 协议不一致）的站点可单独配置拉取地址。
- 模型级管理：拉取的模型支持单个编辑 / 启停 / 删除与检索，刷新采用保留式合并（手动模型与用户编辑永不丢失）。
- 安全加固：敏感字段加密存储、常量时间 token 比较、SSRF 防护（可配置 CIDR 禁止列表 `outbound.deniedIpRanges`，运行时配置页与 agent 工具均可修改）。

## <img src="docs/assets/icons/icon-preview.svg" width="20" alt=""> 界面预览

登录页开场动画（角色轨迹动效 + 背景视频）：

![登录页开场动画](packages/webui/public/assets/elysia-login.mp4)

| 总览 | 协议设计器 · 映射关系 |
| :---: | :---: |
| ![总览](docs/assets/webui-overview.png) | ![协议设计器映射关系](docs/assets/webui-protocol-mapping.png) |

| AI 助手 | 运行配置 |
| :---: | :---: |
| ![AI 助手](docs/assets/webui-agent.png) | ![运行配置](docs/assets/webui-runtime.png) |

## <img src="docs/assets/icons/icon-quickstart.svg" width="20" alt=""> 快速开始

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
  "secretKeyPath": ".master-key",
  "openBrowserOnStart": true
}
```

`databasePath` 和 `secretKeyPath` 使用相对路径时，会按 `config.json` 所在目录解析。
`openBrowserOnStart` 控制启动时是否自动在系统默认浏览器打开控制台（缺省为尝试打开；被其他程序托管或无桌面环境时可设 `false`，打开命令缺失时静默跳过）。

### Windows

将 `elysia-api-windows-amd64.exe` 和 `config.json` 放在同一目录后运行：

```powershell
.\elysia-api-windows-amd64.exe --config .\config.json
```

如果 `config.json` 与 exe 位于同一目录，也可以直接双击 exe；未传 `--config` 时程序会读取当前目录下的 `config.json`。首次启动若该文件不存在会自动创建（带随机 `panelAccessToken`，见启动日志），随后自动在默认浏览器打开控制台（`openBrowserOnStart: false` 可关闭）。

### Linux

将 `elysia-api-linux-amd64` 和 `config.json` 放在同一目录后运行：

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

### macOS

从 Release 下载 `elysia-api-macos.dmg`，双击打开后将 `ElysiaApi` 拖入 `Applications` 快捷方式即完成安装，之后从启动台双击运行（macOS 12+，Intel / Apple Silicon）：

- 首次启动自动生成配置与随机 `panelAccessToken`，数据统一保存在 `~/Library/Application Support/ElysiaApi/`（config.json、SQLite、`.master-key`、`elysia-api.log`）。
- 首次打开显示登录页；从菜单栏选择「复制面板访问令牌」后粘贴登录。手动登录后保留登录态和 Cookie，重开窗口或应用无需重复输入；主动退出登录后需重新登录。
- 关闭窗口（⌘W）后服务继续运行；从菜单栏、Dock 或启动台重开时，已登录则进入总览，未登录则显示登录页，保留窗口位置和主题。外接屏断开后窗口会回到可见屏幕。
- 菜单栏图标任何状态下都不带文字；菜单顶部是品牌信息部件（运行状态点、实际地址、版本与最近 24 小时请求脉冲曲线，与面板同款瑰梅红），操作保留启动/停止服务、复制 API 地址、复制面板访问令牌、查看日志与偏好设置。后端异常会自动重启，最多重试 3 次后提供手动重试。
- 「偏好设置…」（⌘,）提供开机启动与通知开关。开机启动默认关闭，启用后登录时只驻留菜单栏；重要通知默认开启，首次需要发送时才申请系统权限。
- 应用内更新提供下载进度、取消和失败重试；通过 sha256、DMG 完整性与签名检查后替换整个应用包，替换失败自动回滚。配置、数据库、主密钥和窗口偏好保留。
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

## <img src="docs/assets/icons/icon-dsl.svg" width="20" alt=""> Maheshvara 与无代码协议 DSL

跨协议转换统一经过 Maheshvara 核心请求/响应模型：OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 和 Gemini GenerateContent 都先解析为 Maheshvara，再按上游协议渲染。模型源的 `platform` 可以写成 `custom:<协议ID>`（WebUI 可直接选择并填写 ID）；协议在 WebUI 的「协议设计器」页面以字段级映射可视化构建，保存即热生效。协议配置结构示例：

```json
{
  "id": "vendor-json",
  "type": "llm",
  "request": {
    "method": "POST",
    "path": "/v2/generate",
    "auth": {"mode": "header", "header": "x-api-key"},
    "body": {
      "model": {"field": "model", "mode": "string"},
      "input": {"field": "messages"},
      "params": {
        "temperature": {"field": "temperature", "default": 0.7, "omitIfEmpty": true},
        "api_version": {"value": "2026-01-01"}
      }
    }
  },
  "response": {
    "fields": [
      {"path": "answer.text", "field": "text"},
      {"path": "finish", "field": "stop_reason"},
      {"path": "usage.prompt", "field": "usage.input_tokens"},
      {"path": "usage.completion", "field": "usage.output_tokens"}
    ]
  }
}
```

请求体与返回体均为字段级映射：每个叶子声明对应 Maheshvara 的哪个字段（`{"field": ..., "mode": "json|string", "default"?, "omitIfEmpty"?}`）或为常量（`{"value": ...}`）；不执行任意代码。可用字段目录由 schema 端点提供，与 UI 下拉、AI 生成和后端校验同源。非流式响应和 SSE / NDJSON 流都可映射回 Maheshvara，再渲染为客户端所请求的四种协议之一。

- 完整字段模型、四协议映射矩阵、reasoning 安全约定、Gemini Part 不变量：[docs/maheshvara-protocol.md](docs/maheshvara-protocol.md)
- 协议定义字段参考：[docs/protocol-definition-reference.md](docs/protocol-definition-reference.md)
- 真实案例——阿里 DashScope（百炼）接入指南：[docs/custom-protocol-dashscope.md](docs/custom-protocol-dashscope.md)

## <img src="docs/assets/icons/icon-agent.svg" width="20" alt=""> 管理智能体与远程调度

除了在 WebUI 中直接对话，管理智能体还可以被远程程序驱动——无论是脚本、MCP 客户端（如 Claude Desktop、Cursor）还是支持 A2A 的其他 agent，都能把 Elysia-API 当作一个可调度的运维 agent 使用：

| 接口 | 端点 | 说明 |
| --- | --- | --- |
| REST | `/api/agent/*` | 会话与消息管理，SSE 流式响应 |
| MCP | `POST /mcp` | 仅提供无状态的 `elysia_cli`，直接执行网关运维命令 |
| A2A | `POST /a2a`、`GET /.well-known/agent-card.json` | 标准 Agent Card 与消息端点 |

三个远程面共用 agent 作用域的 Bearer API Key 鉴权与 `config.agentRemote` 总开关（默认启用；`publicUrl` 用于对外通告端点地址）。在「运行配置 → AI 助手远程访问」创建专用 Key；它可通过 REST/A2A 驱动内置助手，或通过 MCP 直接运维，不能调用 `/v1` 推理接口。

通过 MCP 调用 `elysia_cli` 时只需 `{"command":"elysia source ls"}`，持 `agent` 作用域 Key 直接执行，无需内置模型或 Agent 会话。MCP 每次调用都会创建临时 CLI 上下文，协议草稿、测试目标和测试凭证只在当前调用内有效；需要复用这些状态时，请把命令合并到同一次 `command` 批处理中。通过 REST/A2A 驱动内置助手时仍遵循会话审批档和计划模式。

- 远程接口协议细节：[docs/remote-agent-api.md](docs/remote-agent-api.md)
- 智能体工具全量目录：[docs/agent-tools-catalog.md](docs/agent-tools-catalog.md)
- `elysia` CLI 命令参考：[docs/agent-cli.md](docs/agent-cli.md)

## <img src="docs/assets/icons/icon-config.svg" width="20" alt=""> 配置

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
| `agentRemote` | AI 助手远程面（REST / MCP / A2A）：`enabled` 总开关、`publicUrl` 通告地址。 |
| `outbound.deniedIpRanges` | SSRF 防护的 CIDR 禁止列表，预置内网/保留网段默认值，运行时配置页与 agent 工具可改。 |
| `usageLog` | 用量日志持久化：保留天数、体积/条数上限、请求体留存策略等。 |
| `modelCatalog` | 模型能力目录：开关、上游 URL、代理与同步间隔。 |

也可以通过环境变量 `ELYSIA_API_MASTER_KEY` 提供主密钥。生产环境中，如果数据库目录会被整体备份或打包，建议把 `secretKeyPath` 放在单独受保护的位置，或使用环境变量注入。

### 旧配置迁移

旧配置中包含的 `tokens` 和 `modelGroups` 会作为兼容数据在启动时导入 SQLite。新安装只应在 `config.json` 中保留 bootstrap 字段，例如 host、port、database path、panel access token、日志和诊断配置。

## <img src="docs/assets/icons/icon-ops.svg" width="20" alt=""> 运维、端点与数据备份

### 运维端点

- `GET /health`：公开健康检查端点。
- `GET /api/admin/health`：管理健康检查端点，需要 panel token。
- `POST /api/admin/reload`：管理侧热重载端点，需要 panel token。
- `POST /__reload`：本机 loopback 热重载端点。
- `POST /__shutdown`：本机 loopback 优雅关停端点。

修改 `host`、`port`、`databasePath` 或 `enablePprof` 后通常需要重启。生产环境建议使用 systemd、Windows 服务管理器、supervisord、Docker 或其他进程管理器托管后端。

### HTTP 端点

| 端点 | 说明 | 鉴权 |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions 入口 | Relay API Token |
| `POST /v1/responses` | OpenAI Responses API 入口 | Relay API Token |
| `POST /v1/messages` | Claude Messages 原生入口 | Relay API Token |
| `POST /v1/messages/count_tokens` | Claude 兼容 token 统计 | Relay API Token |
| `GET /v1/models` | 列出可用模型组 | Relay API Token |
| `GET /v1beta/models` / `POST /v1beta/models/*` | Gemini 兼容入口 | Relay API Token |
| `/api/agent/*` | AI 助手远程 REST 面 | Agent 远程 Key |
| `POST /mcp` | AI 助手 MCP 面 | Agent 远程 Key |
| `POST /a2a` | AI 助手 A2A 面 | Agent 远程 Key |
| `GET /ui/` | WebUI 控制台 | 页面登录 |
| `GET /api/admin/*` | 管理 API | Panel Token |
| `GET /debug/pprof/*` | pprof 性能分析（需启用） | Panel Token |
| `GET /health` | 健康检查 | 无 |

请求鉴权支持 `Authorization: Bearer <token>`、`x-api-key`、`x-goog-api-key` 和 `?key=`。Panel token 场景还支持 `panel_access_token` cookie。

### 数据备份

SQLite 数据库使用 WAL 模式。运行后通常会看到：

- `elysia-api.sqlite3`
- `elysia-api.sqlite3-wal`
- `elysia-api.sqlite3-shm`

备份时应使用 SQLite backup 工具，或先停止后端，再同时复制上述数据库文件。若启用了密钥文件，也必须备份并保护 `secretKeyPath` 指向的 `.master-key`。丢失主密钥后，SQLite 中加密保存的上游 API key 和 Relay API Token 无法解密。

## <img src="docs/assets/icons/icon-build.svg" width="20" alt=""> 构建

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

在 macOS 上（需要兼容的 **Universal** 版 Command Line Tools，例如 26.6；完整 Xcode 为可选）可从 `npm run build` 产出的 darwin 二进制单独组装 `ElysiaApi.app` 与 DMG：

```bash
npm run build:macos-app -- --check-toolchain
npm run test:macos-app
npm run build:macos-app
```

构建先验证 Swift 能否链接两种架构，通过后才替换旧产物，并校验可执行文件的双架构、应用签名和 DMG 内的应用及安装快捷方式。运行及更新已打包的 App 不需要 Command Line Tools。工具链排错、原生测试与手动验收范围见 [macOS 验证说明](docs/macos-testing.md)。

开发 WebUI：

```bash
cd packages/webui
npm run dev
```

Vite dev server 默认代理到 `http://127.0.0.1:8765`。

## <img src="docs/assets/icons/icon-docs.svg" width="20" alt=""> 文档

| 主题 | 文档 |
| --- | --- |
| 部署与验证 | [部署指南](docs/deployment.md) · [macOS 验证说明](docs/macos-testing.md) |
| 协议与接入 | [Maheshvara 核心协议](docs/maheshvara-protocol.md) · [协议定义参考](docs/protocol-definition-reference.md) · [DashScope 接入案例](docs/custom-protocol-dashscope.md) |
| AI 助手 | [远程接口（REST / MCP / A2A）](docs/remote-agent-api.md) · [工具目录](docs/agent-tools-catalog.md) · [elysia CLI](docs/agent-cli.md) |
| WebUI 与 API | [后端 API 参考](docs/webui-api.md) · [数据模型](docs/webui-data-model.md) · [前端规格](docs/webui-frontend-spec.md) · [验收清单](docs/webui-acceptance.md) |

## <img src="docs/assets/icons/icon-structure.svg" width="20" alt=""> 项目结构

```text
elysia-api/
├── backend/                # Go 后端（网关本体）
│   ├── agent/              # 管理智能体引擎（工具、审批、上下文压缩）
│   ├── config/             # 配置加载 / 热重载 / 加密密钥
│   ├── relay/              # 上游转发 / 格式转换 / Maheshvara 核心协议
│   ├── server/             # HTTP 路由 / 鉴权中间件 / 管理 API / AI 助手远程面
│   ├── storage/            # SQLite 持久化
│   └── webui/              # 内嵌 WebUI 静态资源（//go:embed all:dist）
├── packages/webui/         # React + Vite 控制台源码
├── docs/                   # 部署、协议、AI 助手、WebUI API 等文档
├── scripts/                # 独立后端发行构建 / npm 平台包发布脚本
└── config.json.example     # 最小 bootstrap 配置模板
```

## <img src="docs/assets/icons/icon-license.svg" width="20" alt=""> 许可

本项目基于 [MIT License](package.json) 发布。
