// The fixture-capture verb.
//
// Pinned because the failure mode is silent in both directions: an unparsed
// verb is deliberately not an error (the parser returns nil and the caller
// reports it), so a typo here does not crash anything — it just means a
// session that was supposed to build a fixture set quietly builds nothing.
// That is exactly what happened the first time this shipped.

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
    // Adding a case to an enum is where a switch quietly loses a branch, so
    // the neighbours are checked alongside rather than assumed.
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
    // A half-specified tuning is a worse experiment than none, and a zero or
    // negative value would either spin the sampler or freeze it.
    for bad in ["tune", "tune 4", "tune 4 x", "tune 0 0.1", "tune 4 0", "tune -1 0.1"] {
        #expect(ScanCommand(line: bad) == nil, "\(bad) should not parse")
    }
}

@Test("the fire reason survives a round trip, and is absent when unset")
func fireReasonOnTheWire() throws {
    // The field the parent stops guessing with. Absent rather than empty on a
    // manual shutter: an older parent must not see a value it cannot read, and
    // a newer one must be able to tell "no reason given" from "nudge".
    // Encode-only: Event is what a source *sends*, and the Go side is the only
    // thing that parses it.
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
