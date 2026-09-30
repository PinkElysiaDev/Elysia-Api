# 开发指南

[文档索引](README.md) · **简体中文** · [English](development.en.md)

在本地开发、验证和构建 Elysia API。命令除特别说明外均在仓库根目录执行。

## 环境

| 工具 | 要求 |
| --- | --- |
| Node.js | 22+；CI 使用 22 |
| Go | 1.25+；CI 使用 1.25.x |
| npm | 随 Node.js 提供，用于 workspace 安装与构建 |
| macOS 打包 | macOS 主机，以及能链接 macOS 12 arm64 / x86_64 的 Swift / AppKit 工具链 |

Python 与视觉依赖仅用于重做登录素材，常规前端和发行构建不需要安装它们。

## 项目结构

```text
backend/
  agent/       # Agent engine
  config/      # Bootstrap and runtime policies
  relay/       # Protocols and upstream adapters
  server/      # HTTP routes, auth, CLI, orchestration
  storage/     # SQLite
  webui/       # Embedded frontend assets
packages/webui/ # React + Vite
scripts/       # Builds, native app, asset tooling
docs/          # Documentation
```

## 本地开发

安装 workspace 依赖：

```bash
npm install
```

在独立终端启动后端。以下命令将开发配置写在 `backend/`；首次运行从日志读取面板令牌：

```bash
cd backend
go run . --config ./config.json
```

另一个终端从仓库根目录启动前端：

```bash
npm run dev --workspace @root/webui
```

打开 `http://127.0.0.1:5273/`。Vite 默认代理到 `http://127.0.0.1:8765`，可用 `ELYSIA_DEV_PROXY` 覆盖；`ELYSIA_DEV_HOST` 控制开发服务器监听地址。完整代理与前端说明见 [WebUI 开发](../packages/webui/README.md)。

使用开发配置和测试数据。后台出站策略默认阻止回环、私网和保留段；需要本机模拟上游时仅调整相关 CIDR。

## 验证

后端检查，在 `backend/` 执行：

```bash
go test ./...
```

前端检查，在仓库根目录执行：

```bash
npm run lint --workspace @root/webui
npm run build:webui
npm exec --workspace @root/webui playwright install chromium
npm run test:e2e --workspace @root/webui
```

浏览器测试使用模拟 API，并自动启动 `127.0.0.1:5274` 的前端。已安装 Chrome 时可设置 `PLAYWRIGHT_CHANNEL=chrome`。真实模型评测需要显式凭证，会产生用量；常规文档验证不运行它。

原生测试使用 `npm run test:macos-app`；真实打包面板检查使用 `npm run test:macos-app -- --panel`，前置条件见 [macOS 验证](macos-testing.md)。

## 发行构建

```bash
npm run build
```

构建脚本先编译前端，将资源复制至后端嵌入目录，再以 `CGO_ENABLED=0` 交叉编译六种目标。输出目录为 `dist/standalone/`，每次构建会重新创建此目录。

| 平台 | 输出 |
| --- | --- |
| Windows | `elysia-api-windows-amd64.exe`、`elysia-api-windows-arm64.exe` |
| Linux | `elysia-api-linux-amd64`、`elysia-api-linux-arm64` |
| macOS 命令行 | `elysia-api-darwin-amd64`、`elysia-api-darwin-arm64` |

macOS 命令行产物用于本地运行及 App 组装，Release 对 macOS 发布 DMG。WebUI 单独构建输出至 `packages/webui/dist/`，完整构建才同步到后端嵌入目录。

### macOS 打包

```bash
npm run build:macos-app -- --check-toolchain
npm run build
npm run build:macos-app
```

输出通用 `ElysiaApi.app` 和 `elysia-api-macos.dmg`。构建会验证双架构、签名、DMG 完整性及挂载内容。工具链失败时应更换兼容的工具链，不提高最低系统版本或删去 Intel 目标。

### 版本与发布

后端版本由构建脚本从最近 Git tag 解析，经 ldflags 注入 `/health`，无 tag 时为 `dev`。原生 App 的版本解析有自身回退逻辑，见打包脚本。CI 在 `v*` tag 或手动触发时构建平台产物；发布附带 SHA256 校验文件。

## 文档维护

- 先核对源码与测试，再更新中文及对应 `.en.md`；保留相同接口、参数、默认值和操作顺序。
- 入口和专题各有职责：README 负责首次使用；专题承接配置、限制、恢复和完整参考。避免多处复制长参数表。
- 页面包含用途、语言切换和文档索引；操作型文档给出前提、可执行示例、验证与故障处理；参考型文档给出契约、字段、示例与限制。
- 统一使用“模型源 / model source”“模型组 / model group”“面板令牌 / panel token”“推理令牌 / relay token”。字段名、端点和 CLI 标识不翻译。
- 说明性片段用 `jsonc`、`text` 或明确说明；`json` 代码块应可解析。凭证使用占位符，不提交真实配置、日志或令牌。
- 保留现行文档路径和被引用的标题锚点；英文页面优先链接英文专题。阶段性设计、审查与验收记录中的有效内容并入现行指南后，删除旧文件并更新引用；历史细节通过 Git 历史追溯。

CLI 中文手册来自命令表及帮助渲染器。不要直接编辑生成结果，在 `backend/` 执行：

```bash
UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
```

文档开头、导航和章节格式由 `agent_cli_contract_test.go` 中的生成模板控制。英文手册是完整人工译本；命令变更时同步检查参数、示例和权限说明，运行不带更新变量的同一测试确认中文未过期。

## 提交检查

提交前检查 diff、链接、代码块和双语对应。只运行与变更相关的检查并说明实际结果；不要把历史测试记录写成当前验证结果。修改业务行为时同时更新相关文档。
