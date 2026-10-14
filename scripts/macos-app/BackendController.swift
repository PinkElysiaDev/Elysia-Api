import Cocoa
import CryptoKit
import WebKit
import os

private let backendRestartLimit = 3
private let backendRestartDelay: TimeInterval = 2
private let backendStopTimeout: TimeInterval = 15
private let fallbackBackendPorts = 8799...8899

// Controller methods share the delegate owner; process and callback lifetimes stay unchanged.
extension AppDelegate {
    // MARK: 后端进程管理

    func setBackendState(_ state: BackendState, error: String? = nil) {
        guard state != backendState || (error != nil && error != lastBackendError) else { return }
        if state != .running { panelReady = false }
        rescheduleHealthTimer(for: state)
        appLogger.info("Backend state: \(state.rawValue, privacy: .public); \(error ?? "", privacy: .public)")
        backendState = state
        if let error { lastBackendError = error }
        refreshStatusUI()
        renderBackendState()
    }

    func renderBackendState() {
        guard window != nil else { return }
        switch backendState {
        case .starting: showOverlay(text: "正在启动本地后端…", spinning: true)
        case .restarting: showOverlay(text: "服务意外停止，正在重启（\(restartCount)/\(backendRestartLimit)）…", spinning: true)
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
                guard let port = fallbackBackendPorts.first(where: { portIsFree($0, host: listenHost) }) else {
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
    func requestBackendExit(_ process: Process) {
        guard backend === process, backendStopDeadline == nil else { return }
        if process.isRunning { process.terminate() }
        let deadline = DispatchWorkItem { [weak self, weak process] in
            guard let self, let process, self.backend === process, process.isRunning else { return }
            appLogger.error("Backend did not finish shutdown within \(backendStopTimeout) seconds; forcing owned pid \(process.processIdentifier) to exit")
            kill(process.processIdentifier, SIGKILL)
        }
        backendStopDeadline = deadline
        DispatchQueue.main.asyncAfter(deadline: .now() + backendStopTimeout, execute: deadline)
    }

    func stopBackend() {
        restartWork?.cancel()
        guard let process = backend else { setBackendState(.stopped); return }
        userStopping = true
        setBackendState(.stopping)
        if process.isRunning { requestBackendExit(process) }
    }

    func backendDidExit(_ process: Process) {
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
        if restartCount < backendRestartLimit {
            restartCount += 1
            setBackendState(.restarting, error: message + "，正在尝试恢复。")
            notifications.requestAndSend(title: "Elysia API 正在恢复", body: "服务意外停止，正在自动重启（\(restartCount)/\(backendRestartLimit)）。", identifier: "backend-restarting")
            let generation = backendGeneration
            let work = DispatchWorkItem { [weak self] in
                guard let self, self.backendGeneration == generation, self.backendState == .restarting, !self.terminating else { return }
                self.startBackend()
            }
            restartWork = work
            DispatchQueue.main.asyncAfter(deadline: .now() + backendRestartDelay, execute: work)
        } else {
            setBackendState(.failed, error: "后端连续异常退出，已停止自动重启。请查看运行日志后重试。")
            notifications.requestAndSend(title: "Elysia API 已停止", body: "\(backendRestartLimit) 次自动重启均失败，请打开面板重试或查看运行日志。", identifier: "backend-stopped")
        }
    }

    /// 服务运行时先排空旧进程再启动；已停止时直接启动。
    @objc func restartBackend() {
        if isPanelProcess { panelBridge?.send(PanelMessage(type: "action", action: "restart")); return }
        guard canRestartBackend else { return }
        guard backend != nil else { retryStartup(); return }
        pendingBackendRestart = true
        stopBackend()
    }

}
