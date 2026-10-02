import Cocoa

@MainActor
enum LifecycleTests {
    static func run(_ delegate: AppDelegate) async throws {
        let root = URL(fileURLWithPath: dataDirPath)
        let mode = root.appendingPathComponent("fixture-mode")
        defer { try? FileManager.default.removeItem(at: mode) }

        delegate.downloadForTests(from: URL(string: "http://127.0.0.1:\(delegate.backendPort)/slow")!)
        try NativeTests.expect(await NativeTests.wait { (delegate.updateTask?.countOfBytesReceived ?? 0) > 0 }, "update begins receiving actual download data")
        delegate.cancelUpdateForTests()
        try NativeTests.expect(await NativeTests.wait { delegate.updateTask == nil && delegate.updatePhase == .available }, "cancelled update releases its session and returns to available")
        try NativeTests.expect(!delegate.cancellingUpdate && delegate.validateMenuItem(delegate.updateActionItem), "cancelled update allows another install attempt")
        delegate.downloadForTests(from: URL(string: "http://127.0.0.1:\(delegate.backendPort)/slow")!)
        delegate.cancelUpdateForTests()
        try NativeTests.expect(await NativeTests.wait { delegate.updateTask == nil && !delegate.cancellingUpdate }, "repeated cancellation also terminates the download lifecycle")
        delegate.updatePhase = .idle
        delegate.updateUIForTests()

        try Data("drain".utf8).write(to: mode)
        let drained = root.appendingPathComponent("drained")
        try? FileManager.default.removeItem(at: drained)
        delegate.stopBackend()
        try NativeTests.expect(await NativeTests.wait(12) { delegate.backend == nil }, "signal stop waits for the owned backend's four-second drain")
        try NativeTests.expect(FileManager.default.fileExists(atPath: drained.path), "backend finishes saving data before the shell considers it stopped")
        try FileManager.default.removeItem(at: mode)

        delegate.updatePhase = .installing
        delegate.startBackend()
        try NativeTests.expect(delegate.backend == nil && !delegate.validateMenuItem(delegate.restartItem), "installation prevents competing backend launches and service actions")
        delegate.updatePhase = .readyToRelaunch
        delegate.startBackend()
        try NativeTests.expect(delegate.backend == nil && !delegate.validateMenuItem(delegate.restartItem), "update handoff also prevents backend restart")
        delegate.updatePhase = .idle
        delegate.startBackend()
        try NativeTests.expect(await NativeTests.wait { delegate.pollForTests(); return delegate.backendState == .running }, "backend starts normally after leaving the update transaction")

        delegate.userStopping = true
        try delegate.backendInputPipe?.fileHandleForWriting.close()
        try NativeTests.expect(await NativeTests.wait { delegate.backend == nil && delegate.backendState == .stopped }, "loss of the parent's stdin pipe stops the child without an orphan")
        try NativeTests.expect(delegate.backendInputPipe == nil && delegate.backendLogHandle == nil && delegate.backendStopDeadline == nil, "backend exit releases pipe, log handle and stop deadline together")
        delegate.startBackend()
        try NativeTests.expect(await NativeTests.wait { delegate.pollForTests(); return delegate.backendState == .running }, "backend remains usable after parent-pipe cleanup")

        let beforeHandoff = delegate.backend!.processIdentifier
        let missingStage = root.appendingPathComponent(".ElysiaApi-update-missing/ElysiaApi.app")
        delegate.handoffForTests(missingStage)
        try NativeTests.expect(await NativeTests.wait(12) {
            delegate.pollForTests()
            return delegate.updatePhase == .failed && delegate.backendState == .running
                && delegate.backend?.processIdentifier != beforeHandoff
        }, "failed helper startup restores service from the unchanged old bundle")
        try NativeTests.expect(delegate.stagedUpdate == nil && delegate.validateMenuItem(delegate.restartItem),
                               "failed update handoff releases staging ownership and re-enables service actions")
        delegate.updatePhase = .idle
        delegate.updateUIForTests()
    }
}
