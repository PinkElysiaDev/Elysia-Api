import Cocoa
import CryptoKit
import WebKit
import os

// Controller methods share the delegate owner; process and callback lifetimes stay unchanged.
extension AppDelegate {
    // MARK: 更新

    @objc func checkForUpdatesFromMenu() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "checkUpdate")); return }
        checkForUpdates(silent: false)
    }

    func checkForUpdates(silent: Bool) {
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

    @objc func runUpdate() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "update")); return }
        if updatePhase == .readyToRelaunch { relaunchAfterUpdate(); return }
        guard let release = latestRelease, !checkingUpdates, !updatePhase.busy, !terminating else { return }
        do { _ = try UpdateInstaller.validateDigest(release.dmgDigest) }
        catch { updateFailed(error.localizedDescription); return }
        guard let remote = URL(string: release.dmgURL), remote.scheme == "https" else { return }
        beginUpdateDownload(from: remote, release: release)
    }

    func beginUpdateDownload(from remote: URL, release: ReleaseInfo) {
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

    @objc func cancelUpdate() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "cancelUpdate")); return }
        guard updatePhase == .downloading else { return }
        cancellingUpdate = true
        updateMessage = "正在取消下载…"
        refreshUpdateUI()
        updateTask?.cancel()
    }

    func relaunchAfterUpdate() {
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

    func acknowledgeUpdateLaunch() {
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

    func handoffStagedUpdate(_ staged: URL) {
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

}
