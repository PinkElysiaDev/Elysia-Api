// ElysiaApi macOS 原生壳:托盘服务管理 + 独立进程承载 WebUI + 应用内更新。
// 仅依赖系统框架(Cocoa/WebKit),零第三方依赖,用 swiftc -O 编译。
//
// 行为概要:
// - 托盘拥有内嵌后端；独立窗口进程加载面板，关窗即退出并释放 WebKit
// - 数据全部放在 ~/Library/Application Support/ElysiaApi(配置/数据库/日志)
// - 首次运行自动生成配置:随机面板令牌 + 空闲端口探测(8765→8799→8800…)
// - 状态栏:模板图标 + 端口号/状态,菜单提供快捷操作
// - 有新版本时窗口左下角浮出更新胶囊,一键完成 下载→sha256 校验→整包替换→自动重启

import Cocoa
import CryptoKit
import Security
import ServiceManagement
import UserNotifications
import WebKit
import os

// MARK: - 常量与路径

#if NATIVE_TESTS
let dataDirPath = ProcessInfo.processInfo.environment["ELYSIA_NATIVE_TEST_DATA"]!
#else
let dataDirPath = NSHomeDirectory() + "/Library/Application Support/ElysiaApi"
#endif
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

// MARK: - 工具函数

func randomToken() throws -> String {
    var bytes = [UInt8](repeating: 0, count: 24)
    guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
        throw UpdateError(message: "无法生成面板访问令牌")
    }
    return "elysia-app-" + bytes.map { String(format: "%02x", $0) }.joined()
}

func portIsFree(_ port: Int, host: String = "127.0.0.1") -> Bool {
    guard (1...65535).contains(port) else { return false }
    var hints = addrinfo()
    hints.ai_family = AF_UNSPEC
    hints.ai_socktype = SOCK_STREAM
    hints.ai_flags = AI_PASSIVE
    var addresses: UnsafeMutablePointer<addrinfo>?
    guard getaddrinfo(host, String(port), &hints, &addresses) == 0, let first = addresses else { return false }
    defer { freeaddrinfo(first) }
    var current: UnsafeMutablePointer<addrinfo>? = first
    var bound = false
    while let address = current {
        let info = address.pointee
        let fd = socket(info.ai_family, info.ai_socktype, info.ai_protocol)
        if fd >= 0 {
            // 与 Go net.Listen 一致：允许复用已关闭连接的 TIME_WAIT，仍拒绝活跃监听者。
            var reuse: Int32 = 1
            setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &reuse, socklen_t(MemoryLayout<Int32>.size))
            let result = bind(fd, info.ai_addr, info.ai_addrlen)
            close(fd)
            if result != 0 { return false }
            bound = true
        }
        current = info.ai_next
    }
    return bound
}

/// 只在缺失时创建；写入失败/配置损坏时原样保留文件并交给恢复界面。
func loadOrCreateConfig() -> PanelConfig? {
    let fm = FileManager.default
    do {
        if !fm.fileExists(atPath: configPath) {
            try fm.createDirectory(atPath: dataDirPath, withIntermediateDirectories: true)
            var config = PanelConfig()
            config.panelAccessToken = try randomToken()
            let data = try JSONEncoder().encode(config)
            try data.write(to: URL(fileURLWithPath: configPath), options: .atomic)
            try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: configPath)
            return config
        }
        let config = try JSONDecoder().decode(PanelConfig.self, from: Data(contentsOf: URL(fileURLWithPath: configPath)))
        guard (1...65535).contains(config.port), !config.host.isEmpty else { return nil }
        return config
    } catch {
        appLogger.error("Unable to read or create configuration: \(error.localizedDescription, privacy: .public)")
        return nil
    }
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

func javascriptString(_ value: String) -> String {
    let data = try! JSONSerialization.data(withJSONObject: [value])
    let array = String(data: data, encoding: .utf8)!
    return String(array.dropFirst().dropLast())
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
            background: style.backgroundColor,
            title: document.title
          });
        } catch (e) {}
      }
      new MutationObserver(report).observe(
        document.documentElement, { attributes: true, attributeFilter: ['class'] });
      window.addEventListener('load', report);
      if (document.querySelector('title')) new MutationObserver(report).observe(document.querySelector('title'), { childList: true, subtree: true });
      report();
    })();
    """
    return WKUserScript(source: source, injectionTime: .atDocumentEnd, forMainFrameOnly: true)
}

/// 在首帧应用原生保存的主题，消除重新打开窗口时的主题闪烁。
func webStateBootstrapScript(store: WindowStateStore, origin: String) -> WKUserScript {
    let source = """
    (function() {
      if (location.origin !== \(javascriptString(origin))) return;
      try {
        var theme = \(javascriptString(store.webTheme ?? ""));
        if (theme === 'dark' || theme === 'light') {
          localStorage.setItem('elysia-webui.theme', theme);
          document.documentElement.classList.toggle('dark', theme === 'dark');
        }
      } catch (e) {}
    })();
    """
    return WKUserScript(source: source, injectionTime: .atDocumentStart, forMainFrameOnly: true)
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
        if event.clickCount == 2 {
            switch UserDefaults.standard.string(forKey: "AppleActionOnDoubleClick") {
            case "Minimize": window?.performMiniaturize(nil)
            case "None": break
            default: window?.performZoom(nil)
            }
        } else { window?.performDrag(with: event) }
    }
}

/// 菜单栏弹出菜单顶部的信息部件：品牌与运行状态、面板地址、最近 24 小时请求脉冲曲线与摘要。
/// 曲线与 WebUI 面板的实时脉搏同用瑰梅红；视图实例常驻并保留上次内容，
/// 状态由 AppDelegate 在状态变化时刷新，曲线数据在 menuWillOpen 时异步更新。
final class PulseMenuView: NSView {
    static let windowHours = 24
    static let slotCount = 96
    static let bucketMinutes = 15
    static let width: CGFloat = 264
    static let height: CGFloat = 100

    private var stateDot = NSColor.systemGray
    private var stateText = "已停止"
    private var subtitle = ""
    private var summary = ""
    private var slots: [Int] = []

    /// 深浅色菜单分别取色：浅色用面板同款瑰梅红（#DC185D，webui --primary），
    /// 深色提亮降饱和，避免高饱和玫红在暗背景上显得刺眼。
    private var tint: NSColor {
        let dark = effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        return dark ? NSColor(srgbRed: 0.914, green: 0.388, blue: 0.596, alpha: 1)   // #E96398
                    : NSColor(srgbRed: 0.863, green: 0.094, blue: 0.403, alpha: 1)   // #DC185D
    }

    override func viewDidChangeEffectiveAppearance() {
        needsDisplay = true
    }

    override var intrinsicContentSize: NSSize { NSSize(width: Self.width, height: Self.height) }

    func update(stateDot: NSColor, stateText: String, subtitle: String, summary: String, slots: [Int]) {
        self.stateDot = stateDot
        self.stateText = stateText
        self.subtitle = subtitle
        self.summary = summary
        self.slots = slots
        setAccessibilityElement(true)
        setAccessibilityLabel("Elysia API，\(stateText)，\(subtitle)。\(summary)")
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        let inset: CGFloat = 14
        let truncate = NSMutableParagraphStyle()
        truncate.lineBreakMode = .byTruncatingTail

        // 第一行：标题与运行状态（品牌由正上方的菜单栏图标承担，部件内不再重复）
        let titleFont = NSFont.systemFont(ofSize: 13, weight: .semibold)
        let line1Y = bounds.height - 20
        ("Elysia API" as NSString).draw(at: NSPoint(x: inset, y: line1Y),
            withAttributes: [.font: titleFont, .foregroundColor: NSColor.labelColor])
        let stateAttrs: [NSAttributedString.Key: Any] = [.font: NSFont.systemFont(ofSize: 11, weight: .medium),
                                                         .foregroundColor: NSColor.secondaryLabelColor]
        let stateSize = stateText.size(withAttributes: stateAttrs)
        let dotSide: CGFloat = 7
        let dotX = bounds.width - inset - stateSize.width - dotSide - 4
        stateDot.setFill()
        NSBezierPath(ovalIn: NSRect(x: dotX, y: line1Y + 0.5, width: dotSide, height: dotSide)).fill()
        (stateText as NSString).draw(at: NSPoint(x: dotX + dotSide + 4, y: line1Y), withAttributes: stateAttrs)

        // 面积图 + 摘要 + 地址（地址最次要，沉底）
        drawPulse(in: NSRect(x: inset, y: 44, width: max(bounds.width - inset * 2, 0), height: 28))
        (summary as NSString).draw(at: NSPoint(x: inset, y: 30),
            withAttributes: [.font: NSFont.systemFont(ofSize: 11),
                             .foregroundColor: NSColor.secondaryLabelColor,
                             .paragraphStyle: truncate])
        (subtitle as NSString).draw(at: NSPoint(x: inset, y: 12),
            withAttributes: [.font: NSFont.systemFont(ofSize: 11),
                             .foregroundColor: NSColor.secondaryLabelColor,
                             .paragraphStyle: truncate])
    }

    private func drawPulse(in chart: NSRect) {
        NSColor.separatorColor.setFill()
        NSRect(x: chart.minX, y: chart.minY, width: chart.width, height: 1).fill()
        guard !slots.isEmpty, let peak = slots.max(), peak > 0 else { return }
        // sqrt 比例压缩突发峰值，小流量时段的起伏也可见；平滑剪影闭合后做纵向渐变填充，
        // 稀疏突发的请求量呈现为柔和的山丘而非针状。顶部留 3pt 防平滑过冲。
        let sqrtPeak = sqrt(CGFloat(peak))
        let step = chart.width / CGFloat(max(slots.count - 1, 1))
        let usable = chart.height - 3
        let points: [NSPoint] = slots.enumerated().map { index, value in
            NSPoint(x: chart.minX + CGFloat(index) * step,
                    y: chart.minY + 1 + sqrt(CGFloat(value)) / sqrtPeak * usable)
        }
        let curve = NSBezierPath()
        curve.move(to: points[0])
        if points.count == 1 { curve.line(to: points[0]) }
        for i in 1..<points.count {
            let p0 = points[max(i - 2, 0)], p1 = points[i - 1], p2 = points[i], p3 = points[min(i + 1, points.count - 1)]
            curve.curve(to: p2,
                        controlPoint1: NSPoint(x: p1.x + (p2.x - p0.x) / 6, y: p1.y + (p2.y - p0.y) / 6),
                        controlPoint2: NSPoint(x: p2.x - (p3.x - p1.x) / 6, y: p2.y - (p3.y - p1.y) / 6))
        }
        // 渐变填充剪影；山丘轮廓再以细描边勾形，避免在菜单材质上淡到不可见
        let area = curve.copy() as! NSBezierPath
        area.line(to: NSPoint(x: chart.maxX, y: chart.minY))
        area.line(to: NSPoint(x: chart.minX, y: chart.minY))
        area.close()
        NSGradient(colors: [tint.withAlphaComponent(0.65), tint.withAlphaComponent(0.10)])?.draw(in: area, angle: -90)
        tint.setStroke()
        curve.lineWidth = 1.5
        curve.lineCapStyle = .round
        curve.lineJoinStyle = .round
        curve.stroke()
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKDownloadDelegate, WKScriptMessageHandler, NSWindowDelegate, WKUIDelegate, NSMenuDelegate, NSMenuItemValidation {
    // The tray supervisor never constructs WebKit. Closing the panel exits its process.
    let isPanelProcess = CommandLine.arguments.contains("--webui-process")
    private var usesPanelProcess: Bool {
        #if NATIVE_TESTS
        return !isPanelProcess && NSApp.delegate === self
        #else
        return !isPanelProcess
        #endif
    }
    private var panelProcess: Process?
    private var panelBridge: PanelBridge?
    private var panelStopDeadline: DispatchWorkItem?
    private var panelClosing = false
    private var reopenPanelAfterExit = false
    private var panelReady = false
    private var panelBackendGeneration = ""
    private var panelUpdateEnabled = false
    private var instanceLock: Int32 = -1
    private var healthTimer: Timer?
    var window: NSWindow!
    var webView: WKWebView!
    var overlay: NSView!
    var overlaySpinner: NSProgressIndicator!
    var overlayLabel: NSTextField!
    var overlayButton: NSButton!
    // 更新提示：左下角悬浮胶囊。与旧底栏不同，胶囊只改变透明度与位移，
    // WebUI 排版保持静止——布局不再被顶起。
    var updateCapsule: UpdateCapsuleView!
    var updateCapsuleVisible = false
    var updateCapsuleAnimating = false
    var statusItem: NSStatusItem!
    var restartItem: NSMenuItem!
    var pulseItem: NSMenuItem!
    var pulseView: PulseMenuView!
    var pulseFetchGeneration = 0
    var pulseSlots: [Int] = []
    var pulseSummaryText = "最近 24 小时"
    var reloadPanelItem: NSMenuItem!

    var config: PanelConfig!
    var backend: Process?
    var backendHost: String?   // 后端实际监听地址,启动时锁定(config 之后被手改也不会错位)
    var backendPort = 0
    var userStopping = false
    var restartCount = 0
    var backendState: BackendState = .stopped
    var lastBackendError: String?
    var launchedAtLogin = CommandLine.arguments.contains("--login") || CommandLine.arguments.contains("--background")
    var terminating = false
    private var terminationReplyPending = false
    private var terminationCleanupDeadline: DispatchWorkItem?
    #if NATIVE_TESTS
    var terminationRepliesForTests = 0
    #endif
    var restartWork: DispatchWorkItem?
    var healthInFlight = false
    var startupTime = Date()
    var healthySince: Date?
    var backendLogHandle: FileHandle?
    var backendInputPipe: Pipe?
    var backendStopDeadline: DispatchWorkItem?
    var backendGeneration = UUID()
    var notificationSettingsItem: NSMenuItem!
    var loginSettingsItem: NSMenuItem!
    var lastErrorItem: NSMenuItem!
    var updateCheckItem: NSMenuItem!
    /// 更新动作项（单一菜单位置）：由 phase 决定标题（安装/重试/重启完成）。
    /// 空闲时也是唯一的「检查更新…」入口——检查动作挪到此项，视觉上合并更新职能。
    var updateActionItem: NSMenuItem!
    var overlayHelp: NSStackView!
    var healthFailedSince: Date?
    var recoveryTerminating = false
    var checkingUpdates = false
    var updatePhase: UpdatePhase = .idle
    var updateMessage = ""
    var updateDetail = ""
    var updateFraction: Double?
    var cancellingUpdate = false
    var notificationDenied = false
    var timers: [Timer] = []
    var panelLoaded = false
    var loadedPort = 0
    var currentVersion = readBundledVersion()
    var latestRelease: ReleaseInfo?
    let windowState = WindowStateStore()
    let launchAtLogin = LaunchAtLoginManager()
    let notifications = NotificationManager()
    var updateTask: URLSessionDownloadTask?
    var updateSession: URLSession?
    var updateDownloadDelegate: UpdateDownloadDelegate?
    private var updateDownloadGeneration = UUID()
    var stagedUpdate: URL?
    var relaunchHelper: Process?
    var signalSources: [DispatchSourceSignal] = []
    var updateLaunchAcknowledged = false
    var panelReadinessInFlight = false
    var resumeBackendAfterUpdateFailure = false
    #if NATIVE_TESTS
    var testUpdateHandoffStarted = false
    #endif
    /// 重启等待旧后端退出，随后由退出回调启动新进程。
    var pendingBackendRestart = false

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
        let host = backendPort != 0 ? (backendHost ?? config.host) : config.host
        let port = backendPort != 0 ? backendPort : config.port
        return panelOrigin(host: host, port: port)
    }

    // MARK: 生命周期

    func applicationDidFinishLaunching(_ notification: Notification) {
        let event = NSAppleEventManager.shared().currentAppleEvent
        launchedAtLogin = launchedAtLogin || event?.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue == keyAELaunchedAsLogInItem
        if isPanelProcess { launchPanelProcess(); return }
        guard ensureSingleInstance() else { requestTermination(); return }
        #if NATIVE_TESTS
        notifications.enabled = false
        if ProcessInfo.processInfo.environment["ELYSIA_NATIVE_APP_TEST"] == "1" {
            if CommandLine.arguments.contains("--update-ack") {
                try? String(ProcessInfo.processInfo.processIdentifier).write(toFile: dataDirPath + "/new-native.pid", atomically: true, encoding: .utf8)
            } else if !CommandLine.arguments.contains("--native-update-parent") {
                try? String(ProcessInfo.processInfo.processIdentifier).write(toFile: dataDirPath + "/rollback-native.pid", atomically: true, encoding: .utf8)
            }
        }
        #endif
        config = loadOrCreateConfig() ?? PanelConfig()
        appLogger.info("Launching ElysiaApi \(self.currentVersion, privacy: .public), background: \(self.launchedAtLogin)")
        notifications.onOpen = { [weak self] in self?.showMainWindow() }
        NSApp.setActivationPolicy(.accessory)
        buildMainMenu()
        buildStatusItem()
        if !launchedAtLogin { showMainWindow() }
        #if !NATIVE_TESTS
        do { try launchAtLogin.reconcile() }
        catch { appLogger.error("Login item reconciliation: \(error.localizedDescription, privacy: .public)") }
        #endif
        refreshPreferencesUI()
        startBackend()
        scheduleTimers()
        installSignalHandlers()
        #if !NATIVE_TESTS
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in self?.checkForUpdates(silent: true) }
        #endif
        NotificationCenter.default.addObserver(self, selector: #selector(screenLayoutChanged), name: NSApplication.didChangeScreenParametersNotification, object: nil)
        NSWorkspace.shared.notificationCenter.addObserver(self, selector: #selector(wakeFromSleep), name: NSWorkspace.didWakeNotification, object: nil)
    }

    func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool { true }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        // 安装替换阶段不能被中途终止；下载阶段可安全取消。
        if !isPanelProcess && updatePhase == .installing { return .terminateCancel }
        if terminating { return .terminateLater }
        terminating = true
        restartWork?.cancel()
        healthTimer?.invalidate()
        healthTimer = nil
        timers.forEach { $0.invalidate() }
        timers.removeAll()
        apiSession.invalidateAndCancel()
        healthSession.invalidateAndCancel()
        updateSession?.invalidateAndCancel()
        cancelPanelDownloads(reason: "应用正在退出，未完成的导出已取消。")
        saveWindowState()
        stopPanelProcess()
        if let process = backend {
            if process.isRunning {
                userStopping = true
                backendState = .stopping
                refreshStatusUI()
                showOverlay(text: "正在停止服务并保存用量记录…", spinning: true)
                requestBackendExit(process)
            } else { backendDidExit(process) }
        }
        guard !terminationResourcesReleased else { return .terminateNow }
        terminationReplyPending = true
        let deadline = DispatchWorkItem { [weak self] in
            guard let self, self.terminationReplyPending else { return }
            appLogger.error("Termination cleanup deadline reached; discarding only owned download files")
            if let process = self.backend, process.isRunning { kill(process.processIdentifier, SIGKILL) }
            if let process = self.panelProcess, process.isRunning { kill(process.processIdentifier, SIGKILL) }
            self.discardTerminationDownloads()
            self.replyToTermination()
        }
        terminationCleanupDeadline = deadline
        DispatchQueue.main.asyncAfter(deadline: .now() + (isPanelProcess ? 4 : 16), execute: deadline)
        return .terminateLater
    }

    private var terminationResourcesReleased: Bool {
        backend == nil && panelProcess == nil && panelDownloads.isEmpty && cancellingPanelDownloads.isEmpty
            && updateTask == nil && updateDownloadDelegate == nil && updateSession == nil
    }

    private func finishTerminationIfReady() {
        guard terminating, terminationReplyPending, terminationResourcesReleased else { return }
        replyToTermination()
    }

    private func replyToTermination() {
        terminationReplyPending = false
        terminationCleanupDeadline?.cancel()
        terminationCleanupDeadline = nil
        #if NATIVE_TESTS
        terminationRepliesForTests += 1
        // The integration suite invokes the real delegate without installing it on NSApp.
        if NSApp.delegate !== self { return }
        #endif
        NSApp.reply(toApplicationShouldTerminate: true)
    }

    private func discardTerminationDownloads() {
        updateDownloadDelegate?.discardDownloadedFile()
        updateDownloadGeneration = UUID()
        updateSession?.invalidateAndCancel()
        updateSession = nil
        updateDownloadDelegate = nil
        updateTask = nil
        for pending in cancellingPanelDownloads.values { pending.removeTemporaryFile() }
        cancellingPanelDownloads.removeAll()
    }

    func applicationWillTerminate(_ notification: Notification) {
        terminationCleanupDeadline?.cancel()
        panelStopDeadline?.cancel()
        panelBridge?.close()
        panelBridge = nil
        if instanceLock >= 0 { close(instanceLock); instanceLock = -1 }
        discardTerminationDownloads()
        backendStopDeadline?.cancel()
        try? backendInputPipe?.fileHandleForWriting.close()
        try? backendLogHandle?.close()
        window?.close()
        NotificationCenter.default.removeObserver(self)
        NSWorkspace.shared.notificationCenter.removeObserver(self)
        DistributedNotificationCenter.default().removeObserver(self)
        signalSources.forEach { $0.cancel() }
        if let stagedUpdate, relaunchHelper == nil {
            try? FileManager.default.removeItem(at: stagedUpdate.deletingLastPathComponent())
        }
    }

    private func requestTermination() {
        // terminateLater runs a nested AppKit loop. Enter it from the run loop,
        // so main-queue process/download completions can still finish cleanup.
        RunLoop.main.perform(inModes: [.common]) { [weak self] in
            guard let self, !self.terminating else { return }
            NSApp.terminate(nil)
        }
    }

    @objc func quit(_ sender: Any?) {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "quit")) }
        else { requestTermination() }
    }
    @objc private func wakeFromSleep() { pollHealth() }
    @objc private func screenLayoutChanged() { ensureWindowVisible() }

    // MARK: 单实例

    private var reopenNotification: Notification.Name {
        Notification.Name((Bundle.main.bundleIdentifier ?? "ElysiaApi") + ".showPanel")
    }

    private func ensureSingleInstance() -> Bool {
        do { try FileManager.default.createDirectory(atPath: dataDirPath, withIntermediateDirectories: true) }
        catch { appLogger.error("Cannot create application data directory: \(error.localizedDescription, privacy: .public)"); return false }
        instanceLock = open(dataDirPath + "/native.lock", O_CREAT | O_RDWR | O_CLOEXEC, 0o600)
        guard instanceLock >= 0 else { return false }
        guard flock(instanceLock, LOCK_EX | LOCK_NB) == 0 else {
            close(instanceLock)
            instanceLock = -1
            if !launchedAtLogin {
                DistributedNotificationCenter.default().postNotificationName(reopenNotification, object: nil, userInfo: nil, deliverImmediately: true)
            }
            return false
        }
        DistributedNotificationCenter.default().addObserver(self, selector: #selector(showMainWindow), name: reopenNotification, object: nil)
        return true
    }

    // MARK: Disposable panel process

    private func installSignalHandlers() {
        for number in [SIGTERM, SIGINT] {
            signal(number, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: number, queue: .main)
            source.setEventHandler { [weak self] in self?.requestTermination() }
            source.resume()
            signalSources.append(source)
        }
    }

    private func launchPanelProcess() {
        config = loadOrCreateConfig() ?? PanelConfig()
        NSApp.setActivationPolicy(.regular)
        buildMainMenu()
        panelBridge = PanelBridge(input: .standardInput, output: .standardOutput,
            onMessage: { [weak self] message in self?.receivePanelState(message) },
            onClose: { [weak self] in self?.requestTermination() })
        panelBridge?.start()
        buildWindow()
        installSignalHandlers()
        NotificationCenter.default.addObserver(self, selector: #selector(screenLayoutChanged), name: NSApplication.didChangeScreenParametersNotification, object: nil)
        let readiness = Timer(timeInterval: 0.5, repeats: true) { [weak self] _ in self?.reportPanelReadiness() }
        timers = [readiness]
        RunLoop.main.add(readiness, forMode: .common)
        panelBridge?.send(PanelMessage(type: "hello"))
    }

    private func showPanelProcess() {
        guard !terminating else { return }
        if panelClosing { reopenPanelAfterExit = true; return }
        if panelProcess != nil {
            panelBridge?.send(PanelMessage(type: "show"))
            pollHealth()
            return
        }
        do {
            let process = Process()
            process.executableURL = Bundle.main.executableURL
            process.arguments = ["--webui-process"]
            let input = Pipe(), output = Pipe()
            process.standardInput = input
            process.standardOutput = output
            process.standardError = FileHandle.standardError
            process.terminationHandler = { [weak self] process in
                DispatchQueue.main.async { self?.panelDidExit(process) }
            }
            try process.run()
            try? input.fileHandleForReading.close()
            try? output.fileHandleForWriting.close()
            panelProcess = process
            panelReady = false
            panelBridge = PanelBridge(input: output.fileHandleForReading, output: input.fileHandleForWriting,
                onMessage: { [weak self, weak process] message in
                    guard let self, let process, self.panelProcess === process else { return }
                    self.receivePanelEvent(message)
                }, onClose: { [weak self, weak process] in
                    guard let self, let process, self.panelProcess === process else { return }
                    self.stopPanelProcess()
                })
            panelBridge?.start()
            sendPanelState()
            rescheduleHealthTimer()
            pollHealth()
            #if NATIVE_TESTS
            try? String(process.processIdentifier).write(toFile: dataDirPath + "/tray-ui.pid", atomically: true, encoding: .utf8)
            #endif
        } catch { alert("无法打开面板：\n\(error.localizedDescription)") }
    }

    private func stopPanelProcess() {
        guard let process = panelProcess, !panelClosing else { return }
        panelClosing = true
        panelReady = false
        panelBridge?.send(PanelMessage(type: "close"))
        // Also handle a blocked/crashed UI. Only signal the child owned by this supervisor.
        let deadline = DispatchWorkItem { [weak self, weak process] in
            guard let self, let process, self.panelProcess === process, process.isRunning else { return }
            kill(process.processIdentifier, SIGKILL)
        }
        panelStopDeadline = deadline
        DispatchQueue.main.asyncAfter(deadline: .now() + 5, execute: deadline)
    }

    private func panelDidExit(_ process: Process) {
        guard panelProcess === process else { return }
        panelProcess = nil
        panelBridge?.close()
        panelBridge = nil
        panelStopDeadline?.cancel()
        panelStopDeadline = nil
        panelClosing = false
        panelReady = false
        rescheduleHealthTimer()
        finishTerminationIfReady()
        if reopenPanelAfterExit && !terminating {
            reopenPanelAfterExit = false
            showPanelProcess()
        }
    }

    private func sendPanelState() {
        guard usesPanelProcess, panelProcess != nil else { return }
        var message = PanelMessage(type: "state")
        message.config = config
        message.backendState = backendState.rawValue
        message.generation = backendGeneration.uuidString
        message.updateEnabled = updateActionMenuIsEnabled()
        message.host = backendHost
        message.port = backendPort
        message.restartCount = restartCount
        message.error = lastBackendError
        message.updatePhase = updatePhase.rawValue
        message.updateMessage = updateMessage
        message.updateDetail = updateDetail
        message.updateFraction = updateFraction
        message.capsuleVisible = updateCapsuleVisible
        panelBridge?.send(message)
    }

    private func receivePanelEvent(_ message: PanelMessage) {
        guard !terminating || message.type == "closing" else { return }
        switch message.type {
        case "hello": sendPanelState()
        case "closing": stopPanelProcess()
        case "htmlLoaded":
            #if NATIVE_TESTS
            if CommandLine.arguments.contains("--update-ack") {
                try? String(ProcessInfo.processInfo.processIdentifier).write(toFile: dataDirPath + "/new-html-loaded.pid", atomically: true, encoding: .utf8)
            }
            #endif
        case "ready":
            guard message.port == backendPort, message.host == backendHost, message.generation == backendGeneration.uuidString, backendState == .running, !panelClosing else { return }
            panelReady = true
            #if NATIVE_TESTS
            if let process = panelProcess {
                try? String(process.processIdentifier).write(toFile: dataDirPath + "/tray-ui-ready.pid", atomically: true, encoding: .utf8)
            }
            #endif
            acknowledgeUpdateLaunch()
        case "action":
            switch message.action {
            case "restart": restartBackend()
            case "retry": retryStartup()
            case "update": performUpdateAction()
            case "cancelUpdate": cancelUpdate()
            case "checkUpdate": checkForUpdates(silent: false)
            case "dismissUpdate": dismissUpdatePrompt()
            case "preferences": showPreferences()
            case "quit": requestTermination()
            default: break
            }
        default: break
        }
    }

    private func receivePanelState(_ message: PanelMessage) {
        switch message.type {
        case "show": showMainWindow()
        case "close": window?.performClose(nil)
        case "state":
            guard let fresh = message.config, let state = message.backendState.flatMap(BackendState.init(rawValue:)),
                  let phase = message.updatePhase.flatMap(UpdatePhase.init(rawValue:)) else { return }
            let originChanged = backendPort != message.port || backendHost != message.host || panelBackendGeneration != message.generation
            panelBackendGeneration = message.generation ?? ""
            panelUpdateEnabled = message.updateEnabled ?? false
            config = fresh
            backendHost = message.host
            backendPort = message.port ?? 0
            backendState = state
            restartCount = message.restartCount ?? 0
            lastBackendError = message.error
            if phase == .installing && updatePhase != .installing {
                cancelPanelDownloads(reason: "应用正在安装更新，未完成的导出已取消。")
            }
            updatePhase = phase
            checkingUpdates = phase == .checking
            updateMessage = message.updateMessage ?? ""
            updateDetail = message.updateDetail ?? ""
            updateFraction = message.updateFraction
            updateCapsuleVisible = message.capsuleVisible ?? false
            if originChanged || state != .running { panelLoaded = false; panelReady = false }
            renderBackendState()
            refreshUpdateUI()
            loadPanelWhenHealthy()
        default: break
        }
    }

    private func reportPanelReadiness() {
        guard isPanelProcess, !panelReady, !panelReadinessInFlight, backendState == .running,
              let webView, !webView.isLoading, let url = webView.url, isPanelOrigin(url) else { return }
        panelReadinessInFlight = true
        let port = backendPort, host = backendHost, generation = panelBackendGeneration
        webView.evaluateJavaScript("Boolean(document.querySelector('#token')?.closest('form') || document.querySelector('main h1'))") { [weak self, weak webView] result, _ in
            guard let self else { return }
            self.panelReadinessInFlight = false
            guard let webView, webView === self.webView, port == self.backendPort, host == self.backendHost,
                  self.backendState == .running, generation == self.panelBackendGeneration, !self.terminating, result as? Bool == true else { return }
            self.panelReady = true
            var message = PanelMessage(type: "ready")
            message.port = port
            message.host = host
            message.generation = generation
            self.panelBridge?.send(message)
        }
    }

    // MARK: 主窗口与界面

    private static let titleBarHeight: CGFloat = 28

    /// 最小主菜单:编辑菜单项是 WKWebView 复制/粘贴/全选等快捷键的依赖
    /// (标准 selector 不设 target,交由响应链分发到当前第一响应者)
    private func buildMainMenu() {
        let mainMenu = NSMenu()

        let appMenuItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "关于 ElysiaApi", action: #selector(showAbout), keyEquivalent: "")
        addAction(appMenu, "偏好设置…", #selector(showPreferences), ",")
        addAction(appMenu, "检查更新…", #selector(checkForUpdatesFromMenu), "u", [.command, .shift])
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
        let fullScreen = windowMenu.addItem(withTitle: "进入全屏幕", action: #selector(NSWindow.toggleFullScreen(_:)), keyEquivalent: "f")
        fullScreen.keyEquivalentModifierMask = [.command, .control]
        addAction(windowMenu, "显示主窗口", #selector(showMainWindow as () -> Void), "0")
        NSApp.windowsMenu = windowMenu
        windowMenuItem.submenu = windowMenu
        mainMenu.addItem(windowMenuItem)

        let viewMenuItem = NSMenuItem()
        let viewMenu = NSMenu(title: "视图")
        reloadPanelItem = viewMenu.addItem(withTitle: "重新加载面板", action: #selector(reloadPanel), keyEquivalent: "r")
        reloadPanelItem.target = self
        addAction(viewMenu, "在浏览器中打开面板", #selector(openPanelInBrowser), "b", [.command, .shift])
        addAction(viewMenu, "复制面板地址", #selector(copyPanelURL), "l", [.command, .shift])
        addAction(viewMenu, "复制 API 地址", #selector(copyAPIURL), "c", [.command, .option])
        addAction(viewMenu, "复制面板访问令牌", #selector(copyPanelToken), "c", [.command, .option, .shift])
        addAction(viewMenu, "启动服务", #selector(restartBackend), "s", [.command, .option])
        viewMenuItem.submenu = viewMenu
        mainMenu.addItem(viewMenuItem)

        for menu in [appMenu, viewMenu, windowMenu] { menu.delegate = self }
        appMenu.items.first?.target = self
        appMenu.items.last?.target = self
        NSApp.mainMenu = mainMenu
    }

    private func configureWebScripts() {
        guard let webView else { return }
        let content = webView.configuration.userContentController
        content.removeAllUserScripts()
        content.addUserScript(webStateBootstrapScript(store: windowState, origin: apiBaseURL))
        content.addUserScript(themeReporterScript())
    }

    private func initialWindowRect() -> NSRect {
        let visible = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        let width = min(visible.width, min(max(940, visible.width * 0.82), 1400))
        let height = min(visible.height, min(max(600, visible.height * 0.82), 900))
        return NSRect(x: visible.midX - width / 2, y: visible.midY - height / 2, width: width, height: height)
    }

    private func ensureWindowVisible() {
        guard let window, !window.styleMask.contains(.fullScreen) else { return }
        let screens = NSScreen.screens.map(\.visibleFrame)
        let frame = WindowStateStore.fittedFrame(window.frame, screens: screens)
        window.minSize = NSSize(width: min(940, frame.width), height: min(600, frame.height))
        if frame != window.frame { window.setFrame(frame, display: true) }
    }

    private func saveWindowState() {
        guard let window else { return }
        if !window.styleMask.contains(.fullScreen) { window.saveFrame(usingName: "ElysiaApiPanel") }
    }

    private func buildWindow() {
        let rect = initialWindowRect()
        window = NSWindow(contentRect: rect,
                          styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                          backing: .buffered, defer: false)
        window.title = "Elysia API"
        window.minSize = NSSize(width: 940, height: 600)
        window.collectionBehavior = [.fullScreenPrimary]
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
        window.setFrameUsingName("ElysiaApiPanel", force: true)
        ensureWindowVisible()

        let content = NSView(frame: NSRect(origin: .zero, size: window.contentView!.bounds.size))

        let webConfig = WKWebViewConfiguration()
        // 首次手动登录后保留 WebUI 自己写入的登录态，关窗仍销毁 WebView。
        webConfig.websiteDataStore = .default()
        let userContent = WKUserContentController()
        userContent.add(self, name: "theme")
        webConfig.userContentController = userContent
        webView = WKWebView(frame: .zero, configuration: webConfig)
        webView.navigationDelegate = self
        webView.uiDelegate = self
        configureWebScripts()
        webView.setAccessibilityLabel("Elysia API 管理面板")
        content.addSubview(webView)

        let dragStrip = DragTitlebarView()
        dragStrip.setAccessibilityLabel("窗口标题栏，可拖动以移动窗口")
        content.addSubview(dragStrip)

        // 更新胶囊：悬浮于 WebUI 之上，不挤压布局；初始隐藏；动画只改透明度与位移。
        updateCapsule = UpdateCapsuleView(frame: .zero)
        updateCapsule.isHidden = true
        updateCapsule.onPrimaryAction = { [weak self] in self?.runUpdate() }
        updateCapsule.onCancelAction = { [weak self] in self?.cancelUpdate() }
        updateCapsule.onDismiss = { [weak self] in self?.dismissUpdatePrompt() }
        content.addSubview(updateCapsule)

        overlay = NSView()
        overlay.wantsLayer = true
        content.addSubview(overlay, positioned: .below, relativeTo: updateCapsule)
        overlaySpinner = NSProgressIndicator()
        overlaySpinner.style = .spinning
        overlayLabel = NSTextField(wrappingLabelWithString: "正在启动本地后端…")
        overlayLabel.font = .systemFont(ofSize: 14)
        overlayLabel.textColor = .secondaryLabelColor
        overlayLabel.alignment = .center
        overlayLabel.isSelectable = true
        overlayButton = NSButton(title: "重试", target: self, action: #selector(retryStartup))
        overlayButton.bezelStyle = .rounded
        let logs = NSButton(title: "查看运行日志", target: self, action: #selector(openLog))
        let folder = NSButton(title: "打开数据文件夹", target: self, action: #selector(openDataFolder))
        logs.bezelStyle = .rounded
        folder.bezelStyle = .rounded
        overlayHelp = NSStackView(views: [logs, folder])
        overlayHelp.spacing = 12
        let overlayStack = NSStackView(views: [overlaySpinner, overlayLabel, overlayButton, overlayHelp])
        overlayStack.orientation = .vertical
        overlayStack.alignment = .centerX
        overlayStack.spacing = 18
        overlay.addSubview(overlayStack)
        for view in [webView!, dragStrip, updateCapsule!, overlay!, overlayStack] {
            view.translatesAutoresizingMaskIntoConstraints = false
        }
        NSLayoutConstraint.activate([
            dragStrip.topAnchor.constraint(equalTo: content.topAnchor),
            dragStrip.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            dragStrip.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            dragStrip.heightAnchor.constraint(equalToConstant: Self.titleBarHeight),
            // 面板通铺到窗口底（更新胶囊悬浮其上，webView 布局保持不动）。
            webView.topAnchor.constraint(equalTo: content.topAnchor),
            webView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            webView.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            // 更新胶囊：左下悬浮，固定尺寸位置，宽/高跟 intrinsicContentSize 一致
            updateCapsule.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 16),
            updateCapsule.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -16),
            updateCapsule.widthAnchor.constraint(equalToConstant: UpdateCapsuleView.width),
            updateCapsule.heightAnchor.constraint(equalToConstant: UpdateCapsuleView.height),
            overlay.leadingAnchor.constraint(equalTo: webView.leadingAnchor),
            overlay.trailingAnchor.constraint(equalTo: webView.trailingAnchor),
            overlay.topAnchor.constraint(equalTo: webView.topAnchor),
            overlay.bottomAnchor.constraint(equalTo: webView.bottomAnchor),
            overlayStack.centerXAnchor.constraint(equalTo: overlay.centerXAnchor),
            overlayStack.centerYAnchor.constraint(equalTo: overlay.centerYAnchor),
            overlayStack.widthAnchor.constraint(lessThanOrEqualTo: overlay.widthAnchor, multiplier: 0.85),
            overlayLabel.widthAnchor.constraint(lessThanOrEqualToConstant: 580),
            overlaySpinner.widthAnchor.constraint(equalToConstant: 32),
            overlaySpinner.heightAnchor.constraint(equalToConstant: 32),
        ])
        window.contentView = content
        let dark = windowState.webTheme.map { $0 == "dark" } ?? (window.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua)
        applyTheme(dark: dark, background: dark ? NSColor(srgbRed: 0.055, green: 0.065, blue: 0.085, alpha: 1) : .windowBackgroundColor)
        refreshUpdateUI()
        renderBackendState()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        // 关窗口 = 轻量后台(菜单栏仍在),从菜单栏「显示主窗口」重新打开
        return true
    }

    @objc func showMainWindow() {
        if usesPanelProcess { showPanelProcess(); return }
        NSApp.setActivationPolicy(.regular)
        if let fresh = loadOrCreateConfig() { config = fresh }
        if window == nil { buildWindow() }
        // 打开窗口不覆盖“手动停止”的意图；用户可从窗口内启动按钮恢复服务。
        ensureWindowVisible()
        if window.isMiniaturized { window.deminiaturize(nil) }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        pollHealth()
    }

    func windowWillClose(_ notification: Notification) {
        guard let closing = notification.object as? NSWindow, closing === window else { return }
        saveWindowState()
        cancelPanelDownloads(reason: "主窗口已关闭，未完成的导出已取消。")
        NSApp.setActivationPolicy(.accessory)
        overlaySpinner?.stopAnimation(nil)
        webView?.stopLoading()
        webView?.navigationDelegate = nil
        webView?.uiDelegate = nil
        webView?.configuration.userContentController.removeScriptMessageHandler(forName: "theme")
        webView?.configuration.userContentController.removeAllUserScripts()
        webView?.removeFromSuperview()
        webView = nil
        window.contentView = nil
        window = nil
        overlay = nil
        overlayLabel = nil
        overlaySpinner = nil
        overlayButton = nil
        overlayHelp = nil
        updateCapsule?.configure(phase: .idle, title: "", detail: "", fraction: nil)
        updateCapsule?.layer?.removeAllAnimations()
        updateCapsule?.removeFromSuperview()
        updateCapsule = nil
        updateCapsuleAnimating = false
        panelLoaded = false
        loadedPort = 0
        refreshStatusUI()
        if isPanelProcess {
            panelBridge?.send(PanelMessage(type: "closing"))
            requestTermination()
        }
    }

    private func showOverlay(text: String, spinning: Bool, retry: Bool = false) {
        guard window != nil else { return }
        overlayLabel.stringValue = text
        overlaySpinner.isHidden = !spinning
        if spinning { overlaySpinner.startAnimation(nil) } else { overlaySpinner.stopAnimation(nil) }
        overlayButton.isHidden = !retry
        overlayHelp.isHidden = !retry
        overlay.isHidden = false
    }

    private func hideOverlay() {
        guard window != nil else { return }
        overlay.isHidden = true
    }

    @objc private func retryStartup() {
        if isPanelProcess {
            panelLoaded = false
            panelBridge?.send(PanelMessage(type: "action", action: "retry"))
            panelBridge?.send(PanelMessage(type: "hello"))
            return
        }
        restartCount = 0
        showOverlay(text: "正在启动本地后端…", spinning: true)
        if let process = backend {
            panelLoaded = false
            if backendState == .failed {
                recoveryTerminating = true
                setBackendState(.restarting)
                requestBackendExit(process)
            } else { pollHealth() }
        } else { startBackend() }
    }

    /// 用户点击胶囊的关闭 ×：仅收起提示，状态机保留；菜单可重新触发安装。
    @objc func dismissUpdatePrompt() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "dismissUpdate")); return }
        updateCapsuleVisible = false
        // 关闭是单向交互、无层级竞争：直接同步隐藏，不走退场动画的快闪窗口
        updateCapsuleAnimating = false
        updateCapsule?.isHidden = true
        refreshUpdateUI(animated: false)
    }

    /// 给定 phase 是否值得显示胶囊：检查期/空闲期一律不显示（修报启动闪现）。
    private func capsuleShouldBeVisible(for phase: UpdatePhase) -> Bool {
        switch phase {
        case .checking, .idle: return false
        case .available, .downloading, .installing, .failed, .readyToRelaunch: return true
        }
    }

    /// 同步更新胶囊与菜单状态。
    private func refreshUpdateUI(animated: Bool = false) {
        refreshStatusUI()

        // 显隐：phase 决定常态可见性，用户手动关 × 在特定 phase 中遮蔽
        let shouldShow = capsuleShouldBeVisible(for: updatePhase) && updateCapsuleVisible
        guard updateCapsule != nil else { return }
        updateCapsule.configure(phase: updatePhase, title: updateMessage,
                                detail: updateDetail.isEmpty ? updateMessage : updateDetail,
                                fraction: updateFraction)
        if shouldShow {
            guard updateCapsule.isHidden else { return }
            updateCapsule.isHidden = false
            if animated { updateCapsule.animateEntrance() }
        } else {
            guard !updateCapsule.isHidden, !updateCapsuleAnimating else { return }
            if animated {
                updateCapsuleAnimating = true
                updateCapsule.animateExit { [weak self] in self?.updateCapsuleAnimating = false }
            } else {
                updateCapsule.isHidden = true
            }
        }
    }

    // MARK: 状态栏

    @discardableResult
    private func addAction(_ menu: NSMenu, _ title: String, _ action: Selector, _ key: String = "",
                           _ modifiers: NSEvent.ModifierFlags = .command) -> NSMenuItem {
        let item = menu.addItem(withTitle: title, action: action, keyEquivalent: key)
        item.target = self
        item.keyEquivalentModifierMask = modifiers
        return item
    }

    private func buildStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        let logo = Bundle.main.bundlePath + "/Contents/Resources/logo.png"
        let base = templateIcon(from: logo, size: 18)
            ?? NSImage(systemSymbolName: "shippingbox", accessibilityDescription: "Elysia API")?.withSymbolConfiguration(.init(pointSize: 15, weight: .medium))
        statusItem.button?.image = base
        let menu = NSMenu()
        menu.delegate = self
        // 菜单项视图不会按 intrinsicContentSize 自动布局，必须显式给定 frame。
        pulseView = PulseMenuView(frame: NSRect(x: 0, y: 0, width: PulseMenuView.width, height: PulseMenuView.height))
        pulseItem = NSMenuItem()
        pulseItem.view = pulseView
        menu.addItem(pulseItem)
        lastErrorItem = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        menu.addItem(lastErrorItem)

        // —— 服务操作 ——
        menu.addItem(.separator())
        addAction(menu, "显示主窗口", #selector(showMainWindow as () -> Void), "0")
            .image = NSImage(systemSymbolName: "macwindow", accessibilityDescription: nil)
        restartItem = addAction(menu, "启动服务", #selector(restartBackend), "s", [.command, .option])
        restartItem.image = NSImage(systemSymbolName: "arrow.clockwise", accessibilityDescription: nil)

        // —— 快速复制 ——
        menu.addItem(.separator())
        addAction(menu, "复制 API 地址", #selector(copyAPIURL), "c", [.command, .option])
            .image = NSImage(systemSymbolName: "link", accessibilityDescription: nil)
        addAction(menu, "复制面板访问令牌", #selector(copyPanelToken), "c", [.command, .option, .shift])
            .image = NSImage(systemSymbolName: "key", accessibilityDescription: nil)

        // —— 更新（单一动作入口，标题随 phase 变化） ——
        menu.addItem(.separator())
        updateActionItem = addAction(menu, "检查更新…", #selector(performUpdateAction), "u", [.command, .shift])
        updateCheckItem = updateActionItem // 共用同一 NSMenuItem，避免双份入口

        // —— 偏好与诊断 ——
        menu.addItem(.separator())
        addAction(menu, "偏好设置…", #selector(showPreferences), ",")
            .image = NSImage(systemSymbolName: "gearshape", accessibilityDescription: nil)
        // 开机启动与通知开关只在偏好设置里提供；这里仅保留系统要求人工批准/授权时的修复入口。
        loginSettingsItem = addAction(menu, "开机启动待批准：打开系统设置…", #selector(openLoginSettings))
        notificationSettingsItem = addAction(menu, "通知被系统禁用：打开系统设置…", #selector(openNotificationSettings))
        addAction(menu, "打开数据文件夹", #selector(openDataFolder))
            .image = NSImage(systemSymbolName: "folder", accessibilityDescription: nil)
        addAction(menu, "查看运行日志", #selector(openLog))
            .image = NSImage(systemSymbolName: "doc.text", accessibilityDescription: nil)
        addAction(menu, "关于 ElysiaApi", #selector(showAbout))
            .image = NSImage(systemSymbolName: "info.circle", accessibilityDescription: nil)
        menu.addItem(.separator())
        addAction(menu, "退出 ElysiaApi", #selector(quit), "q")
        statusItem.menu = menu
        refreshStatusUI()
        refreshPreferencesUI()
    }

    private var backendStateText: String {
        switch backendState {
        case .starting: "正在启动"
        case .running: "运行中"
        case .stopping: "正在停止"
        case .restarting: "正在重启（\(restartCount)/3）"
        case .failed: "服务异常"
        case .stopped: "已停止"
        }
    }

    private func stateDotColor(_ state: BackendState) -> NSColor {
        switch state {
        case .running: .systemGreen
        case .starting, .stopping, .restarting: .systemYellow
        case .failed: .systemRed
        case .stopped: .systemGray
        }
    }

    private func refreshStatusUI() {
        sendPanelState()
        guard statusItem != nil else { return }
        // 图标旁永不显示文字：状态经 tooltip 与菜单头部传达。
        let description = "Elysia API · \(backendStateText) · \(apiBaseURL)"
        statusItem.button?.title = ""
        statusItem.button?.toolTip = description
        statusItem.button?.setAccessibilityLabel(description)
        lastErrorItem.title = "最近错误：" + (lastBackendError ?? "").replacingOccurrences(of: "\n", with: " ")
        lastErrorItem.toolTip = lastBackendError
        lastErrorItem.isHidden = lastBackendError == nil
        restartItem.title = serviceActionTitle
        restartItem.isEnabled = canRestartBackend
        reloadPanelItem?.isEnabled = webView != nil && backendState == .running
        // 单一更新动作项的标题/使能由 phase 决定
        updateActionItem?.title = updateActionMenuTitle
        updateActionItem?.isEnabled = updateActionMenuIsEnabled()
        renderHeader()
    }

    /// 单一更新动作项的标题（空闲=检查…；可用=安装…；下载中=取消；就绪=重启）
    private var updateActionMenuTitle: String {
        switch updatePhase {
        case .idle: return "检查更新…"
        case .checking: return "正在检查更新…"
        case .available: return "安装更新…"
        case .downloading: return "取消更新下载"
        case .installing: return "正在安装更新…"
        case .failed: return "重试更新…"
        case .readyToRelaunch: return "重新启动以完成更新"
        }
    }

    private func updateActionMenuIsEnabled() -> Bool {
        if isPanelProcess { return panelUpdateEnabled && !terminating }
        if cancellingUpdate || terminating { return false }
        switch updatePhase {
        case .idle, .checking:
            return !checkingUpdates && !updatePhase.busy                // 空闲 & 手动查
        case .available:
            return latestRelease != nil
        case .downloading:
            return true
        case .installing:
            return false
        case .failed, .readyToRelaunch:
            return latestRelease != nil || updatePhase == .readyToRelaunch
        }
    }

    /// 更新菜单项动作统一入口：.phase 决定下一步是检查、安装、取消还是重启。
    @objc private func performUpdateAction() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "update")); return }
        switch updatePhase {
        case .idle: checkForUpdates(silent: false)
        case .downloading: cancelUpdate()
        case .available, .failed, .readyToRelaunch: runUpdate()
        case .checking, .installing: break
        }
    }

    /// 用当前状态与缓存的脉冲数据重绘菜单头部部件。
    private func renderHeader() {
        guard let pulseView else { return }
        let running = backendState == .running
        let hostPort = apiBaseURL.replacingOccurrences(of: "http://", with: "")
        pulseView.update(stateDot: stateDotColor(backendState),
                         stateText: backendStateText,
                         subtitle: "\(hostPort) · \(currentVersion)",
                         summary: running ? pulseSummaryText : "服务未运行",
                         slots: running ? pulseSlots : [])
    }

    private var serviceActionTitle: String {
        (backend != nil || (isPanelProcess && backendPort != 0)) ? "重启服务" : (backendState == .failed ? "重试启动服务" : "启动服务")
    }

    private var canRestartBackend: Bool { !terminating && !recoveryTerminating && updatePhase != .installing && updatePhase != .readyToRelaunch && [.running, .stopped, .failed].contains(backendState) }

    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        if terminating { return false }
        switch item.action {
        case #selector(restartBackend):
            item.title = serviceActionTitle
            return canRestartBackend
        case #selector(reloadPanel): return webView != nil && backendState == .running
        case #selector(openPanelInBrowser): return backendState == .running
        case #selector(copyPanelToken): return !config.panelAccessToken.isEmpty
        case #selector(checkForUpdatesFromMenu):
            item.title = checkingUpdates ? "正在检查更新…" : "检查更新…"
            return !checkingUpdates && !updatePhase.busy && updatePhase != .readyToRelaunch
        // 状态栏和主菜单共用更新动作与快捷键。
        case #selector(performUpdateAction): return updateActionMenuIsEnabled()
        case #selector(quit): return updatePhase != .installing
        default: return item.action != nil
        }
    }

    func menuWillOpen(_ menu: NSMenu) {
        refreshStatusUI()
        refreshPreferencesUI()
        if menu == statusItem?.menu { refreshUsagePulse() }
    }

    private func refreshPreferencesUI() {
        loginSettingsItem?.isHidden = !launchAtLogin.requiresApproval
        notifications.refreshAuthorization { [weak self] denied in
            self?.notificationDenied = denied
            self?.notificationSettingsItem?.isHidden = !denied
        }
    }

    /// 菜单每次弹出时按需拉取最近 24 小时用量脉冲；失败静默保留上一次的图。
    private func refreshUsagePulse() {
        guard pulseView != nil else { return }
        guard backendState == .running else { renderHeader(); return }
        pulseFetchGeneration += 1
        let generation = pulseFetchGeneration
        let backendID = backendGeneration
        let now = Date()
        guard let url = usagePulseURL(base: apiBaseURL, now: now) else { return }
        var request = URLRequest(url: url)
        request.setValue("Bearer \(config.panelAccessToken)", forHTTPHeaderField: "Authorization")
        healthSession.dataTask(with: request) { [weak self] data, response, _ in
            guard (response as? HTTPURLResponse)?.statusCode == 200, let data,
                  let summary = parseUsagePulse(data) else { return }
            DispatchQueue.main.async {
                guard let self, generation == self.pulseFetchGeneration,
                      backendID == self.backendGeneration, self.backendState == .running, !self.terminating else { return }
                self.pulseSlots = usagePulseSlots(points: summary.points,
                                                  from: now.addingTimeInterval(-Double(PulseMenuView.windowHours) * 3600),
                                                  to: now, slots: PulseMenuView.slotCount)
                let requests = "最近 24 小时 · \(formatRequestCount(summary.windowRequests)) 次请求"
                self.pulseSummaryText = summary.windowTokens > 0
                    ? requests + " · \(formatTokenCount(summary.windowTokens)) tokens"
                    : requests
                self.refreshStatusUI()
            }
        }.resume()
    }

    @objc private func openLoginSettings() { launchAtLogin.openSystemSettings() }
    @objc private func openNotificationSettings() { notifications.openSystemSettings() }

    // MARK: 后端进程管理

    private func setBackendState(_ state: BackendState, error: String? = nil) {
        guard state != backendState || (error != nil && error != lastBackendError) else { return }
        if state != .running { panelReady = false }
        rescheduleHealthTimer(for: state)
        if state != backendState || (error != nil && error != lastBackendError) {
            appLogger.info("Backend state: \(state.rawValue, privacy: .public); \(error ?? "", privacy: .public)")
        }
        backendState = state
        if let error { lastBackendError = error }
        refreshStatusUI()
        renderBackendState()
    }

    private func renderBackendState() {
        guard window != nil else { return }
        switch backendState {
        case .starting: showOverlay(text: "正在启动本地后端…", spinning: true)
        case .restarting: showOverlay(text: "服务意外停止，正在重启（\(restartCount)/3）…", spinning: true)
        case .stopping: showOverlay(text: "正在停止服务并保存用量记录…", spinning: true)
        case .stopped:
            showOverlay(text: "服务已停止。点击启动服务继续使用面板。", spinning: false, retry: true)
            overlayButton.title = "启动服务"
        case .failed:
            showOverlay(text: lastBackendError ?? "服务不可用，请重试或查看运行日志。", spinning: false, retry: true)
            overlayButton.title = "重试"
        case .running: break // 仅页面成功加载后收起遮罩。
        }
    }

    func startBackend() {
        guard backend == nil, !terminating, updatePhase != .installing, updatePhase != .readyToRelaunch else { return }
        restartWork?.cancel()
        guard let fresh = loadOrCreateConfig() else {
            setBackendState(.failed, error: "无法读取配置，请修正后重试：\n\(configPath)")
            notifications.requestAndSend(title: "Elysia API 配置错误", body: "请打开数据文件夹检查 config.json；原文件已保留。", identifier: "config-error")
            return
        }
        config = fresh
        setBackendState(restartCount > 0 ? .restarting : .starting)
        do {
            // 修改 port 时保留所有未知配置键，后端始终读取同一份配置文件。
            let host = ProcessInfo.processInfo.environment["ELYSIA_API_HOST"]?.trimmingCharacters(in: .whitespacesAndNewlines)
            let listenHost = (host?.isEmpty == false ? host : nil) ?? config.host
            if !portIsFree(config.port, host: listenHost) {
                guard let port = (8799...8899).first(where: { portIsFree($0, host: listenHost) }) else {
                    throw UpdateError(message: "没有可用端口，请释放配置端口后重试。")
                }
                let original = config.port
                try persistPort(port, at: URL(fileURLWithPath: configPath))
                config.port = port
                notifications.requestAndSend(title: "Elysia API 端口已调整", body: "端口 \(original) 被占用，已改用 \(port)。可从菜单栏复制新地址。", identifier: "port-changed")
                appLogger.info("Port changed from \(original) to \(port)")
            }
            let fm = FileManager.default
            if !fm.fileExists(atPath: logPath) { fm.createFile(atPath: logPath, contents: nil) }
            let handle = try FileHandle(forWritingTo: URL(fileURLWithPath: logPath))
            try handle.seekToEnd()
            backendLogHandle = handle
            let process = Process()
            process.executableURL = URL(fileURLWithPath: backendPath)
            process.arguments = ["--config", configPath]
            process.environment = ProcessInfo.processInfo.environment.merging(["ELYSIA_API_OPEN_BROWSER": "false", "ELYSIA_PARENT_STDIN": "1"]) { _, value in value }
            let input = Pipe()
            process.standardInput = input
            backendInputPipe = input
            process.currentDirectoryURL = URL(fileURLWithPath: dataDirPath)
            process.standardOutput = handle
            process.standardError = handle
            process.terminationHandler = { [weak self] process in
                DispatchQueue.main.async { self?.backendDidExit(process) }
            }
            backendGeneration = UUID()
            backendHost = listenHost
            backendPort = config.port
            startupTime = Date()
            healthySince = nil
            healthFailedSince = nil
            recoveryTerminating = false
            healthInFlight = false
            panelLoaded = false
            userStopping = false
            try process.run()
            // Only the child keeps the read end; parent death closes the writer and triggers graceful shutdown.
            try? input.fileHandleForReading.close()
            backend = process
            #if NATIVE_TESTS
            try String(process.processIdentifier).write(toFile: dataDirPath + "/owned-backend.pid", atomically: true, encoding: .utf8)
            if CommandLine.arguments.contains("--update-ack") {
                try? String(process.processIdentifier).write(toFile: dataDirPath + "/new-backend.pid", atomically: true, encoding: .utf8)
            }
            #endif
            configureWebScripts()
            appLogger.info("Backend started, pid \(process.processIdentifier), port \(self.backendPort)")
            refreshStatusUI()
            pollHealth()
        } catch {
            try? backendLogHandle?.close()
            backendLogHandle = nil
            try? backendInputPipe?.fileHandleForWriting.close()
            try? backendInputPipe?.fileHandleForReading.close()
            backendInputPipe = nil
            backendPort = 0
            backendHost = nil
            setBackendState(.failed, error: "后端启动失败：\n\(error.localizedDescription)")
            scheduleRecovery("后端启动失败：\(error.localizedDescription)")
        }
    }

    /// 所有停止路径直接终止拥有的子进程；信号与后端 HTTP 关闭共用排空/刷盘序列。
    private func requestBackendExit(_ process: Process) {
        guard backend === process, backendStopDeadline == nil else { return }
        if process.isRunning { process.terminate() }
        let deadline = DispatchWorkItem { [weak self, weak process] in
            guard let self, let process, self.backend === process, process.isRunning else { return }
            appLogger.error("Backend did not finish shutdown within 15 seconds; forcing owned pid \(process.processIdentifier) to exit")
            kill(process.processIdentifier, SIGKILL)
        }
        backendStopDeadline = deadline
        DispatchQueue.main.asyncAfter(deadline: .now() + 15, execute: deadline)
    }

    func stopBackend() {
        restartWork?.cancel()
        guard let process = backend else { setBackendState(.stopped); return }
        userStopping = true
        setBackendState(.stopping)
        if process.isRunning { requestBackendExit(process) }
    }

    private func backendDidExit(_ process: Process) {
        guard backend === process else { return }
        appLogger.info("Backend exited, status \(process.terminationStatus)")
        backend = nil
        backendStopDeadline?.cancel()
        backendStopDeadline = nil
        try? backendInputPipe?.fileHandleForWriting.close()
        backendInputPipe = nil
        backendGeneration = UUID()
        backendHost = nil
        backendPort = 0
        healthInFlight = false
        healthySince = nil
        panelLoaded = false
        try? backendLogHandle?.close()
        backendLogHandle = nil
        if terminating { finishTerminationIfReady(); return }
        if updatePhase == .readyToRelaunch {
            userStopping = false
            pendingBackendRestart = false
            setBackendState(.stopped)
            relaunchAfterUpdate()
            return
        }
        if userStopping {
            userStopping = false
            if pendingBackendRestart {
                pendingBackendRestart = false
                // 重启语义：停止完成后立即接力启动，不落入 .stopped 等待人工。
                retryStartup()
                return
            }
            setBackendState(.stopped)
            return
        }
        // 接力标志只服务本次 stop→start；其它退出路径一律清除，避免滞留误启动。
        pendingBackendRestart = false
        recoveryTerminating = false
        scheduleRecovery("后端意外退出（状态 \(process.terminationStatus)）")
    }

    private func scheduleRecovery(_ message: String) {
        guard !terminating, updatePhase != .installing, updatePhase != .readyToRelaunch else { return }
        if restartCount < 3 {
            restartCount += 1
            setBackendState(.restarting, error: message + "，正在尝试恢复。")
            notifications.requestAndSend(title: "Elysia API 正在恢复", body: "服务意外停止，正在自动重启（\(restartCount)/3）。", identifier: "backend-restarting")
            let generation = backendGeneration
            let work = DispatchWorkItem { [weak self] in
                guard let self, self.backendGeneration == generation, self.backendState == .restarting, !self.terminating else { return }
                self.startBackend()
            }
            restartWork = work
            DispatchQueue.main.asyncAfter(deadline: .now() + 2, execute: work)
        } else {
            setBackendState(.failed, error: "后端连续异常退出，已停止自动重启。请查看运行日志后重试。")
            notifications.requestAndSend(title: "Elysia API 已停止", body: "三次自动重启均失败，请打开面板重试或查看运行日志。", identifier: "backend-stopped")
        }
    }

    /// 服务运行时先排空旧进程再启动；已停止时直接启动。
    @objc private func restartBackend() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "restart")); return }
        guard canRestartBackend else { return }
        guard backend != nil else { retryStartup(); return }
        pendingBackendRestart = true
        stopBackend()
    }

    @objc private func reloadPanel() {
        guard let webView, backendState == .running else { return }
        webView.reload()
    }

    @objc private func openPanelInBrowser() {
        guard let url = URL(string: "\(apiBaseURL)/ui/") else { return }
        NSWorkspace.shared.open(url)
    }

    private func requestNotificationPermission() {
        notifications.requestPermission { [weak self] granted in
            guard let self else { return }
            self.refreshPreferencesUI()
            if !granted {
                let alert = NSAlert()
                alert.messageText = "通知已被系统关闭"
                alert.informativeText = "请在系统设置中允许 ElysiaApi 发送通知。菜单栏和窗口内仍会显示服务状态。"
                alert.addButton(withTitle: "打开系统设置")
                alert.addButton(withTitle: "稍后")
                NSApp.activate(ignoringOtherApps: true)
                if alert.runModal() == .alertFirstButtonReturn { self.notifications.openSystemSettings() }
            }
        }
    }

    @objc private func showPreferences() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "preferences")); return }
        let alert = NSAlert()
        alert.messageText = "Elysia API 偏好设置"
        alert.informativeText = "开机启动时仅驻留菜单栏。关闭主窗口后服务继续运行。" +
            (notificationDenied ? "\n通知已被系统禁用，可在下方打开系统设置。" : "") +
            (launchAtLogin.requiresApproval ? "\n开机启动等待系统批准。" : "")
        let login = NSButton(checkboxWithTitle: "登录 macOS 时自动启动并驻留菜单栏", target: nil, action: nil)
        login.state = launchAtLogin.isEnabled ? .on : .off
        let notify = NSButton(checkboxWithTitle: "发送重要状态通知", target: nil, action: nil)
        notify.state = notifications.enabled ? .on : .off
        let settings = NSButton(title: "通知系统设置…", target: self, action: #selector(openNotificationSettings))
        settings.bezelStyle = .rounded
        var controls: [NSView] = [login, notify, settings]
        if launchAtLogin.requiresApproval {
            let approval = NSButton(title: "登录项系统设置…", target: self, action: #selector(openLoginSettings))
            approval.bezelStyle = .rounded
            controls.append(approval)
        }
        let stack = NSStackView(views: controls)
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.translatesAutoresizingMaskIntoConstraints = false
        let container = NSView(frame: NSRect(x: 0, y: 0, width: 380, height: launchAtLogin.requiresApproval ? 138 : 104))
        container.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            stack.topAnchor.constraint(equalTo: container.topAnchor),
            stack.bottomAnchor.constraint(equalTo: container.bottomAnchor),
        ])
        alert.accessoryView = container
        alert.addButton(withTitle: "保存")
        alert.addButton(withTitle: "取消")
        NSApp.activate(ignoringOtherApps: true)
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let wasEnabled = notifications.enabled
        do {
            try launchAtLogin.setEnabled(login.state == .on)
            windowState.set(login.state == .on, for: DefaultsKey.launchAtLogin)
            notifications.enabled = notify.state == .on
            windowState.set(notifications.enabled, for: DefaultsKey.notificationsEnabled)
            refreshPreferencesUI()
            if notifications.enabled && !wasEnabled { requestNotificationPermission() }
        } catch {
            self.alert("偏好设置保存失败:\n\(error.localizedDescription)")
        }
    }

    // MARK: 健康检查与面板加载

    private func scheduleTimers() {
        rescheduleHealthTimer()
        let update = Timer(timeInterval: 24 * 3600, repeats: true) { [weak self] _ in self?.checkForUpdates(silent: true) }
        update.tolerance = 60
        timers.append(update)
        RunLoop.main.add(update, forMode: .common)
        #if NATIVE_TESTS
        if ProcessInfo.processInfo.environment["ELYSIA_NATIVE_TRAY_TEST"] == "1" {
            let control = Timer(timeInterval: 0.1, repeats: true) { [weak self] _ in
                guard let self, let command = try? String(contentsOfFile: dataDirPath + "/tray-test-command", encoding: .utf8) else { return }
                try? FileManager.default.removeItem(atPath: dataDirPath + "/tray-test-command")
                switch command {
                case "show": self.showMainWindow()
                case "close": self.stopPanelProcess()
                case "quit": self.requestTermination()
                default: break
                }
            }
            timers.append(control)
            RunLoop.main.add(control, forMode: .common)
        }
        #endif
    }

    private func rescheduleHealthTimer(for state: BackendState? = nil) {
        guard !isPanelProcess, !terminating else { return }
        let current = state ?? backendState
        if backend == nil && (current == .stopped || current == .failed) {
            healthTimer?.invalidate()
            healthTimer = nil
            return
        }
        let interval: TimeInterval = current == .running && panelProcess == nil && window == nil ? 30 : 3
        guard healthTimer?.timeInterval != interval else { return }
        healthTimer?.invalidate()
        let health = Timer(timeInterval: interval, repeats: true) { [weak self] _ in self?.pollHealth() }
        health.tolerance = interval == 30 ? 5 : 0.3
        healthTimer = health
        RunLoop.main.add(health, forMode: .common)
    }

    private func loadPanelWhenHealthy() {
        guard backendState == .running, let webView else { return }
        if !panelLoaded || loadedPort != backendPort {
            if let url = URL(string: "\(apiBaseURL)/ui/#/overview") {
                panelLoaded = true
                panelReady = false
                loadedPort = backendPort
                configureWebScripts()
                showOverlay(text: "正在加载面板…", spinning: true)
                webView.load(URLRequest(url: url))
            }
        } else if !webView.isLoading { hideOverlay() }
    }

    private func pollHealth() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "hello")); return }
        guard let process = backend, process.isRunning, !healthInFlight, !userStopping, !terminating, !recoveryTerminating,
              updatePhase != .installing, updatePhase != .readyToRelaunch,
              let url = URL(string: "\(apiBaseURL)/health") else { return }
        let generation = backendGeneration
        healthInFlight = true
        healthSession.dataTask(with: url) { [weak self] _, response, error in
            DispatchQueue.main.async {
                guard let self, self.backendGeneration == generation, self.backend === process else { return }
                self.healthInFlight = false
                guard !self.userStopping, !self.terminating, !self.recoveryTerminating,
                      self.updatePhase != .installing, self.updatePhase != .readyToRelaunch else { return }
                let ok = (response as? HTTPURLResponse)?.statusCode == 200
                if ok {
                    self.healthFailedSince = nil
                    let recovering = self.backendState == .restarting || self.backendState == .failed
                    if self.healthySince == nil { self.healthySince = Date() }
                    // 连续健康 60 秒才重置失败预算，避免启动即崩溃无限循环。
                    if Date().timeIntervalSince(self.healthySince!) >= 60 { self.restartCount = 0 }
                    self.setBackendState(.running)
                    #if NATIVE_TESTS
                    if self.handoffForNativeAppTest() { return }
                    #endif
                    self.acknowledgeUpdateLaunch()
                    if recovering {
                        self.notifications.requestAndSend(title: "Elysia API 已恢复", body: "服务已恢复，可以继续使用。", identifier: "backend-recovered")
                    }
                    self.loadPanelWhenHealthy()
                } else {
                    self.healthySince = nil
                    if self.healthFailedSince == nil { self.healthFailedSince = Date() }
                    if self.backendState == .running || Date().timeIntervalSince(self.startupTime) > 15 {
                        self.setBackendState(.failed, error: "服务暂时无法连接，正在检查恢复。\n\(error?.localizedDescription ?? "健康检查未通过")")
                    }
                    // 短暂网络/唤醒抖动可自行恢复；持续 15 秒不健康则重启本壳拥有的进程。
                    if Date().timeIntervalSince(self.healthFailedSince!) >= 15 {
                        self.recoveryTerminating = true
                        self.setBackendState(.restarting, error: "健康检查持续失败，正在重启服务。")
                        self.requestBackendExit(process)
                    }
                }
            }
        }.resume()
    }

    // MARK: - WKNavigationDelegate

    private func isPanelOrigin(_ url: URL) -> Bool {
        guard let base = URL(string: apiBaseURL) else { return false }
        return url.scheme == base.scheme && url.host == base.host && url.port == base.port
    }

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { decisionHandler(.cancel); return }
        if action.shouldPerformDownload && (isPanelOrigin(url) || url.scheme == "blob") {
            decisionHandler(.download)
        } else if isPanelOrigin(url) && (url.path == "/ui" || url.path.hasPrefix("/ui/")) {
            decisionHandler(.allow)
        } else {
            if action.targetFrame == nil || action.targetFrame?.isMainFrame == true {
                if ["https", "http", "mailto"].contains(url.scheme ?? "") { NSWorkspace.shared.open(url) }
            }
            decisionHandler(.cancel)
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        decisionHandler(response.canShowMIMEType ? .allow : .download)
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if action.targetFrame == nil, let url = action.request.url,
           isPanelOrigin(url), url.path.hasPrefix("/ui/") { webView.load(action.request) }
        return nil
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        guard webView === self.webView else { return }
        panelLoaded = false
        showOverlay(text: "面板进程已退出，正在重新加载…", spinning: true)
        appLogger.error("WebKit content process terminated")
        pollHealth()
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard webView === self.webView else { return }
        #if NATIVE_TESTS
        if ProcessInfo.processInfo.environment["ELYSIA_NATIVE_APP_TEST"] == "1", CommandLine.arguments.contains("--update-ack") {
            try? String(ProcessInfo.processInfo.processIdentifier).write(toFile: dataDirPath + "/new-html-loaded.pid", atomically: true, encoding: .utf8)
        }
        #endif
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "htmlLoaded")) }
        if backendState == .running {
            hideOverlay()
            reportPanelReadiness()
            acknowledgeUpdateLaunch()
        }
    }

    /// 面板加载失败(含连接被拒的 provisional 阶段):清掉"已加载"标记,
    /// 健康轮询确认后端恢复后会自动重载页面,无需用户手动重试。
    private func handlePanelLoadFailure(_ error: Error) {
        if (error as NSError).domain == NSURLErrorDomain && (error as NSError).code == NSURLErrorCancelled { return }
        panelLoaded = false
        showOverlay(text: "面板加载失败:\n\(error.localizedDescription)", spinning: false, retry: true)
        if isPanelProcess {
            DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak self] in
                guard let self, self.window != nil, !self.terminating, !self.panelLoaded else { return }
                self.panelBridge?.send(PanelMessage(type: "hello"))
            }
        }
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        guard webView === self.webView else { return }
        handlePanelLoadFailure(error)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        guard webView === self.webView else { return }
        handlePanelLoadFailure(error)
    }

    // MARK: - 下载(WKDownloadDelegate)

    /// 保存面板和实际下载属于同一个窗口操作；路径回调必须终结一次才能释放 WebKit。
    private final class PanelDownload {
        let download: WKDownload
        var panel: NSSavePanel?
        var destination: URL?
        var temporaryURL: URL?
        var destinationIdentity: URLResourceValues?
        var reply: ((URL?) -> Void)?

        init(_ download: WKDownload) { self.download = download }

        @discardableResult
        func resolveDestination(_ url: URL?) throws -> Bool {
            guard let reply else { return false }
            if let url, FileManager.default.fileExists(atPath: url.path) {
                destinationIdentity = try Self.identity(at: url)
            }
            self.reply = nil
            destination = url
            temporaryURL = url.map {
                $0.deletingLastPathComponent().appendingPathComponent(".elysia-export-\(UUID().uuidString).partial")
            }
            reply(temporaryURL)
            return true
        }

        private static func identity(at url: URL) throws -> URLResourceValues {
            var fresh = URL(fileURLWithPath: url.path)
            fresh.removeAllCachedResourceValues()
            return try fresh.resourceValues(forKeys: [.fileResourceIdentifierKey, .contentModificationDateKey, .fileSizeKey])
        }

        func destinationIsUnchanged() throws -> Bool {
            guard let destination else { return false }
            let exists = FileManager.default.fileExists(atPath: destination.path)
            guard let original = destinationIdentity else { return !exists }
            guard exists else { return false }
            let current = try Self.identity(at: destination)
            if let originalID = original.fileResourceIdentifier as? NSObject,
               let currentID = current.fileResourceIdentifier as? NSObject,
               !originalID.isEqual(currentID) { return false }
            return original.contentModificationDate == current.contentModificationDate && original.fileSize == current.fileSize
        }

        func removeTemporaryFile() {
            if let temporaryURL { try? FileManager.default.removeItem(at: temporaryURL) }
        }

        func dismissPanel() {
            guard let panel else { return }
            self.panel = nil
            if let parent = panel.sheetParent { parent.endSheet(panel, returnCode: .cancel) }
            else { panel.cancel(nil) }
            panel.orderOut(nil)
        }
    }

    private var panelDownloads: [ObjectIdentifier: PanelDownload] = [:]
    private var cancellingPanelDownloads: [ObjectIdentifier: PanelDownload] = [:]
    private var panelDownloadMessage = ""
    #if NATIVE_TESTS
    private var panelDestinationReplies = 0
    var panelDownloadCountForTests: Int { panelDownloads.count + cancellingPanelDownloads.count }
    var panelTemporaryDestinationForTests: URL? { panelDownloads.values.first?.temporaryURL }
    var pendingSavePanelForTests: NSSavePanel? { panelDownloads.values.first(where: { $0.reply != nil })?.panel }
    var panelDestinationRepliesForTests: Int { panelDestinationReplies }
    var panelDownloadMessageForTests: String { panelDownloadMessage }
    func resolvePanelDestinationForTests(_ url: URL?) {
        guard let pending = panelDownloads.values.first(where: { $0.reply != nil }) else { return }
        completePanelDestination(pending, url: url)
    }
    #endif

    private func trackPanelDownload(_ download: WKDownload, from source: WKWebView) {
        guard source === webView, window != nil, !terminating,
              updatePhase != .installing, updatePhase != .readyToRelaunch else {
            download.cancel { _ in }
            return
        }
        panelDownloads[ObjectIdentifier(download)] = PanelDownload(download)
        download.delegate = self
    }

    /// 关窗、退出和安装更新共享清理；先移除所有权，迟到的 delegate 回调不会再次提示。
    func cancelPanelDownloads(reason: String) {
        let downloads = Array(panelDownloads.values)
        panelDownloads.removeAll()
        for pending in downloads {
            completePanelDestination(pending, url: nil)
            pending.download.delegate = nil
            let identifier = ObjectIdentifier(pending.download)
            cancellingPanelDownloads[identifier] = pending
            pending.download.cancel { [weak self] _ in
                pending.removeTemporaryFile()
                self?.cancellingPanelDownloads[identifier] = nil
                self?.finishTerminationIfReady()
            }
        }
        if !downloads.isEmpty { reportPanelDownload("导出已取消", detail: reason) }
    }

    private func completePanelDestination(_ pending: PanelDownload, url: URL?) {
        var selected = url
        let resolved: Bool
        do { resolved = try pending.resolveDestination(url) }
        catch {
            selected = nil
            resolved = (try? pending.resolveDestination(nil)) ?? false
            reportPanelDownload("导出失败", detail: "无法检查保存目标：\(error.localizedDescription)")
        }
        if resolved {
            #if NATIVE_TESTS
            panelDestinationReplies += 1
            #endif
        }
        pending.dismissPanel()
        if selected == nil {
            panelDownloads[ObjectIdentifier(pending.download)] = nil
            pending.download.delegate = nil
        }
    }

    private func reportPanelDownload(_ title: String, detail: String) {
        panelDownloadMessage = "\(title)：\(detail)"
        appLogger.info("\(self.panelDownloadMessage, privacy: .public)")
        if !terminating {
            notifications.requestAndSend(title: title, body: detail, identifier: "panel-download")
        }
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        trackPanelDownload(download, from: webView)
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        trackPanelDownload(download, from: webView)
    }

    /// 弹系统保存对话框决定落盘位置;用户取消时回调 nil,WebKit 会取消该下载。
    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse,
                  suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        guard let pending = panelDownloads[ObjectIdentifier(download)], let window,
              !terminating, updatePhase != .installing, updatePhase != .readyToRelaunch else {
            panelDownloads[ObjectIdentifier(download)] = nil
            download.delegate = nil
            completionHandler(nil)
            return
        }
        let panel = NSSavePanel()
        panel.nameFieldStringValue = suggestedFilename
        panel.canCreateDirectories = true
        pending.panel = panel
        pending.reply = completionHandler
        panel.beginSheetModal(for: window) { [weak self, weak pending] response in
            guard let self, let pending, pending.reply != nil else { return }
            let destination = response == .OK ? pending.panel?.url : nil
            self.completePanelDestination(pending, url: destination)
            if destination == nil { self.reportPanelDownload("导出已取消", detail: "未选择保存位置。") }
        }
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let pending = panelDownloads.removeValue(forKey: ObjectIdentifier(download)) else { return }
        download.delegate = nil
        defer { pending.removeTemporaryFile() }
        guard let url = pending.destination, let temporary = pending.temporaryURL else { return }
        do {
            guard try pending.destinationIsUnchanged() else {
                throw UpdateError(message: "保存目标在下载期间发生变化，已保留该文件。请重新导出。")
            }
            if pending.destinationIdentity != nil {
                _ = try FileManager.default.replaceItemAt(url, withItemAt: temporary)
            } else {
                try FileManager.default.moveItem(at: temporary, to: url)
            }
            reportPanelDownload("导出已完成", detail: "已保存到：\(url.path)")
        } catch { reportPanelDownload("导出失败", detail: error.localizedDescription) }
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        guard let pending = panelDownloads.removeValue(forKey: ObjectIdentifier(download)) else { return }
        completePanelDestination(pending, url: nil)
        pending.removeTemporaryFile()
        download.delegate = nil
        reportPanelDownload("导出失败", detail: error.localizedDescription)
    }

    // MARK: - 主题同步(WKScriptMessageHandler)

    func userContentController(_ userContentController: WKUserContentController,
                               didReceive message: WKScriptMessage) {
        guard message.name == "theme", message.frameInfo.isMainFrame,
              let url = message.frameInfo.request.url, isPanelOrigin(url),
              let body = message.body as? [String: Any] else { return }
        let dark = body["dark"] as? Bool ?? false
        let background = (body["background"] as? String).flatMap(cssColor) ?? .windowBackgroundColor
        windowState.save(theme: dark ? "dark" : "light")
        if let title = body["title"] as? String, !title.isEmpty {
            window?.title = String(title.prefix(120))
        }
        applyTheme(dark: dark, background: background)
    }

    /// 用页面主题为窗口背景/遮罩/更新条着色,使原生区域与网页无缝衔接
    private func applyTheme(dark: Bool, background: NSColor) {
        guard window != nil else { return }
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.backgroundColor = background
        overlay.layer?.backgroundColor = background.cgColor
        // 胶囊自己监听 appearance 并在 updateLayer 里取 window.backgroundColor——无需显式赋值。
        updateCapsule?.needsDisplay = true
        webView.underPageBackgroundColor = background
    }

    // MARK: 更新

    @objc private func checkForUpdatesFromMenu() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "checkUpdate")); return }
        checkForUpdates(silent: false)
    }

    private func checkForUpdates(silent: Bool) {
        guard !checkingUpdates, !updatePhase.busy, updatePhase != .readyToRelaunch, !terminating,
              let url = URL(string: releasesAPI) else { return }
        checkingUpdates = true
        updatePhase = .checking
        updateMessage = "正在检查更新…"
        updateDetail = "与 GitHub 发布同步"
        // 静默检查不弹胶囊；手动查不闪胶囊（idle 穿梭时胶囊隐藏）
        refreshUpdateUI()
        var request = URLRequest(url: url)
        request.setValue("ElysiaApi/\(currentVersion)", forHTTPHeaderField: "User-Agent")
        apiSession.dataTask(with: request) { [weak self] data, response, error in
            DispatchQueue.main.async {
                guard let self, !self.terminating else { return }
                self.checkingUpdates = false
                let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                guard status == 200, let data, error == nil, let info = Self.parseRelease(data) else {
                    self.updatePhase = self.latestRelease == nil ? .idle : .available
                    self.updateMessage = "检查更新失败"
                    self.updateDetail = Self.updateCheckFailure(error: error, status: status)
                    appLogger.error("\(self.updateDetail, privacy: .public)")
                    self.refreshUpdateUI()
                    if !silent { self.alert(self.updateDetail) }
                    return
                }
                guard isNewer(info.tag, than: self.currentVersion) else {
                    self.latestRelease = nil
                    self.updatePhase = .idle
                    self.updateMessage = "版本 \(self.currentVersion)"
                    self.updateDetail = "已是最新版本"
                    self.refreshUpdateUI()
                    if !silent { self.alert("已是最新版本\n\(self.currentVersion)") }
                    return
                }
                let isNewRelease = self.latestRelease?.tag != info.tag
                self.latestRelease = info
                self.updatePhase = .available
                self.updateMessage = "发现新版本 \(info.tag)"
                self.updateDetail = "\(self.currentVersion) → \(info.tag) · 安装需要重启"
                // 新可用版本切换到提示态：弹出胶囊
                self.updateCapsuleVisible = true
                self.refreshUpdateUI(animated: true)
                appLogger.info("Update available: \(info.tag, privacy: .public)")
                if isNewRelease {
                    self.notifications.requestAndSend(title: "Elysia API 有新版本", body: "\(info.tag) 已发布，点击打开应用更新。", identifier: "update-available")
                }
                // 手动检查发现新版本时，把窗口带出来像旧版「开始下载」可见一样显式
                if !silent { self.showMainWindow() }
            }
        }.resume()
    }

    private static func updateCheckFailure(error: Error?, status: Int) -> String {
        if let error { return "检查更新失败：无法连接 GitHub（\(error.localizedDescription)）" }
        if status == 404 { return "尚未发布可用版本。" }
        if status == 200 { return "最新版本未包含有效的 macOS DMG 文件。" }
        return "检查更新失败：GitHub 返回 HTTP \(status)。"
    }

    private static func parseRelease(_ data: Data) -> ReleaseInfo? {
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let tag = json["tag_name"] as? String,
              let assets = json["assets"] as? [[String: Any]] else { return nil }
        for entry in assets where entry["name"] as? String == "elysia-api-macos.dmg" {
            if let url = entry["browser_download_url"] as? String, URL(string: url)?.scheme == "https" {
                return ReleaseInfo(tag: tag, dmgURL: url, dmgDigest: entry["digest"] as? String ?? "")
            }
        }
        return nil
    }

    @objc private func runUpdate() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "update")); return }
        if updatePhase == .readyToRelaunch { relaunchAfterUpdate(); return }
        guard let release = latestRelease, !checkingUpdates, !updatePhase.busy, !terminating else { return }
        do { _ = try UpdateInstaller.validateDigest(release.dmgDigest) }
        catch { updateFailed(error.localizedDescription); return }
        guard let remote = URL(string: release.dmgURL), remote.scheme == "https" else { return }
        beginUpdateDownload(from: remote, release: release)
    }

    private func beginUpdateDownload(from remote: URL, release: ReleaseInfo) {
        let generation = UUID()
        updateDownloadGeneration = generation
        updatePhase = .downloading
        updateMessage = "下载 \(release.tag)"
        updateDetail = "准备下载"
        updateFraction = nil
        cancellingUpdate = false
        // 主动/自动进入下载时也走上提示态（胶囊装呈现进度）
        updateCapsuleVisible = true
        refreshUpdateUI(animated: true)
        appLogger.info("Downloading update \(release.tag, privacy: .public)")
        let delegate = UpdateDownloadDelegate(onProgress: { [weak self] written, expected in
            guard let self, self.updateDownloadGeneration == generation, !self.terminating,
                  self.updatePhase == .downloading, !self.cancellingUpdate else { return }
            self.updateFraction = expected > 0 ? Double(written) / Double(expected) : nil
            let size = ByteCountFormatter.string(fromByteCount: written, countStyle: .file)
            let percent = expected > 0 ? " · \(Int(100 * Double(written) / Double(expected)))%" : ""
            self.updateMessage = "下载 \(release.tag)"
            self.updateDetail = "已接收 \(size)\(percent)"
            self.refreshUpdateUI()
        }, onFinished: { [weak self] localURL, error in
            guard let self, self.updateDownloadGeneration == generation else {
                if let localURL { try? FileManager.default.removeItem(at: localURL) }
                return
            }
            self.updateSession?.finishTasksAndInvalidate()
            self.updateSession = nil
            self.updateDownloadDelegate = nil
            self.updateTask = nil
            self.cancellingUpdate = false
            if self.terminating || (error as NSError?)?.code == NSURLErrorCancelled {
                if let localURL { try? FileManager.default.removeItem(at: localURL) }
                if self.terminating { self.finishTerminationIfReady(); return }
                self.updatePhase = .available
                self.updateMessage = "发现新版本 \(release.tag)"
                self.updateDetail = "下载已取消"
                self.refreshUpdateUI()
                return
            }
            guard let localURL, error == nil else {
                if let localURL { try? FileManager.default.removeItem(at: localURL) }
                self.updateFailed(error?.localizedDescription ?? "下载文件不存在")
                return
            }
            self.updatePhase = .installing
            self.updateMessage = "安装 \(release.tag)"
            self.updateDetail = "正在校验并暂存应用，完成后自动重启…"
            self.resumeBackendAfterUpdateFailure = !self.userStopping && (self.backend != nil || self.backendState == .restarting || self.backendState == .starting)
            self.restartWork?.cancel()
            self.pendingBackendRestart = false
            self.cancelPanelDownloads(reason: "应用正在安装更新，未完成的导出已取消。")
            self.refreshUpdateUI()
            let current = Bundle.main.bundleURL
            DispatchQueue.global(qos: .userInitiated).async {
                defer { try? FileManager.default.removeItem(at: localURL) }
                do {
                    try UpdateInstaller.verify(localURL, digest: release.dmgDigest)
                    let staged = try UpdateInstaller.extractApp(fromDMG: localURL, beside: current)
                    DispatchQueue.main.async {
                        self.handoffStagedUpdate(staged)
                    }
                } catch {
                    DispatchQueue.main.async { self.updateFailed(error.localizedDescription) }
                }
            }
        })
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 30
        configuration.timeoutIntervalForResource = 15 * 60
        updateDownloadDelegate = delegate
        let session = URLSession(configuration: configuration, delegate: delegate, delegateQueue: .main)
        updateSession = session
        updateTask = session.downloadTask(with: remote)
        updateTask?.resume()
    }

    @objc private func cancelUpdate() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "cancelUpdate")); return }
        guard updatePhase == .downloading else { return }
        cancellingUpdate = true
        updateMessage = "正在取消下载…"
        refreshUpdateUI()
        updateTask?.cancel()
    }

    private func relaunchAfterUpdate() {
        guard !terminating, relaunchHelper == nil, let stagedUpdate else { return }
        saveWindowState()
        if let process = backend {
            userStopping = true
            setBackendState(.stopping)
            requestBackendExit(process)
            return
        }
        do {
            relaunchHelper = try UpdateInstaller.startHelper(staged: stagedUpdate, current: Bundle.main.bundleURL,
                                                            parentPID: ProcessInfo.processInfo.processIdentifier,
                                                            background: usesPanelProcess ? panelProcess == nil : window == nil)
            #if NATIVE_TESTS
            try? String(relaunchHelper!.processIdentifier).write(toFile: dataDirPath + "/owned-helper.pid", atomically: true, encoding: .utf8)
            #endif
            // From this point the independent helper owns staging, replacement and rollback.
            self.stagedUpdate = nil
            requestTermination()
        }
        catch {
            try? FileManager.default.removeItem(at: stagedUpdate.deletingLastPathComponent())
            self.stagedUpdate = nil
            appLogger.error("Relaunch failed: \(error.localizedDescription, privacy: .public)")
            updateFailed("无法启动更新辅助程序，当前版本已保留：\(error.localizedDescription)")
        }
    }

    private func acknowledgeUpdateLaunch() {
        guard !updateLaunchAcknowledged, backendState == .running,
              let url = UpdateInstaller.acknowledgementURL(arguments: CommandLine.arguments) else { return }
        if launchedAtLogin { writeUpdateAcknowledgement(url); return }
        if usesPanelProcess {
            if panelReady && panelProcess != nil && !panelClosing { writeUpdateAcknowledgement(url) }
            return
        }
        guard window != nil else { return }
        guard !panelReadinessInFlight, let webView, !webView.isLoading,
              let panelURL = webView.url, isPanelOrigin(panelURL) else { return }
        panelReadinessInFlight = true
        let generation = backendGeneration
        // Navigation completion only proves HTML loaded; lazy React chunks may still fail.
        webView.evaluateJavaScript("Boolean(document.querySelector('#token')?.closest('form') || document.querySelector('main h1'))") { [weak self, weak webView] result, _ in
            guard let self else { return }
            self.panelReadinessInFlight = false
            guard let webView, webView === self.webView, generation == self.backendGeneration,
                  self.backendState == .running, !self.terminating, result as? Bool == true else { return }
            self.writeUpdateAcknowledgement(url)
        }
    }

    private func writeUpdateAcknowledgement(_ url: URL) {
        do {
            try UpdateInstaller.acknowledgeLaunch(at: url)
            updateLaunchAcknowledged = true
            #if NATIVE_TESTS
            try String(ProcessInfo.processInfo.processIdentifier).write(toFile: dataDirPath + "/native-ready.pid", atomically: true, encoding: .utf8)
            #endif
        } catch {
            appLogger.error("Could not acknowledge update startup: \(error.localizedDescription, privacy: .public)")
        }
    }

    private func handoffStagedUpdate(_ staged: URL) {
        resumeBackendAfterUpdateFailure = resumeBackendAfterUpdateFailure || (!userStopping && backend != nil)
        stagedUpdate = staged
        updatePhase = .readyToRelaunch
        updateMessage = "更新已校验"
        updateDetail = "正在保存记录，随后安装并重启…"
        refreshUpdateUI()
        relaunchAfterUpdate()
    }

    private func updateFailed(_ message: String) {
        updatePhase = .failed
        updateMessage = "更新失败"
        var detail = message
        if updateDetail.hasPrefix("已接收") || updateDetail.hasPrefix("正在校验") {
            detail = "\(message)"
        }
        updateDetail = detail
        // 失败值得重新唤起胶囊（用户之前手关过 ×，也要再看到失败事实）
        updateCapsuleVisible = true
        appLogger.error("更新失败：\(message, privacy: .public)")
        refreshUpdateUI(animated: true)
        notifications.requestAndSend(title: "Elysia API 更新失败", body: "\(message) 点击打开应用重试。", identifier: "update-failed")
        if resumeBackendAfterUpdateFailure && backend == nil { startBackend() }
        resumeBackendAfterUpdateFailure = false
        pollHealth()
    }

    // MARK: 菜单动作

    @objc private func copyPanelURL() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString("\(apiBaseURL)/ui/", forType: .string)
    }

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

// Pipe writes report EPIPE instead of terminating the owner when its peer exits.
signal(SIGPIPE, SIG_IGN)

if CommandLine.arguments.contains("--update-helper") {
    do {
        #if NATIVE_TESTS
        let launchTimeout = Double(ProcessInfo.processInfo.environment["ELYSIA_NATIVE_UPDATE_LAUNCH_TIMEOUT"] ?? "") ?? 45
        try UpdateInstaller.runHelper(arguments: CommandLine.arguments, launchTimeout: launchTimeout)
        #else
        try UpdateInstaller.runHelper(arguments: CommandLine.arguments)
        #endif
        exit(0)
    } catch {
        fputs("Update helper failed: \(error.localizedDescription)\n", stderr)
        exit(1)
    }
}

#if NATIVE_TESTS
// Test hooks are compiled out of the shipped app. All fixture data/preferences have a unique domain.
extension AppDelegate {
    func prepareForTests() {
        config = loadOrCreateConfig() ?? PanelConfig()
        notifications.enabled = false
        buildMainMenu()
        buildStatusItem()
    }
    func pollForTests() { pollHealth() }
    func retryForTests() { retryStartup() }
    func updateUIForTests() { refreshUpdateUI() }
    func ageHealthFailureForTests() { healthFailedSince = Date().addingTimeInterval(-16) }
    func downloadForTests(from url: URL) {
        let release = ReleaseInfo(tag: "v99.0.0", dmgURL: url.absoluteString, dmgDigest: String(repeating: "0", count: 64))
        latestRelease = release
        beginUpdateDownload(from: url, release: release)
    }
    func cancelUpdateForTests() { cancelUpdate() }
    func handoffForTests(_ staged: URL) { handoffStagedUpdate(staged) }
    private func handoffForNativeAppTest() -> Bool {
        guard !testUpdateHandoffStarted, CommandLine.arguments.contains("--native-update-parent"),
              let path = ProcessInfo.processInfo.environment["ELYSIA_NATIVE_STAGED_APP"] else { return false }
        testUpdateHandoffStarted = true
        try? String(backend!.processIdentifier).write(toFile: dataDirPath + "/old-backend.pid", atomically: true, encoding: .utf8)
        handoffStagedUpdate(URL(fileURLWithPath: path))
        return true
    }
    static func releaseForTests(_ data: Data) -> ReleaseInfo? { parseRelease(data) }
}
if ProcessInfo.processInfo.environment["ELYSIA_NATIVE_APP_TEST"] == "1" {
    let app = NSApplication.shared
    let delegate = AppDelegate()
    app.delegate = delegate
    app.setActivationPolicy(.accessory)
    app.run()
} else {
    MainActor.assumeIsolated { NativeTests.run() }
}
#else
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()

#endif
