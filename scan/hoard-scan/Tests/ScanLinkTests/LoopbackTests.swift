// The link, tested against itself.
//
// A listener and a browser in one process, over the loopback interface, with a
// real Bonjour advertisement and a real handshake. Not a mock: the things that
// go wrong here are the two connections of a session pairing to the wrong
// partner, the pairing check not actually checking, and framing breaking across
// a real socket's read boundaries — and a mock reproduces none of them.
//
// These are slower than the rest of the suite (a second or two) and they earn
// it. The alternative is finding out with a phone in one hand.

import CryptoKit
import Foundation
import Network
import ScanWire
import Testing

@testable import ScanLink

/// Waits for `check` to become true, pumping the run loop. Returns whether it
/// did — never a bare sleep, so a passing test is fast and a failing one is not
/// a guess about how long to wait.
private func waitFor(_ seconds: Double = 5, _ check: () -> Bool) -> Bool {
    let deadline = Date().addingTimeInterval(seconds)
    while !check(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
    }
    return check()
}

@Test("a pairing code is six digits, and anything else is refused")
func codeShape() {
    #expect(PairingCode("123456")?.digits == "123456")
    #expect(PairingCode("123 456")?.digits == "123456")
    #expect(PairingCode("12-34-56")?.digits == "123456")
    #expect(PairingCode("12345") == nil)
    #expect(PairingCode("1234567") == nil)
    #expect(PairingCode("abcdef") == nil)
    #expect(PairingCode("")  == nil)
}

@Test("a generated code is always six digits, leading zeros included")
func generatedCodes() {
    // String(n) would drop a leading zero and produce a five-digit code that
    // PairingCode itself then refuses — a bug that shows up one time in ten.
    for _ in 0..<200 {
        let code = PairingCode.random()
        #expect(code.digits.count == 6)
        #expect(PairingCode(code.digits) == code)
    }
}

@Test("the code is displayed grouped for reading aloud")
func codeDisplay() {
    #expect(PairingCode("123456")?.display == "123 456")
}

@Test("the same code derives the same key, a different one does not")
func keyDerivation() {
    let a = PairingCode("123456")!, b = PairingCode("123456")!, c = PairingCode("654321")!
    #expect(a.key == b.key)
    #expect(a.key != c.key)
    // Stretched, not the digits themselves.
    #expect(a.key != SymmetricKeyFromDigits("123456"))
}

private func SymmetricKeyFromDigits(_ s: String) -> SymmetricKey {
    SymmetricKey(data: Data(s.utf8))
}

@Test("two ends with the same code complete a session and exchange frames",
      .timeLimit(.minutes(1)))
func loopbackSession() throws {
    let code = PairingCode.random()
    let listener = PeerListener(name: "hoard-test-\(UUID().uuidString.prefix(8))", code: code)

    var accepted: PeerSession?
    var listenerError: String?
    listener.onSession = { accepted = $0 }
    listener.onError = { listenerError = $0 }
    try listener.start()
    defer { listener.stop() }

    let browser = PeerBrowser()
    let services = browser.browse(seconds: 4)
    let service = try #require(
        services.first { $0.name.hasPrefix("hoard-test-") },
        "the listener never appeared on the network; saw \(services.map(\.name))")

    let client = browser.connect(to: service, code: code)
    defer { client.cancel() }

    // Track the client's own view of the handshake. Without this a failure here
    // says only "no session", which is equally consistent with the service not
    // resolving, TLS never completing, and the hello never being read.
    var states: [String] = []
    client.control.onState = { states.append("control \($0)") }
    client.preview.onState = { states.append("preview \($0)") }

    #expect(
        waitFor { accepted != nil },
        """
        no session was paired
          listener error: \(listenerError ?? "none")
          client states: \(states.isEmpty ? "(none — TLS never settled)" : states.joined(separator: ", "))
          client now:    control \(client.control.state), preview \(client.preview.state)
        """)
    let server = try #require(accepted)

    // The roles landed on the right connections. Getting this backwards would
    // put preview JPEGs on the Nagle-disabled control channel and shutter verbs
    // behind them, which is the exact failure two connections exist to prevent.
    #expect(server.control.role == .control)
    #expect(server.preview.role == .preview)

    // Control, phone to Mac.
    var received: [Frame] = []
    client.control.onFrame = { received.append($0) }
    let event = Event(event: "ready", device: "loopback", features: ["auto"])
    server.control.send(try #require(Frame.json(event)))
    #expect(waitFor { !received.isEmpty }, "no frame arrived on control")
    #expect(received.first?.kind == .ndjson)
    #expect(received.first?.text?.contains("\"device\":\"loopback\"") == true)

    // Preview, with a payload full of the newlines NDJSON could not have carried.
    var previews: [Frame] = []
    client.preview.onFrame = { previews.append($0) }
    let jpegish = Data([0xFF, 0xD8] + Array(repeating: UInt8(0x0A), count: 4096) + [0xFF, 0xD9])
    server.preview.sendDroppable(Frame(kind: .preview, payload: jpegish))
    #expect(waitFor { !previews.isEmpty }, "no frame arrived on preview")
    #expect(previews.first?.payload == jpegish)
}

@Test("a wrong code does not get a session", .timeLimit(.minutes(1)))
func wrongCodeRejected() throws {
    let listener = PeerListener(
        name: "hoard-deny-\(UUID().uuidString.prefix(8))", code: PairingCode("111111")!)
    var accepted: PeerSession?
    var rejected: String?
    listener.onSession = { accepted = $0 }
    listener.onError = { rejected = $0 }
    try listener.start()
    defer { listener.stop() }

    let browser = PeerBrowser()
    let services = browser.browse(seconds: 4)
    let service = try #require(services.first { $0.name.hasPrefix("hoard-deny-") })

    let client = browser.connect(to: service, code: PairingCode("222222")!)
    defer { client.cancel() }

    // The gate. A peer that cannot prove it knows the code is dropped before it
    // reaches a session — the scanner auto-commits, so a stranger who can
    // connect can write to the collection.
    _ = waitFor(3) { accepted != nil }
    #expect(accepted == nil, "a peer with the wrong code was given a session")
    #expect(rejected != nil, "the wrong code was not reported as a failed pairing")
}

@Test("a proof is only valid for the session it was made for")
func proofBinding() {
    let code = PairingCode("424242")!
    let p = proof(session: "session-a", code: code)
    #expect(verifyProof(p, session: "session-a", code: code))
    // Replaying a captured proof against a different session must fail, or the
    // check is a password sent in the clear rather than a challenge.
    #expect(!verifyProof(p, session: "session-b", code: code))
    #expect(!verifyProof(p, session: "session-a", code: PairingCode("999999")!))
    #expect(!verifyProof("not base64 at all", session: "session-a", code: code))
    #expect(!verifyProof("", session: "session-a", code: code))
}
