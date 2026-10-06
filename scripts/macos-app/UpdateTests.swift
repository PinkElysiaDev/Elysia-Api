import Cocoa

@MainActor
enum UpdateTests {
    static func run() async throws {
        let manager = FileManager.default
        let root = URL(fileURLWithPath: dataDirPath).appendingPathComponent("update-tests")
        try manager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? manager.removeItem(at: root) }
        try replacementFailures(root)
        try await systemToolTimeout(root)
        try await extraction(root)
        try await transaction(root, mode: "ready")
        try await transaction(root, mode: "exit")
        try await transaction(root, mode: "wrong-ack")
        try await transaction(root, mode: "no-ack")
        try await parentTimeout(root)
        try await failedRecoveryLaunch(root)
        try await forkedHelper(root)
    }

    private static func replacementFailures(_ root: URL) throws {
        let manager = FileManager.default
        let directory = root.appendingPathComponent("rename-failures")
        let current = directory.appendingPathComponent("Current.app")
        let staged = directory.appendingPathComponent("Staged.app")
        try makeApp(current, mode: "old")
        try makeApp(staged, mode: "ready")
        try NativeTests.rejects("first rename failure never removes the current bundle") {
            try UpdateInstaller.replace(staged: staged, current: current) { _, _ in throw UpdateError(message: "injected first rename failure") }
        }
        try NativeTests.expect(try String(contentsOf: current.appendingPathComponent("version"), encoding: .utf8) == "old",
                               "old app remains usable when the backup rename fails")
        try NativeTests.rejects("failed replacement and rollback preserve the recoverable backup") {
            try UpdateInstaller.replace(staged: staged, current: current) { from, to in
                if from == staged {
                    try manager.createDirectory(at: current, withIntermediateDirectories: false)
                    throw UpdateError(message: "injected replacement failure with blocked rollback destination")
                }
                try manager.moveItem(at: from, to: to)
            }
        }
        let backup = try manager.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil).first { $0.lastPathComponent.hasPrefix(".ElysiaApi-backup-") }
        let version = try backup.map { try String(contentsOf: $0.appendingPathComponent("version"), encoding: .utf8) }
        try NativeTests.expect(version == "old" && manager.fileExists(atPath: staged.path),
                               "rollback failure retains both old backup and staged new version")
    }

    private static func systemToolTimeout(_ root: URL) async throws {
        let command = root.appendingPathComponent("ignore-term")
        try Data("#!/bin/sh\ntrap '' TERM\nwhile :; do :; done\n".utf8).write(to: command)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: command.path)
        let start = ProcessInfo.processInfo.systemUptime
        let failure: String? = await Task.detached {
            do { try runSystemTool(command.path, [], timeout: 0.1); return nil }
            catch { return error.localizedDescription }
        }.value
        try NativeTests.expect(failure?.contains("超时") == true && ProcessInfo.processInfo.systemUptime - start < 4,
                               "hung update tools terminate with a bounded timeout even when TERM is ignored")
    }

    private static func extraction(_ root: URL) async throws {
        let source = root.appendingPathComponent("image-source")
        let app = source.appendingPathComponent("ElysiaApi.app")
        let macos = app.appendingPathComponent("Contents/MacOS")
        let manager = FileManager.default
        try manager.createDirectory(at: macos, withIntermediateDirectories: true)
        let identifier = Bundle.main.bundleIdentifier!
        let plist: [String: Any] = ["CFBundleIdentifier": identifier, "CFBundleExecutable": "ElysiaApi", "CFBundlePackageType": "APPL"]
        try PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0)
            .write(to: app.appendingPathComponent("Contents/Info.plist"))
        for name in ["ElysiaApi", "elysia-api"] {
            try manager.copyItem(at: URL(fileURLWithPath: dataDirPath).appendingPathComponent("fixture-universal"), to: macos.appendingPathComponent(name))
        }
        let dmg = root.appendingPathComponent("valid update '$()` .dmg")
        let current = root.appendingPathComponent("current '$()` .app")
        let previousMounts = Set(try manager.contentsOfDirectory(atPath: manager.temporaryDirectory.path).filter { $0.hasPrefix("elysia-dmg-") })
        let staged = try await Task.detached {
            try runSystemTool("/usr/bin/codesign", ["--force", "--sign", "-", "--deep", app.path])
            try runSystemTool("/usr/bin/hdiutil", ["create", "-quiet", "-format", "UDZO", "-srcfolder", source.path, "-o", dmg.path], timeout: 60)
            return try UpdateInstaller.extractApp(fromDMG: dmg, beside: current)
        }.value
        defer { try? manager.removeItem(at: staged.deletingLastPathComponent()) }
        try NativeTests.expect(manager.isExecutableFile(atPath: staged.appendingPathComponent("Contents/MacOS/ElysiaApi").path),
                               "a real signed universal DMG extracts without shell interpolation")
        try NativeTests.expect(!manager.fileExists(atPath: current.path), "DMG validation and staging never mutate the current bundle")
        let mounts = Set(try manager.contentsOfDirectory(atPath: manager.temporaryDirectory.path).filter { $0.hasPrefix("elysia-dmg-") })
        try NativeTests.expect(mounts.subtracting(previousMounts).isEmpty, "successful DMG extraction detaches and removes its mount point")
    }

    private static func makeApp(_ app: URL, mode: String) throws {
        let macos = app.appendingPathComponent("Contents/MacOS")
        try FileManager.default.createDirectory(at: macos, withIntermediateDirectories: true)
        let tag = mode == "old" ? "old" : "new"
        var body = """
        #!/bin/sh
        root=$(cd "$(dirname "$0")/../../.." && pwd)
        printf '%s\\n' "$$" > "$root/\(tag).pid"
        """
        if mode == "ready" { body += "\nif [ \"$2\" = \"--update-ack\" ]; then printf '%s\\n' \"$$\" > \"$3\"; fi\n" }
        if mode == "wrong-ack" { body += "\nprintf '%s\\n' 1 > \"$3\"\n" }
        body += mode == "exit" ? "\nexit 23\n" : "\nexec /bin/sleep 60\n"
        let executable = macos.appendingPathComponent("ElysiaApi")
        try Data(body.utf8).write(to: executable)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: executable.path)
        try Data(tag.utf8).write(to: app.appendingPathComponent("version"))
    }

    private static func transaction(_ root: URL, mode: String) async throws {
        let manager = FileManager.default
        let directory = root.appendingPathComponent(mode)
        let current = directory.appendingPathComponent("Current '$()` app.app")
        let staging = directory.appendingPathComponent(".ElysiaApi-update-\(UUID().uuidString)")
        let staged = staging.appendingPathComponent("ElysiaApi.app")
        try makeApp(current, mode: "old")
        try makeApp(staged, mode: mode)
        defer {
            for name in ["old.pid", "new.pid"] {
                if let text = try? String(contentsOf: directory.appendingPathComponent(name), encoding: .utf8),
                   let pid = Int32(text.trimmingCharacters(in: .whitespacesAndNewlines)), pid > 1 { kill(pid, SIGKILL) }
            }
            try? manager.removeItem(at: directory)
        }
        let arguments = UpdateInstaller.helperArguments(staged: staged, current: current, parentPID: Int32.max, background: false)
        let failure: String? = await Task.detached {
            do {
                try UpdateInstaller.runHelper(arguments: arguments, parentExitTimeout: 0.2, launchTimeout: 0.5, childExitTimeout: 0.2)
                return nil
            } catch { return error.localizedDescription }
        }.value
        let version = try String(contentsOf: current.appendingPathComponent("version"), encoding: .utf8)
        if mode == "ready" {
            try NativeTests.expect(failure == nil && version == "new", "update commits only after the new child acknowledges readiness")
            let backups = try manager.contentsOfDirectory(atPath: directory.path).filter { $0.hasPrefix(".ElysiaApi-backup-") }
            try NativeTests.expect(backups.isEmpty && !manager.fileExists(atPath: staging.path), "acknowledged update releases backup and staging resources")
        } else {
            try NativeTests.expect(failure != nil && version == "old", "\(mode) launch failure restores the old bundle")
            try NativeTests.expect(await NativeTests.wait { manager.fileExists(atPath: directory.appendingPathComponent("old.pid").path) },
                                   "\(mode) rollback relaunches the old shell")
        }
    }

    private static func parentTimeout(_ root: URL) async throws {
        let current = root.appendingPathComponent("parent-timeout.app")
        let staging = root.appendingPathComponent(".ElysiaApi-update-\(UUID().uuidString)")
        let staged = staging.appendingPathComponent("ElysiaApi.app")
        try makeApp(current, mode: "old")
        try makeApp(staged, mode: "ready")
        let arguments = UpdateInstaller.helperArguments(staged: staged, current: current, parentPID: getpid(), background: true)
        let failure: String? = await Task.detached {
            do { try UpdateInstaller.runHelper(arguments: arguments, parentExitTimeout: 0.1); return nil }
            catch { return error.localizedDescription }
        }.value
        let version = try String(contentsOf: current.appendingPathComponent("version"), encoding: .utf8)
        try NativeTests.expect(failure?.contains("未能在期限内退出") == true && version == "old",
                               "parent exit timeout cancels replacement and preserves the running bundle")
        try NativeTests.expect(!FileManager.default.fileExists(atPath: staging.path),
                               "parent exit timeout releases the handed-off staging directory")
    }

    private static func failedRecoveryLaunch(_ root: URL) async throws {
        let manager = FileManager.default
        let directory = root.appendingPathComponent("failed-recovery-launch")
        let current = directory.appendingPathComponent("Current.app")
        let staging = directory.appendingPathComponent(".ElysiaApi-update-\(UUID().uuidString)")
        let staged = staging.appendingPathComponent("ElysiaApi.app")
        try makeApp(current, mode: "old")
        try manager.removeItem(at: current.appendingPathComponent("Contents/MacOS/ElysiaApi"))
        try makeApp(staged, mode: "exit")
        let arguments = UpdateInstaller.helperArguments(staged: staged, current: current, parentPID: Int32.max, background: false)
        let failure: String? = await Task.detached {
            do { try UpdateInstaller.runHelper(arguments: arguments, launchTimeout: 0.5); return nil }
            catch { return error.localizedDescription }
        }.value
        let version = try String(contentsOf: current.appendingPathComponent("version"), encoding: .utf8)
        try NativeTests.expect(version == "old" && failure?.contains("新版进程提前退出") == true
                               && failure?.contains("启动错误") == true && failure?.contains(current.path) == true,
                               "failed old-app relaunch preserves the restored bundle and reports original failure with its recovery path")
    }

    private static func forkedHelper(_ root: URL) async throws {
        let manager = FileManager.default
        let directory = root.appendingPathComponent("forked '$()` helper")
        let current = directory.appendingPathComponent("Current '$()` app.app")
        let staging = directory.appendingPathComponent(".ElysiaApi-update-\(UUID().uuidString)")
        let staged = staging.appendingPathComponent("ElysiaApi.app")
        try makeApp(current, mode: "old")
        let executable = current.appendingPathComponent("Contents/MacOS/ElysiaApi")
        try manager.removeItem(at: executable)
        try manager.copyItem(at: Bundle.main.executableURL!, to: executable)
        try makeApp(staged, mode: "ready")
        let parent = Process()
        parent.executableURL = URL(fileURLWithPath: "/bin/sleep")
        parent.arguments = ["0.6"]
        try parent.run()
        let helper = try UpdateInstaller.startHelper(staged: staged, current: current, parentPID: parent.processIdentifier, background: true)
        defer {
            if parent.isRunning { parent.terminate() }
            if helper.isRunning { helper.terminate() }
            if let text = try? String(contentsOf: directory.appendingPathComponent("new.pid"), encoding: .utf8),
               let pid = Int32(text.trimmingCharacters(in: .whitespacesAndNewlines)), pid > 1 { kill(pid, SIGKILL) }
            try? manager.removeItem(at: directory)
        }
        try? await Task.sleep(nanoseconds: 200_000_000)
        let before = try String(contentsOf: current.appendingPathComponent("version"), encoding: .utf8)
        try NativeTests.expect(parent.isRunning && before == "old", "forked helper waits for the old shell before replacing its app")
        try NativeTests.expect(await NativeTests.wait(8) { !helper.isRunning }, "copied native helper completes a real fork and acknowledgement transaction")
        let after = try String(contentsOf: current.appendingPathComponent("version"), encoding: .utf8)
        try NativeTests.expect(helper.terminationStatus == 0 && after == "new" && !manager.fileExists(atPath: staging.path),
                               "forked helper accepts literal paths and cleans staging after successful relaunch")
    }
}
