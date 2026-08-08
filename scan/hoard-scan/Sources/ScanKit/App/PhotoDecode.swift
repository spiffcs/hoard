// Keeping the bytes the phone sent that are worth keeping.

// macOS only. ScanKit is the Mac end of the link. See Package.swift.
// (decodeJPEG lived here too until the mirror window it decoded for was
// deleted — nothing ever sent it a frame.)
#if os(macOS)

import Foundation

/// saveRemoteStill writes a full-resolution still the phone sent to
/// HOARD_SCAN_DEBUG_DIR, which is how the iOS fixture set gets built. Silently
/// does nothing when no debug directory is set, like every other diagnostic.
func saveRemoteStill(_ data: Data) {
    guard let dir = ProcessInfo.processInfo.environment["HOARD_SCAN_DEBUG_DIR"],
          !dir.isEmpty
    else { return }
    let name = "remote-still-\(Int(Date().timeIntervalSince1970 * 1000)).jpg"
    try? data.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name))
}

#endif
