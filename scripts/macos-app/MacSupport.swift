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
    private let defaultsOverride: UserDefaults?
    init(defaults: UserDefaults = .standard, overrides: UserDefaults? = nil) {
        self.defaults = defaults
        self.defaultsOverride = overrides
        defaults.removeObject(forKey: "ElysiaApi.lastRoute")
    }
    /// 读写顺序：测试覆盖 > 标准库。
    private var effective: UserDefaults { defaultsOverride ?? defaults }
    func get(_ key: String) -> Bool { effective.bool(forKey: key) }
    func set(_ value: Bool, for key: String) { defaults.set(value, forKey: key) }
    var webTheme: String? {
        guard let theme = defaults.string(forKey: DefaultsKey.webTheme), ["light", "dark"].contains(theme) else { return nil }
        return theme
    }
    func save(theme: String) {
        guard ["light", "dark"].contains(theme) else { return }
        defaults.set(theme, forKey: DefaultsKey.webTheme)
    }

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
func runSystemTool(_ path: String, _ arguments: [String], timeout: TimeInterval = 30) throws {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: path)
    process.arguments = arguments
    process.standardOutput = FileHandle.nullDevice
    process.standardError = FileHandle.nullDevice
    try process.run()
    let deadline = ProcessInfo.processInfo.systemUptime + timeout
    while process.isRunning && ProcessInfo.processInfo.systemUptime < deadline { usleep(100_000) }
    if process.isRunning {
        process.terminate()
        let termDeadline = ProcessInfo.processInfo.systemUptime + 2
        while process.isRunning && ProcessInfo.processInfo.systemUptime < termDeadline { usleep(100_000) }
        if process.isRunning { kill(process.processIdentifier, SIGKILL) }
        let killDeadline = ProcessInfo.processInfo.systemUptime + 2
        while process.isRunning && ProcessInfo.processInfo.systemUptime < killDeadline { usleep(100_000) }
        throw UpdateError(message: "\(URL(fileURLWithPath: path).lastPathComponent) 执行超时")
    }
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
    #if NATIVE_TESTS
    var onMovedForTests: ((URL) -> Void)?
    #endif

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
            #if NATIVE_TESTS
            onMovedForTests?(target)
            #endif
        } catch {
            fileError = error
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask,
                    didCompleteWithError error: Error?) {
        onFinished(downloadedURL, error ?? fileError)
    }

    /// Final bounded-exit cleanup owns only the file this delegate moved out of URLSession.
    func discardDownloadedFile() {
        if let downloadedURL { try? FileManager.default.removeItem(at: downloadedURL) }
        downloadedURL = nil
    }
}


enum UpdatePhase: String {
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
    private static let helperFlag = "--update-helper"
    private static let stagedFlag = "--staged"
    private static let currentFlag = "--current"
    private static let parentPIDFlag = "--parent-pid"
    private static let backgroundFlag = "--background"
    private static let relaunchFlag = "--relaunch"
    private static let ackFlag = "--update-ack"

    static func acknowledgementURL(for staged: URL) -> URL {
        staged.deletingLastPathComponent().appendingPathComponent(".ElysiaApi-update-ack")
    }

    static func helperArguments(staged: URL, current: URL, parentPID: Int32, background: Bool) -> [String] {
        [helperFlag, stagedFlag, staged.path, currentFlag, current.path,
         parentPIDFlag, String(parentPID), background ? backgroundFlag : relaunchFlag,
         ackFlag, acknowledgementURL(for: staged).path]
    }

    /// Copy outside the app bundle: the helper must survive both the old app's
    /// exit and the staged bundle's rename. Paths are argv values, never shell code.
    @discardableResult
    static func startHelper(staged: URL, current: URL, parentPID: Int32, background: Bool) throws -> Process {
        let manager = FileManager.default
        let helper = staged.deletingLastPathComponent().appendingPathComponent(".ElysiaApi-update-helper-\(UUID().uuidString)")
        let source = current.appendingPathComponent("Contents/MacOS/ElysiaApi")
        guard manager.isExecutableFile(atPath: source.path) else {
            throw UpdateError(message: "当前应用缺少可执行的更新辅助程序")
        }
        do {
            try manager.copyItem(at: source, to: helper)
            try manager.setAttributes([.posixPermissions: 0o755], ofItemAtPath: helper.path)
            let process = Process()
            process.executableURL = helper
            process.arguments = helperArguments(staged: staged, current: current, parentPID: parentPID, background: background)
            let output = try helperOutput()
            defer { try? output.close() }
            process.standardOutput = output
            process.standardError = output
            try process.run()
            return process
        } catch {
            try? manager.removeItem(at: helper)
            throw error
        }
    }

    static func acknowledgeLaunch(at url: URL) throws {
        try Data("\(ProcessInfo.processInfo.processIdentifier)\n".utf8).write(to: url, options: .atomic)
    }

    static func acknowledgementURL(arguments: [String]) -> URL? {
        value(arguments, ackFlag).map { URL(fileURLWithPath: $0) }
    }

    /// The backup belongs to this transaction until the new shell acknowledges
    /// backend readiness. Failed rollback leaves recoverable bundles on disk.
    static func runHelper(arguments: [String], parentExitTimeout: TimeInterval = 18,
                          launchTimeout: TimeInterval = 45, childExitTimeout: TimeInterval = 18) throws {
        guard let staged = value(arguments, stagedFlag), staged.hasPrefix("/"),
              let current = value(arguments, currentFlag), current.hasPrefix("/"),
              let parent = Int32(value(arguments, parentPIDFlag) ?? ""), parent > 1,
              let ack = acknowledgementURL(arguments: arguments),
              arguments.contains(backgroundFlag) || arguments.contains(relaunchFlag) else {
            throw UpdateError(message: "更新辅助程序参数不完整")
        }
        let stagedURL = URL(fileURLWithPath: staged).standardizedFileURL
        let currentURL = URL(fileURLWithPath: current).standardizedFileURL
        let staging = stagedURL.deletingLastPathComponent()
        guard staging.deletingLastPathComponent() == currentURL.deletingLastPathComponent(),
              staging.lastPathComponent.hasPrefix(".ElysiaApi-update-"),
              stagedURL.lastPathComponent == "ElysiaApi.app",
              ack.standardizedFileURL == acknowledgementURL(for: stagedURL) else {
            throw UpdateError(message: "更新暂存路径不合法")
        }
        let manager = FileManager.default
        let output = try helperOutput()
        defer { try? output.close() }
        let helperURL = URL(fileURLWithPath: CommandLine.arguments.first ?? "").standardizedFileURL
        defer {
            try? manager.removeItem(at: ack)
            if helperURL.deletingLastPathComponent() == staging && helperURL.lastPathComponent.hasPrefix(".ElysiaApi-update-helper-") {
                try? manager.removeItem(at: helperURL)
            }
        }
        // Remove a stale acknowledgement before launching, and still require the
        // PID inside the acknowledgement to match our exact new child.
        try? manager.removeItem(at: ack)
        do { try waitForProcessExit(parent, timeout: parentExitTimeout) }
        catch {
            // No rename has happened yet; a failed handoff must not strand the
            // staged app and helper after the old shell already relinquished them.
            try? manager.removeItem(at: staging)
            throw error
        }

        var backup: URL?
        var child: Process?
        let mode = arguments.contains(backgroundFlag) ? backgroundFlag : relaunchFlag
        do {
            backup = try replace(staged: stagedURL, current: currentURL)
            let launch = try launchApp(currentURL, arguments: [mode, ackFlag, ack.path], output: output)
            child = launch
            try waitForLaunch(child: launch, ack: ack, timeout: launchTimeout)
            if let backup { try? manager.removeItem(at: backup) }
            try? manager.removeItem(at: staging)
            print("Update committed after readiness acknowledgement from PID \(launch.processIdentifier)")
        } catch {
            let failure = error
            if let child {
                do { try stopChild(child, timeout: childExitTimeout) }
                catch { throw UpdateError(message: "\(error.localizedDescription) 旧版本备份：\(backup?.path ?? currentURL.path)") }
            }
            if let backup {
                // Move the failed new app aside before restoring the old one.
                // Never delete the only current bundle when an earlier rename failed.
                do {
                    try manager.moveItem(at: currentURL, to: stagedURL)
                    do { try manager.moveItem(at: backup, to: currentURL) }
                    catch {
                        try? manager.moveItem(at: stagedURL, to: currentURL)
                        throw error
                    }
                } catch {
                    throw UpdateError(message: "更新失败且无法回滚，旧版本保留在 \(backup.path)：\(error.localizedDescription)")
                }
            }
            do { _ = try launchApp(currentURL, arguments: [mode], output: output) }
            catch {
                let recoveryPath = backup.flatMap { manager.fileExists(atPath: $0.path) ? $0.path : nil } ?? currentURL.path
                throw UpdateError(message: "更新失败，无法重新启动旧版本。应用或可恢复备份位置：\(recoveryPath)。原错误：\(failure.localizedDescription)。启动错误：\(error.localizedDescription)")
            }
            try? manager.removeItem(at: staging)
            throw UpdateError(message: "更新失败，已恢复并启动旧版本：\(failure.localizedDescription)")
        }
    }

    private static func helperOutput() throws -> FileHandle {
        let manager = FileManager.default
        let directory = URL(fileURLWithPath: dataDirPath)
        try manager.createDirectory(at: directory, withIntermediateDirectories: true)
        let log = directory.appendingPathComponent("update.log")
        if !manager.fileExists(atPath: log.path) { manager.createFile(atPath: log.path, contents: nil) }
        try manager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: log.path)
        let handle = try FileHandle(forWritingTo: log)
        do { try handle.seekToEnd(); return handle }
        catch { try? handle.close(); throw error }
    }

    private static func launchApp(_ app: URL, arguments: [String], output: FileHandle) throws -> Process {
        let process = Process()
        process.executableURL = app.appendingPathComponent("Contents/MacOS/ElysiaApi")
        process.arguments = arguments
        process.standardOutput = output
        process.standardError = output
        try process.run()
        return process
    }

    private static func value(_ arguments: [String], _ flag: String) -> String? {
        guard let index = arguments.firstIndex(of: flag), index + 1 < arguments.count else { return nil }
        return arguments[index + 1]
    }

    private static func waitForProcessExit(_ pid: Int32, timeout: TimeInterval) throws {
        let deadline = ProcessInfo.processInfo.systemUptime + timeout
        while kill(pid, 0) == 0 && ProcessInfo.processInfo.systemUptime < deadline { usleep(100_000) }
        guard kill(pid, 0) != 0 else { throw UpdateError(message: "旧版本未能在期限内退出，当前应用未更改。") }
    }

    private static func stopChild(_ child: Process, timeout: TimeInterval) throws {
        guard child.isRunning else { return }
        child.terminate()
        var deadline = ProcessInfo.processInfo.systemUptime + timeout
        while child.isRunning && ProcessInfo.processInfo.systemUptime < deadline { usleep(100_000) }
        if child.isRunning { kill(child.processIdentifier, SIGKILL) }
        deadline = ProcessInfo.processInfo.systemUptime + 2
        while child.isRunning && ProcessInfo.processInfo.systemUptime < deadline { usleep(100_000) }
        guard !child.isRunning else { throw UpdateError(message: "新版进程无法停止，旧版本备份已保留，请查看 update.log。") }
    }

    private static func waitForLaunch(child: Process, ack: URL, timeout: TimeInterval) throws {
        let deadline = ProcessInfo.processInfo.systemUptime + timeout
        while ProcessInfo.processInfo.systemUptime < deadline {
            guard child.isRunning else { throw UpdateError(message: "新版进程提前退出") }
            if let text = try? String(contentsOf: ack, encoding: .utf8),
               Int32(text.trimmingCharacters(in: .whitespacesAndNewlines)) == child.processIdentifier,
               child.isRunning { return }
            usleep(100_000)
        }
        throw UpdateError(message: "新版服务或面板未在期限内就绪")
    }

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

    /// The caller keeps this backup until the new app acknowledges readiness.
    @discardableResult
    static func replace(staged: URL, current: URL, move: (URL, URL) throws -> Void = { try FileManager.default.moveItem(at: $0, to: $1) }) throws -> URL {
        let manager = FileManager.default
        let backup = current.deletingLastPathComponent().appendingPathComponent(".ElysiaApi-backup-\(UUID().uuidString)")
        try move(current, backup)
        do { try move(staged, current) }
        catch {
            do { try manager.moveItem(at: backup, to: current) }
            catch { throw UpdateError(message: "更新替换与回滚均失败，旧版本保留在 \(backup.path)。") }
            throw UpdateError(message: "无法替换应用包，已恢复旧版本。请检查应用目录的写入权限。")
        }
        return backup
    }

    static func extractApp(fromDMG dmg: URL, beside current: URL) throws -> URL {
        let manager = FileManager.default
        try runSystemTool("/usr/bin/hdiutil", ["verify", dmg.path], timeout: 120)
        let mount = manager.temporaryDirectory.appendingPathComponent("elysia-dmg-\(UUID().uuidString)")
        try manager.createDirectory(at: mount, withIntermediateDirectories: false)
        var mounted = false
        var attachAttempted = false
        defer {
            // attach can mount a volume before reporting an error or timing out.
            // Always attempt cleanup after an attach attempt, without recursively
            // deleting anything beneath a possibly still mounted volume.
            if mounted || attachAttempted {
                do { try detach(mount) }
                catch { appLogger.error("Update volume cleanup failed: \(mount.path, privacy: .public); \(error.localizedDescription, privacy: .public)") }
            }
            // 挂载卷不递归删除；detach 失败时留给系统清理。
            if (try? manager.contentsOfDirectory(atPath: mount.path).isEmpty) == true { try? manager.removeItem(at: mount) }
        }
        attachAttempted = true
        try runSystemTool("/usr/bin/hdiutil", ["attach", "-nobrowse", "-readonly", "-mountpoint", mount.path, dmg.path], timeout: 120)
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
            try detach(mount)
            mounted = false
            attachAttempted = false
            return target
        } catch {
            try? manager.removeItem(at: staging)
            throw error
        }
    }

    private static func detach(_ mount: URL) throws {
        do { try runSystemTool("/usr/bin/hdiutil", ["detach", mount.path, "-quiet"], timeout: 10) }
        catch { try runSystemTool("/usr/bin/hdiutil", ["detach", mount.path, "-force", "-quiet"], timeout: 10) }
    }
}
