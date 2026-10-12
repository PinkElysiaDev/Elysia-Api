import Cocoa
import CryptoKit
import WebKit
import os

// Controller methods share the delegate owner; process and callback lifetimes stay unchanged.
extension AppDelegate {
    // MARK: - 下载(WKDownloadDelegate)

    /// 保存面板和实际下载属于同一个窗口操作；路径回调必须终结一次才能释放 WebKit。
    final class PanelDownload {
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

    #if NATIVE_TESTS
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

}
