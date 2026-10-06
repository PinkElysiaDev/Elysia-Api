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
| `databasePath` | `elysia-api.sqlite3`；模型、令牌、会话和日志存储 |
| `secretKeyPath` | `.master-key`；SQLite 敏感字段的加密密钥 |
| `logLevel` | `info`；可选 `debug`、`info`、`warn`、`error` |
| `httpTimeout` | 自动创建配置时为 120 秒；显式 `0` 或字段缺省为不限制 |
| `openBrowserOnStart` | 缺省尝试打开浏览器 |
| `webuiDir` | 空时使用嵌入资源；非空时覆盖 WebUI 文件目录 |
| `enablePprof` | `false`；启用受管理鉴权保护的 `/debug/pprof` 路由，修改需重启 |
| `maxBodyBytes` | `33554432`（32 MiB）；请求体大小上限 |
| `debugMode` / `verboseLog` | 默认关闭；详细日志要求两者同时开启 |

`databasePath`、`secretKeyPath`、`webuiDir` 的相对路径按配置文件目录解析。模型源、模型组和访问令牌通过管理 API 保存在 SQLite，不写入此文件。

### 环境变量与优先级

| 变量 | 作用 |
| --- | --- |
| `ELYSIA_API_HOST` | 覆盖配置中的监听地址，加载及热重载均应用 |
| `ELYSIA_API_OPEN_BROWSER` | 覆盖浏览器启动设置，不写回文件；无效布尔值记录提示并回退 |
| `ELYSIA_API_MASTER_KEY` | 优先于密钥文件；应由受保护的运行环境注入 |

密钥读取顺序：环境变量 → `secretKeyPath` → 数据库目录中的旧 `.db-key` → 自动创建随机密钥。旧密钥存在时会继续使用，避免升级后无法解密。密钥创建失败会记录告警；检查启动日志，不要将服务启动成功视为加密已建立的证明。

### 运行策略

| 配置块 | 字段与默认行为 |
| --- | --- |
| `responses` | `enabled: true`，`upstreamMode: "auto"`；模式支持 `native`、`transform`、`auto` |
| `usage` | 缺少上游用量时默认估算；`charsPerToken: 4`、`defaultOutputTokenEstimate: 1024`、`imageInputTokenEstimate: 300`、`fileInputTokenEstimatePerKB: 128`；`estimateWhenMissing: false` 关闭估算 |
| `healthCheck` | 默认关闭；`intervalSeconds: 300`、`timeoutSeconds: 10`、`failureThreshold: 3`；探测可能产生上游请求费用 |
| `modelCatalog` | 默认启用 models.dev 目录；内置快照及本地缓存，在线失败可回退镜像；`url`、`proxy` 可覆盖，`syncIntervalMinutes` 缺省 1440，显式 `0` 停止定时同步 |
| `agentRemote` | `enabled` 缺省为 `true`；`publicUrl` 用于反向代理后的 Agent Card 地址；详见[远程接口](remote-agent-api.md) |
| `outbound` | `deniedIpRanges` 缺省使用私网、回环与保留段预置；显式 `[]` 表示全部放行 |

本机或内网模型源需要按实际地址调整出站禁止段。只移除必要的 CIDR；源访问和目录更新的代理配置不是同一设置。完整默认列表见根目录 [config.json.example](../config.json.example)。

## 请求日志

默认记录请求元数据和用量，不保存请求／响应正文，包括失败请求。需要排障正文时，将 `usageLog.bodyMaxKB` 设为正数；WebUI 开关初次启用设置为 1024 KB。自动清理默认关闭。

```json
{
  "usageLog": {
    "persistEnabled": true,
    "retentionDays": 0,
    "maxContentMB": 0,
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
| `externalizeMedia` | 将正文内 base64 媒体去重写至数据库目录下的 `usage-assets/`，正文保存占位符 |
| `cleanupIntervalMinutes` | 默认 60 分钟，正值最低 5 分钟 |

策略作用于后续请求，不追溯删除旧正文。留存清理保留小时聚合统计；外置媒体随日志删除。现有显式限制在升级时保留。旧键只在一次性迁移时读取，迁移后不再运行时兼容。

`systemLog.retentionDays`、`systemLog.maxRecords` 和 `systemLog.maxContentMB` 分别限制系统日志的时间、条数及内容字节数，默认均为 `0`（不限）。请求日志预算不包含系统日志、用量汇总、模型配置或 Agent 会话。内容预算不是 SQLite 文件的磁盘硬上限，索引、空闲页和 WAL 单独显示。首次升级会在数据目录保留数据库与配置的 `.pre-log-lifecycle` 备份，不会自动删除；留存清理保留历史用量汇总，只有“重置用量”清除汇总。

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

[Install]
WantedBy=multi-user.target
```

保存为 `/etc/systemd/system/elysia-api.service` 后执行 `sudo systemctl daemon-reload` 和 `sudo systemctl enable --now elysia-api`。用 `journalctl -u elysia-api` 查看启动日志。

## 升级、备份与恢复

SQLite 使用 WAL、5000 ms busy timeout、外键和 `synchronous=NORMAL`。

1. 升级前停止服务，备份 `config.json`、数据库及仍存在的 `-wal` / `-shm` 文件、`usage-assets/`。
2. 单独保护并备份实际使用的加密密钥；若来自环境变量，记录其安全恢复方式。数据库和密钥一同泄漏会使静态加密失去保护。
3. 保留旧程序版本，替换二进制或镜像，使用原配置与数据启动。
4. 检查 `/health`、面板登录、模型列表和一次推理请求。真实推理会产生上游费用。
5. 恢复时停止服务，将同一备份的配置、数据库、媒体和匹配密钥放回，并恢复权限，再启动兼容版本。不要仅替换数据库主文件或重新生成密钥。

在线备份应使用 SQLite 备份工具取得一致数据库，并协调媒体文件快照；不要直接复制仍在写入的数据库主文件。数据库目录不可写或磁盘已满会导致写入失败。

## 令牌恢复

面板令牌丢失时，停止服务，修改配置中的 `panelAccessToken` 为新的随机值，再启动并重新登录。推理令牌存于 SQLite，恢复面板访问后通过管理页修改。

## 热重载

`POST /api/admin/reload` 要求面板鉴权；运行配置更新接口见 [API 参考](webui-api.md#运行配置)。监听地址、端口、数据库路径和 pprof 路由变化需要重启，热重载不会重建这些资源。

`POST /__reload`、`POST /__shutdown` 只允许回环来源；代理转发时不要将这两个端点公开。

## 旧版本迁移

- `server.host` / `server.port` 仅在对应顶层字段缺省时作为回退。
- 当前配置解析不再读取 `tokens`、`modelGroups`，也不把 `dashboardToken` 当作面板令牌。不要依赖旧文档所述的自动迁移；在兼容旧版本导出数据或通过管理接口重建，并保留原备份。
- 已废弃的 `customProtocols` 字段由启动迁移处理并从配置移除。先备份，启动后在协议设计器检查结果。
- 模型能力目录、流式转换和自定义协议的限制见 [Maheshvara](maheshvara-protocol.md) 与[协议定义](protocol-definition-reference.md)。

## 故障处理

| 现象 | 检查 |
| --- | --- |
| 连接被拒绝 | 服务日志、实际端口；Docker 内是否设置 `ELYSIA_API_HOST=0.0.0.0` |
| 面板 401 | 使用面板令牌；检查配置路径，避免误用推理或 Agent Key |
| 上游连接被拦截 | 出站 CIDR 策略、DNS 解析结果和代理配置 |
| 用量日志没有正文 | `bodyMaxKB` 是否为正；历史请求不会补采集 |
| 升级后密钥不可解密 | 是否使用原主密钥或旧 `.db-key`，环境变量是否改变 |
| 模型列表为空 | 上游模型发现端点、逐 Key 权限、自定义协议的 `models` 定义 |
