// The session's telemetry, kept where it can be fetched.
//
// The timing lines are sent to the Mac as trace frames, which is right — they
// belong in HOARD_SCAN_LOG next to everything else about a session. But that is
// the *only* copy, and it only exists if the Mac was started with logging on.
// A tuning session that produced no retrievable numbers is a tuning session
// wasted, and that has now happened once.
//
// So every trace is also appended here, in the app's Library/Caches — not
// Documents, deliberately. This log records every card name and price a
// session read, and Documents is what Info.plist's file-sharing keys expose to
// anyone holding the unlocked phone via the Files app (the corpus workflow
// wants those keys, so they stay). Caches is invisible there and still one
// command away for the person the log is for:
//
//     xcrun devicectl device copy from --device <udid> \
//       --domain-type appDataContainer \
//       --domain-identifier dev.spiffcs.hoard.scan.ios \
//       --source Library/Caches --destination .
//
// Appended per line rather than buffered: a session that ends by the app being
// killed is exactly the session whose last lines matter most.

import Foundation
import OSLog

enum SessionLog {
    static var fileURL: URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("session-log.txt")
    }

    private static let logger = Logger(
        subsystem: "dev.spiffcs.hoard.scan.ios", category: "session")

    /// One serial writer, one open handle. write() used to open, seek, write
    /// and close per line, from whatever thread called it — the capture path
    /// logs from the main actor while the link logs from its own hops, and
    /// two unserialised appends can interleave mid-line. The queue is the
    /// lock; the kept handle is what makes per-line appends affordable on the
    /// capture hot path.
    private static let queue = DispatchQueue(label: "hoard-scan.session-log")
    /// Queue-confined; touched nowhere else.
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

    /// clear starts a fresh session's log. Called when a Mac connects, so a
    /// pulled file describes one sitting rather than everything since install.
    static func startSession() {
        queue.async {
            try? handle?.close()
            handle = nil
            try? "── session \(Date().formatted(date: .abbreviated, time: .standard))\n"
                .data(using: .utf8)?.write(to: fileURL)
        }
    }
}
