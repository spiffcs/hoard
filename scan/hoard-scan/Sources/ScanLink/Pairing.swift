import CryptoKit
import Foundation
import Network

public struct PairingCode: Equatable, Sendable {
    public let digits: String

    public init?(_ raw: String) {
        let cleaned = raw.filter(\.isNumber)
        guard cleaned.count == 6 else { return nil }
        digits = cleaned
    }

    public static func random() -> PairingCode {
        var g = SystemRandomNumberGenerator()
        let n = UInt32.random(in: 0..<1_000_000, using: &g)
        return PairingCode(String(format: "%06u", n))!
    }

    public var display: String {
        let mid = digits.index(digits.startIndex, offsetBy: 3)
        return "\(digits[digits.startIndex..<mid]) \(digits[mid...])"
    }

    var key: SymmetricKey {
        let salt = Data("dev.spiffcs.hoard.scan.pairing.v1".utf8)
        return HKDF<SHA256>.deriveKey(
            inputKeyMaterial: SymmetricKey(data: Data(digits.utf8)),
            salt: salt,
            outputByteCount: 32)
    }
}

func proof(session: String, code: PairingCode, peerFingerprint: Data? = nil) -> String {
    var mac = HMAC<SHA256>(key: code.key)
    mac.update(data: Data(session.utf8))
    if let peerFingerprint {
        mac.update(data: Data([0x00]))
        mac.update(data: peerFingerprint)
    }
    return Data(mac.finalize()).base64EncodedString()
}

func verifyProof(
    _ claimed: String, session: String, code: PairingCode, ownFingerprint: Data? = nil
) -> Bool {
    guard let bytes = Data(base64Encoded: claimed) else { return false }
    var authenticated = Data(session.utf8)
    if let ownFingerprint {
        authenticated.append(0x00)
        authenticated.append(ownFingerprint)
    }
    return HMAC<SHA256>.isValidAuthenticationCode(
        bytes, authenticating: authenticated, using: code.key)
}

public let scanServiceType = "_hoardscan._tcp"

public enum PeerRole: String, Codable, Sendable {
    case control
    case preview
}

struct PeerHello: Codable {
    var role: PeerRole
    var session: String
    var proof: String
    var name: String = ""
}
