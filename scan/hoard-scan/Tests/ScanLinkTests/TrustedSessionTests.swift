import CryptoKit
import Foundation
import Network
import ScanWire
import Testing

@testable import ScanLink

private func waitUntil(_ seconds: Double = 6, _ check: () -> Bool) -> Bool {
    let deadline = Date().addingTimeInterval(seconds)
    while !check(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
    }
    return check()
}

@Suite(.serialized)
struct TrustedSessionSuite {
    private func scratch(_ name: String) throws -> (PeerIdentity, PinnedPeers, String) {
        let service = "dev.spiffcs.hoard.scan.test.\(name).\(UUID().uuidString.prefix(8))"
        let identity = try loadOrCreatePeerIdentity(service: service)
        return (identity, PinnedPeers(service: service + ".pins"), service)
    }

    @Test("a session establishes over TLS and pins the peer", .timeLimit(.minutes(1)))
    func trustedSessionPairs() throws {
        let (phoneID, phonePins, phoneService) = try scratch("phone")
        let (macID, _, macService) = try scratch("mac")
        defer {
            phonePins.forgetAll()
            deletePeerIdentity(service: phoneService)
            deletePeerIdentity(service: macService)
        }

        let code = PairingCode.random()
        let listener = PeerListener(
            name: "hoard-tls-\(UUID().uuidString.prefix(8))", code: code,
            trust: PeerTrust(identity: phoneID, pinned: [], acceptUnknown: true),
            pins: phonePins)

        var accepted: PeerSession?
        var listenerError: String?
        listener.onSession = { accepted = $0 }
        listener.onError = { listenerError = $0 }
        try listener.start()
        defer { listener.stop() }

        let browser = PeerBrowser()
        let services = browser.browse(seconds: 4)
        let service = try #require(
            services.first { $0.name.hasPrefix("hoard-tls-") },
            "the TLS listener never advertised; saw \(services.map(\.name))")

        let client = browser.connect(
            to: service, code: code,
            trust: PeerTrust(identity: macID, pinned: [], acceptUnknown: true))
        defer { client.cancel() }

        #expect(waitUntil { accepted != nil }, """
            no session paired over TLS
              listener error: \(listenerError ?? "none")
              client now: control \(client.control.state), preview \(client.preview.state)
            """)
        let server = try #require(accepted)

        #expect(server.control.peerFingerprint == macID.fingerprint)
        #expect(client.control.peerFingerprint == phoneID.fingerprint)

        #expect(phonePins.all.contains(macID.fingerprint),
                "the paired peer was not pinned; store holds \(phonePins.all.count) entries")

        var received: [Frame] = []
        client.control.onFrame = { received.append($0) }
        server.control.send(try #require(
            Frame.json(Event(event: "ready", device: "tls-loopback", features: ["auto"]))))
        #expect(waitUntil { !received.isEmpty }, "no frame arrived on an encrypted control link")
        #expect(received.first?.text?.contains("\"device\":\"tls-loopback\"") == true)
    }

    @Test("a pinned pair connects with pairing closed", .timeLimit(.minutes(1)))
    func pinnedPairNeedsNoLeniency() throws {
        let (phoneID, phonePins, phoneService) = try scratch("phone2")
        let (macID, _, macService) = try scratch("mac2")
        defer {
            phonePins.forgetAll()
            deletePeerIdentity(service: phoneService)
            deletePeerIdentity(service: macService)
        }

        let code = PairingCode.random()
        let listener = PeerListener(
            name: "hoard-pin-\(UUID().uuidString.prefix(8))", code: code,
            trust: PeerTrust(
                identity: phoneID, pinned: [macID.fingerprint], acceptUnknown: false),
            pins: phonePins)

        var accepted: PeerSession?
        listener.onSession = { accepted = $0 }
        try listener.start()
        defer { listener.stop() }

        let browser = PeerBrowser()
        let service = try #require(
            browser.browse(seconds: 4).first { $0.name.hasPrefix("hoard-pin-") })
        let client = browser.connect(
            to: service, code: code,
            trust: PeerTrust(
                identity: macID, pinned: [phoneID.fingerprint], acceptUnknown: false))
        defer { client.cancel() }

        #expect(waitUntil { accepted != nil },
                "a mutually pinned pair could not connect with pairing closed")
    }

    @Test("a pinned peer connects after the code has rotated", .timeLimit(.minutes(1)))
    func rotatedCodeDoesNotBreakPinnedPeer() throws {
        let (phoneID, phonePins, phoneService) = try scratch("phoneRot")
        let (macID, _, macService) = try scratch("macRot")
        defer {
            phonePins.forgetAll()
            deletePeerIdentity(service: phoneService)
            deletePeerIdentity(service: macService)
        }

        phonePins.pin(macID.fingerprint, name: "mac")
        let rotated = PairingCode.random()
        let stale = PairingCode.random()
        #expect(rotated != stale, "the two codes must differ for this test to mean anything")

        let listener = PeerListener(
            name: "hoard-rot-\(UUID().uuidString.prefix(8))", code: rotated,
            trust: PeerTrust(
                identity: phoneID, pinned: { phonePins.all }, acceptUnknown: { false }),
            pins: phonePins)

        var accepted: PeerSession?
        var listenerError: String?
        listener.onSession = { accepted = $0 }
        listener.onError = { listenerError = $0 }
        try listener.start()
        defer { listener.stop() }

        let browser = PeerBrowser()
        let service = try #require(
            browser.browse(seconds: 4).first { $0.name.hasPrefix("hoard-rot-") })
        let client = browser.connect(
            to: service, code: stale,
            trust: PeerTrust(
                identity: macID, pinned: [phoneID.fingerprint], acceptUnknown: false))
        defer { client.cancel() }

        #expect(waitUntil { accepted != nil }, """
            a pinned peer was refused after the code rotated
              listener error: \(listenerError ?? "none")
            """)
    }

    @Test("a paired peer reconnects after a session ends", .timeLimit(.minutes(1)))
    func pairedPeerReconnectsAfterSessionEnds() throws {
        let (phoneID, phonePins, phoneService) = try scratch("phoneRe")
        let (macID, _, macService) = try scratch("macRe")
        defer {
            phonePins.forgetAll()
            deletePeerIdentity(service: phoneService)
            deletePeerIdentity(service: macService)
        }

        phonePins.pin(macID.fingerprint, name: "mac")
        let name = "hoard-re-\(UUID().uuidString.prefix(8))"
        let listener = PeerListener(
            name: name, code: PairingCode.random(),
            trust: PeerTrust(
                identity: phoneID, pinned: { phonePins.all }, acceptUnknown: { false }),
            pins: phonePins)

        var sessions: [PeerSession] = []
        var listenerError: String?
        listener.onSession = { sessions.append($0) }
        listener.onError = { listenerError = $0 }
        try listener.start()
        defer { listener.stop() }

        let macTrust = PeerTrust(
            identity: macID, pinned: [phoneID.fingerprint], acceptUnknown: false)

        let first = try #require(
            PeerBrowser().browse(seconds: 4).first { $0.name == name },
            "the listener never advertised at all")
        let one = PeerBrowser().connect(to: first, code: PairingCode.random(), trust: macTrust)
        #expect(waitUntil { sessions.count == 1 }, """
            the first session never established
              listener error: \(listenerError ?? "none")
            """)

        sessions.first?.cancel()
        one.cancel()

        let stillThere = PeerBrowser().browse(seconds: 4)
        let second = try #require(
            stillThere.first { $0.name == name },
            """
            the phone stopped advertising when its session ended, so no Mac \
            could find it again without re-pairing; saw \(stillThere.map(\.name))
            """)

        let two = PeerBrowser().connect(to: second, code: PairingCode.random(), trust: macTrust)
        defer { two.cancel() }
        #expect(waitUntil { sessions.count == 2 }, """
            a previously paired Mac could not re-establish a session
              listener error: \(listenerError ?? "none")
              client now: control \(two.control.state), preview \(two.preview.state)
            """)

        #expect(phonePins.all == [macID.fingerprint],
                "reconnecting changed the trust store; it holds \(phonePins.all.count) entries")
    }

    @Test("a stranger cannot complete TLS once pairing is closed", .timeLimit(.minutes(1)))
    func strangerRefusedWhenClosed() throws {
        let (phoneID, phonePins, phoneService) = try scratch("phone3")
        let (macID, _, macService) = try scratch("mac3")
        let (strangerID, _, strangerService) = try scratch("stranger")
        defer {
            phonePins.forgetAll()
            deletePeerIdentity(service: phoneService)
            deletePeerIdentity(service: macService)
            deletePeerIdentity(service: strangerService)
        }

        let code = PairingCode.random()
        let listener = PeerListener(
            name: "hoard-deny-\(UUID().uuidString.prefix(8))", code: code,
            trust: PeerTrust(
                identity: phoneID, pinned: [macID.fingerprint], acceptUnknown: false),
            pins: phonePins)

        var accepted: PeerSession?
        listener.onSession = { accepted = $0 }
        try listener.start()
        defer { listener.stop() }

        let browser = PeerBrowser()
        let service = try #require(
            browser.browse(seconds: 4).first { $0.name.hasPrefix("hoard-deny-") })

        let stranger = browser.connect(
            to: service, code: code,
            trust: PeerTrust(identity: strangerID, pinned: [], acceptUnknown: true))
        defer { stranger.cancel() }

        _ = waitUntil(4) { accepted != nil }
        #expect(accepted == nil, "an unpinned stranger with the right code was given a session")
        #expect(!phonePins.all.contains(strangerID.fingerprint),
                "a refused stranger was pinned anyway")
    }

    @Test("a wrong code is still refused over TLS", .timeLimit(.minutes(1)))
    func wrongCodeRefusedOverTLS() throws {
        let (phoneID, phonePins, phoneService) = try scratch("phone4")
        let (macID, _, macService) = try scratch("mac4")
        defer {
            phonePins.forgetAll()
            deletePeerIdentity(service: phoneService)
            deletePeerIdentity(service: macService)
        }

        let listener = PeerListener(
            name: "hoard-wrong-\(UUID().uuidString.prefix(8))", code: PairingCode("111111")!,
            trust: PeerTrust(identity: phoneID, pinned: [], acceptUnknown: true),
            pins: phonePins)

        var accepted: PeerSession?
        var rejected: String?
        listener.onSession = { accepted = $0 }
        listener.onError = { rejected = $0 }
        try listener.start()
        defer { listener.stop() }

        let browser = PeerBrowser()
        let service = try #require(
            browser.browse(seconds: 4).first { $0.name.hasPrefix("hoard-wrong-") })
        let client = browser.connect(
            to: service, code: PairingCode("222222")!,
            trust: PeerTrust(identity: macID, pinned: [], acceptUnknown: true))
        defer { client.cancel() }

        _ = waitUntil(4) { accepted != nil }
        #expect(accepted == nil, "a peer with the wrong code was given a session over TLS")
        #expect(rejected != nil, "the wrong code was not reported as a failed pairing")
        #expect(!phonePins.all.contains(macID.fingerprint),
                "a peer that failed the pairing check was pinned anyway")
    }
}
