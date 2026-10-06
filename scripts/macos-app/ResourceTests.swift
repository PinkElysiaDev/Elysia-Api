import Cocoa
import WebKit

/// Exercise window-owned downloads through real WKDownload delegates and native save sheets.
@MainActor
enum ResourceTests {
    static func run(_ delegate: AppDelegate) async throws {
        for cycle in 1...3 {
            try await openPanel(delegate)
            weak var oldWindow = delegate.window
            weak var oldWebView = delegate.webView
            weak var oldCapsule = delegate.updateCapsule
            delegate.window.close()
            try NativeTests.expect(await NativeTests.wait { oldWindow == nil && oldWebView == nil && oldCapsule == nil },
                                   "window, WebView and update capsule release after close cycle \(cycle)")
        }

        try await openPanel(delegate)
        var staleWebView: WKWebView? = delegate.webView
        delegate.window.close()
        try await openPanel(delegate)
        let staleError = NSError(domain: NSURLErrorDomain, code: NSURLErrorCannotConnectToHost)
        delegate.webView(staleWebView!, didFail: nil, withError: staleError)
        delegate.webView(staleWebView!, didFailProvisionalNavigation: nil, withError: staleError)
        delegate.webViewWebContentProcessDidTerminate(staleWebView!)
        try NativeTests.expect(delegate.panelLoaded && delegate.overlay.isHidden,
                               "late errors from a closed WebView cannot change the reopened panel")
        staleWebView = nil
        delegate.window.close()

        try await openPanel(delegate)
        var replies = delegate.panelDestinationRepliesForTests
        try await export(delegate, path: "/ui/export-slow")
        weak var savePanel = delegate.pendingSavePanelForTests
        weak var closedWebView = delegate.webView
        delegate.window.close()
        try NativeTests.expect(await NativeTests.wait {
            savePanel == nil && closedWebView == nil && delegate.panelDownloadCountForTests == 0
        }, "closing a pending save sheet terminates and releases the real WebKit download")
        try NativeTests.expect(delegate.panelDestinationRepliesForTests == replies + 1,
                               "closing a pending save sheet resolves its destination exactly once")
        try NativeTests.expect(delegate.panelDownloadMessageForTests.contains("导出已取消"),
                               "closing the window records an explicit export cancellation")

        try await openPanel(delegate)
        replies = delegate.panelDestinationRepliesForTests
        try await export(delegate, path: "/ui/export-slow")
        let partial = URL(fileURLWithPath: dataDirPath).appendingPathComponent("partial-export.txt")
        defer { try? FileManager.default.removeItem(at: partial) }
        try Data("original".utf8).write(to: partial)
        delegate.resolvePanelDestinationForTests(partial)
        delegate.resolvePanelDestinationForTests(partial)
        let temporary = delegate.panelTemporaryDestinationForTests!
        try NativeTests.expect(delegate.panelDestinationRepliesForTests == replies + 1,
                               "save sheet replies once even if completion is requested twice")
        try NativeTests.expect(await NativeTests.wait {
            let size = (try? FileManager.default.attributesOfItem(atPath: temporary.path)[.size] as? Int) ?? 0
            return size > 0 && delegate.panelDownloadCountForTests == 1
        }, "real slow WKDownload writes data after a native destination is selected")
        closedWebView = delegate.webView
        delegate.window.close()
        try NativeTests.expect(await NativeTests.wait { closedWebView == nil && delegate.panelDownloadCountForTests == 0 },
                               "closing an active export cancels it and releases its WebView")
        try NativeTests.expect(!FileManager.default.fileExists(atPath: temporary.path)
                               && (try Data(contentsOf: partial)) == Data("original".utf8),
                               "cancelled export removes only its temporary file and preserves an existing destination")

        try await openPanel(delegate)
        try await export(delegate, path: "/ui/export")
        let completed = URL(fileURLWithPath: dataDirPath).appendingPathComponent("completed-export.txt")
        defer { try? FileManager.default.removeItem(at: completed) }
        try Data("previous".utf8).write(to: completed)
        delegate.resolvePanelDestinationForTests(completed)
        try NativeTests.expect(await NativeTests.wait { delegate.panelDownloadCountForTests == 0
            && delegate.panelDownloadMessageForTests.contains("导出已完成") },
                               "completed real WKDownload clears ownership and reports its saved path")
        try NativeTests.expect(try Data(contentsOf: completed) == Data("abc".utf8), "completed export preserves downloaded bytes")

        try await export(delegate, path: "/ui/export")
        let collision = URL(fileURLWithPath: dataDirPath).appendingPathComponent("collision-export.txt")
        defer { try? FileManager.default.removeItem(at: collision) }
        delegate.resolvePanelDestinationForTests(collision)
        let collisionTemporary = delegate.panelTemporaryDestinationForTests!
        try Data("new owner".utf8).write(to: collision)
        try NativeTests.expect(await NativeTests.wait { delegate.panelDownloadCountForTests == 0
            && delegate.panelDownloadMessageForTests.contains("发生变化") },
                               "a real export rejects a target created after the save destination was chosen")
        try NativeTests.expect((try Data(contentsOf: collision)) == Data("new owner".utf8)
                               && !FileManager.default.fileExists(atPath: collisionTemporary.path),
                               "a conflicting export preserves the new target and removes only its own temporary file")
        delegate.window.close()

        try await terminationDownloads(servedBy: delegate)
    }

    private static func openPanel(_ delegate: AppDelegate) async throws {
        delegate.showMainWindow()
        try NativeTests.expect(await NativeTests.wait {
            delegate.pollForTests()
            return delegate.webView != nil && delegate.panelLoaded && !delegate.webView.isLoading
        }, "resource test panel loads through the managed backend")
    }

    private static func export(_ delegate: AppDelegate, path: String) async throws {
        _ = try await delegate.webView.evaluateJavaScript("""
        (() => {
          const link = document.createElement('a');
          link.href = \(javascriptString(path));
          link.download = 'native-export.txt';
          document.body.appendChild(link);
          link.click();
          link.remove();
        })()
        """)
        try NativeTests.expect(await NativeTests.wait { delegate.pendingSavePanelForTests != nil },
                               "real WKDownload opens the native save sheet for \(path)")
    }

    private static func terminationDownloads(servedBy owner: AppDelegate) async throws {
        // A separate shell owns these downloads but no child process. The existing
        // fixture serves real HTTP/WebKit traffic, exercising the immediate-exit branch.
        let delegate = AppDelegate()
        delegate.config = owner.config
        delegate.backendHost = owner.backendHost
        delegate.backendPort = owner.backendPort
        delegate.showMainWindow()
        defer { delegate.window?.close() }
        let base = panelOrigin(host: owner.backendHost ?? owner.config.host, port: owner.backendPort)
        delegate.webView.load(URLRequest(url: URL(string: base + "/ui/")!))
        // Foundation URL.path normalizes the terminal slash in the fixture URL.
        try NativeTests.expect(await NativeTests.wait { delegate.webView.url?.path == "/ui" && !delegate.webView.isLoading },
                               "a shell without an owned backend loads the real export fixture")
        try await export(delegate, path: "/ui/export-slow")
        let destination = URL(fileURLWithPath: dataDirPath).appendingPathComponent("quit-export.txt")
        defer { try? FileManager.default.removeItem(at: destination) }
        try Data("keep".utf8).write(to: destination)
        delegate.resolvePanelDestinationForTests(destination)
        let temporary = delegate.panelTemporaryDestinationForTests!
        delegate.downloadForTests(from: URL(string: base + "/slow")!)
        try NativeTests.expect(await NativeTests.wait {
            let size = (try? FileManager.default.attributesOfItem(atPath: temporary.path)[.size] as? Int) ?? 0
            return size > 0 && (delegate.updateTask?.countOfBytesReceived ?? 0) > 0
        }, "quit regression starts both real WebKit export and update downloads")
        try NativeTests.expect(delegate.applicationShouldTerminate(NSApp) == .terminateLater,
                               "quit waits for download cleanup even when no backend is owned")
        try NativeTests.expect(await NativeTests.wait { delegate.terminationRepliesForTests == 1 },
                               "download cancellation replies to AppKit once all owned resources are released")
        try NativeTests.expect(delegate.panelDownloadCountForTests == 0 && delegate.updateTask == nil
                               && delegate.updateDownloadDelegate == nil && delegate.updateSession == nil,
                               "termination releases WebKit and URLSession download ownership together")
        try NativeTests.expect(!FileManager.default.fileExists(atPath: temporary.path)
                               && (try Data(contentsOf: destination)) == Data("keep".utf8),
                               "termination removes the partial export before replying and preserves its destination")

        let finished = AppDelegate()
        finished.config = owner.config
        var copiedDMG: URL?
        defer { if let copiedDMG { try? FileManager.default.removeItem(at: copiedDMG) } }
        var reply: NSApplication.TerminateReply?
        finished.downloadForTests(from: URL(string: base + "/download")!)
        finished.updateDownloadDelegate?.onMovedForTests = { [weak finished] file in
            copiedDMG = file
            reply = finished?.applicationShouldTerminate(NSApp)
        }
        try NativeTests.expect(await NativeTests.wait { finished.terminationRepliesForTests == 1 },
                               "quit during a real URLSession moved-file callback waits for terminal cleanup")
        try NativeTests.expect(reply == .terminateLater && copiedDMG != nil
                               && !FileManager.default.fileExists(atPath: copiedDMG!.path),
                               "the moved update DMG is reclaimed before the termination reply")
    }
}
