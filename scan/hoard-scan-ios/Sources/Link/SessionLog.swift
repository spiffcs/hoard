import Foundation
import OSLog

enum SessionLog {
    static var fileURL: URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("session-log.txt")
    }

    private static let logger = Logger(
        subsystem: "dev.spiffcs.hoard.scan.ios", category: "session")

    private static let queue = DispatchQueue(label: "hoard-scan.session-log")
    nonisolated(unsafe) private static var handle: FileHandle?

    private static func openHandle() -> FileHandle? {
        if let handle { return handle }
        let url = fileURL
        if !FileManager.default.fileExists(atPath: url.path) {
            FileManager.default.createFile(atPath: url.path, contents: nil)
        }
        handle = try? FileHandle(forWritingTo: url)
        _ = try? handle?.seekToEnd()
        return handle
    }

    static func write(_ line: String) {
        logger.notice("\(line, privacy: .public)")
        guard let data = (line + "\n").data(using: .utf8) else { return }
        queue.async {
            try? openHandle()?.write(contentsOf: data)
        }
    }

    static func startSession() {
        queue.async {
            try? handle?.close()
            handle = nil
            try? "── session \(Date().formatted(date: .abbreviated, time: .standard))\n"
                .data(using: .utf8)?.write(to: fileURL)
        }
    }
}
