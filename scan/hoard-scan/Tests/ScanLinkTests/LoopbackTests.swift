import CryptoKit
import Foundation
import Network
import ScanWire
import Testing

@testable import ScanLink

private func waitFor(_ seconds: Double = 5, _ check: () -> Bool) -> Bool {
    let deadline = Date().addingTimeInterval(seconds)
    while !check(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
    }
    return check()
}

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

    #expect(server.control.role == .control)
    #expect(server.preview.role == .preview)

    var received: [Frame] = []
    client.control.onFrame = { received.append($0) }
    let event = Event(event: "ready", device: "loopback", features: ["auto"])
    server.control.send(try #require(Frame.json(event)))
    #expect(waitFor { !received.isEmpty }, "no frame arrived on control")
    #expect(received.first?.kind == .ndjson)
    #expect(received.first?.text?.contains("\"device\":\"loopback\"") == true)

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

    #expect(waitFor { verified == [.control] },
            "a verified control connection was not reported; saw \(verified)")
    #expect(accepted == nil, "a session was handed over with only one connection")
    #expect(!lost, "the half-session was dropped before its timeout")
}

@Test("a verified connection whose partner never arrives is dropped",
      .timeLimit(.minutes(1)))
func halfSessionTimesOut() throws {
    let code = PairingCode.random()
    let listener = PeerListener(name: "hoard-half-\(UUID().uuidString.prefix(8))", code: code)
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

    #expect(waitFor { lost }, "a half-session was never dropped")
    #expect(accepted == nil)
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
    #expect(!verifyProof(p, session: "session-b", code: code))
    #expect(!verifyProof(p, session: "session-a", code: PairingCode("999999")!))
    #expect(!verifyProof("not base64 at all", session: "session-a", code: code))
    #expect(!verifyProof("", session: "session-a", code: code))
}

@Test("the restart schedule tries three times and then stops")
func restartScheduleIsBounded() {
    #expect(PeerListener.restartDelay(after: 0) == 0)
    #expect(PeerListener.restartDelay(after: 1) == 1)
    #expect(PeerListener.restartDelay(after: 2) == 4)
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

    health.removeAll()
    listener.listener?.cancel()
    listener.listenerStateChanged(.failed(.posix(.ENETDOWN)))

    #expect(waitFor { health.contains(.up) },
            "the listener was never brought back; saw \(health)")
    #expect(!health.contains(where: { if case .down = $0 { return true } else { return false } }),
            "a recovered death was reported as unrecoverable")

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

    health.removeAll()
    listener.listenerStateChanged(.failed(.posix(.ENETDOWN)))
    listener.stop()

    _ = waitFor(2) { health.contains(.up) }
    #expect(!health.contains(.up), "a stopped listener put itself back on the network")
    #expect(listener.listener == nil, "a stopped listener rebuilt itself")
}
