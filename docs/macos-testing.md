# macOS 原生体验验证

[文档首页](README.md) · **简体中文** · [English](macos-testing.en.md)

本指南用于验证原生壳、更新器和内嵌面板。以下命令均在仓库根目录执行；先按[开发指南](development.md)安装项目依赖。

## 前提

App 运行目标为 macOS 12+。构建机需要与其 macOS 版本兼容的 Command Line Tools（或完整 Xcode）、Node.js 和 Go 1.25+；工具链还必须能够链接 arm64 和 x86_64 的 macOS 12 Swift 程序。原生壳仅使用系统框架，测试使用 Swift、AppKit、WebKit、Python 标准库及由 Clang 生成的架构校验夹具。运行及更新已打包的 App 不需要开发工具。

## 进程与关闭生命周期

菜单栏主壳是后端和更新的 supervisor；面板复用包内 `ElysiaApi` 可执行文件，通过 `--webui-process` 在独立子进程中运行 AppKit 窗口和 WebKit。关窗先完成窗口与导出清理，再退出面板进程及其 WebKit 服务，主壳和原后端保持运行。这样关闭后的资源回收不再依赖 WebKit 是否保留已经释放的 `WKWebView` 所使用的服务。再次显示面板创建新的子进程；反复显示已打开的面板复用当前进程。

面板使用默认持久化 WebKit 数据存储保留手动登录状态和 Cookie，主题、窗口尺寸及位置保存在同一应用的偏好中。关闭面板只结束运行中的资源，不清除持久数据。主壳异常退出时，面板通信管道和后端 stdin 收到 EOF，两者进入各自的退出流程。

主壳在后端健康且面板关闭时每 30 秒检查健康状态；面板打开、服务启动或恢复期间每 3 秒检查。后端进程退出回调立即进入退出处理和恢复流程，不等待下一次健康轮询。

## 工具链与双架构构建

按 Apple 的[安装说明](https://developer.apple.com/documentation/xcode/installing-the-command-line-tools)配置 Command Line Tools，或使用完整 Xcode。通过 [Xcode 支持表](https://developer.apple.com/support/xcode/)核对构建机系统与部署目标的兼容性。

先运行独立检查，不需要预先生成后端二进制：

```sh
xcode-select -p
pkgutil --pkg-info com.apple.pkg.CLTools_Executables
xcrun --sdk macosx swiftc --version
npm run build:macos-app -- --check-toolchain
```

构建脚本通过 `xcrun --sdk macosx` 选择 Swift 编译器，并实际链接两种架构的最小 AppKit 程序。检查失败时立即退出，不删除旧 App 或 DMG；正常构建也会先执行同一检查。

如果 Intel 链接提示缺少 Swift 兼容库，检查编译器工具链中的库是否包含 x86_64 与 arm64。只切换 SDK 不一定能解决编译器兼容库缺失的问题。通过 Apple 的 [More Downloads](https://developer.apple.com/download/all/?q=command%20line%20tools)选择与构建机兼容、包含两种架构库的工具链（例如 Universal CLT），安装后重新运行检查。替换默认 CLT 会影响本机其他项目；已有另一套完整 Xcode 时，可以只为本次命令选择它：

```sh
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer npm run build:macos-app
```

保持 macOS 12 和双架构目标，不通过忽略缺失库、提高最低系统版本或静默改成 arm64-only 绕过错误。`git describe --exact-match` 找不到当前提交的标签时按 `package.json` 回退版本号，属于正常情况。

## 自动验证

```sh
npm run build:macos-app -- --check-toolchain
npm run build:webui
npm run lint
npm run test:macos-app
npm run build
npm run build:macos-app
npm run test:macos-app -- --panel
PLAYWRIGHT_CHANNEL=chrome npm run test:e2e --workspace @root/webui
```

`test:macos-app` 在临时、独立 bundle ID 的测试 App 中运行本地 HTTP 子进程，同时验证真实主壳与独立 WebUI 进程。它使用独立的配置、SQLite、主密钥目录和 UserDefaults 域，不注册登录项，不发送系统通知，不请求通知权限。测试钩子只在 `NATIVE_TESTS` 编译条件下存在，不会进入发布包。测试结束清理测试偏好、目录和本测试拥有的进程。

自动检查覆盖：

- 配置缺失时生成默认值；损坏时保留原文件；修改端口时保留未知配置键和文件权限。
- 已占用端口的回退、IPv4/IPv6 URL、后台健康检查、菜单启动与重启、持续不健康时重启、退出时优雅排空、三次自动重启失败后停止、手动重试。
- 菜单栏用量脉冲部件：响应解析（毫秒时间桶、窗口汇总与 token 总量）、稀疏点按时间槽填充与越界丢弃、异常信封拒绝、RFC3339 查询编码、token 紧凑格式化，以及真实后端 `/api/admin/usage/pulse` 的管理员鉴权读取。
- 窗口重开不恢复最近页面（忽略并清理旧页面记录），已登录则进入总览；已有窗口保持当前页面。覆盖最小化恢复、窗口位置与主题、菜单栏复制令牌入口、首次不注入 Token/Cookie、持久化 WebKit 数据存储、关闭窗口释放 WebView 与消息处理器。
- 外接显示器移除后的几何恢复、窗口过大及部分移出屏幕的边界。
- 检查更新时禁用安装、下载进度、取消、HTTP 错误、下载临时文件生命周期、摘要缺失/不匹配、DMG 损坏和应用替换失败回滚。
- 连续关窗释放窗口、WebView、胶囊及保存对话框；导出中关窗或退出时取消下载并清理临时文件，保留已有目标文件，防止下载期间出现的同名文件被覆盖。
- 真实主壳与 WebUI 子进程连续开关 3 次：通过 `launchctl print pid/<WebUI PID>` 确认 WebContent、Networking 和 GPU 服务的归属，关窗后所属进程退出，主壳无 WebKit 服务且原后端继续健康运行；夹具页面的 JavaScript 心跳和周期 HTTP 请求停止。
- 反复显示及第二次启动应用复用原主壳、面板和后端；主壳异常退出后，通过管道 EOF 结束面板、其 WebKit 服务及后端。
- 下载取消后可再次更新；安装期间禁止服务重启；后端收到 TERM 后完成排空，父进程管道关闭时自行退出。
- 真实签名 DMG 暂存、挂起工具超时、旧壳退出后独立 helper 替换、新壳就绪确认，以及启动失败、错误确认和超时回滚；HTML 加载但 React 界面未就绪时保留旧包并回滚。
- 使用 CoreFoundation 检查真实 Mach-O 夹具：开发工具不可用时仍接受双架构文件，拒绝单架构、无效及缺失文件；应用运行时不调用 `lipo`。
- macOS 12 LaunchAgent 稳定配置和含空格、引号及 shell 字符的路径参数。

`--panel` 需要先构建 App，使用包内真实的 universal Go 后端及 React WebUI，验证首次空白登录表单、手动登录后保存 Token/Cookie、关窗重开保留登录态、主动退出后重开仍需登录、主题和总览首屏、菜单重启时优雅排空、端口及配置/主密钥/SQLite 保留。另以真实后端和 React 面板执行主壳/子进程的 3 次开关及主壳异常退出检查；JavaScript 心跳检查使用普通测试中的 HTTP 夹具。测试应用使用独立 bundle ID，并清理其 WebKit 数据存储；更新交接检查仅替换临时测试副本，不访问发布端或修改已安装的应用。窗口静态预览保存在 `dist/macos-panel-preview.png`，使用测试数据。锁屏/无显示环境会暂停 WebKit 动画，因此快照仅在测试页面中结束有限的入场动画；它验证最终布局，不替代解锁后的动画与交互验收。

`build:macos-app` 会分别编译 macOS 12 目标的 arm64 / x86_64 原生壳，然后执行：

- 对 `ElysiaApi` 与 `elysia-api` 执行 `lipo -verify_arch arm64 x86_64`。
- 对 Info.plist 执行 `plutil -lint`。
- 对应用执行 `codesign --verify --deep --strict --verbose=2`。
- 执行 `hdiutil verify`，只读挂载 DMG，检查 App、双架构、签名和 `/Applications` 快捷方式，最后卸载。

产物：`dist/standalone/ElysiaApi.app`、`dist/standalone/elysia-api-macos.dmg`。构建签名是 ad-hoc 签名，签名验证不等同于 Developer ID 公证。

## 真机验收矩阵

以下需要对应系统、系统授权或交互，自动测试不替代这些验证。

| 范围 | 操作与预期 |
| --- | --- |
| macOS 12 / Intel | 安装 DMG；验证双架构中的 x86_64 原生运行、WebKit 面板、窗口、菜单、导出。 |
| macOS 13+ / Apple Silicon | 从 Applications 启动；检查首次窗口尺寸、深浅色、标准全屏和减少动态效果。 |
| macOS 12 登录项 | 连续启用、禁用、再启用；确认只有固定 label 的 LaunchAgent，注销后重新登录仅显示菜单栏；移动应用后启动应更新路径；卸载后下次登录清理旧项。 |
| macOS 13+ 登录项 | 启用后检查系统登录项；如需批准，从菜单入口进入系统设置；系统中禁用后再次启动应用不得擅自重新启用。 |
| 通知 | 允许、拒绝、系统设置中重新允许、应用内关闭；重要通知点击后打开窗口，新版本/更新失败点击后可见更新条；菜单项能解释授权状态。 |
| 窗口和辅助功能 | ⌘W、Dock 重开、菜单重开、最小化、全屏、标题栏拖动/双击、拔除外接屏；VoiceOver 朗读窗口、状态栏、菜单项、进度及错误动作。 |
| 关窗后的进程 | 连续开关面板 3 次；关窗后 WebUI 子进程及其 WebContent/Networking/GPU 服务退出，原主壳和后端保持运行；重开保留登录、主题、尺寸和位置。用面板 PID 的 `launchctl print` 核对 WebKit 归属。 |
| 服务操作 | 未运行时显示「启动服务」，运行时显示「重启服务」；重启等待旧进程退出后再拉起新进程；启动、重启和安装期间禁用重复操作。 |
| 系统退出 | 正常服务与挂起服务分别执行 ⌘Q/系统退出；直接向拥有的后端发送 TERM，等待记录落盘，15 秒仍未退出才 KILL。父壳异常退出后，后端通过 stdin EOF 进入共同关停序列。校验暂存中暂时禁用退出。 |
| 后台健康与恢复 | 面板关闭且后端健康时检查周期为 30 秒；打开面板、启动或恢复时为 3 秒；后端异常退出由进程退出回调及时恢复。 |
| 更新 | 无新版本、离线、缺少 DMG、摘要错误、取消下载、只读安装目录、替换失败及成功更新；先校验暂存，再停止后端和退出旧壳，helper 替换并拉起新版；前台启动需后端健康且 React 界面就绪，后台启动需后端健康，之后才删除旧包；失败回滚。保留窗口、主题、配置、数据库和密钥；未安装 CLT 的机器也能完成更新校验。 |
| WebKit 导出 | 日志导出、取消保存、关闭窗口期间下载，确认导出仍完成或给出明确错误，外部链接交给默认浏览器。 |

## 键盘与日志

| 动作 | 快捷键 |
| --- | --- |
| 关闭窗口 / 最小化 / 全屏 | ⌘W / ⌘M / ⌃⌘F |
| 显示主窗口 / 偏好设置 | ⌘0 / ⌘, |
| 重新加载面板 | ⌘R |
| 在浏览器打开 / 复制面板地址 | ⇧⌘B / ⇧⌘L |
| 复制 API 地址 / 复制面板令牌 | ⌥⌘C / ⇧⌥⌘C |
| 启动或重启服务 | ⌥⌘S |
| 检查更新 / 退出 | ⇧⌘U / ⌘Q |

菜单栏「查看运行日志」打开 `~/Library/Application Support/ElysiaApi/elysia-api.log`。更新交接、启动确认和回滚日志保存在同目录的 `update.log`。原生启动、端口、健康检查和更新事件记录到 OSLog：

```sh
log stream --predicate 'subsystem == "dev.pinkelysiadev.ElysiaApi"' --level info
```

## 故障处理

| 现象 | 处理 |
| --- | --- |
| Intel 链接缺少 Swift 兼容库 | 运行工具链检查，选择含两种架构库的 CLT/Xcode；保留 macOS 12 部署目标 |
| `--panel` 找不到应用 | 先执行 `npm run build` 和 `npm run build:macos-app` |
| 无显示环境的快照不更新 | 使用测试提供的最终布局快照；动画与交互在解锁后的显示环境单独验收 |
| 更新验证失败 | 查看 OSLog 和运行日志，检查 DMG、摘要、目录权限和恢复备份，不跳过校验 |

部署与数据恢复见[部署指南](deployment.md)。
