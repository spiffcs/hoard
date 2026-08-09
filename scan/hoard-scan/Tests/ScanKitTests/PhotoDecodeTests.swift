// The diagnostic that must stay silent unless asked.
//
// saveRemoteStill is how the iOS fixture set gets built, and it is off by
// default for a good reason: these are 4032x3024 JPEGs, one per card, and not
// shipping them is most of the point of reading on the phone. A regression that
// made it write unconditionally would fill a user's disk during an ordinary
// scanning session, silently.

// macOS only, like everything in ScanKit. See Package.swift.
#if os(macOS)

import Foundation
import Testing

@testable import ScanKit

@Test("a still is written when a debug directory is given")
func saveRemoteStillWrites() throws {
    let dir = FileManager.default.temporaryDirectory
        .appendingPathComponent("scankit-still-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }

    let payload = Data("not really a jpeg".utf8)
    saveRemoteStill(payload, dir: dir.path)

    let files = try FileManager.default.contentsOfDirectory(atPath: dir.path)
    #expect(files.count == 1)
    let name = try #require(files.first)
    // The name carries a millisecond stamp so a pile session does not overwrite
    // itself card by card.
    #expect(name.hasPrefix("remote-still-"))
    #expect(name.hasSuffix(".jpg"))
    #expect(try Data(contentsOf: dir.appendingPathComponent(name)) == payload)
}

@Test("nothing is written when no debug directory is set")
func saveRemoteStillSilentByDefault() throws {
    let dir = FileManager.default.temporaryDirectory
        .appendingPathComponent("scankit-still-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }

    saveRemoteStill(Data("x".utf8), dir: nil)
    saveRemoteStill(Data("x".utf8), dir: "")

    #expect(try FileManager.default.contentsOfDirectory(atPath: dir.path).isEmpty)
}

@Test("two stills in the same session do not collide")
func saveRemoteStillDistinctNames() throws {
    let dir = FileManager.default.temporaryDirectory
        .appendingPathComponent("scankit-still-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }

    // The stamp is milliseconds, and a pile session commits a card roughly
    // every 400ms, so distinct timestamps are the normal case. Passing `now`
    // explicitly states the assumption instead of racing the clock.
    let t0 = Date(timeIntervalSince1970: 1_700_000_000.000)
    saveRemoteStill(Data("first".utf8), dir: dir.path, now: t0)
    saveRemoteStill(Data("second".utf8), dir: dir.path, now: t0.addingTimeInterval(0.4))

    #expect(try FileManager.default.contentsOfDirectory(atPath: dir.path).count == 2)
}

@Test("a still written to a directory that does not exist is dropped, not fatal")
func saveRemoteStillMissingDirectory() {
    // `try?` on purpose: a mistyped HOARD_SCAN_DEBUG_DIR should cost the
    // fixture, not the scanning session the operator is in the middle of.
    saveRemoteStill(Data("x".utf8), dir: "/nonexistent-\(UUID().uuidString)/nested")
}

#endif
