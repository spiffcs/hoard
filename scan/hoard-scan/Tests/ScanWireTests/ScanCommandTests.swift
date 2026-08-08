// The stdin verbs, which are a contract with the Go side.
//
// Every string here is one the parent actually sends — see
// internal/scan/session_darwin.go. A typo in a verb is not a crash, it is a
// feature that silently does nothing on a helper the parent has already
// feature-detected as supporting it, which is the kind of bug that gets
// diagnosed as "the torch doesn't work on this Mac".

import Testing

@testable import ScanWire

@Test("every verb the Go side sends parses")
func allVerbsFromTheParentParse() {
    // session_darwin.go sends exactly these.
    let expected: [(String, ScanCommand)] = [
        ("capture", .capture),
        ("auto-on", .auto(true)),
        ("auto-off", .auto(false)),
        ("rearm", .rearm),
        ("chime", .chime),
        ("torch-on", .torch(true)),
        ("torch-off", .torch(false)),
        ("quit", .quit),
    ]
    for (line, want) in expected {
        #expect(ScanCommand(line: line) == want, "verb \"\(line)\" did not parse as expected")
    }
}

@Test("the retired Continuity verbs are not silently still accepted")
func continuityVerbsAreGone() {
    // Rotation, Center Stage and the Video Effects panel left with the
    // Continuity Camera. They must read as unknown rather than parse into
    // something the phone would then ignore, because "parsed and dropped" is
    // indistinguishable from "worked" at the far end of the pipe.
    for line in ["rotate-left", "rotate-right", "frame-on", "frame-off", "effects"] {
        #expect(ScanCommand(line: line) == nil, "retired verb \"\(line)\" still parses")
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

@Test("tune parses in range and clamps out of range")
func tuneIsClamped() {
    // In range passes through untouched.
    #expect(ScanCommand(line: "tune 6 0.05") == .tune(stable: 6, interval: 0.05))
    // The tune knobs gate the capture path — the sample throttle sits in
    // front of manual capture too — so a wire value must not be able to
    // stall it. "tune 2000000000 1e9" once meant a trigger interval of
    // roughly thirty years; now it means the slowest tuning that is still a
    // session.
    #expect(ScanCommand(line: "tune 2000000000 1e9")
        == .tune(stable: 60, interval: 5.0))
    // Below the floor clamps up rather than spinning the sampler.
    #expect(ScanCommand(line: "tune 1 0.0001") == .tune(stable: 1, interval: 0.01))
    // Zero and negative stay rejected: they are malformed, not extreme.
    #expect(ScanCommand(line: "tune 0 0.5") == nil)
    #expect(ScanCommand(line: "tune 6 -1") == nil)
    #expect(ScanCommand(line: "tune 6") == nil, "a half-specified tuning must not parse")
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
