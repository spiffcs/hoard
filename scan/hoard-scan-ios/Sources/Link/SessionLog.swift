// The session's telemetry, kept where it can be fetched.
//
// The timing lines are sent to the Mac as trace frames, which is right — they
// belong in HOARD_SCAN_LOG next to everything else about a session. But that is
// the *only* copy, and it only exists if the Mac was started with logging on.
// A tuning session that produced no retrievable numbers is a tuning session
// wasted, and that has now happened once.
//
// So every trace is also appended here, in the app's Documents container:
//
//     xcrun devicectl device copy from --device <udid> \
//       --domain-type appDataContainer \
//       --domain-identifier dev.spiffcs.hoard.scan.ios \
//       --source Documents --destination .
//
// Appended per line rather than buffered: a session that ends by the app being
// killed is exactly the session whose last lines matter most.

import Foundation
import OSLog

enum SessionLog {
    static var fileURL: URL {
        FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("session-log.txt")
    }

    private static let logger = Logger(
        subsystem: "dev.spiffcs.hoard.scan.ios", category: "session")

    static func write(_ line: String) {
        logger.notice("\(line, privacy: .public)")
        guard let data = (line + "\n").data(using: .utf8) else { return }
        let url = fileURL
        if let handle = try? FileHandle(forWritingTo: url) {
            defer { try? handle.close() }
            _ = try? handle.seekToEnd()
            try? handle.write(contentsOf: data)
        } else {
            try? data.write(to: url)
        }
    }

    /// clear starts a fresh session's log. Called when a Mac connects, so a
    /// pulled file describes one sitting rather than everything since install.
    static func startSession() {
        try? "── session \(Date().formatted(date: .abbreviated, time: .standard))\n"
            .data(using: .utf8)?.write(to: fileURL)
    }
}
