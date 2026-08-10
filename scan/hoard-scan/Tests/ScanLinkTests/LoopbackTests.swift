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

/// Opens one half of a session, correctly proved.
///
/// `PeerBrowser.connect` always opens both, which is right for the Mac and
/// useless for testing the interval between them — the whole point here is a
/// listener holding a verified connection whose partner has not come. Same body
/// as `connect`'s inner `open`, one role at a time.
private func openHalf(
    to service: PeerService, role: PeerRole, code: PairingCode, session: String
) -> PeerLink {
    let conn = NWConnection(to: service.endpoint, using: parameters(role: role))
    let link = PeerLink(
        connection: conn, role: role, queue: DispatchQueue(label: "hoard-scan.test.half"))
    link.start()
    let hello = PeerHello(
        role: role, session: session, proof: proof(session: session, code: code))
    if let frame = Frame.json(hello) { link.send(frame) }
    return link
}

/// Finds a listener that has just started advertising, by name prefix.
private func resolve(prefix: String) throws -> PeerService {
    let services = PeerBrowser().browse(seconds: 4)
    return try #require(
        services.first { $0.name.hasPrefix(prefix) },
        "the listener never appeared on the network; saw \(services.map(\.name))")
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

@Test("a control connection is reported the moment it proves the code",
      .timeLimit(.minutes(1)))
func verifiedBeforePartner() throws {
    let code = PairingCode.random()
    let listener = PeerListener(name: "hoard-early-\(UUID().uuidString.prefix(8))", code: code)

    var verified: [PeerRole] = []
    var accepted: PeerSession?
    var lost = false
    listener.onPeerVerified = { verified.append($0) }
    listener.onPeerLost = { lost = true }
    listener.onSession = { accepted = $0 }
    try listener.start()
    defer { listener.stop() }

    let service = try resolve(prefix: "hoard-early-")
    let control = openHalf(to: service, role: .control, code: code, session: UUID().uuidString)
    defer { control.cancel() }

    // The point of the whole change: this fires without the preview connection
    // ever existing. If it needed both, the phone's screen would go on saying
    // "Not connected" for a second TCP connect and round trip.
    #expect(waitFor { verified == [.control] },
            "a verified control connection was not reported; saw \(verified)")
    // And it is genuinely early — the session is still half-assembled.
    #expect(accepted == nil, "a session was handed over with only one connection")
    #expect(!lost, "the half-session was dropped before its timeout")
}

@Test("a verified connection whose partner never arrives is dropped",
      .timeLimit(.minutes(1)))
func halfSessionTimesOut() throws {
    let code = PairingCode.random()
    let listener = PeerListener(name: "hoard-half-\(UUID().uuidString.prefix(8))", code: code)
    // Short enough to test, long enough not to race the hello it is waiting on.
    listener.halfSessionTimeout = 0.5

    var lost = false
    var accepted: PeerSession?
    listener.onPeerLost = { lost = true }
    listener.onSession = { accepted = $0 }
    try listener.start()
    defer { listener.stop() }

    let service = try resolve(prefix: "hoard-half-")
    let control = openHalf(to: service, role: .control, code: code, session: UUID().uuidString)
    defer { control.cancel() }

    // Claiming a session on one connection means being able to give it back.
    // Without this the phone shows a green "hoard connected" over a link that
    // was never assembled, which is worse than the delay this all removes.
    #expect(waitFor { lost }, "a half-session was never dropped")
    #expect(accepted == nil)
    // Dropped means dropped: the parked connection is cancelled, not merely
    // forgotten. Forgetting it is the leak that was already here.
    #expect(waitFor { control.state == .cancelled || isFailed(control.state) },
            "the parked connection was left open; it is \(control.state)")
}

private func isFailed(_ state: PeerState) -> Bool {
    if case .failed = state { return true }
    return false
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

@Test("the restart schedule tries three times and then stops")
func restartScheduleIsBounded() {
    // Immediate, then spaced. The first attempt answers the common death — a
    // blink of the network — while the operator is still holding the card.
    #expect(PeerListener.restartDelay(after: 0) == 0)
    #expect(PeerListener.restartDelay(after: 1) == 1)
    #expect(PeerListener.restartDelay(after: 2) == 4)
    // And then it gives up, which is the half that has to be right. A schedule
    // with no end is a loop against a permanent failure: the battery goes, and
    // every attempt briefly looks like recovery to anything watching.
    #expect(PeerListener.restartDelay(after: 3) == nil)
    #expect(PeerListener.restartDelay(after: 99) == nil)
    #expect(PeerListener.restartDelay(after: -1) == nil)
}

@Test("a listener that dies is restarted and is reachable again",
      .timeLimit(.minutes(1)))
func deadListenerComesBack() throws {
    let code = PairingCode.random()
    let name = "hoard-revive-\(UUID().uuidString.prefix(8))"
    let listener = PeerListener(name: name, code: code)

    var sessions: [PeerSession] = []
    var health: [PeerListener.Advertisement] = []
    listener.onSession = { sessions.append($0) }
    listener.onAdvertisement = { health.append($0) }
    try listener.start()
    defer { listener.stop() }
    #expect(waitFor { health.contains(.up) },
            "the listener never reported that it was advertising")

    // The death, made real, in two halves — and both halves are needed.
    //
    // Cancelling the NWListener is what actually takes the phone off the air;
    // injecting the state is the report the framework would have made, which
    // is the one thing a test cannot arrange (an NWListener does not fail to
    // order). Injecting alone would prove nothing: the original listener would
    // still be advertising underneath, so the connect below would succeed with
    // no fix present at all.
    //
    // Measured against the fix removed — `recover` cut back to the `onError`
    // line it replaced — this fails on the restart expectation and then again
    // on the browse, which saw no service at all. The reachability check below
    // is still the one that has to be there: a cancelled listener is served
    // from the mDNS cache often enough that visibility alone has passed for a
    // phone that was answering nothing (see TrustedSessionTests' reconnect
    // test, where it was measured doing exactly that).
    health.removeAll()
    listener.listener?.cancel()
    listener.listenerStateChanged(.failed(.posix(.ENETDOWN)))

    #expect(waitFor { health.contains(.up) },
            "the listener was never brought back; saw \(health)")
    #expect(!health.contains(where: { if case .down = $0 { return true } else { return false } }),
            "a recovered death was reported as unrecoverable")

    // Reachable, not merely visible. A fresh browser, because PeerBrowser
    // accumulates into `found` and never removes.
    let service = try resolve(prefix: "hoard-revive-")
    let client = PeerBrowser().connect(to: service, code: code)
    defer { client.cancel() }
    #expect(waitFor { sessions.count == 1 }, """
        the restarted listener could not be reached
          client now: control \(client.control.state), preview \(client.preview.state)
        """)
}

@Test("a listener taken down by the app is not restarted underneath it",
      .timeLimit(.minutes(1)))
func stopWinsOverAPendingRestart() throws {
    let name = "hoard-stopwin-\(UUID().uuidString.prefix(8))"
    let listener = PeerListener(name: name, code: PairingCode.random())
    var health: [PeerListener.Advertisement] = []
    listener.onAdvertisement = { health.append($0) }
    try listener.start()
    #expect(waitFor { health.contains(.up) }, "the listener never advertised")

    // A death and an immediate `stop()` — "Forget all Macs" landing in the same
    // instant as a failure. The restart is already scheduled at this point, and
    // a listener that came back after the app deliberately took it down would
    // be advertising a code the app has already rotated away from.
    health.removeAll()
    listener.listenerStateChanged(.failed(.posix(.ENETDOWN)))
    listener.stop()

    _ = waitFor(2) { health.contains(.up) }
    #expect(!health.contains(.up), "a stopped listener put itself back on the network")
    #expect(listener.listener == nil, "a stopped listener rebuilt itself")
}
