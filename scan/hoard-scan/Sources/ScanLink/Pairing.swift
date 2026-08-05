// Pairing, and why it is not optional.
//
// The scanner auto-commits: a card it is confident about is written to the
// collection without asking. An unauthenticated listener on the local network
// is therefore a way for anyone on that network to inject cards into someone's
// database — not a dramatic exploit, but a real one, and the kind that is much
// harder to add a gate to later than now.
//
// So the link is TLS with a pre-shared key derived from a six-digit code shown
// on the phone. Six digits is not a lot of entropy; it is not meant to be. It
// is a gate against the coffee-shop network and the housemate's laptop, on a
// link that is usually a cable, and it is what a person will actually type. If
// this ever leaves a home network, replace the code — not the TLS.

import CryptoKit
import Foundation
import Network

/// A pairing code: six digits, shown on the phone, typed on the Mac.
public struct PairingCode: Equatable, Sendable {
    public let digits: String

    public init?(_ raw: String) {
        let cleaned = raw.filter(\.isNumber)
        guard cleaned.count == 6 else { return nil }
        digits = cleaned
    }

    /// A fresh code. `SystemRandomNumberGenerator` rather than anything seeded:
    /// a predictable pairing code is no gate at all.
    public static func random() -> PairingCode {
        var g = SystemRandomNumberGenerator()
        let n = UInt32.random(in: 0..<1_000_000, using: &g)
        return PairingCode(String(format: "%06u", n))!
    }

    /// Grouped for reading aloud, which is how this code is actually
    /// transmitted between a phone on a stand and a person at a keyboard.
    public var display: String {
        let mid = digits.index(digits.startIndex, offsetBy: 3)
        return "\(digits[digits.startIndex..<mid]) \(digits[mid...])"
    }

    /// The pre-shared key.
    ///
    /// Stretched rather than used raw: six digits is a million possibilities,
    /// and a key derived by one hash invocation is a key an attacker who
    /// captures a handshake can grind offline in seconds. HKDF with a fixed
    /// salt does not fix that — nothing fixes six digits against an offline
    /// attack — but it costs nothing and it means the key is not simply the
    /// code. The real protection is that TLS-PSK requires an *online* guess per
    /// attempt against a listener that is only up during a scanning session.
    var key: SymmetricKey {
        let salt = Data("dev.spiffcs.hoard.scan.pairing.v1".utf8)
        return HKDF<SHA256>.deriveKey(
            inputKeyMaterial: SymmetricKey(data: Data(digits.utf8)),
            salt: salt,
            outputByteCount: 32)
    }
}

/// proof is what a peer sends to show it knows the code, without sending it.
///
/// HMAC of the session id under the derived key. The session id is fresh per
/// connection attempt, so a captured proof is worth nothing against the next
/// one, and a peer who does not know the code cannot produce one.
///
/// **This authenticates; it does not encrypt.** Traffic on this link is
/// plaintext, and that is a deliberate, narrow decision rather than an
/// oversight. The risk being managed is a stranger on the network *writing to
/// the collection* — the scanner auto-commits, so an open listener is an
/// injection path. Card scans are not secret; nobody needs confidentiality for
/// "this is a Lightning Bolt". Authentication is the property that matters here
/// and this provides it.
///
/// TLS-PSK was built first and is not in this file because it did not work:
/// with `sec_protocol_options_add_pre_shared_key`, a TLS 1.3 ciphersuite and a
/// permissive verify block, both ends sat in `.connecting` forever — no error,
/// no timeout. Plain TCP over the identical code paths pairs in under a second,
/// which is what isolated it. Left as a known gap rather than half-shipped; see
/// docs/sprint-iphone-capture-head.md.
func proof(session: String, code: PairingCode) -> String {
    var mac = HMAC<SHA256>(key: code.key)
    mac.update(data: Data(session.utf8))
    return Data(mac.finalize()).base64EncodedString()
}

/// verifyProof checks a hello's proof in constant time.
///
/// `HMAC.isValidAuthenticationCode` rather than `==`: comparing MACs with a
/// short-circuiting equality leaks how much of the value was right through
/// timing, which is the standard way this check is quietly not a check.
func verifyProof(_ claimed: String, session: String, code: PairingCode) -> Bool {
    guard let bytes = Data(base64Encoded: claimed) else { return false }
    return HMAC<SHA256>.isValidAuthenticationCode(
        bytes, authenticating: Data(session.utf8), using: code.key)
}

/// The Bonjour service type. Nine characters, inside the fifteen the spec
/// allows, and specific enough not to collide with anything.
public let scanServiceType = "_hoardscan._tcp"

/// What each connection is for.
///
/// Two connections rather than one, and this is the reason: a 200 KB preview
/// JPEG queued ahead of a `scan` event is head-of-line blocking on exactly the
/// latency this design exists to protect. Control is small, ordered and never
/// dropped; preview is large, lossy and dropped the moment it would queue.
public enum PeerRole: String, Codable, Sendable {
    case control
    case preview
}

/// The first frame on every connection: which connection it is, which session
/// it belongs to, and proof that the sender knows the pairing code.
struct PeerHello: Codable {
    var role: PeerRole
    var session: String
    /// HMAC of `session` under the code's derived key. See proof().
    var proof: String
    /// What the far end is, for the Mac's device picker. Empty from the Mac.
    var name: String = ""
}
