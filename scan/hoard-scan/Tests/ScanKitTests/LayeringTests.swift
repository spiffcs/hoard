// The one architectural rule ScanKit has, enforced here because it is not
// enforced by the compiler.
//
// ScanKit is a single module, so Core/ and App/ can see each other. The whole
// value of the split is the direction: Core is the read pipeline — parser,
// border reader, trigger — and it has to stay runnable, and testable, without a
// camera, a window or an audio device. That is what makes `--image` able to
// replay a fixture through the identical path a live capture takes, which is
// what makes scan/fixtures mean anything.
//
// A second module would let the compiler say this instead. That trade was
// weighed and deferred: today it would cost ~140 `public` annotations on a
// single-binary app, most of them on interfaces stage F is about to narrow.

import Foundation
import Testing

@testable import ScanKit

/// Frameworks that only exist to talk to a person or a device. Importing one
/// under Core/ means the read pipeline has grown a dependency on the machine it
/// happens to be running on.
private let uiFrameworks = ["AppKit", "AVFoundation", "QuartzCore", "SwiftUI", "AudioToolbox"]

/// Sources/ScanKit, found relative to this file so the test does not care where
/// the checkout lives.
private var scanKitRoot: URL {
    URL(fileURLWithPath: #filePath)  // Tests/ScanKitTests/LayeringTests.swift
        .deletingLastPathComponent()  // Tests/ScanKitTests
        .deletingLastPathComponent()  // Tests
        .deletingLastPathComponent()  // package root
        .appendingPathComponent("Sources/ScanKit")
}

private func swiftFiles(under directory: URL) throws -> [URL] {
    guard let walker = FileManager.default.enumerator(atPath: directory.path) else { return [] }
    return walker.compactMap { entry in
        guard let name = entry as? String, name.hasSuffix(".swift") else { return nil }
        return directory.appendingPathComponent(name)
    }
}

@Test("the read pipeline does not depend on a camera, a window or a speaker")
func coreDoesNotImportUIFrameworks() throws {
    let core = scanKitRoot.appendingPathComponent("Core")
    let files = try swiftFiles(under: core)
    #expect(!files.isEmpty, "found no sources under \(core.path)")

    for file in files {
        let imports = try String(contentsOf: file, encoding: .utf8)
            .split(separator: "\n")
            .filter { $0.hasPrefix("import ") }
            .map { $0.dropFirst("import ".count).trimmingCharacters(in: .whitespaces) }
        for framework in uiFrameworks where imports.contains(framework) {
            Issue.record("Core/\(file.lastPathComponent) imports \(framework)")
        }
    }
}

@Test("every source file lives on one side of the line or the other")
func everySourceIsFiled() throws {
    let core = try swiftFiles(under: scanKitRoot.appendingPathComponent("Core"))
    let app = try swiftFiles(under: scanKitRoot.appendingPathComponent("App"))
    let all = try swiftFiles(under: scanKitRoot)
    // CLI.swift is the entry point and sits above both, so it is the only file
    // allowed to be loose at the top of the module.
    let loose = all.count - core.count - app.count
    #expect(loose == 1, "unfiled sources at the top of ScanKit: \(loose)")
}
