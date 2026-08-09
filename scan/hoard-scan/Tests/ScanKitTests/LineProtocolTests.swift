// The one place a frame stops being verbatim.
//
// ScanKit's whole job is translation, and it passes the phone's NDJSON through
// undecoded so that a field this build never heard of still reaches the Go
// side. ndjsonLine is the single exception to that, which makes it the single
// place the passthrough can corrupt an event — so it is worth pinning byte for
// byte rather than trusting the comment above it.

// macOS only, like everything in ScanKit. See Package.swift.
#if os(macOS)

import Foundation
import Testing

@testable import ScanKit

@Test("a payload with no newlines is passed through untouched, plus a terminator")
func ndjsonLinePassesCleanPayloads() {
    let payload = Data(#"{"event":"scan","name":"Black Lotus"}"#.utf8)
    let out = ndjsonLine(payload)
    #expect(out.dropLast() == payload)
    #expect(out.last == 0x0A)
}

@Test("interior newlines and carriage returns become spaces")
func ndjsonLineFlattensInteriorNewlines() {
    // This is the failure it exists to prevent: a raw \n inside the payload
    // splits one event into two half-lines and poisons everything after it in
    // the Go side's line parser.
    let out = ndjsonLine(Data("{\"a\":1}\n{\"b\":2}\r\n".utf8))
    let text = String(decoding: out, as: UTF8.self)
    #expect(text == "{\"a\":1} {\"b\":2}  \n")
    // Exactly one newline survives, and it is the terminator.
    #expect(out.filter { $0 == 0x0A }.count == 1)
    #expect(out.last == 0x0A)
}

@Test("an escaped newline is left alone")
func ndjsonLineLeavesEscapesAlone() {
    // A legal JSON encoder writes the two-byte escape rather than a raw
    // newline, which is what makes the flattening safe rather than lossy —
    // backslash-n is 0x5C 0x6E and neither byte is touched.
    let payload = Data(#"{"note":"line\none"}"#.utf8)
    #expect(ndjsonLine(payload).dropLast() == payload)
}

@Test("multi-byte UTF-8 survives the byte-wise scan")
func ndjsonLinePreservesMultibyte() {
    // The replacement walks bytes, not characters. That is safe only because
    // every byte of a multi-byte UTF-8 sequence has its high bit set, so 0x0A
    // and 0x0D can never appear inside one. Card names make this concrete:
    // Æther, Márton Stromgald, Jötun Grunt, and the em dashes hoard's own
    // prose is full of.
    let payload = Data(#"{"name":"Æther Vial — Jötun Grunt","set":"Márton"}"#.utf8)
    let out = ndjsonLine(payload)
    #expect(out.dropLast() == payload)
    #expect(String(decoding: out.dropLast(), as: UTF8.self)
        == #"{"name":"Æther Vial — Jötun Grunt","set":"Márton"}"#)
}

@Test("an empty payload still terminates its line")
func ndjsonLineEmptyPayload() {
    // A zero-length frame must not produce a zero-length write: the Go side is
    // reading lines, and no terminator means the next event lands on this one.
    #expect(ndjsonLine(Data()) == Data([0x0A]))
}

@Test("a payload that is only newlines becomes only spaces")
func ndjsonLineAllNewlines() {
    #expect(ndjsonLine(Data([0x0A, 0x0D, 0x0A])) == Data([0x20, 0x20, 0x20, 0x0A]))
}

#endif
