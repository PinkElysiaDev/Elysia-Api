# 部署与运维

[文档索引](README.md) · **简体中文** · [English](deployment.en.md)

从首次启动到升级、备份与故障处理。源码构建见[开发指南](development.md)，首次模型调用见[快速开始](../README.md#快速开始)。

## 安装与启动

从 [Releases](https://github.com/PinkElysiaDev/Elysia-Api/releases/latest) 下载对应架构产物和 `SHA256SUMS`。Windows / Linux 提供 amd64、arm64；macOS 提供通用 DMG。核对下载文件的 SHA256，再启动程序。

### Windows / Linux

在下载目录启动。以下为 Linux amd64 示例：

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

Windows 使用 `.\elysia-api-windows-amd64.exe --config .\config.json`；ARM64 使用对应的 arm64 文件名。

- 不传 `--config` 时，配置路径为**当前工作目录**下的 `config.json`，不一定是可执行文件所在目录。
- 仅文件不存在时自动创建配置，并在日志输出随机面板令牌。配置损坏时启动失败，不覆盖原文件。
- 默认监听 `127.0.0.1:8765`；普通二进制遇到端口占用直接报错，不自动选择其他端口。
- 启动后访问 `/ui/`，使用面板令牌登录。首次登录后轮换令牌，保护配置和启动日志。
- 默认尝试打开浏览器；无桌面环境可设置 `openBrowserOnStart: false`。

### macOS App

将 DMG 内的 ElysiaApi 拖入 Applications。支持 macOS 12+、Intel 和 Apple Silicon；运行和更新不需要 Xcode。

主壳作为菜单栏 supervisor 托管后端、健康检查和更新。打开面板时，它复用包内 `ElysiaApi` 可执行文件，以 `--webui-process` 启动独立的 WebUI 子进程；窗口和 WebKit 属于这个子进程。关窗会结束 WebUI 及其 WebKit 服务，菜单栏主壳和原后端继续运行。仅释放 `WKWebView` 的 Swift 引用不足以结束 WebKit 服务，因此资源回收以所属进程退出为边界。

| 操作 | 行为 |
| --- | --- |
| 首次登录 | 从菜单栏复制面板令牌；应用不会自动注入凭证 |
| 数据目录 | `~/Library/Application Support/ElysiaApi/`，包含配置、数据库、主密钥和运行日志 |
| 端口占用 | 尝试配置端口；占用时在 `8799–8899` 寻找可用端口，以菜单栏显示地址为准 |
| 关闭窗口 | 退出 WebUI 子进程及其 WebKit 服务；菜单栏和原后端继续运行，重开保留登录态、窗口位置和主题 |
| 启动 / 重启服务 | 服务未运行时启动；运行时先优雅退出，再启动新的后端进程 |
| 退出应用 | 请求后端优雅退出，超时后终止子进程 |
| 服务恢复 | 后台健康状态下每 30 秒检查；前台、启动或恢复中每 3 秒检查。进程退出回调及时处理后端异常退出；持续健康检查失败时自动重启，最多连续尝试 3 次，再提供手动重试 |
| 开机启动 | 默认关闭；启用后登录系统时仅驻留菜单栏 |
| 通知 | 重要通知默认开启，首次发送或显式启用时请求系统授权 |
| 更新 | 校验并暂存 DMG 中的应用，停止后端及旧壳后由 helper 替换；新版服务和面板就绪后清理旧包，失败回滚；保留配置和数据库 |

菜单提供服务启动和重启、API 地址和令牌复制、日志、偏好设置、浏览器打开和检查更新。应用托管的后端使用 `ELYSIA_API_OPEN_BROWSER=false`。WebUI 使用默认持久化 WebKit 数据存储保存手动登录后的 Cookie 与登录状态，主题、窗口尺寸和位置保存在应用偏好中；重建子进程不会清除这些数据。主动退出登录后需要重新认证。

CI 产物使用 ad-hoc 签名，未等同于 Developer ID 公证。确认文件来自项目 Release 后，如遇 Gatekeeper 拦截，可执行：

```bash
xattr -d com.apple.quarantine /Applications/ElysiaApi.app
```

详细行为与验收见 [macOS 验证](macos-testing.md)。

### Docker

在仓库根目录构建镜像。容器内必须监听 `0.0.0.0`，宿主机端口仍绑定回环地址：

```bash
docker build -t elysia-api:local .
docker run -d --name elysia-api \
  --restart unless-stopped --init \
  --read-only --tmpfs /tmp:size=64m,mode=1777 \
  --security-opt no-new-privileges:true --cap-drop ALL \
  -p 127.0.0.1:8765:8765 \
  -e ELYSIA_API_HOST=0.0.0.0 \
  -v elysia-data:/data \
  elysia-api:local
docker logs elysia-api
```

镜像以 UID/GID `65532` 运行，配置为 `/data/config.json`。命名卷保留配置、数据库和密钥。使用宿主目录挂载时，需为该用户提供写权限。

也可将以下内容保存为仓库根目录的 `compose.yaml`：

```yaml
services:
  elysia-api:
    build: .
    image: elysia-api:local
    container_name: elysia-api
    restart: unless-stopped
    init: true
    read_only: true
    tmpfs:
      - /tmp:size=64m,mode=1777
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    ports:
      - "127.0.0.1:8765:8765"
    environment:
      ELYSIA_API_HOST: 0.0.0.0
      ELYSIA_API_OPEN_BROWSER: "false"
    volumes:
      - elysia-data:/data
volumes:
  elysia-data:
```

```bash
docker compose up -d --build
docker compose logs elysia-api
```

使用反向代理提供 TLS 和外部访问；仅为需要访问的网段开放端口。若更改容器内端口，需同时调整映射和镜像内固定 `8765` 的健康检查、关闭探针。

## 配置

以下为首次启动写入的主要字段；示例令牌必须替换，不要将 `change-me` 用于部署：

```json
{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "change-me",
  "databasePath": "elysia-api.sqlite3",
  "secretKeyPath": ".master-key",
  "logLevel": "info",
  "httpTimeout": 120,
  "openBrowserOnStart": true
}
```

| 字段 | 默认与用途 |
| --- | --- |
| `host` / `port` | `127.0.0.1` / `8765`；改变监听需重启 |
| `panelAccessToken` | 首次创建时生成；保护管理 API，不是推理令牌 |
| `databasePath` | `elysia-api.sqlite3`；模型、令牌、会话等业务数据 |
| `logDatabasePath` | 默认在业务库文件名后加 `-logs`，如 `elysia-api-logs.sqlite3`；系统日志、调用明细、媒体索引和用量汇总；变更需重启 |
| `secretKeyPath` | `.master-key`；SQLite 敏感字段的加密密钥 |
| `logLevel` | `info`；可选 `debug`、`info`、`warn`、`error` |
| `logPersistLevel` | `info`；数据库诊断日志阈值，与控制台独立；操作事件和 error 始终尝试持久化 |
| `logFormat` | `auto`；终端可读文本，重定向时 JSON；可显式选 `json` / `console` |
| `httpTimeout` | 非流式 HTTP 请求总超时；自动创建配置时为 120 秒，显式 `0` 或字段缺省为不限制 |
| `openBrowserOnStart` | 缺省尝试打开浏览器 |
| `webuiDir` | 空时使用嵌入资源；非空时覆盖 WebUI 文件目录 |
| `enablePprof` | `false`；启用受管理鉴权保护的 `/debug/pprof` 路由，修改需重启 |
| `maxBodyBytes` | `33554432`（32 MiB）；请求体大小上限 |
| `debugMode` / `verboseLog` | 默认关闭；详细日志要求两者同时开启 |

`databasePath`、`logDatabasePath`、`secretKeyPath`、`webuiDir` 的相对路径按配置文件目录解析。业务库与日志库必须是不同文件（包括软／硬链接）。模型源、模型组和访问令牌通过管理 API 保存在 SQLite，不写入此文件。

流式 SSE/NDJSON 使用协议的 `framing.idleMillis`：缺省或 `0` 为 10 分钟，显式值允许 1—3600000 毫秒。它限制等待响应头和相邻数据之间的空闲时间，不限制总流时长；`httpTimeout: 0` 不会关闭此保护。长时间思考的上游可调整该协议字段，主动取消仍立即生效。延长等待不能解决上游 429 限流，已开始生成的请求不会自动重放。

### 环境变量与优先级

| 变量 | 作用 |
| --- | --- |
| `ELYSIA_API_HOST` | 覆盖配置中的监听地址，加载及热重载均应用 |
| `ELYSIA_API_OPEN_BROWSER` | 覆盖浏览器启动设置，不写回文件；无效布尔值记录提示并回退 |
| `ELYSIA_API_MASTER_KEY` | 优先于密钥文件；应由受保护的运行环境注入 |

密钥读取顺序：环境变量 → `secretKeyPath` → 自动创建随机密钥。不再读取旧 `.db-key`；启用加密的存储不接受历史明文密钥字段。密钥创建失败会记录告警；检查启动日志，不要将服务启动成功视为加密已建立的证明。

### 运行策略

| 配置块 | 字段与默认行为 |
| --- | --- |
| `responses` | `enabled: true`；协议转换由当前协议绑定和转换策略决定 |
| `usage` | 缺少上游用量时默认估算；`charsPerToken: 4`、`defaultOutputTokenEstimate: 1024`、`imageInputTokenEstimate: 300`、`fileInputTokenEstimatePerKB: 128`；`estimateWhenMissing: false` 关闭估算 |
| `healthCheck` | 默认关闭；`intervalSeconds: 300`、`timeoutSeconds: 10`、`failureThreshold: 3`；探测可能产生上游请求费用 |
| `modelCatalog` | 默认启用 models.dev 目录；内置快照及本地缓存，在线失败可回退镜像；`url`、`proxy` 可覆盖，`syncIntervalMinutes` 缺省 1440，显式 `0` 停止定时同步 |
| `agentRemote` | `enabled` 缺省为 `true`；`publicUrl` 用于反向代理后的 Agent Card 地址；详见[远程接口](remote-agent-api.md) |
| `outbound` | `proxyUrl` 可选 HTTP 出站代理；`deniedIpRanges` 缺省使用私网、回环与保留段预置；显式 `[]` 表示全部放行 |

运行配置 → 安全设置中可填写 `outbound.proxyUrl`，如 `http://127.0.0.1:7890` 或 `http://user:password@127.0.0.1:7890`。用户名、密码中的特殊字符需进行 URL 编码。输入框默认隐藏，显隐按钮可查看完整内容；配置文件和管理员配置 API 保存、返回原值，错误日志隐藏代理凭据。

代理用于模型请求（含流式、WebSocket）、模型列表、上游健康检查和模型目录，以及 macOS 检查更新、DMG 下载。保存后新请求生效，进行中的请求继续使用原连接；macOS 每次检查或下载时重新读取配置。本机面板和健康/API 请求不使用该代理。留空时后端直连，macOS 更新保持系统默认网络设置；显式代理失败不回退直连。`modelCatalog.proxy` 如已配置则优先于全局代理。

显式配置的代理端点允许位于本机或内网，不必删除环回禁止段。上游目标及重定向仍检查 `deniedIpRanges`；代理请求需通过本机 DNS 检查，代理负责最终 DNS 解析和拨号，因此只配置可信代理，并在代理端同样限制私网目标。本机或内网模型源仍需按实际目标地址调整禁止段，只移除必要的 CIDR。完整默认列表见 [config.json.example](../config.json.example)。

## 请求日志

默认记录请求元数据和用量，不保存请求／响应正文，包括失败请求。需要排障正文时，将 `usageLog.bodyMaxKB` 设为正数；WebUI 开关初次启用设置为 1024 KB。调用明细默认保留 14 天、内容预算 1024 MiB；显式设置 0 可关闭对应限制。

```json
{
  "usageLog": {
    "persistEnabled": true,
    "retentionDays": 14,
    "maxContentMB": 1024,
    "maxRecords": 0,
    "bodyMaxKB": 0,
    "bodyOnErrorOnly": false,
    "externalizeMedia": true,
    "cleanupIntervalMinutes": 60
  }
}
```

| 字段 | 含义 |
| --- | --- |
| `persistEnabled` | 默认 `true`；`false` 停止请求日志持久化 |
| `retentionDays` / `maxRecords` | 按天数或记录数清理，`0` 不限制 |
| `maxContentMB` | 按请求日志 JSON 与去重媒体内容大小限制，`0` 不限制；数据库空闲页独立回收 |
| `bodyMaxKB` | 四段链路正文分别应用的 KB 上限；`0` 不保存正文 |
| `bodyOnErrorOnly` | 仅失败请求保存正文，仍需 `bodyMaxKB > 0` |
| `externalizeMedia` | 将正文内 base64 媒体去重写至 `<logDatabasePath>-assets/`，正文保存占位符 |
| `cleanupIntervalMinutes` | 默认 60 分钟，正值最低 5 分钟 |

策略作用于后续请求，不追溯删除旧正文。留存清理保留小时聚合统计；外置媒体随日志删除。本版本不迁移旧数据库，不提供旧 schema 兼容；请使用新数据库。

`systemLog.retentionDays`、`systemLog.maxRecords` 和 `systemLog.maxContentMB` 分别限制系统日志的时间、条数及内容字节数，默认 30 天、条数不限、256 MiB。请求日志预算不包含系统日志和用量汇总。内容预算不是 SQLite 文件的磁盘硬上限，索引、空闲页和 WAL 单独显示；留存清理保留历史用量汇总，只有“重置用量”清除汇总。

日志通过 zerolog 输出，HTTP 日志与调用明细共享服务端生成的 `request_id`（响应头 `X-Elysia-Request-Id`）。访问日志为 debug，5xx 与 panic 为 error。系统日志队列最多 2048 条 / 16 MiB，按 64 条或 100 ms 批量提交；普通诊断日志在饱和时丢弃并计数，重要事件同步回退。调用日志队列最多 1024 条 / 128 MiB，按 64 条或 50 ms 提交，队列满时同步回退，写入失败在有界时间内重试。写入指标在设置页及 `GET /api/admin/usage/storage` 的 `writers` 字段提供。

SIGTERM、SIGINT、本地关闭接口与父进程 EOF 共用关停流程：停止请求和后台生产者，再给予日志排空独立的 15 秒预算，最后 checkpoint 并关闭数据库。持久化失败直接写 stderr、计入失败指标，并使服务以非零状态退出；不会宣称成功落盘。内存队列中的记录不能抵御 SIGKILL；已提交记录由 SQLite `synchronous=FULL` 的 WAL 保证持久性，仍依赖底层文件系统正确实现同步写入。

## 服务托管

普通二进制是长驻进程，可由 systemd、Windows 服务包装器或容器托管。以下 unit 假设已创建系统用户 `elysia`，程序位于 `/opt/elysia-api/`，且该用户可写配置和数据目录：

```ini
[Unit]
Description=Elysia API
After=network.target

[Service]
User=elysia
WorkingDirectory=/opt/elysia-api
ExecStart=/opt/elysia-api/elysia-api-linux-amd64 --config /opt/elysia-api/config.json
Environment=ELYSIA_API_OPEN_BROWSER=false
Restart=on-failure
RestartSec=3
TimeoutStopSec=45

[Install]
WantedBy=multi-user.target
```

保存为 `/etc/systemd/system/elysia-api.service` 后执行 `sudo systemctl daemon-reload` 和 `sudo systemctl enable --now elysia-api`。用 `journalctl -u elysia-api` 查看启动日志。

## 升级、备份与恢复

两个 SQLite 库均使用 WAL、5000 ms busy timeout 和外键。业务库使用 `synchronous=NORMAL`，日志库使用 `synchronous=FULL`。业务 schema 版本为 2，日志 schema 版本为 1；旧业务 schema 直接拒绝，不自动迁移。

1. 升级前停止服务，备份 `config.json`、业务库、日志库及仍存在的 `-wal` / `-shm` 文件、`<logDatabasePath>-assets/`。
2. 单独保护并备份实际使用的加密密钥；若来自环境变量，记录其安全恢复方式。数据库和密钥一同泄漏会使静态加密失去保护。
3. 保留旧程序版本，替换二进制或镜像。使用本版本当前 schema 的数据库；从旧 schema 升级时需配置新的数据库路径并重新初始化数据。
4. 检查 `/health`、面板登录、模型列表和一次推理请求。真实推理会产生上游费用。
5. 恢复时停止服务，将同一备份的配置、数据库、媒体和匹配密钥放回，并恢复权限，再启动兼容版本。不要仅替换数据库主文件或重新生成密钥。

在线备份应使用 SQLite 备份工具取得一致数据库，并协调媒体文件快照；不要直接复制仍在写入的数据库主文件。数据库目录不可写或磁盘已满会导致写入失败。

## 令牌恢复

面板令牌丢失时，停止服务，修改配置中的 `panelAccessToken` 为新的随机值，再启动并重新登录。推理令牌存于 SQLite，恢复面板访问后通过管理页修改。

## 热重载

`POST /api/admin/reload` 要求面板鉴权；运行配置更新接口见 [API 参考](webui-api.md#运行配置)。监听地址、端口、数据库路径和 pprof 路由变化需要重启，热重载不会重建这些资源。

`POST /__reload`、`POST /__shutdown` 只允许回环来源；代理转发时不要将这两个端点公开。

## 当前格式要求

后端不保留向后兼容或自动迁移路径。使用当前格式的新数据库和配置；已有数据请先独立备份，必要内容通过当前管理接口重建，服务不会替你删除旧文件。

- 监听设置只读取顶层 `host` / `port`，不再读取 `server.host` / `server.port`。
- API Key、模型和分组只存 SQLite；不读取或导入 `tokens`、`modelGroups`、`customProtocols`。
- 协议只使用当前定义与显式绑定，平台为 `custom:<协议 ID>`；模型引用必须为 `sourceId:modelId`。
- 已删除旧协议迁移接口、历史预置改名/路径修补和旧日志布局回填。
- 内置协议模块始终使用当前统一语义；`requires` 只声明所需引擎能力，不选择历史实现。自定义样例与转换规则需要重新验证，旧验证证据不再授权执行。
- `responses_storage` 规则必须显式填写 `sourceCodec`、`targetCodec`、`onUnsupported`；不再把缺失来源当成 Responses。
- 用量筛选使用 `keyName`、`groupName`、`modelName`、`sourceId` 的重复参数；删除 `modelGroup` 别名和 `keyHash` 筛选。
- 全局和目录专用代理统一使用 `http://` 代理地址（HTTPS 目标经 CONNECT 访问）；目录代理不再接受旧 `https://` / SOCKS 地址例外。

## 故障处理

| 现象 | 检查 |
| --- | --- |
| 连接被拒绝 | 服务日志、实际端口；Docker 内是否设置 `ELYSIA_API_HOST=0.0.0.0` |
| 面板 401 | 使用面板令牌；检查配置路径，避免误用推理或 Agent Key |
| 上游连接被拦截 | 出站 CIDR 策略、DNS 解析结果和代理配置 |
| 用量日志没有正文 | `bodyMaxKB` 是否为正；历史请求不会补采集 |
| 密钥不可解密 | 是否使用与该数据库匹配的主密钥或 `secretKeyPath`，环境变量是否改变 |
| 模型列表为空 | 上游模型发现端点、逐 Key 权限、自定义协议的 `models` 定义 |
