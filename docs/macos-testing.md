# macOS 原生体验验证

App 运行目标为 macOS 12+。构建机需要与其 macOS 版本兼容的 Command Line Tools（或完整 Xcode）、Node.js 和 Go 1.25+；工具链还必须能够链接 arm64 和 x86_64 的 macOS 12 Swift 程序。原生壳仅使用系统框架，测试使用 Swift、AppKit、WebKit、Python 标准库及由 Clang 生成的架构校验夹具。运行及更新已打包的 App 不需要开发工具。

## 工具链与双架构构建

截至 2026-09-14 核对的 Apple 官方文档：

- [安装 Command Line Tools](https://developer.apple.com/documentation/xcode/installing-the-command-line-tools)：该包可以替代完整 Xcode，包含 macOS SDK 和工具链；系统同一时间只安装一个 CLT 版本，更新会替换旧版本。
- [Xcode 支持表](https://developer.apple.com/support/xcode/)：选择与构建机系统兼容的工具链。当前表中的 Xcode 26.6 支持 macOS Tahoe 26.2 至 26.x，并支持 macOS 11 至 26.5 部署目标。
- [Xcode 27 发布说明](https://developer.apple.com/documentation/xcode-release-notes/xcode-27-release-notes#Intel-Deprecation)：Xcode 本身只在 Apple Silicon 上运行，但 macOS 27 SDK 仍支持部署到 macOS 12+ 的 Intel + Apple Silicon 通用应用。不能将本机缺少 Intel 库误判为官方取消 universal 支持。

先运行独立检查，不需要预先生成后端二进制：

```sh
xcode-select -p
pkgutil --pkg-info com.apple.pkg.CLTools_Executables
xcrun --sdk macosx swiftc --version
npm run build:macos-app -- --check-toolchain
```

构建脚本通过 `xcrun --sdk macosx` 选择 Swift 编译器，并实际链接两种架构的最小 AppKit 程序。检查失败时立即退出，不删除旧 App 或 DMG；正常构建也会先执行同一检查。

本机 CLT `27.0.0.0.1788430756` / Swift 6.4 的 `libswiftCompatibility56.a` 和 `libswiftCompatibilityPacks.a` 只有 arm64/arm64e，缺少 x86_64，导致 macOS 12 Intel 链接失败。旧提交在同一环境也失败；只切换到 SDK 26.5 仍失败，因为兼容库属于编译器工具链。该结论针对这套本机安装，不代表所有 Xcode 27 安装都有同样问题。

遇到这种错误，应从 Apple 的 [More Downloads](https://developer.apple.com/download/all/?q=command%20line%20tools) 登录并下载安装兼容的 **Universal** CLT 包，再重跑检查。下载页的 CLT 26.6 稳定版分为 Apple silicon 与 Universal 两个安装包，本项目双架构构建应选择 `Command_Line_Tools_26.6_Universal.dmg`。本机安装后版本为 `26.6.0.0.1781586589`，上述 Swift 兼容库包含 x86_64、arm64 和 arm64e，双架构链接预检查通过。CLT 替换会影响这台机器上使用默认工具链的其他项目。若已有另一套完整 Xcode，可以只为本次命令选择它：

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

`test:macos-app` 在临时、独立 bundle ID 的测试 App 中运行本地 HTTP 子进程。它使用独立的配置、SQLite、主密钥目录和 UserDefaults 域，不注册登录项，不发送系统通知，不请求通知权限。测试钩子只在 `NATIVE_TESTS` 编译条件下存在，不会进入发布包。测试结束清理测试偏好、目录和本测试拥有的后端进程。

自动检查覆盖：

- 配置缺失时生成默认值；损坏时保留原文件；修改端口时保留未知配置键和文件权限。
- 已占用端口的回退、IPv4/IPv6 URL、后台健康检查、持续不健康时重启、正常停止、三次自动重启失败后停止、手动重试。
- 菜单栏用量脉冲部件：响应解析（毫秒时间桶、窗口汇总与 token 总量）、稀疏点按时间槽填充与越界丢弃、异常信封拒绝、RFC3339 查询编码、token 紧凑格式化，以及真实后端 `/api/admin/usage/pulse` 的管理员鉴权读取。
- 窗口重开不恢复最近页面（忽略并清理旧页面记录），已登录则进入总览；已有窗口保持当前页面。覆盖最小化恢复、窗口位置与主题、菜单栏复制令牌入口、首次不注入 Token/Cookie、持久化 WebKit 数据存储、关闭窗口释放 WebView 与消息处理器。
- 外接显示器移除后的几何恢复、窗口过大及部分移出屏幕的边界。
- 检查更新时禁用安装、下载进度、取消、HTTP 错误、下载临时文件生命周期、摘要缺失/不匹配、DMG 损坏和应用替换失败回滚。
- 使用 CoreFoundation 检查真实 Mach-O 夹具：开发工具不可用时仍接受双架构文件，拒绝单架构、无效及缺失文件；应用运行时不调用 `lipo`。
- macOS 12 LaunchAgent 稳定配置和含空格、引号及 shell 字符的路径参数。

`--panel` 需要先构建 App，使用包内真实的 universal Go 后端及 React WebUI，验证首次空白登录表单、手动登录后保存 Token/Cookie、关窗重开保留登录态、主动退出后重开仍需登录、主题和总览首屏、优雅停止与重启、端口及配置/主密钥/SQLite 保留。测试应用使用独立 bundle ID，并清理其 WebKit 数据存储。窗口静态预览保存在 `dist/macos-panel-preview.png`，使用测试数据，不会下载或安装更新。锁屏/无显示环境会暂停 WebKit 动画，因此快照仅在测试页面中结束有限的入场动画；它验证最终布局，不替代解锁后的动画与交互验收。

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
| 系统退出 | 正常服务与挂起服务分别执行 ⌘Q/系统退出，确认先请求优雅关闭；8 秒后 TERM、再 3 秒后 KILL，无孤立子进程。安装替换中暂时禁用退出。 |
| 更新 | 无新版本、离线、缺少 DMG、摘要错误、取消下载、只读安装目录、替换失败及成功更新；旧版本或可恢复备份保留，更新后进入总览，窗口、主题、配置、数据库和密钥保留；未安装 CLT 的机器也能完成更新校验。 |
| WebKit 导出 | 日志导出、取消保存、关闭窗口期间下载，确认导出仍完成或给出明确错误，外部链接交给默认浏览器。 |

2026-09-14 在 Apple Silicon / macOS 26.6.2 上安装 CLT 26.6 Universal 后，通过 69 项原生集成检查、19 项真实后端面板检查、7 个浏览器回归用例和 WebUI lint。`npm run build` 与 `npm run build:macos-app` 均通过，产出包含最新 WebUI 的双架构后端及原生壳，并完成签名、DMG 完整性和挂载内容校验。此前已复现工具链缺库时的提前失败与旧产物保留。浏览器回归覆盖网络错误与无效令牌的不同提示、失败后焦点恢复及使用同一令牌重试；桌面与手机截图确认协议设计器位于系统分组首位且标题说明已移除。macOS 12/Intel 真机、登录注销、系统通知权限、VoiceOver 和真实发布版本的完整自更新重启需按上表补充验收。

## 键盘与日志

| 动作 | 快捷键 |
| --- | --- |
| 关闭窗口 / 最小化 / 全屏 | ⌘W / ⌘M / ⌃⌘F |
| 显示主窗口 / 偏好设置 | ⌘0 / ⌘, |
| 重新加载面板 | ⌘R |
| 在浏览器打开 / 复制面板地址 | ⇧⌘B / ⇧⌘L |
| 复制 API 地址 / 复制面板令牌 | ⌥⌘C / ⇧⌥⌘C |
| 启动或停止服务 | ⌥⌘S |
| 检查更新 / 退出 | ⇧⌘U / ⌘Q |

菜单栏「查看运行日志」打开 `~/Library/Application Support/ElysiaApi/elysia-api.log`。原生启动、端口、健康检查和更新事件记录到 OSLog：

```sh
log stream --predicate 'subsystem == "dev.pinkelysiadev.ElysiaApi"' --level info
```
