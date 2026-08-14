import Testing

@testable import ScanWire

@Test("every verb the Go side sends parses")
func allVerbsFromTheParentParse() {
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
    for line in ["rotate-left", "rotate-right", "frame-on", "frame-off", "effects"] {
        #expect(ScanCommand(line: line) == nil, "retired verb \"\(line)\" still parses")
    }
}

@Test("result carries its JSON payload through untouched")
func resultKeepsItsPayload() {
    let json = #"{"amount": 12.5, "tier": "win"}"#
    #expect(ScanCommand(line: "result " + json) == .result(payload: json))
}

@Test("result with no payload is still the result verb")
func bareResultParses() {
    #expect(ScanCommand(line: "result") == .result(payload: ""))
}

@Test("tune parses in range and clamps out of range")
func tuneIsClamped() {
    #expect(ScanCommand(line: "tune 6 0.05") == .tune(stable: 6, interval: 0.05))
    #expect(ScanCommand(line: "tune 2000000000 1e9")
        == .tune(stable: 60, interval: 5.0))
    #expect(ScanCommand(line: "tune 1 0.0001") == .tune(stable: 1, interval: 0.01))
    #expect(ScanCommand(line: "tune 0 0.5") == nil)
    #expect(ScanCommand(line: "tune 6 -1") == nil)
    #expect(ScanCommand(line: "tune 6") == nil, "a half-specified tuning must not parse")
}

@Test("an unknown verb is nil, not a crash and not a silent no-op")
func unknownVerbsAreRejected() {
    #expect(ScanCommand(line: "teleport") == nil)
    #expect(ScanCommand(line: "") == nil)
    #expect(ScanCommand(line: "auto") == nil, "a truncated verb must not match auto-on/off")
}

@Test("verbs are matched whole, never by prefix")
func verbsAreNotPrefixMatched() {
    #expect(ScanCommand(line: "capture-now") == nil)
    #expect(ScanCommand(line: "quitter") == nil)
}
