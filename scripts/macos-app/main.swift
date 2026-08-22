// ElysiaApi macOS 原生壳:主窗口内嵌 WebUI 面板 + 状态栏 + 应用内更新。
// 仅依赖系统框架(Cocoa/WebKit),零第三方依赖,用 swiftc -O 编译。
//
// 行为概要:
// - 启动时拉起内嵌后端(Contents/MacOS/elysia-api),后端就绪后在主窗口加载面板
// - 数据全部放在 ~/Library/Application Support/ElysiaApi(配置/数据库/日志)
// - 首次运行自动生成配置:随机面板令牌 + 空闲端口探测(8765→8799→8800…)
// - 状态栏:模板图标 + 端口号/状态,菜单提供快捷操作
// - 有新版本时窗口左下角出现更新条,一键完成 下载→sha256 校验→整包替换→自动重启

import Cocoa
import CryptoKit
import Security
import WebKit

// MARK: - 常量与路径

let dataDirPath = NSHomeDirectory() + "/Library/Application Support/ElysiaApi"
let configPath = dataDirPath + "/config.json"
let logPath = dataDirPath + "/elysia-api.log"
let backendPath = Bundle.main.bundlePath + "/Contents/MacOS/elysia-api"
let versionPath = Bundle.main.bundlePath + "/Contents/Resources/version.txt"
let releasesAPI = "https://api.github.com/repos/PinkElysiaDev/Elysia-Api/releases/latest"
let initialVersion = "v0.0.0"

// MARK: - 配置模型

struct PanelConfig: Codable {
    var host: String = "127.0.0.1"
    var port: Int = 8765
    var panelAccessToken: String = ""
    var databasePath: String = "elysia-api.sqlite3"
    var logLevel: String = "info"
    var httpTimeout: Int = 120
    var secretKeyPath: String = ".master-key"

    init() {}

    // 与后端语义一致:字段缺失/为空对象时按默认值补齐,而不是解析失败,
    // 避免用户手改过的配置因少写一个键就被当作损坏。
    init(from decoder: Decoder) throws {
        self = PanelConfig()
        let container = try decoder.container(keyedBy: CodingKeys.self)
        host = try container.decodeIfPresent(String.self, forKey: .host) ?? host
        port = try container.decodeIfPresent(Int.self, forKey: .port) ?? port
        panelAccessToken = try container.decodeIfPresent(String.self, forKey: .panelAccessToken) ?? panelAccessToken
        databasePath = try container.decodeIfPresent(String.self, forKey: .databasePath) ?? databasePath
        logLevel = try container.decodeIfPresent(String.self, forKey: .logLevel) ?? logLevel
        httpTimeout = try container.decodeIfPresent(Int.self, forKey: .httpTimeout) ?? httpTimeout
        secretKeyPath = try container.decodeIfPresent(String.self, forKey: .secretKeyPath) ?? secretKeyPath
    }
}

/// GitHub release 中与本应用更新相关的信息
struct ReleaseInfo {
    let tag: String
    let dmgURL: String
    let dmgDigest: String   // "sha256:<hex>";缺失时拒绝自动更新
}

struct UpdateError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

// MARK: - 工具函数

func randomToken() -> String {
    var bytes = [UInt8](repeating: 0, count: 24)
    _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
    return "elysia-app-" + bytes.map { String(format: "%02x", $0) }.joined()
}

func portIsFree(_ port: UInt16) -> Bool {
    let fd = socket(AF_INET, SOCK_STREAM, 0)
    guard fd >= 0 else { return false }
    defer { close(fd) }
    var addr = sockaddr_in()
    addr.sin_family = sa_family_t(AF_INET)
    addr.sin_port = port.bigEndian
    addr.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))
    let result = withUnsafePointer(to: &addr) { pointer in
        pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
            bind(fd, sa, socklen_t(MemoryLayout<sockaddr_in>.size))
        }
    }
    return result == 0
}

/// 读取配置;仅当文件确实不存在时才生成默认配置(随机令牌 + 空闲端口探测)并写入。
/// 文件存在但无法解析时绝不覆盖原文件,返回 nil 交由调用方提示用户,
/// 否则一次解析失败就会重置用户的令牌与数据库路径。
func loadOrCreateConfig() -> PanelConfig? {
    let fm = FileManager.default
    if !fm.fileExists(atPath: configPath) {
        if !fm.fileExists(atPath: dataDirPath) {
            try? fm.createDirectory(atPath: dataDirPath, withIntermediateDirectories: true)
        }
        var port = 8765
        for candidate in [8765, 8799, 8800, 8801, 8802] where portIsFree(UInt16(candidate)) {
            port = candidate
            break
        }
        var config = PanelConfig()
        config.port = port
        config.panelAccessToken = randomToken()
        if let data = try? JSONEncoder().encode(config) {
            fm.createFile(atPath: configPath, contents: data, attributes: [.posixPermissions: 0o600])
        }
        return config
    }
    guard let data = fm.contents(atPath: configPath),
          let config = try? JSONDecoder().decode(PanelConfig.self, from: data) else { return nil }
    return config
}

func readBundledVersion() -> String {
    if let text = try? String(contentsOfFile: versionPath, encoding: .utf8) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty { return trimmed }
    }
    return initialVersion
}

/// "v1.2.3" → [1, 2, 3],用于版本比较;容忍 "4-local" 之类的后缀
func parseVersion(_ tag: String) -> [Int] {
    tag.dropFirst(tag.hasPrefix("v") ? 1 : 0)
        .split(separator: ".")
        .map { part in
            let digits = part.prefix { $0.isNumber }
            return Int(digits) ?? 0
        }
}

func isNewer(_ lhs: String, than rhs: String) -> Bool {
    let a = parseVersion(lhs), b = parseVersion(rhs)
    for i in 0..<max(a.count, b.count) {
        let x = i < a.count ? a[i] : 0
        let y = i < b.count ? b[i] : 0
        if x != y { return x > y }
    }
    return false
}

/// 由 logo 生成 macOS 风格的单色模板图标(黑色 + alpha,自动适配深浅色菜单栏)
func templateIcon(from path: String, size: CGFloat) -> NSImage? {
    guard let source = NSImage(contentsOfFile: path),
          source.size.width > 0 else { return nil }
    let image = NSImage(size: NSSize(width: size, height: size))
    image.lockFocus()
    let bounds = NSRect(x: 0, y: 0, width: size, height: size)
    let srcSize = source.size
    let scale = min(bounds.width / srcSize.width, bounds.height / srcSize.height)
    let drawWidth = srcSize.width * scale, drawHeight = srcSize.height * scale
    let drawRect = NSRect(x: (bounds.width - drawWidth) / 2, y: (bounds.height - drawHeight) / 2,
                          width: drawWidth, height: drawHeight)
    source.draw(in: drawRect)
    if let context = NSGraphicsContext.current?.cgContext {
        context.setFillColor(NSColor.black.cgColor)
        context.setBlendMode(.sourceAtop)
        context.fill(bounds)
    }
    image.unlockFocus()
    image.isTemplate = true
    return image
}

// MARK: - 注入 WebUI 的脚本

/// 主题上报:把页面背景色与深浅色状态回传给壳,壳据此着色标题栏/更新条等原生区域,
/// 并在用户切换深浅色时实时跟随。
func themeReporterScript() -> WKUserScript {
    let source = """
    (function() {
      function report() {
        try {
          var style = getComputedStyle(document.body);
          window.webkit.messageHandlers.theme.postMessage({
            dark: document.documentElement.classList.contains('dark'),
            background: style.backgroundColor
          });
        } catch (e) {}
      }
      new MutationObserver(report).observe(
        document.documentElement, { attributes: true, attributeFilter: ['class'] });
      window.addEventListener('load', report);
      report();
    })();
    """
    return WKUserScript(source: source, injectionTime: .atDocumentEnd, forMainFrameOnly: true)
}

/// 解析 CSS 颜色("rgb(r, g, b)" / "rgba(r, g, b, a)")
func cssColor(_ text: String) -> NSColor? {
    let pattern = #"rgba?\(\s*([0-9.]+)[,\s]+([0-9.]+)[,\s]+([0-9.]+)(?:[,\s/]+([0-9.]+))?\s*\)"#
    guard let regex = try? NSRegularExpression(pattern: pattern),
          let match = regex.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) else {
        return nil
    }
    func double(_ index: Int) -> Double? {
        guard let range = Range(match.range(at: index), in: text) else { return nil }
        return Double(text[range])
    }
    guard let red = double(1), let green = double(2), let blue = double(3) else { return nil }
    let alpha = double(4) ?? 1
    return NSColor(srgbRed: CGFloat(red / 255), green: CGFloat(green / 255),
                   blue: CGFloat(blue / 255), alpha: CGFloat(alpha))
}

// MARK: - 应用主体

/// 标题栏区域的透明拖拽条:不绘制任何内容,按住它拖动窗口。
/// 显式调用 performDrag 而非依赖 mouseDownCanMoveWindow 声明,
/// 后者在 WKWebView 覆盖同区域时不可靠。
final class DragTitlebarView: NSView {
    override func mouseDown(with event: NSEvent) {
        window?.performDrag(with: event)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKScriptMessageHandler, NSWindowDelegate {
    var window: NSWindow!
    var webView: WKWebView!
    var overlay: NSView!
    var overlaySpinner: NSProgressIndicator!
    var overlayLabel: NSTextField!
    var overlayButton: NSButton!
    var updateBar: NSView!
    var updateLabel: NSTextField!
    var updateButton: NSButton!
    var updateSpinner: NSProgressIndicator!
    var statusItem: NSStatusItem!
    var toggleItem: NSMenuItem!
    var statusLineItem: NSMenuItem!

    var config: PanelConfig!
    var backend: Process?
    var backendHost: String?   // 后端实际监听地址,启动时锁定(config 之后被手改也不会错位)
    var backendPort = 0
    var userStopping = false
    var restartCount = 0
    var panelLoaded = false
    var loadedPort = 0
    var currentVersion = readBundledVersion()
    var latestRelease: ReleaseInfo?

    let healthSession: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 3
        return URLSession(configuration: config)
    }()
    let apiSession: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 15
        return URLSession(configuration: config)
    }()

    /// 当前可用的 API 地址:后端在跑时用其实际监听地址(启动时锁定),
    /// 已停止时用配置地址(下次启动生效)
    private var apiBaseURL: String {
        if backend != nil, backendPort != 0 {
            return "http://\(backendHost ?? config.host):\(backendPort)"
        }
        return "http://\(config.host):\(config.port)"
    }

    // MARK: 生命周期

    func applicationDidFinishLaunching(_ notification: Notification) {
        guard ensureSingleInstance() else { exit(0) }

        let loadedConfig = loadOrCreateConfig()
        config = loadedConfig ?? PanelConfig()   // 解析失败时仅用内存默认值兜底,不写盘
        buildMainMenu()
        buildWindow()
        buildStatusItem()
        if loadedConfig == nil {
            showOverlay(text: "配置文件无法解析,已保留原文件:\n\(configPath)\n请修正或删除后点击重试", spinning: false, retry: true)
        } else {
            startBackend()
        }
        scheduleTimers()
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in
            self?.checkForUpdates(silent: true)
        }
    }

    func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool { true }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let process = backend, process.isRunning else { return .terminateNow }
        let semaphore = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in semaphore.signal() }
        requestBackendExit(process)
        _ = semaphore.wait(timeout: .now() + 12)
        if process.isRunning { kill(process.processIdentifier, SIGKILL) }
        return .terminateNow
    }

    @objc func quit(_ sender: Any?) { NSApp.terminate(nil) }

    // MARK: 单实例

    private func ensureSingleInstance() -> Bool {
        guard let bundleID = Bundle.main.bundleIdentifier else { return true }
        let others = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID)
            .filter { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }
        guard others.isEmpty else { return false }
        return true
    }

    // MARK: 主窗口与界面

    private static let updateBarHeight: CGFloat = 40
    private static let titleBarHeight: CGFloat = 28

    /// 最小主菜单:编辑菜单项是 WKWebView 复制/粘贴/全选等快捷键的依赖
    /// (标准 selector 不设 target,交由响应链分发到当前第一响应者)
    private func buildMainMenu() {
        let mainMenu = NSMenu()

        let appMenuItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "关于 ElysiaApi", action: #selector(showAbout), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "隐藏 ElysiaApi", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        appMenu.addItem(withTitle: "隐藏其他", action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "")
        appMenu.addItem(withTitle: "显示全部", action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "退出 ElysiaApi", action: #selector(quit), keyEquivalent: "q")
        appMenuItem.submenu = appMenu
        mainMenu.addItem(appMenuItem)

        let editMenuItem = NSMenuItem()
        let editMenu = NSMenu(title: "编辑")
        editMenu.addItem(withTitle: "撤销", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "重做", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(.separator())
        editMenu.addItem(withTitle: "剪切", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "复制", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "粘贴", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "全选", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editMenuItem.submenu = editMenu
        mainMenu.addItem(editMenuItem)

        let windowMenuItem = NSMenuItem()
        let windowMenu = NSMenu(title: "窗口")
        windowMenu.addItem(withTitle: "最小化", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        windowMenu.addItem(withTitle: "缩放", action: #selector(NSWindow.performZoom(_:)), keyEquivalent: "")
        windowMenu.addItem(withTitle: "关闭窗口", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        windowMenuItem.submenu = windowMenu
        mainMenu.addItem(windowMenuItem)

        NSApp.mainMenu = mainMenu
    }

    /// 自动登录:壳已持有 panelAccessToken(来自它自己生成的 config.json),在页面启动前
    /// 写入 WebUI 约定的 localStorage / Cookie 键,跳过登录页。不改 WebUI 与后端任何逻辑;
    /// sessionStorage 保证每次会话只注入一次,WebUI 内手动登出后不会被立即顶回登录态。
    /// 令牌只注入给本机回环来源的页面,不与具体端口绑定(端口可能因配置/重启变化)。
    private func autoLoginScript() -> WKUserScript {
        let token = (config.panelAccessToken).replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "'", with: "\\'")
        let source = """
        (function() {
          try {
            if (!/^http:\\/\\/(127\\.0\\.0\\.1|localhost|\\[::1\\])(:\\d+)?$/.test(location.origin)) { return; }
            var key = 'elysia-webui.panel-token';
            var done = sessionStorage.getItem('elysia-app.autologin-done');
            if (!done && !localStorage.getItem(key)) {
              var token = '\(token)';
              localStorage.setItem(key, token);
              document.cookie = 'panel_access_token=' + encodeURIComponent(token) +
                '; path=/; SameSite=Lax; max-age=2592000';
              sessionStorage.setItem('elysia-app.autologin-done', '1');
            }
          } catch (e) {}
        })();
        """
        return WKUserScript(source: source, injectionTime: .atDocumentStart, forMainFrameOnly: true)
    }

    private func buildWindow() {
        let rect = NSRect(x: 0, y: 0, width: 1180, height: 760)
        window = NSWindow(contentRect: rect,
                          styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                          backing: .buffered, defer: false)
        window.title = "Elysia API"
        window.minSize = NSSize(width: 940, height: 600)
        // 关闭窗口时不要释放 NSWindow:Swift 强引用无法感知 AppKit 的额外 release,
        // 否则窗口对象变成悬垂指针,再次「显示主窗口」会崩溃
        window.isReleasedWhenClosed = false
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.backgroundColor = .windowBackgroundColor
        window.delegate = self
        // 记住窗口位置;首次(重建)若无历史位置则居中
        if UserDefaults.standard.string(forKey: "NSWindow Frame ElysiaApiPanel") == nil {
            window.center()
        }
        window.setFrameAutosaveName("ElysiaApiPanel")

        let content = NSView(frame: rect)

        let webConfig = WKWebViewConfiguration()
        // 非持久化数据存储:登录态由启动脚本注入(见 autoLoginScript),无需落盘;
        // 这样关窗销毁 WebView 时,WebKit 的网络/GPU 辅助进程会一并退出
        webConfig.websiteDataStore = .nonPersistent()
        let userContent = WKUserContentController()
        userContent.add(self, name: "theme")
        userContent.addUserScript(autoLoginScript())
        userContent.addUserScript(themeReporterScript())
        webConfig.userContentController = userContent
        // 页面平铺整个窗口(含标题栏区域),红绿灯悬浮其上
        webView = WKWebView(frame: NSRect(x: 0, y: 0, width: rect.width, height: rect.height),
                            configuration: webConfig)
        webView.autoresizingMask = [.width, .height]
        webView.navigationDelegate = self
        webView.underPageBackgroundColor = .windowBackgroundColor
        content.addSubview(webView)

        // 标题栏区域的透明拖拽条:页面可见,但保证窗口可按住顶部拖动
        let dragStrip = DragTitlebarView(frame: NSRect(x: 0, y: rect.height - Self.titleBarHeight,
                                                       width: rect.width, height: Self.titleBarHeight))
        dragStrip.autoresizingMask = [.width, .minYMargin]
        content.addSubview(dragStrip)

        // 左下角更新条(有新版本时显示)
        updateBar = NSView(frame: NSRect(x: 0, y: 0, width: rect.width, height: Self.updateBarHeight))
        updateBar.autoresizingMask = [.width, .maxYMargin]
        updateBar.wantsLayer = true
        updateBar.layer?.backgroundColor = NSColor.controlBackgroundColor.cgColor
        let hairline = NSView(frame: NSRect(x: 0, y: Self.updateBarHeight - 1, width: rect.width, height: 1))
        hairline.autoresizingMask = [.width, .minYMargin]
        hairline.wantsLayer = true
        hairline.layer?.backgroundColor = NSColor.separatorColor.cgColor
        updateBar.addSubview(hairline)

        updateSpinner = NSProgressIndicator(frame: NSRect(x: 12, y: 12, width: 16, height: 16))
        updateSpinner.controlSize = .small
        updateSpinner.style = .spinning
        updateSpinner.isDisplayedWhenStopped = false
        updateBar.addSubview(updateSpinner)

        updateLabel = NSTextField(labelWithString: "")
        updateLabel.font = .systemFont(ofSize: 12)
        updateLabel.frame.origin = NSPoint(x: 36, y: 13)
        updateLabel.frame.size.width = 380
        updateBar.addSubview(updateLabel)

        updateButton = NSButton(title: "立即更新", target: self, action: #selector(runUpdate))
        updateButton.bezelStyle = .rounded
        updateButton.controlSize = .small
        updateButton.frame = NSRect(x: rect.width - 100, y: 6, width: 88, height: 28)
        updateButton.autoresizingMask = [.minXMargin]
        updateBar.addSubview(updateButton)
        updateBar.isHidden = true
        content.addSubview(updateBar)

        // 加载/错误遮罩
        overlay = NSView(frame: content.bounds)
        overlay.autoresizingMask = [.width, .height]
        overlay.wantsLayer = true
        overlay.layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
        overlaySpinner = NSProgressIndicator(frame: NSRect(x: rect.width / 2 - 16, y: rect.height / 2, width: 32, height: 32))
        overlaySpinner.style = .spinning
        overlaySpinner.autoresizingMask = [.minXMargin, .maxXMargin, .minYMargin, .maxYMargin]
        overlaySpinner.startAnimation(nil)
        overlay.addSubview(overlaySpinner)
        overlayLabel = NSTextField(labelWithString: "正在启动本地后端…")
        overlayLabel.font = .systemFont(ofSize: 13)
        overlayLabel.textColor = .secondaryLabelColor
        overlayLabel.frame.origin = NSPoint(x: rect.width / 2 - 100, y: rect.height / 2 - 34)
        overlayLabel.frame.size.width = 200
        overlayLabel.alignment = .center
        overlayLabel.autoresizingMask = [.minXMargin, .maxXMargin, .minYMargin, .maxYMargin]
        overlay.addSubview(overlayLabel)
        overlayButton = NSButton(title: "重试", target: self, action: #selector(retryStartup))
        overlayButton.bezelStyle = .rounded
        overlayButton.isHidden = true
        overlayButton.frame = NSRect(x: rect.width / 2 - 40, y: rect.height / 2 - 80, width: 80, height: 30)
        overlayButton.autoresizingMask = [.minXMargin, .maxXMargin, .minYMargin, .maxYMargin]
        overlay.addSubview(overlayButton)
        content.addSubview(overlay)

        window.contentView = content
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        // 关窗口 = 轻量后台(菜单栏仍在),从菜单栏「显示主窗口」重新打开
        return true
    }

    @objc func showMainWindow() {
        // 从纯菜单栏模式切回常规应用(Dock 图标恢复);窗口已随关闭销毁的,重建。
        // 打开面板意味着要使用服务:后端已停止时一并拉起;
        // 同时重读配置,保证自动登录注入的是面板里可能轮换过的最新令牌。
        NSApp.setActivationPolicy(.regular)
        if let fresh = loadOrCreateConfig() { config = fresh }
        if window == nil {
            buildWindow()
        }
        if backend == nil {
            restartCount = 0
            showOverlay(text: "正在启动本地后端…", spinning: true)
            startBackend()
        }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    /// 关闭窗口 = 轻量后台模式:Dock 图标隐藏,仅保留菜单栏图标。
    /// 同时销毁 WebView(WebKit 是壳进程的内存大头,约百 MB),重开窗口时重建,
    /// 登录态由启动脚本在每次重建窗口时重新注入,不受影响。
    func windowWillClose(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        webView?.removeFromSuperview()
        webView = nil
        window.contentView = nil
        window = nil
        panelLoaded = false
        loadedPort = 0
    }

    private func showOverlay(text: String, spinning: Bool, retry: Bool = false) {
        guard window != nil else { return }
        overlayLabel.stringValue = text
        overlaySpinner.isHidden = !spinning
        if spinning { overlaySpinner.startAnimation(nil) } else { overlaySpinner.stopAnimation(nil) }
        overlayButton.isHidden = !retry
        overlay.isHidden = false
    }

    private func hideOverlay() {
        guard window != nil else { return }
        overlay.isHidden = true
    }

    @objc private func retryStartup() {
        restartCount = 0
        showOverlay(text: "正在启动本地后端…", spinning: true)
        startBackend()
    }

    private func setUpdateBarVisible(_ visible: Bool) {
        guard window != nil else { return }
        guard updateBar.isHidden == visible else { return }
        updateBar.isHidden = !visible
        let barHeight = visible ? Self.updateBarHeight : 0
        var frame = webView.frame
        frame.origin.y = barHeight
        frame.size.height = window.contentView!.bounds.height - barHeight
        webView.frame = frame
    }

    private func setUpdateBarMode(updating: Bool, version: String?) {
        guard window != nil else { return }
        if updating {
            updateSpinner.startAnimation(nil)
            updateLabel.stringValue = "正在下载并安装更新,完成后自动重启…"
            updateButton.isHidden = true
        } else {
            updateSpinner.stopAnimation(nil)
            updateLabel.stringValue = "可更新到 \(version ?? "")"
            updateButton.isHidden = false
        }
    }

    // MARK: 状态栏

    private func buildStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        let logo = Bundle.main.bundlePath + "/Contents/Resources/logo.png"
        if let icon = templateIcon(from: logo, size: 18) {
            statusItem.button?.image = icon
        } else {
            statusItem.button?.image = NSImage(systemSymbolName: "shippingbox", accessibilityDescription: "Elysia API")
        }
        let menu = NSMenu()
        statusLineItem = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        statusLineItem.isEnabled = false
        menu.addItem(statusLineItem)
        menu.addItem(.separator())
        menu.addItem(withTitle: "显示主窗口", action: #selector(showMainWindow), keyEquivalent: "")
        toggleItem = menu.addItem(withTitle: "停止服务", action: #selector(toggleBackend), keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "拷贝 API 地址", action: #selector(copyAPIURL), keyEquivalent: "")
        menu.addItem(withTitle: "拷贝面板访问令牌", action: #selector(copyPanelToken), keyEquivalent: "")
        menu.addItem(withTitle: "检查更新…", action: #selector(checkForUpdatesFromMenu), keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "打开数据文件夹", action: #selector(openDataFolder), keyEquivalent: "")
        menu.addItem(withTitle: "查看运行日志", action: #selector(openLog), keyEquivalent: "")
        menu.addItem(withTitle: "关于 ElysiaApi", action: #selector(showAbout), keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "退出 ElysiaApi", action: #selector(quit), keyEquivalent: "q")
        for item in menu.items where item.action != nil {
            item.target = self
        }
        statusItem.menu = menu
        refreshStatusUI()
    }

    private func refreshStatusUI() {
        let running = backend != nil
        let base = apiBaseURL
        statusItem.button?.toolTip = running ? "Elysia API · \(base)" : "Elysia API · 已停止"
        statusLineItem.title = running
            ? "运行中 · \(base) · \(currentVersion)"
            : "已停止 · \(base) · \(currentVersion)"
        toggleItem.title = running ? "停止服务" : "启动服务"
    }

    // MARK: 后端进程管理

    func startBackend() {
        guard backend == nil else { return }
        guard let fresh = loadOrCreateConfig() else {
            showOverlay(text: "配置文件无法解析,已保留原文件:\n\(configPath)\n请修正或删除后重试", spinning: false, retry: true)
            refreshStatusUI()
            return
        }
        config = fresh
        let fm = FileManager.default
        if !fm.fileExists(atPath: logPath) {
            fm.createFile(atPath: logPath, contents: nil)
        }
        guard let logHandle = try? FileHandle(forWritingTo: URL(fileURLWithPath: logPath)) else {
            showOverlay(text: "无法写入日志文件:\n\(logPath)", spinning: false, retry: true)
            return
        }
        _ = try? logHandle.seekToEnd()
        let process = Process()
        process.executableURL = URL(fileURLWithPath: backendPath)
        process.arguments = ["--config", configPath]
        process.standardOutput = logHandle
        process.standardError = logHandle
        process.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async { self?.backendDidExit() }
        }
        do {
            try process.run()
            backend = process
            backendHost = config.host
            backendPort = config.port
            userStopping = false
            refreshStatusUI()
        } catch {
            logHandle.closeFile()
            showOverlay(text: "后端启动失败:\n\(error.localizedDescription)", spinning: false, retry: true)
            refreshStatusUI()
        }
    }

    /// 优雅停止后端:优先调用后端自带的 /__shutdown(等待在途请求并把 usage 队列落库),
    /// 超时再 SIGTERM,仍不退出才 SIGKILL。
    private func requestBackendExit(_ process: Process) {
        if let url = URL(string: "\(apiBaseURL)/__shutdown") {
            var request = URLRequest(url: url)
            request.httpMethod = "POST"
            healthSession.dataTask(with: request).resume()
        }
        DispatchQueue.global().async { [weak process] in
            var deadline = Date().addingTimeInterval(8)
            while let process, process.isRunning, Date() < deadline { usleep(200_000) }
            guard let process, process.isRunning else { return }
            process.terminate()
            deadline = Date().addingTimeInterval(3)
            while Date() < deadline, process.isRunning { usleep(200_000) }
            if process.isRunning { kill(process.processIdentifier, SIGKILL) }
        }
    }

    func stopBackend() {
        guard let process = backend, process.isRunning else {
            backend = nil
            backendHost = nil
            backendPort = 0
            refreshStatusUI()
            return
        }
        userStopping = true
        requestBackendExit(process)
        refreshStatusUI()
    }

    private func backendDidExit() {
        backend = nil
        backendHost = nil
        backendPort = 0
        if userStopping {
            userStopping = false
        } else if restartCount < 3 {
            // 意外退出:自动重启(最多 3 次)
            restartCount += 1
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
                self?.startBackend()
            }
            refreshStatusUI()
            return
        } else {
            showOverlay(text: "后端已停止运行", spinning: false, retry: true)
        }
        refreshStatusUI()
    }

    @objc private func toggleBackend() {
        if backend == nil {
            restartCount = 0
            showOverlay(text: "正在启动本地后端…", spinning: true)
            startBackend()
        } else {
            stopBackend()
        }
    }

    // MARK: 健康检查与面板加载

    private func scheduleTimers() {
        Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            self?.pollHealth()
        }
        Timer.scheduledTimer(withTimeInterval: 24 * 3600, repeats: true) { [weak self] _ in
            self?.checkForUpdates(silent: true)
        }
    }

    private func pollHealth() {
        guard backend != nil else { return }
        let base = apiBaseURL
        guard let url = URL(string: "\(base)/health") else { return }
        healthSession.dataTask(with: url) { [weak self] _, response, _ in
            DispatchQueue.main.async {
                guard let self, self.backend != nil, self.webView != nil else { return }
                let ok = (response as? HTTPURLResponse)?.statusCode == 200
                if ok {
                    self.restartCount = 0
                    // 面板尚未加载,或后端换端口重启过(config 可能被改过)→ 加载/重载面板
                    if !self.panelLoaded || self.loadedPort != self.backendPort,
                       let panelURL = URL(string: "\(base)/ui/") {
                        self.panelLoaded = true
                        self.loadedPort = self.backendPort
                        self.webView.load(URLRequest(url: panelURL))
                    }
                    self.hideOverlay()
                }
            }
        }.resume()
    }

    // MARK: - WKNavigationDelegate

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        hideOverlay()
    }

    /// 面板加载失败(含连接被拒的 provisional 阶段):清掉"已加载"标记,
    /// 健康轮询确认后端恢复后会自动重载页面,无需用户手动重试。
    private func handlePanelLoadFailure(_ error: Error) {
        panelLoaded = false
        showOverlay(text: "面板加载失败:\n\(error.localizedDescription)", spinning: false, retry: true)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        handlePanelLoadFailure(error)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        handlePanelLoadFailure(error)
    }

    // MARK: - 主题同步(WKScriptMessageHandler)

    func userContentController(_ userContentController: WKUserContentController,
                               didReceive message: WKScriptMessage) {
        guard message.name == "theme",
              let body = message.body as? [String: Any] else { return }
        let dark = body["dark"] as? Bool ?? false
        let background = (body["background"] as? String).flatMap(cssColor) ?? .windowBackgroundColor
        applyTheme(dark: dark, background: background)
    }

    /// 用页面主题为窗口背景/遮罩/更新条着色,使原生区域与网页无缝衔接
    private func applyTheme(dark: Bool, background: NSColor) {
        guard window != nil else { return }
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.backgroundColor = background
        overlay.layer?.backgroundColor = background.cgColor
        updateBar.layer?.backgroundColor = background.cgColor
        updateLabel.textColor = dark ? .white : .black
        webView.underPageBackgroundColor = background
    }

    // MARK: 更新

    @objc private func checkForUpdatesFromMenu() { checkForUpdates(silent: false) }

    private func checkForUpdates(silent: Bool) {
        guard let url = URL(string: releasesAPI) else { return }
        apiSession.dataTask(with: url) { [weak self] data, response, error in
            DispatchQueue.main.async {
                guard let self else { return }
                let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                guard let data, error == nil, let info = Self.parseRelease(data) else {
                    if !silent { self.alert(Self.updateCheckFailure(error: error, status: status)) }
                    return
                }
                guard isNewer(info.tag, than: self.currentVersion) else {
                    if !silent { self.alert("已是最新版本 \(self.currentVersion)。") }
                    return
                }
                self.latestRelease = info
                if self.window == nil { self.showMainWindow() }
                self.setUpdateBarMode(updating: false, version: info.tag)
                self.setUpdateBarVisible(true)
                if !silent { self.alert("发现新版本 \(info.tag),已在窗口左下角显示更新按钮。") }
            }
        }.resume()
    }

    /// 区分检查更新的失败场景,避免把「无 release / 无 DMG 资产」误报成网络问题
    private static func updateCheckFailure(error: Error?, status: Int) -> String {
        if let error { return "检查更新失败:无法连接 GitHub(\(error.localizedDescription))" }
        if status == 404 { return "GitHub 上还没有发布 release,暂无可更新内容。" }
        if status == 200 { return "最新 release 未包含 elysia-api-macos.dmg,暂不支持应用内更新。" }
        return "检查更新失败:GitHub 返回 HTTP \(status)。"
    }

    /// 从 releases/latest 的 JSON 提取 tag 与 macOS DMG 的下载地址/digest
    private static func parseRelease(_ data: Data) -> ReleaseInfo? {
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let tag = json["tag_name"] as? String,
              let assets = json["assets"] as? [[String: Any]] else { return nil }
        for entry in assets where entry["name"] as? String == "elysia-api-macos.dmg" {
            if let url = entry["browser_download_url"] as? String {
                return ReleaseInfo(tag: tag, dmgURL: url, dmgDigest: entry["digest"] as? String ?? "")
            }
        }
        return nil
    }

    /// 一键更新:下载新 DMG 并校验 → 取出完整新应用包 → 原子替换当前 .app → 重启。
    /// 替换的是整个应用包(壳、后端、面板一并更新),新版由 CI 完整签名,本机无需重签;
    /// 配置与数据都在应用包外,不受影响。
    @objc private func runUpdate() {
        guard let release = latestRelease else { return }
        setUpdateBarMode(updating: true, version: nil)
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self else { return }
            do {
                let dmg = try self.downloadVerified(url: release.dmgURL, digest: release.dmgDigest)
                defer { try? FileManager.default.removeItem(at: dmg) }
                let stagedApp = try Self.extractApp(fromDMG: dmg)
                defer { try? FileManager.default.removeItem(at: stagedApp.deletingLastPathComponent()) }
                try Self.replaceBundle(with: stagedApp)
                DispatchQueue.main.async { self.relaunchAfterUpdate() }
            } catch {
                let message = error.localizedDescription
                DispatchQueue.main.async { self.updateFailed(message) }
            }
        }
    }

    /// 下载 release 资产并校验 sha256。官方未提供 digest 时按不可信处理、直接失败
    /// (fail closed),不做无校验安装。
    private func downloadVerified(url: String, digest: String) throws -> URL {
        let expected = digest.hasPrefix("sha256:") ? String(digest.dropFirst(7)) : digest
        guard expected.count == 64 else {
            throw UpdateError(message: "官方未提供 DMG 的 sha256 摘要,已取消更新")
        }
        guard let remote = URL(string: url) else { throw UpdateError(message: "下载地址无效") }
        let semaphore = DispatchSemaphore(value: 0)
        var downloaded: URL?
        var failure: Error?
        URLSession.shared.downloadTask(with: remote) { localURL, _, error in
            if let localURL { downloaded = localURL } else { failure = error }
            semaphore.signal()
        }.resume()
        semaphore.wait()
        if let failure { throw failure }
        guard let downloaded else { throw UpdateError(message: "下载失败") }
        let stable = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try? FileManager.default.removeItem(at: stable)
        try FileManager.default.moveItem(at: downloaded, to: stable)
        let actual = try Self.sha256(fileAt: stable)
        if actual != expected {
            throw UpdateError(message: "sha256 校验不一致(官方 \(expected.prefix(12))… / 实际 \(actual.prefix(12))…)")
        }
        return stable
    }

    private static func sha256(fileAt url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty {
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    /// 静默挂载 DMG,把其中的完整 ElysiaApi.app 复制到与当前应用包同一卷的暂存目录
    /// (跨卷无法原子重命名)。返回暂存的应用包 URL,调用方负责清理暂存目录。
    private static func extractApp(fromDMG dmg: URL) throws -> URL {
        let manager = FileManager.default
        let mountPoint = manager.temporaryDirectory
            .appendingPathComponent("elysia-dmg-\(UUID().uuidString)")
        try manager.createDirectory(at: mountPoint, withIntermediateDirectories: false)
        defer {
            let detach = Process()
            detach.executableURL = URL(fileURLWithPath: "/usr/bin/hdiutil")
            detach.arguments = ["detach", mountPoint.path, "-quiet"]
            try? detach.run()
            detach.waitUntilExit()
            try? manager.removeItem(at: mountPoint)
        }
        let attach = Process()
        attach.executableURL = URL(fileURLWithPath: "/usr/bin/hdiutil")
        attach.arguments = ["attach", "-nobrowse", "-readonly", "-mountpoint",
                            mountPoint.path, dmg.path]
        try attach.run()
        attach.waitUntilExit()
        guard attach.terminationStatus == 0 else {
            throw UpdateError(message: "DMG 挂载失败")
        }
        let mountedApp = mountPoint.appendingPathComponent("ElysiaApi.app")
        guard manager.fileExists(atPath: mountedApp.appendingPathComponent("Contents/MacOS/elysia-api").path) else {
            throw UpdateError(message: "DMG 中未找到完整的 ElysiaApi.app")
        }
        let stagingDir = Bundle.main.bundleURL.deletingLastPathComponent()
            .appendingPathComponent(".ElysiaApi-update-\(UUID().uuidString)")
        try manager.createDirectory(at: stagingDir, withIntermediateDirectories: false)
        do {
            try manager.copyItem(at: mountedApp, to: stagingDir.appendingPathComponent("ElysiaApi.app"))
        } catch {
            try? manager.removeItem(at: stagingDir)
            throw error
        }
        return stagingDir.appendingPathComponent("ElysiaApi.app")
    }

    /// 原子替换当前应用包:旧包先重命名让位,新包就位失败则回滚,
    /// 全程不存在"半新半旧"的损坏状态;写入失败(如只读位置)则原样保留旧包。
    private static func replaceBundle(with stagedApp: URL) throws {
        let manager = FileManager.default
        let bundleURL = Bundle.main.bundleURL
        let backupURL = bundleURL.deletingLastPathComponent()
            .appendingPathComponent("ElysiaApp.old-\(ProcessInfo.processInfo.processIdentifier)")
        try? manager.removeItem(at: backupURL)
        try manager.moveItem(at: bundleURL, to: backupURL)
        do {
            try manager.moveItem(at: stagedApp, to: bundleURL)
        } catch {
            try? manager.moveItem(at: backupURL, to: bundleURL)
            throw UpdateError(message: "无法替换应用包(\(error.localizedDescription)),请手动下载 DMG 安装")
        }
        try? manager.removeItem(at: backupURL)
    }

    /// 替换完成后重启:由分离的小 shell 等待本进程退出再启动新包(避开单实例保护);
    /// 退出流程会顺带优雅停止后端。
    private func relaunchAfterUpdate() {
        let pid = ProcessInfo.processInfo.processIdentifier
        let script = "while kill -0 \(pid) 2>/dev/null; do sleep 0.2; done; open \"\(Bundle.main.bundlePath)\""
        let helper = Process()
        helper.executableURL = URL(fileURLWithPath: "/bin/sh")
        helper.arguments = ["-c", script]
        do {
            try helper.run()
            NSApp.terminate(nil)
        } catch {
            updateFailed("新版本已就位,但无法自动重启:\(error.localizedDescription)")
        }
    }

    private func updateFailed(_ message: String) {
        if let tag = latestRelease?.tag {
            setUpdateBarMode(updating: false, version: tag)
        } else {
            setUpdateBarVisible(false)
        }
        alert("更新未完成:\(message)")
    }

    // MARK: 菜单动作

    @objc private func copyAPIURL() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(apiBaseURL, forType: .string)
    }

    @objc private func copyPanelToken() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(config.panelAccessToken, forType: .string)
    }

    @objc private func openDataFolder() {
        NSWorkspace.shared.open(URL(fileURLWithPath: dataDirPath))
    }

    @objc private func openLog() {
        NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
    }

    @objc private func showAbout() {
        alert("ElysiaApi for macOS\n当前版本 \(currentVersion)\n后端与面板来自 Elysia-Api 项目。")
    }

    private func alert(_ text: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = text
        alert.addButton(withTitle: "好")
        alert.runModal()
    }
}

// MARK: - 入口

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
