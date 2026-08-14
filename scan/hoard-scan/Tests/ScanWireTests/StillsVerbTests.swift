import Foundation
import Testing

@testable import ScanWire

@Test("the stills verb round-trips in both directions")
func stillsVerb() {
    guard case .stills(true)? = ScanCommand(line: "stills-on") else {
        Issue.record("stills-on did not parse")
        return
    }
    guard case .stills(false)? = ScanCommand(line: "stills-off") else {
        Issue.record("stills-off did not parse")
        return
    }
}

@Test("the verbs the phone already understood still parse")
func existingVerbsUnchanged() {
    for (line, ok) in [("capture", ScanCommand(line: "capture") != nil),
                       ("rearm", ScanCommand(line: "rearm") != nil),
                       ("auto-on", ScanCommand(line: "auto-on") != nil),
                       ("torch-off", ScanCommand(line: "torch-off") != nil)] {
        #expect(ok, "\(line) stopped parsing")
    }
    #expect(ScanCommand(line: "not-a-verb") == nil)
}

@Test("the tune verb carries both knobs or none")
func tuneVerb() {
    guard case .tune(let n, let i)? = ScanCommand(line: "tune 4 0.05") else {
        Issue.record("tune did not parse")
        return
    }
    #expect(n == 4)
    #expect(abs(i - 0.05) < 1e-9)
    for bad in ["tune", "tune 4", "tune 4 x", "tune 0 0.1", "tune 4 0", "tune -1 0.1"] {
        #expect(ScanCommand(line: bad) == nil, "\(bad) should not parse")
    }
}

@Test("the fire reason survives a round trip, and is absent when unset")
func fireReasonOnTheWire() throws {
    func json(_ e: Event) throws -> String {
        String(data: try JSONEncoder().encode(e), encoding: .utf8) ?? ""
    }
    let placed = try json(
        Event(event: "scan", name: "X", auto: true, fireReason: "replaced"))
    #expect(placed.contains("\"fireReason\":\"replaced\""), "\(placed)")

    let manual = try json(Event(event: "scan", name: "X"))
    #expect(!manual.contains("fireReason"),
            "a manual shutter sends no reason: \(manual)")
}
