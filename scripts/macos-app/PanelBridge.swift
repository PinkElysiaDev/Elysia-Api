import Foundation
import Darwin

struct PanelMessage: Codable {
    var type: String
    var action: String? = nil
    var generation: String? = nil
    var updateEnabled: Bool? = nil
    var backendState: String? = nil
    var host: String? = nil
    var port: Int? = nil
    var config: PanelConfig? = nil
    var restartCount: Int? = nil
    var error: String? = nil
    var updatePhase: String? = nil
    var updateMessage: String? = nil
    var updateDetail: String? = nil
    var updateFraction: Double? = nil
    var capsuleVisible: Bool? = nil
}

/// One JSON message per line. Each endpoint owns its pipe handles.
final class PanelBridge {
    private static let maximumFrameSize = 128 * 1024
    private let input: FileHandle
    private let output: FileHandle
    private let onMessage: (PanelMessage) -> Void
    private let onClose: () -> Void
    private let queue = DispatchQueue(label: "ElysiaApi.PanelBridge", qos: .utility)
    private var buffer = Data()
    private var outputBuffer = Data()
    private var writeSource: DispatchSourceWrite?
    private var started = false
    private var closed = false

    init(input: FileHandle, output: FileHandle,
         onMessage: @escaping (PanelMessage) -> Void, onClose: @escaping () -> Void) {
        self.input = input
        self.output = output
        self.onMessage = onMessage
        self.onClose = onClose
    }

    func start() {
        queue.async { [self] in
            guard !self.started, !self.closed else { return }
            for handle in [self.input, self.output] {
                let flags = fcntl(handle.fileDescriptor, F_GETFL)
                guard flags >= 0, fcntl(handle.fileDescriptor, F_SETFL, flags | O_NONBLOCK) == 0 else {
                    self.closeOnQueue()
                    return
                }
            }
            self.started = true
            self.input.readabilityHandler = { [weak self] _ in
                guard let self else { return }
                self.queue.async { self.readAvailableData() }
            }
            self.flushOutput()
        }
    }

    func send(_ message: PanelMessage) {
        queue.async {
            guard !self.closed else { return }
            do {
                var data = try JSONEncoder().encode(message)
                guard data.count < Self.maximumFrameSize else { self.closeOnQueue(); return }
                data.append(0x0a)
                // A stalled panel must not accumulate snapshots in the service process.
                guard data.count <= Self.maximumFrameSize - self.outputBuffer.count else {
                    self.closeOnQueue()
                    return
                }
                self.outputBuffer.append(data)
                if self.started { self.flushOutput() }
            } catch { self.closeOnQueue() }
        }
    }

    func close() {
        queue.async { self.closeOnQueue() }
    }

    private func readAvailableData() {
        guard !closed else { return }
        var data = Data(count: 16 * 1024)
        let count = data.withUnsafeMutableBytes { Darwin.read(input.fileDescriptor, $0.baseAddress, $0.count) }
        if count < 0 {
            if errno == EAGAIN || errno == EINTR { return }
            closeOnQueue()
            return
        }
        data.count = count
        receive(data)
    }

    private func flushOutput() {
        guard !closed else { return }
        while !outputBuffer.isEmpty {
            let count = outputBuffer.withUnsafeBytes { Darwin.write(output.fileDescriptor, $0.baseAddress, $0.count) }
            if count > 0 { outputBuffer.removeFirst(count); continue }
            if count < 0 && errno == EINTR { continue }
            if count < 0 && errno == EAGAIN {
                if writeSource == nil {
                    let source = DispatchSource.makeWriteSource(fileDescriptor: output.fileDescriptor, queue: queue)
                    source.setEventHandler { [weak self] in self?.flushOutput() }
                    writeSource = source
                    source.resume()
                }
                return
            }
            closeOnQueue()
            return
        }
        writeSource?.cancel()
        writeSource = nil
    }

    private func receive(_ data: Data) {
        guard !closed else { return }
        guard !data.isEmpty else { closeOnQueue(); return }
        buffer.append(data)
        while let newline = buffer.firstIndex(of: 0x0a) {
            let frame = buffer.prefix(upTo: newline)
            guard frame.count < Self.maximumFrameSize,
                  let message = try? JSONDecoder().decode(PanelMessage.self, from: frame) else {
                closeOnQueue()
                return
            }
            buffer.removeSubrange(...newline)
            DispatchQueue.main.async { self.onMessage(message) }
        }
        if buffer.count >= Self.maximumFrameSize { closeOnQueue() }
    }

    private func closeOnQueue() {
        guard !closed else { return }
        closed = true
        input.readabilityHandler = nil
        writeSource?.cancel()
        writeSource = nil
        try? input.close()
        try? output.close()
        buffer.removeAll(keepingCapacity: false)
        outputBuffer.removeAll(keepingCapacity: false)
        DispatchQueue.main.async { self.onClose() }
    }
}
