import Cocoa
import CryptoKit
import WebKit
import os

// Controller methods share the delegate owner; process and callback lifetimes stay unchanged.
extension AppDelegate {
    // MARK: Disposable panel process

    func installSignalHandlers() {
        for number in [SIGTERM, SIGINT] {
            signal(number, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: number, queue: .main)
            source.setEventHandler { [weak self] in self?.requestTermination() }
            source.resume()
            signalSources.append(source)
        }
    }

    func launchPanelProcess() {
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

    func showPanelProcess() {
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

    func stopPanelProcess() {
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

    func sendPanelState() {
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

    func reportPanelReadiness() {
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

}
