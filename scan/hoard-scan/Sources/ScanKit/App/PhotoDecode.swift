// Keeping the bytes the phone sent that are worth keeping.

// macOS only. ScanKit is the Mac end of the link. See Package.swift.
// (decodeJPEG lived here too until the mirror window it decoded for was
// deleted — nothing ever sent it a frame.)
#if os(macOS)

import Foundation

/// saveRemoteStill writes a full-resolution still the phone sent to
/// HOARD_SCAN_DEBUG_DIR, which is how the iOS fixture set gets built. Silently
/// does nothing when no debug directory is set, like every other diagnostic.
///
/// `dir` is a parameter with the environment as its default rather than a
/// straight `getenv` inside, so a test can exercise the write without mutating
/// the process environment — which is global state, and racy under a parallel
/// test runner.
func saveRemoteStill(_ data: Data,
                     dir: String? = ProcessInfo.processInfo.environment["HOARD_SCAN_DEBUG_DIR"],
                     now: Date = Date()) {
    guard let dir, !dir.isEmpty else { return }
    let name = "remote-still-\(Int(now.timeIntervalSince1970 * 1000)).jpg"
    try? data.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name))
}

#endif
