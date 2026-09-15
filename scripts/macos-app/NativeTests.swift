import Cocoa
import WebKit

// Executed only by test-macos-app.mjs in a temporary, separately identified .app.
@MainActor
enum NativeTests {
    static var checks = 0
    static func expect(_ value: Bool, _ message: String) throws {
        guard value else { throw UpdateError(message: "FAIL: " + message) }
        checks += 1
        print("PASS: " + message)
    }
    static func rejects(_ message: String, _ action: () throws -> Void) throws {
        do { try action() }
        catch { checks += 1; print("PASS: " + message); return }
        throw UpdateError(message: "FAIL: " + message)
    }
    static func wait(_ timeout: TimeInterval = 8, _ condition: () -> Bool) async -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while !condition(), Date() < deadline {
            // Yield to the real AppKit loop so timers, rendering and autorelease pools all advance.
            try? await Task.sleep(nanoseconds: 50_000_000)
        }
        return condition()
    }
    static func webMatches(_ delegate: AppDelegate, _ script: String) async -> Bool {
        let deadline = Date().addingTimeInterval(15)
        while Date() < deadline {
            if (try? await delegate.webView.evaluateJavaScript(script)) as? Bool == true { return true }
            try? await Task.sleep(nanoseconds: 50_000_000)
        }
        return false
    }
    static func clearWebData() async {
        // The test runner gives each app a unique bundle ID, isolating its persistent store.
        await withCheckedContinuation { continuation in
            WKWebsiteDataStore.default().removeData(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(), modifiedSince: .distantPast) {
                continuation.resume()
            }
        }
    }
    static func run() {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        Task { @MainActor in
            await runSuite()
            // No production delegate is installed: stopping the test loop cannot launch or quit the user's app.
            app.stop(nil)
            let wake = NSEvent.otherEvent(with: .applicationDefined, location: .zero, modifierFlags: [], timestamp: 0,
                                          windowNumber: 0, context: nil, subtype: 0, data1: 0, data2: 0)!
            app.postEvent(wake, atStart: true)
        }
        app.run()
    }

    private static func runSuite() async {
        let delegate = AppDelegate()
        let domain = Bundle.main.bundleIdentifier!
        defer { UserDefaults.standard.removePersistentDomain(forName: domain) }
        do {
            if CommandLine.arguments.contains("--panel") {
                delegate.prepareForTests()
                try await realPanel(delegate)
            } else {
                try support()
                delegate.prepareForTests()
                try await lifecycle(delegate)
            }
            if delegate.backend != nil { delegate.stopBackend(); _ = await wait { delegate.backend == nil } }
            delegate.window?.close()
            delegate.statusItem?.isVisible = false
            await clearWebData()
            UserDefaults.standard.removePersistentDomain(forName: domain)
            print("Native tests passed: \(checks) checks")
        } catch {
            delegate.restartWork?.cancel()
            if let backend = delegate.backend { delegate.stopBackend(); _ = await wait(12) { !backend.isRunning } }
            delegate.window?.close()
            await clearWebData()
            UserDefaults.standard.removePersistentDomain(forName: domain)
            fputs("\(error.localizedDescription)\n", stderr)
            exit(1)
        }
    }

    static func support() throws {
        let fm = FileManager.default
        let root = URL(fileURLWithPath: dataDirPath)
        let defaults = UserDefaults.standard
        defaults.set("#/logs", forKey: "ElysiaApi.lastRoute")
        let store = WindowStateStore(defaults: defaults)
        try expect(store.webTheme == nil && defaults.object(forKey: "ElysiaApi.lastRoute") == nil, "legacy route is removed and theme defaults are empty")
        store.save(theme: "dark"); store.save(theme: "invalid")
        try expect(store.webTheme == "dark", "invalid themes do not overwrite the saved theme")
        let screen = NSRect(x: 0, y: 25, width: 1280, height: 775)
        let lost = NSRect(x: -4000, y: 100, width: 1440, height: 900)
        try expect(WindowStateStore.fittedFrame(lost, screens: [screen]) == screen, "unplugged display and oversized frame fit visible screen")
        let second = NSRect(x: 1280, y: -200, width: 1920, height: 1080)
        let frame = NSRect(x: 1800, y: 0, width: 950, height: 650)
        try expect(WindowStateStore.fittedFrame(frame, screens: [screen, second]) == frame, "visible secondary-display frame remains in place")
        let clipped = NSRect(x: 1200, y: -100, width: 1000, height: 700)
        try expect(screen.contains(WindowStateStore.fittedFrame(clipped, screens: [screen])), "partially offscreen frame is fully recovered")
        try expect(panelOrigin(host: "0.0.0.0", port: 8765) == "http://127.0.0.1:8765", "wildcard IPv4 has usable panel URL")
        try expect(panelOrigin(host: "::", port: 8765) == "http://[::1]:8765", "wildcard IPv6 has usable panel URL")
        let payload = LaunchAtLoginManager.legacyPayload(bundlePath: "/tmp/A '$()` app.app", plistPath: "/tmp/login.plist", label: "test.login")
        let args = payload["ProgramArguments"] as! [String]
        try expect(args[4] == "/tmp/A '$()` app.app" && !args[2].contains(args[4]), "LaunchAgent passes paths as literal positional arguments")
        try expect((payload["KeepAlive"] as? Bool) == false && args[2].contains("--login") && args[2].contains("/bin/rm"), "legacy login stays in background and cleans missing app registration")
        let repeated = LaunchAtLoginManager.legacyPayload(bundlePath: args[4], plistPath: args[5], label: args[6])
        try expect(NSDictionary(dictionary: payload).isEqual(to: repeated), "identical login configuration has stable payload")
        try architectureValidation(root: root)
        let file = root.appendingPathComponent("checksum.dmg")
        try Data("abc".utf8).write(to: file)
        let digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
        try UpdateInstaller.verify(file, digest: "sha256:" + digest)
        try expect(true, "streamed SHA256 accepts known digest")
        try rejects("missing SHA256 is rejected") { _ = try UpdateInstaller.validateDigest("") }
        try rejects("wrong SHA256 leaves artifact untouched") { try UpdateInstaller.verify(file, digest: String(repeating: "0", count: 64)) }
        try rejects("invalid DMG is rejected before extraction") { _ = try UpdateInstaller.extractApp(fromDMG: file, beside: root.appendingPathComponent("current.app")) }
        let current = root.appendingPathComponent("current.app"), staged = root.appendingPathComponent("staged.app")
        try fm.createDirectory(at: current, withIntermediateDirectories: false)
        try fm.createDirectory(at: staged, withIntermediateDirectories: false)
        try Data("old".utf8).write(to: current.appendingPathComponent("version"))
        try Data("new".utf8).write(to: staged.appendingPathComponent("version"))
        try rejects("replacement failure rolls back old bundle") {
            try UpdateInstaller.replace(staged: staged, current: current) { from, to in
                if from == staged { throw UpdateError(message: "injected write failure") }
                try fm.moveItem(at: from, to: to)
            }
        }
        let restored = try String(contentsOf: current.appendingPathComponent("version"))
        try expect(restored == "old" && fm.fileExists(atPath: staged.path), "rollback preserves old app and staged update")
        try UpdateInstaller.replace(staged: staged, current: current)
        let updated = try String(contentsOf: current.appendingPathComponent("version"))
        try expect(updated == "new" && !fm.fileExists(atPath: staged.path), "successful replacement installs staged bundle")
        try expect(isNewer("v1.2.0", than: "v1.1.9") && !isNewer("v1.2", than: "v1.2.0"), "version comparisons handle missing patch component")
        try expect(AppDelegate.releaseForTests(Data("{\"tag_name\":\"v2\",\"assets\":[]}".utf8)) == nil, "release with missing DMG is rejected")
        let fresh = loadOrCreateConfig()
        try expect(fresh?.port == 8765 && fresh?.panelAccessToken.hasPrefix("elysia-app-") == true, "missing configuration creates secure first-run defaults")
        let configURL = URL(fileURLWithPath: configPath)
        let malformed = Data("{invalid json".utf8)
        try malformed.write(to: configURL)
        let loaded = loadOrCreateConfig(), bytes = try Data(contentsOf: configURL)
        try expect(loaded == nil && bytes == malformed, "malformed configuration is reported and never overwritten")
        let contents: [String: Any] = ["host": "127.0.0.1", "port": 8765, "panelAccessToken": "fixture-token", "custom": ["keep": true]]
        try JSONSerialization.data(withJSONObject: contents).write(to: configURL)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: configPath)
        try persistPort(8799, at: configURL)
        let saved = try JSONSerialization.jsonObject(with: Data(contentsOf: configURL)) as! [String: Any]
        let permissions = try fm.attributesOfItem(atPath: configPath)[.posixPermissions] as? Int
        try expect((saved["custom"] as? [String: Bool])?["keep"] == true && permissions == 0o600, "port changes retain unknown configuration and private permissions")
        let pulseSlots = usagePulseSlots(
            points: [UsagePulsePoint(time: Date(timeIntervalSince1970: 1_700_000_100), requests: 4),
                     UsagePulsePoint(time: Date(timeIntervalSince1970: 1_700_001_000), requests: 2),
                     UsagePulsePoint(time: Date(timeIntervalSince1970: 1_700_009_000), requests: 9)],
            from: Date(timeIntervalSince1970: 1_700_000_000), to: Date(timeIntervalSince1970: 1_700_003_600), slots: 96)
        try expect(pulseSlots.count == 96 && pulseSlots[2] == 4 && pulseSlots[26] == 2 && pulseSlots.filter { $0 > 0 }.count == 2,
                   "sparse pulse points fill their time slots and drop points outside the window")
        let pulsePayload = Data(#"{"ok":true,"data":{"points":[{"t":1700000100000,"requests":4,"avgDurationMs":90},{"t":1700001000000,"requests":2}],"window":{"requests":6,"totalTokens":866000}}}"#.utf8)
        let pulse = parseUsagePulse(pulsePayload)
        try expect(pulse?.windowRequests == 6 && pulse?.windowTokens == 866_000 && pulse?.points == [
            UsagePulsePoint(time: Date(timeIntervalSince1970: 1_700_000_100), requests: 4),
            UsagePulsePoint(time: Date(timeIntervalSince1970: 1_700_001_000), requests: 2),
        ], "usage pulse payload parses millisecond buckets and window summary")
        try expect(formatTokenCount(866_000) == "866k" && formatTokenCount(9_800) == "9.8k"
                   && formatTokenCount(1_100_000) == "1.1M" && formatTokenCount(15_300_000) == "15M" && formatTokenCount(500) == "500",
                   "token counts format compactly like the dashboard")
        try expect(parseUsagePulse(Data("not json".utf8)) == nil && parseUsagePulse(Data(#"{"ok":false}"#.utf8)) == nil,
                   "failed or malformed pulse envelopes yield no summary")
        let pulseURL = usagePulseURL(base: "http://127.0.0.1:8765", now: Date(timeIntervalSince1970: 1_700_000_000))
        try expect(pulseURL?.path == "/api/admin/usage/pulse" && pulseURL?.query?.contains("bucketMinutes=15") == true
                   && pulseURL?.query?.contains("from=") == true && pulseURL?.query?.contains("+") != true,
                   "usage pulse URL targets the admin endpoint without ambiguous query encoding")
        try expect(formatRequestCount(12_345).contains("12") && formatRequestCount(0) == "0", "request counts format with grouping")
    }

    static func architectureValidation(root: URL) throws {
        let previous = ProcessInfo.processInfo.environment["DEVELOPER_DIR"]
        setenv("DEVELOPER_DIR", root.appendingPathComponent("no-developer-tools").path, 1)
        defer {
            if let previous { setenv("DEVELOPER_DIR", previous, 1) }
            else { unsetenv("DEVELOPER_DIR") }
        }
        try UpdateInstaller.validateArchitectures(at: root.appendingPathComponent("fixture-universal"))
        try expect(true, "universal update validates without a developer toolchain")
        for name in ["fixture-arm64", "fixture-x86_64"] {
            try rejects("single-architecture update is rejected: " + name) {
                try UpdateInstaller.validateArchitectures(at: root.appendingPathComponent(name))
            }
        }
        let invalid = root.appendingPathComponent("invalid-executable")
        try Data("not Mach-O".utf8).write(to: invalid)
        try rejects("non-Mach-O update is rejected") { try UpdateInstaller.validateArchitectures(at: invalid) }
        try rejects("missing update executable is rejected") {
            try UpdateInstaller.validateArchitectures(at: root.appendingPathComponent("missing"))
        }
    }

    static func lifecycle(_ delegate: AppDelegate) async throws {
        let root = URL(fileURLWithPath: dataDirPath)
        // Occupy a free local port to exercise fallback without disturbing any existing service.
        let port = (31000...32000).first { portIsFree($0) }!
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        defer { close(fd) }
        var addr = sockaddr_in()
        addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = UInt16(port).bigEndian
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")
        let bound = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
        try expect(bound == 0 && !portIsFree(port), "port probe detects occupied port")
        try persistPort(port, at: URL(fileURLWithPath: configPath))
        delegate.startBackend()
        try expect(await wait { delegate.pollForTests(); return delegate.backendState == .running }, "backend becomes healthy without a WebView")
        try expect(delegate.backendPort != port && delegate.window == nil, "port fallback and menu-only startup")
        let copyToken = delegate.statusItem.menu?.item(withTitle: "复制面板访问令牌")
        try expect(copyToken?.action == NSSelectorFromString("copyPanelToken") && copyToken?.target === delegate && delegate.validateMenuItem(copyToken!), "menu bar exposes the enabled panel token copy action")
        try await downloadTests(port: delegate.backendPort)
        UserDefaults.standard.set("#/logs", forKey: "ElysiaApi.lastRoute")
        delegate.windowState.save(theme: "dark")
        weak var releasedWebView: WKWebView?
        var frame = NSRect.zero
        let process = delegate.backend
        delegate.showMainWindow()
        try expect(await wait { delegate.pollForTests(); return delegate.webView != nil && delegate.panelLoaded && !delegate.webView.isLoading && delegate.overlay.isHidden }, "native window loads panel after health succeeds")
        try expect(delegate.webView.url?.fragment == "/overview", "native window opens overview even with a legacy saved route")
        let jsState = try await delegate.webView.evaluateJavaScript("localStorage.getItem('elysia-webui.theme') === 'dark' && localStorage.getItem('elysia-webui.panel-token') === null && !document.cookie.includes('panel_access_token=')") as? Bool
        try expect(jsState == true, "theme is restored without injecting a token or authentication cookie")
        try expect(delegate.webView.configuration.websiteDataStore.isPersistent, "WebKit persists the user's manual login")
        delegate.updatePhase = .downloading; delegate.updateMessage = "正在下载测试更新 · 50%"; delegate.updateFraction = 0.5
        delegate.updateUIForTests()
        delegate.window.contentView?.layoutSubtreeIfNeeded()
        try expect(!delegate.updateCancelButton.isHidden && delegate.updateProgress.doubleValue == 50, "download exposes progress and cancellation")
        try expect(delegate.webView.frame.maxY == delegate.window.contentView!.bounds.maxY && delegate.webView.frame.minY >= 27, "panel is full-bleed to the window top and reserves update bar space")
        delegate.updatePhase = .checking; delegate.checkingUpdates = true; delegate.updateUIForTests()
        try expect(delegate.updateButton.isHidden && !delegate.validateMenuItem(delegate.updateCheckItem), "checking never enables installation or concurrent checks")
        delegate.checkingUpdates = false; delegate.updatePhase = .failed; delegate.updateUIForTests()
        try expect(delegate.updateButton.title == "重试更新" && !delegate.updateButton.isHidden, "update failures expose retry")
        delegate.updatePhase = .idle; delegate.updateUIForTests()
        _ = try await delegate.webView.evaluateJavaScript("location.hash = '#/protocols'")
        try expect(await wait { delegate.webView.url?.fragment == "/protocols" }, "panel can navigate away from overview")
        delegate.showMainWindow()
        try expect(delegate.webView.url?.fragment == "/protocols", "showing an existing window keeps the current page")
        frame = delegate.window.frame
        releasedWebView = delegate.webView
        delegate.window.close()
        try expect(await wait { releasedWebView == nil }, "closing window releases WebView and script handlers")
        try expect(delegate.window == nil && delegate.backend === process && process?.isRunning == true, "window close keeps backend running")
        _ = delegate.applicationShouldHandleReopen(NSApp, hasVisibleWindows: false)
        try expect(delegate.window.frame == frame, "Dock reopen restores saved frame")
        try expect(await wait { delegate.pollForTests(); return delegate.panelLoaded && !delegate.webView.isLoading }, "reopened window reloads panel")
        try expect(delegate.webView.url?.fragment == "/overview", "reopening a closed window starts at overview")
        delegate.window.miniaturize(nil); delegate.showMainWindow()
        try expect(!delegate.window.isMiniaturized, "show window restores a minimized window")
        delegate.window.close()
        let mode = root.appendingPathComponent("fixture-mode")
        try Data("unhealthy".utf8).write(to: mode)
        try expect(await wait { delegate.pollForTests(); return delegate.backendState == .failed }, "health failure updates menu state while window is closed")
        delegate.ageHealthFailureForTests()
        let initialPID = delegate.backend!.processIdentifier
        try expect(await wait { delegate.pollForTests(); return delegate.backendState == .restarting }, "sustained health failure initiates recovery")
        try FileManager.default.removeItem(at: mode)
        try expect(await wait(12) { delegate.pollForTests(); return delegate.backendState == .running && delegate.backend?.processIdentifier != initialPID }, "health recovery replaces only the owned child process")
        delegate.stopBackend()
        try expect(delegate.backendState == .stopping && !delegate.validateMenuItem(delegate.toggleItem), "stop disables conflicting service actions")
        try expect(await wait { delegate.backendState == .stopped && delegate.backend == nil }, "normal stop completes without automatic restart")
        delegate.showMainWindow()
        try expect(delegate.backend == nil && delegate.overlayButton.title == "启动服务", "opening stopped service preserves manual stop intent")
        delegate.window.close()
        try Data("crash".utf8).write(to: mode)
        delegate.retryForTests()
        try expect(await wait(16) { delegate.backendState == .failed && delegate.restartCount == 3 && delegate.backend == nil }, "three failed automatic restarts stop at failure state")
        try expect(delegate.validateMenuItem(delegate.toggleItem), "failed service allows manual retry")
        try FileManager.default.removeItem(at: mode)
        delegate.retryForTests()
        try expect(await wait { delegate.pollForTests(); return delegate.backendState == .running }, "manual retry recovers after repeated failures")
        try expect(delegate.restartCount == 0, "manual retry resets restart budget")
    }

    /// Use the actual universal Go binary + embedded React UI against a temporary data directory.
    static func realPanel(_ delegate: AppDelegate) async throws {
        delegate.windowState.save(theme: "dark")
        UserDefaults.standard.set("#/runtime", forKey: "ElysiaApi.lastRoute")
        delegate.startBackend()
        delegate.showMainWindow()
        try expect(await wait(30) { delegate.pollForTests(); return delegate.backendState == .running && delegate.panelLoaded && !delegate.webView.isLoading }, "bundled universal backend serves the real WebUI")
        try await pulseEndpointTest(port: delegate.backendPort, token: delegate.config.panelAccessToken)
        let loginReady = "location.hash === '#/login' && document.querySelector('#token')?.value === '' && localStorage.getItem('elysia-webui.panel-token') === null && !document.cookie.includes('panel_access_token=')"
        try expect(await webMatches(delegate, loginReady), "first launch shows the real login page with no prefilled token or cookie")
        try expect(await webMatches(delegate, "document.documentElement.classList.contains('dark')"), "login page retains the native theme")
        // Exercise the real login form; credentials are never written directly to browser storage.
        _ = try await delegate.webView.evaluateJavaScript("""
        (() => {
          const input = document.querySelector('#token');
          Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, \(javascriptString(delegate.config.panelAccessToken)));
          input.dispatchEvent(new Event('input', { bubbles: true }));
        })()
        """)
        _ = try await delegate.webView.evaluateJavaScript("document.querySelector('#token').form.requestSubmit()")
        let overviewReady = "!!document.querySelector('main h1') && document.documentElement.classList.contains('dark') && location.hash === '#/overview'"
        try expect(await webMatches(delegate, overviewReady), "manual login reaches overview")
        try expect(await webMatches(delegate, "localStorage.getItem('elysia-webui.panel-token') === \(javascriptString(delegate.config.panelAccessToken)) && document.cookie.includes('panel_access_token=')"), "manual login saves the token and authentication cookie")
        delegate.window.close()
        delegate.showMainWindow()
        try expect(await wait { delegate.pollForTests(); return delegate.panelLoaded && !delegate.webView.isLoading }, "reopened window loads the real panel")
        try expect(await webMatches(delegate, overviewReady), "reopening the window keeps the manual login")
        delegate.latestRelease = ReleaseInfo(tag: "v99.0.0", dmgURL: "https://example.com/unused.dmg", dmgDigest: "")
        delegate.updatePhase = .available
        delegate.updateMessage = "可更新到 v99.0.0 · 原生更新条预览"
        delegate.updateUIForTests()
        delegate.window.contentView?.layoutSubtreeIfNeeded()
        // Locked/headless Macs pause the document timeline. Finish finite entrance animations
        // only in this isolated test page to capture its final layout, not animation behavior.
        _ = try await delegate.webView.evaluateJavaScript("document.getAnimations().filter(a => Number.isFinite(a.effect.getComputedTiming().endTime)).forEach(a => a.finish())")
        // WKWebView snapshot includes composited web content; the AppKit bitmap adds native controls.
        var snapshot: NSImage?, finished = false
        delegate.webView.takeSnapshot(with: nil) { result, _ in snapshot = result; finished = true }
        try expect(await wait { finished } && snapshot != nil, "real WebKit panel snapshot renders")
        if let content = delegate.window.contentView, let bitmap = content.bitmapImageRepForCachingDisplay(in: content.bounds),
           let path = ProcessInfo.processInfo.environment["ELYSIA_NATIVE_SCREENSHOT"] {
            content.cacheDisplay(in: content.bounds, to: bitmap)
            let output = NSImage(size: content.bounds.size)
            output.lockFocus()
            bitmap.draw(in: content.bounds)
            snapshot?.draw(in: delegate.webView.frame)
            output.unlockFocus()
            if let tiff = output.tiffRepresentation, let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) {
                try png.write(to: URL(fileURLWithPath: path))
            }
        }
        let configBefore = try Data(contentsOf: URL(fileURLWithPath: configPath))
        let root = URL(fileURLWithPath: dataDirPath)
        let keyBefore = try Data(contentsOf: root.appendingPathComponent(".master-key"))
        let oldPort = delegate.backendPort
        delegate.stopBackend()
        try expect(await wait(12) { delegate.backend == nil }, "real backend exits through graceful shutdown")
        delegate.startBackend()
        try expect(await wait(15) { delegate.pollForTests(); return delegate.backendState == .running }, "real backend restarts with existing data")
        let configAfter = try Data(contentsOf: URL(fileURLWithPath: configPath))
        let keyAfter = try Data(contentsOf: root.appendingPathComponent(".master-key"))
        try expect(delegate.backendPort == oldPort, "recently closed sockets do not cause spurious port changes")
        try expect(configBefore == configAfter, "real backend restart preserves configuration")
        try expect(keyBefore == keyAfter, "real backend restart preserves master key")
        try expect(FileManager.default.fileExists(atPath: root.appendingPathComponent("elysia-api.sqlite3").path), "real backend retains SQLite database")
        _ = try await delegate.webView.evaluateJavaScript("document.querySelector('button[aria-label=\"退出登录\"]').click()")
        try expect(await webMatches(delegate, "!!document.querySelector('[role=dialog]')"), "logout opens its confirmation dialog")
        _ = try await delegate.webView.evaluateJavaScript("Array.from(document.querySelectorAll('[role=dialog] button')).find(button => button.textContent.trim() === '退出').click()")
        try expect(await webMatches(delegate, loginReady), "logout clears the saved token and cookie")
        delegate.window.close()
        delegate.showMainWindow()
        try expect(await wait { delegate.pollForTests(); return delegate.panelLoaded && !delegate.webView.isLoading }, "window reopens after logout")
        try expect(await webMatches(delegate, loginReady), "reopening after logout does not silently sign back in")
    }

    /// 命中真实后端的用量接口：验证菜单栏脉冲图依赖的管理员鉴权与响应信封都能被解析。
    /// 仅在 `--panel` 模式调用——替身后端不实现 admin API。
    static func pulseEndpointTest(port: Int, token: String) async throws {
        guard let url = usagePulseURL(base: panelOrigin(host: "127.0.0.1", port: port)) else {
            throw UpdateError(message: "usage pulse URL could not be built")
        }
        var request = URLRequest(url: url)
        request.timeoutInterval = 5
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let session = URLSession(configuration: .ephemeral)
        defer { session.finishTasksAndInvalidate() }
        var summary: UsagePulseSummary?
        var done = false
        session.dataTask(with: request) { data, response, _ in
            if (response as? HTTPURLResponse)?.statusCode == 200, let data { summary = parseUsagePulse(data) }
            done = true
        }.resume()
        try expect(await wait { done } && summary != nil, "real backend serves a parsable usage pulse for the menu bar")
    }

    static func downloadTests(port: Int) async throws {
        for path in ["/download", "/missing", "/slow"] {
            var done = false, local: URL?, error: Error?
            var task: URLSessionDownloadTask!
            let delegate = UpdateDownloadDelegate(onProgress: { _, _ in if path == "/slow" { task.cancel() } }, onFinished: { url, failure in local = url; error = failure; done = true })
            let session = URLSession(configuration: .ephemeral, delegate: delegate, delegateQueue: .main)
            defer { session.invalidateAndCancel(); if let local { try? FileManager.default.removeItem(at: local) } }
            task = session.downloadTask(with: URL(string: "http://127.0.0.1:\(port)\(path)")!)
            task.resume()
            try expect(await wait { done }, "download callback finishes for \(path)")
            if path == "/download" {
                let data = try local.map { try Data(contentsOf: $0) }
                try expect(error == nil && data == Data("abc".utf8), "download file survives URLSession temporary-file cleanup")
            } else if path == "/missing" {
                try expect(error != nil && local == nil, "HTTP 404 never becomes an update artifact")
            } else {
                try expect((error as NSError?)?.code == NSURLErrorCancelled, "download cancellation reports a recoverable cancellation")
            }
        }
    }
}
