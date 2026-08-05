// The stdin verbs, which are a contract with the Go side.
//
// Every string here is one the parent actually sends — see
// internal/scan/session_darwin.go. A typo in a verb is not a crash, it is a
// feature that silently does nothing on a helper the parent has already
// feature-detected as supporting it, which is the kind of bug that gets
// diagnosed as "the torch doesn't work on this Mac".

import Testing

@testable import ScanKit

@Test("every verb the Go side sends parses")
func allVerbsFromTheParentParse() {
    // session_darwin.go sends exactly these.
    let expected: [(String, ScanCommand)] = [
        ("capture", .capture),
        ("auto-on", .auto(true)),
        ("auto-off", .auto(false)),
        ("rearm", .rearm),
        ("chime", .chime),
        ("frame-on", .framing(true)),
        ("frame-off", .framing(false)),
        ("torch-on", .torch(true)),
        ("torch-off", .torch(false)),
        ("effects", .effects),
        ("rotate-left", .rotate(clockwise: false)),
        ("rotate-right", .rotate(clockwise: true)),
        ("quit", .quit),
    ]
    for (line, want) in expected {
        #expect(ScanCommand(line: line) == want, "verb \"\(line)\" did not parse as expected")
    }
}

@Test("result carries its JSON payload through untouched")
func resultKeepsItsPayload() {
    // The payload is scan.HUDResult as compact JSON. Splitting on the first
    // space only matters because the JSON itself may contain spaces.
    let json = #"{"amount": 12.5, "tier": "win"}"#
    #expect(ScanCommand(line: "result " + json) == .result(payload: json))
}

@Test("result with no payload is still the result verb")
func bareResultParses() {
    // The caller reports a bad payload as an error event rather than treating
    // it as an unknown verb, which is a different message to the user.
    #expect(ScanCommand(line: "result") == .result(payload: ""))
}

@Test("an unknown verb is nil, not a crash and not a silent no-op")
func unknownVerbsAreRejected() {
    // A newer hoard talking to an older helper is a supported pairing, so this
    // path has to stay a reported error that keeps the session alive.
    #expect(ScanCommand(line: "teleport") == nil)
    #expect(ScanCommand(line: "") == nil)
    #expect(ScanCommand(line: "auto") == nil, "a truncated verb must not match auto-on/off")
}

@Test("verbs are matched whole, never by prefix")
func verbsAreNotPrefixMatched() {
    #expect(ScanCommand(line: "capture-now") == nil)
    #expect(ScanCommand(line: "quitter") == nil)
}
