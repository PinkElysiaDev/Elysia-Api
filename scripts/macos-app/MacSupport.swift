import Cocoa
import CryptoKit
import ServiceManagement
import UserNotifications
import os

struct UpdateError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

enum DefaultsKey {
    static let launchAtLogin = "ElysiaApi.launchAtLogin"
    static let notificationsEnabled = "ElysiaApi.notificationsEnabled"
    static let webTheme = "ElysiaApi.webTheme"
}

enum BackendState: String {
    case stopped
    case starting
    case running
    case stopping
    case restarting
    case failed
}

/// 保存窗口位置之外的原生偏好；WebUI 登录态由 WebKit 数据存储管理。
final class WindowStateStore {
    private let defaults: UserDefaults
    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        defaults.removeObject(forKey: "ElysiaApi.lastRoute")
    }
    var webTheme: String? {
        guard let theme = defaults.string(forKey: DefaultsKey.webTheme), ["light", "dark"].contains(theme) else { return nil }
        return theme
    }
    func save(theme: String) {
        guard ["light", "dark"].contains(theme) else { return }
        defaults.set(theme, forKey: DefaultsKey.webTheme)
    }
    func set(_ value: Bool, for key: String) { defaults.set(value, forKey: key) }

    /// 以最大重叠屏幕为目标，完整放回工作区；也处理窗口只有一小条仍在屏内的情况。
    static func fittedFrame(_ frame: NSRect, screens: [NSRect]) -> NSRect {
        guard let fallback = screens.first else { return frame }
        let screen = screens.max {
            let a = $0.intersection(frame), b = $1.intersection(frame)
            return (a.isNull ? 0 : a.width * a.height) < (b.isNull ? 0 : b.width * b.height)
        } ?? fallback
        let target = screens.contains(where: { $0.intersects(frame) }) ? screen : fallback
        let width = min(frame.width, target.width), height = min(frame.height, target.height)
        return NSRect(x: min(max(frame.minX, target.minX), target.maxX - width),
                      y: min(max(frame.minY, target.minY), target.maxY - height), width: width, height: height)
    }
}

/// macOS 13+ 的注册状态以系统为准；12 的固定文件名避免路径变化后留下多个登录项。
final class LaunchAtLoginManager {
    private let bundleID = Bundle.main.bundleIdentifier ?? "dev.pinkelysiadev.ElysiaApi"
    private var label: String { "\(bundleID).login" }
    private var launchAgentURL: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/LaunchAgents/\(label).plist")
    }
    var isEnabled: Bool {
        if #available(macOS 13.0, *) {
            return [.enabled, .requiresApproval].contains(SMAppService.mainApp.status)
        }
        return FileManager.default.fileExists(atPath: launchAgentURL.path)
    }
    var requiresApproval: Bool {
        if #available(macOS 13.0, *) { return SMAppService.mainApp.status == .requiresApproval }
        return false
    }

    static func legacyPayload(bundlePath: String, plistPath: String, label: String) -> [String: Any] {
        // 路径作为参数传递，不拼入 shell 源码。若应用已移走/卸载，下次登录自清理。
        let script = """
        if [ -x "$1/Contents/MacOS/ElysiaApi" ]; then
          exec /usr/bin/open -gj "$1" --args --login
        else
          /bin/rm -f "$2"
          /bin/launchctl remove "$3"
        fi
        """
        return ["Label": label, "ProgramArguments": ["/bin/sh", "-c", script, "elysia-login", bundlePath, plistPath, label],
                "RunAtLoad": true, "KeepAlive": false, "ProcessType": "Interactive"]
    }

    func setEnabled(_ enabled: Bool) throws {
        if #available(macOS 13.0, *) {
            if enabled {
                if !isEnabled { try SMAppService.mainApp.register() }
            } else if SMAppService.mainApp.status != .notRegistered {
                try SMAppService.mainApp.unregister()
            }
            try removeLegacyAgent()
        } else if enabled {
            let payload = Self.legacyPayload(bundlePath: Bundle.main.bundlePath, plistPath: launchAgentURL.path, label: label)
            let data = try PropertyListSerialization.data(fromPropertyList: payload, format: .xml, options: 0)
            // 相同配置无需卸载/重载，避免重复打开应用。
            if (try? Data(contentsOf: launchAgentURL)) != data {
                try removeLegacyAgent()
                try FileManager.default.createDirectory(at: launchAgentURL.deletingLastPathComponent(), withIntermediateDirectories: true)
                try data.write(to: launchAgentURL, options: .atomic)
                do {
                    try runSystemTool("/bin/launchctl", ["bootstrap", "gui/\(getuid())", launchAgentURL.path])
                } catch {
                    try? FileManager.default.removeItem(at: launchAgentURL)
                    throw error
                }
            }
        } else {
            try removeLegacyAgent()
        }
        UserDefaults.standard.set(enabled, forKey: DefaultsKey.launchAtLogin)
    }

    /// 启动时只修复已有的旧版登录项，不擅自重新启用在系统设置中被用户关闭的注册。
    func reconcile() throws {
        guard FileManager.default.fileExists(atPath: launchAgentURL.path) else { return }
        if #available(macOS 13.0, *) {
            if UserDefaults.standard.bool(forKey: DefaultsKey.launchAtLogin) { try setEnabled(true) }
            else { try removeLegacyAgent() }
        } else {
            try setEnabled(UserDefaults.standard.bool(forKey: DefaultsKey.launchAtLogin))
        }
    }

    private func removeLegacyAgent() throws {
        guard FileManager.default.fileExists(atPath: launchAgentURL.path) else { return }
        // bootout 对尚未加载的 job 返回非零；只删除属于本应用固定 label 的文件。
        let service = "gui/\(getuid())/\(label)"
        if (try? runSystemTool("/bin/launchctl", ["print", service])) != nil {
            try runSystemTool("/bin/launchctl", ["bootout", service])
        }
        try FileManager.default.removeItem(at: launchAgentURL)
    }

    func openSystemSettings() {
        if #available(macOS 13.0, *) { SMAppService.openSystemSettingsLoginItems() }
    }
}

/// 同步工具只用于短小本地命令或更新后台队列；参数不经过 shell 插值。
func runSystemTool(_ path: String, _ arguments: [String]) throws {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = arguments
    process.standardOutput = FileHandle.nullDevice
    process.standardError = FileHandle.nullDevice
    try process.run()
    process.waitUntilExit()
    guard process.terminationStatus == 0 else {
        throw UpdateError(message: "\(URL(fileURLWithPath: path).lastPathComponent) 执行失败（\(process.terminationStatus)）")
    }
}

/// 统一 macOS 通知权限、发送与点击回调。默认允许重要状态通知，系统只在
/// 第一次发送前询问权限，用户也可从菜单栏关闭。
final class NotificationManager: NSObject, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()
    var enabled: Bool {
        get { UserDefaults.standard.object(forKey: DefaultsKey.notificationsEnabled) == nil || UserDefaults.standard.bool(forKey: DefaultsKey.notificationsEnabled) }
        set { UserDefaults.standard.set(newValue, forKey: DefaultsKey.notificationsEnabled) }
    }
    var onOpen: (() -> Void)?

    override init() {
        super.init()
        center.delegate = self
    }

    func requestAndSend(title: String, body: String, identifier: String, userInfo: [AnyHashable: Any] = [:]) {
        guard enabled else { return }
        center.getNotificationSettings { [weak self] settings in
            guard let self, self.enabled else { return }
            switch settings.authorizationStatus {
            case .authorized, .provisional:
                self.send(title: title, body: body, identifier: identifier, userInfo: userInfo)
            case .notDetermined:
                self.center.requestAuthorization(options: [.alert, .sound]) { [weak self] granted, _ in
                    guard granted else { return }
                    self?.send(title: title, body: body, identifier: identifier, userInfo: userInfo)
                }
            default:
                break
            }
        }
    }

    func refreshAuthorization(_ completion: @escaping (Bool) -> Void) {
        center.getNotificationSettings { settings in
            DispatchQueue.main.async { completion(settings.authorizationStatus == .denied) }
        }
    }

    /// 用户显式打开开关时申请权限；默认打开时仍等第一条重要通知。
    func requestPermission(_ completion: @escaping (Bool) -> Void) {
        center.requestAuthorization(options: [.alert, .sound]) { granted, _ in
            DispatchQueue.main.async { completion(granted) }
        }
    }

    func openSystemSettings() {
        let path: String
        if #available(macOS 13.0, *) { path = "x-apple.systempreferences:com.apple.Notifications-Settings.extension" }
        else { path = "x-apple.systempreferences:com.apple.preference.notifications" }
        guard let url = URL(string: path) else { return }
        NSWorkspace.shared.open(url)
    }

    private func send(title: String, body: String, identifier: String, userInfo: [AnyHashable: Any]) {
        guard enabled else { return }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        content.userInfo = userInfo
        center.add(UNNotificationRequest(identifier: identifier, content: content, trigger: nil))
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler(enabled ? [.banner, .list, .sound] : [])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        DispatchQueue.main.async { [weak self] in self?.onOpen?() }
        completionHandler()
    }
}

let appLogger = Logger(subsystem: Bundle.main.bundleIdentifier ?? "dev.pinkelysiadev.ElysiaApi", category: "app")

final class UpdateDownloadDelegate: NSObject, URLSessionDownloadDelegate {
    let onProgress: (Int64, Int64) -> Void
    let onFinished: (URL?, Error?) -> Void
    private var downloadedURL: URL?
    private var fileError: Error?

    init(onProgress: @escaping (Int64, Int64) -> Void,
         onFinished: @escaping (URL?, Error?) -> Void) {
        self.onProgress = onProgress
        self.onFinished = onFinished
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask,
                    didWriteData bytesWritten: Int64, totalBytesWritten: Int64,
                    totalBytesExpectedToWrite: Int64) {
        onProgress(totalBytesWritten, totalBytesExpectedToWrite)
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask,
                    didFinishDownloadingTo location: URL) {
        guard (downloadTask.response as? HTTPURLResponse)?.statusCode == 200 else {
            fileError = UpdateError(message: "下载服务器未返回有效文件")
            return
        }
        let target = FileManager.default.temporaryDirectory.appendingPathComponent("elysia-update-\(UUID().uuidString).dmg")
        do {
            try FileManager.default.moveItem(at: location, to: target)
            downloadedURL = target
        } catch {
            fileError = error
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask,
                    didCompleteWithError error: Error?) {
        onFinished(downloadedURL, error ?? fileError)
    }
}


enum UpdatePhase {
    case idle, checking, available, downloading, installing, failed, readyToRelaunch
    var busy: Bool { self == .downloading || self == .installing }
}

func panelOrigin(host: String, port: Int) -> String {
    let local = host == "0.0.0.0" ? "127.0.0.1" : (host == "::" || host == "[::]" ? "::1" : host)
    let address = local.contains(":") && !local.hasPrefix("[") ? "[\(local)]" : local
    return "http://\(address):\(port)"
}

// MARK: - 菜单栏用量脉冲

struct UsagePulsePoint: Equatable {
    let time: Date
    let requests: Int
}

struct UsagePulseSummary: Equatable {
    let points: [UsagePulsePoint]
    let windowRequests: Int
    let windowTokens: Int
}

/// 解析 /api/admin/usage/pulse 响应（{ok, data:{points:[{t: Unix 毫秒, requests}], window:{requests, totalTokens}}}）。
func parseUsagePulse(_ data: Data) -> UsagePulseSummary? {
    struct Envelope: Decodable {
        struct Content: Decodable {
            struct Point: Decodable { let t: Double; let requests: Int }
            struct Window: Decodable { let requests: Int; let totalTokens: Int? }
            let points: [Point]?
            let window: Window?
        }
        let ok: Bool
        let data: Content?
    }
    guard let payload = try? JSONDecoder().decode(Envelope.self, from: data), payload.ok, let content = payload.data else { return nil }
    let points = (content.points ?? []).map { UsagePulsePoint(time: Date(timeIntervalSince1970: $0.t / 1000), requests: $0.requests) }
    let requests = content.window?.requests ?? points.reduce(0) { $0 + $1.requests }
    return UsagePulseSummary(points: points, windowRequests: requests, windowTokens: content.window?.totalTokens ?? 0)
}

/// 构造最近窗口的 pulse 请求地址。时间用 UTC 的 RFC3339（避免 '+' 被 Go query 解析成空格）。
func usagePulseURL(base: String, now: Date = Date(), windowHours: Int = 24, bucketMinutes: Int = 15) -> URL? {
    let formatter = DateFormatter()
    formatter.locale = Locale(identifier: "en_US_POSIX")
    formatter.timeZone = TimeZone(identifier: "UTC")
    formatter.dateFormat = "yyyy-MM-dd'T'HH:mm:ss'Z'"
    var components = URLComponents(string: "\(base)/api/admin/usage/pulse")
    components?.queryItems = [
        URLQueryItem(name: "bucketMinutes", value: String(bucketMinutes)),
        URLQueryItem(name: "from", value: formatter.string(from: now.addingTimeInterval(-Double(windowHours) * 3600))),
        URLQueryItem(name: "to", value: formatter.string(from: now)),
        URLQueryItem(name: "utcOffsetMinutes", value: String(TimeZone.current.secondsFromGMT(for: now) / 60)),
    ]
    return components?.url
}

/// 后端只返回非空时间桶；把稀疏点按时间填进固定数量的槽位（无数据为 0），超出窗口的点丢弃。
func usagePulseSlots(points: [UsagePulsePoint], from: Date, to: Date, slots: Int) -> [Int] {
    guard slots > 0, to > from else { return Array(repeating: 0, count: max(slots, 0)) }
    var filled = Array(repeating: 0, count: slots)
    let interval = to.timeIntervalSince(from) / Double(slots)
    for point in points {
        let index = Int(point.time.timeIntervalSince(from) / interval)
        guard filled.indices.contains(index) else { continue }
        filled[index] += point.requests
    }
    return filled
}

private let requestCountFormatter: NumberFormatter = {
    let formatter = NumberFormatter()
    formatter.numberStyle = .decimal
    return formatter
}()

func formatRequestCount(_ requests: Int) -> String {
    requestCountFormatter.string(from: NSNumber(value: requests)) ?? String(requests)
}

/// token 紧凑格式，与 WebUI 仪表盘一致：866k / 1.1M / 15M。
func formatTokenCount(_ tokens: Int) -> String {
    switch tokens {
    case 10_000_000...: "\(tokens / 1_000_000)M"
    case 1_000_000..<10_000_000: String(format: "%.1fM", Double(tokens) / 1_000_000)
    case 10_000..<1_000_000: "\(tokens / 1_000)k"
    case 1_000..<10_000: String(format: "%.1fk", Double(tokens) / 1_000)
    default: String(tokens)
    }
}

func persistPort(_ port: Int, at url: URL) throws {
    let data = try Data(contentsOf: url)
    guard var object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
        throw UpdateError(message: "配置文件必须是 JSON 对象")
    }
    let permissions = try FileManager.default.attributesOfItem(atPath: url.path)[.posixPermissions] ?? 0o600
    object["port"] = port
    try JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys]).write(to: url, options: .atomic)
    try FileManager.default.setAttributes([.posixPermissions: permissions], ofItemAtPath: url.path)
}

struct UpdateInstaller {
    static func validateArchitectures(at file: URL) throws {
        // CoreFoundation inspects Mach-O files without invoking Xcode's lipo shim.
        let architectures = CFBundleCopyExecutableArchitecturesForURL(file as CFURL) as? [Int] ?? []
        guard architectures.contains(Int(kCFBundleExecutableArchitectureARM64)),
              architectures.contains(Int(kCFBundleExecutableArchitectureX86_64)) else {
            throw UpdateError(message: "更新包中的 \(file.lastPathComponent) 必须同时包含 arm64 和 x86_64 架构")
        }
    }

    static func validateDigest(_ digest: String) throws -> String {
        let expected = (digest.hasPrefix("sha256:") ? String(digest.dropFirst(7)) : digest).lowercased()
        guard expected.count == 64, expected.allSatisfy({ $0.isHexDigit && $0.isASCII }) else {
            throw UpdateError(message: "发布方未提供有效的 DMG sha256 摘要，已取消更新")
        }
        return expected
    }

    static func verify(_ file: URL, digest: String) throws {
        let expected = try validateDigest(digest)
        let handle = try FileHandle(forReadingFrom: file)
        defer { try? handle.close() }
        var hasher = SHA256()
        while let data = try handle.read(upToCount: 1 << 20), !data.isEmpty { hasher.update(data: data) }
        let actual = hasher.finalize().map { String(format: "%02x", $0) }.joined()
        guard expected == actual else { throw UpdateError(message: "sha256 校验不一致，当前版本未更改。") }
    }

    /// 源包预先校验；旧包保留在同卷，所有 rename 都是原子的，失败恢复旧包。
    static func replace(staged: URL, current: URL, move: (URL, URL) throws -> Void = { try FileManager.default.moveItem(at: $0, to: $1) }) throws {
        let manager = FileManager.default
        let backup = current.deletingLastPathComponent().appendingPathComponent(".ElysiaApi-backup-\(UUID().uuidString)")
        try move(current, backup)
        do { try move(staged, current) }
        catch {
            do { try manager.moveItem(at: backup, to: current) }
            catch { throw UpdateError(message: "更新替换与回滚均失败，旧版本保留在 \(backup.path)。") }
            throw UpdateError(message: "无法替换应用包，已恢复旧版本。请检查应用目录的写入权限。")
        }
        try? manager.removeItem(at: backup)
    }

    static func extractApp(fromDMG dmg: URL, beside current: URL) throws -> URL {
        let manager = FileManager.default
        try runSystemTool("/usr/bin/hdiutil", ["verify", dmg.path])
        let mount = manager.temporaryDirectory.appendingPathComponent("elysia-dmg-\(UUID().uuidString)")
        try manager.createDirectory(at: mount, withIntermediateDirectories: false)
        var mounted = false
        defer {
            if mounted { try? runSystemTool("/usr/bin/hdiutil", ["detach", mount.path, "-quiet"]) }
            // 挂载卷不递归删除；detach 失败时留给系统清理。
            if (try? manager.contentsOfDirectory(atPath: mount.path).isEmpty) == true { try? manager.removeItem(at: mount) }
        }
        try runSystemTool("/usr/bin/hdiutil", ["attach", "-nobrowse", "-readonly", "-mountpoint", mount.path, dmg.path])
        mounted = true
        let app = mount.appendingPathComponent("ElysiaApi.app")
        guard let bundle = Bundle(url: app), bundle.bundleIdentifier == Bundle.main.bundleIdentifier else {
            throw UpdateError(message: "DMG 中的应用标识不匹配")
        }
        for executable in ["ElysiaApi", "elysia-api"] {
            let path = app.appendingPathComponent("Contents/MacOS/\(executable)").path
            guard manager.isExecutableFile(atPath: path) else { throw UpdateError(message: "更新包缺少 \(executable)") }
            try validateArchitectures(at: URL(fileURLWithPath: path))
        }
        try runSystemTool("/usr/bin/codesign", ["--verify", "--deep", "--strict", app.path])
        let staging = current.deletingLastPathComponent().appendingPathComponent(".ElysiaApi-update-\(UUID().uuidString)")
        try manager.createDirectory(at: staging, withIntermediateDirectories: false)
        do {
            let target = staging.appendingPathComponent("ElysiaApi.app")
            try manager.copyItem(at: app, to: target)
            try runSystemTool("/usr/bin/codesign", ["--verify", "--deep", "--strict", target.path])
            return target
        } catch {
            try? manager.removeItem(at: staging)
            throw error
        }
    }
}
