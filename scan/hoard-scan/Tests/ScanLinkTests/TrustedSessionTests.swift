// The whole link, over TLS, with real identities and real pinning.
//
// LoopbackTests covers the same ground on a plaintext link, and deliberately
// still does: those tests are about framing, role assignment and half-session
// timeouts, and running them over TLS would only make them slower and their
// failures harder to read. These are the ones about identity.
//
// What is actually being proved here, in order of how badly each would fail:
//
//   1. a session establishes over TLS at all — which exercises the deferred
//      hello, since a fingerprint-bound proof cannot be built until the
//      handshake has settled;
//   2. the listener pins the peer it just paired with, so the second
//      connection needs no leniency;
//   3. a pinned pair connects with `acceptUnknown: false` on both ends — the
//      steady state, where an unknown certificate cannot even finish TLS;
//   4. the pairing proof still gates, over TLS, with the right code and the
//      wrong one.
//
// Serialized for the same reason PeerIdentitySuite is: these mint keychain
// items, and the legacy keychain does not take concurrent writes from one
// process.

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

    /// A scratch identity plus its pin store, torn down after each test.
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
        // Pairing is open on both ends: neither has seen the other before, so
        // TLS cannot be the gate yet. The proof is.
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

        // Each end actually saw a certificate — a nil here would mean the link
        // silently fell back to plaintext and every assertion above passed for
        // the wrong reason.
        #expect(server.control.peerFingerprint == macID.fingerprint)
        #expect(client.control.peerFingerprint == phoneID.fingerprint)

        // Trust on first use: the listener recorded the Mac.
        #expect(phonePins.all.contains(macID.fingerprint),
                "the paired peer was not pinned; store holds \(phonePins.all.count) entries")

        // And traffic still flows, which is the point of all of it.
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

        // The steady state: each end already knows the other, so neither will
        // complete a handshake with anything else. This is what every session
        // after the first looks like, and the code below is incidental to it —
        // it is still checked, but nothing depends on its strength any more.
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

        // Already paired, and the phone has since rotated its code — which is
        // now what happens on every launch and after every pairing. The Mac
        // still holds the old one, because nothing tells it otherwise.
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

        // The whole reason the code could not rotate before. A pinned peer is
        // not asked for it, so a stale code on the Mac is simply irrelevant —
        // and if this ever regresses, every existing pairing breaks on the
        // next launch and reports as a network fault.
        #expect(waitUntil { accepted != nil }, """
            a pinned peer was refused after the code rotated
              listener error: \(listenerError ?? "none")
            """)
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

        // The stranger has the right code. Under the old design that was the
        // whole gate and this would pair; under pinning the handshake never
        // completes, so the code never even gets offered. That is the entire
        // point of the change, stated as a test.
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
        // And crucially it was not pinned: pairing open means TLS lets it in,
        // so the proof is the only thing standing between a stranger and a
        // permanent entry in the trust store.
        #expect(!phonePins.all.contains(macID.fingerprint),
                "a peer that failed the pairing check was pinned anyway")
    }
}
