// Where the pairing code lives between launches.
//
// The keychain rather than UserDefaults. The code is the only thing standing
// between a stranger on the network and write access to someone's collection —
// the scanner auto-commits — and UserDefaults is a plist in the app container
// that any backup, any file-sharing browse, and any device dump reads in the
// clear. The keychain is not a lot of extra work and it is the right shelf.
//
// `kSecAttrAccessibleAfterFirstUnlock` rather than the default: the app is meant
// to sit in a stand and be driven from a Mac, and a locked phone that cannot
// read its own pairing code would fail to advertise for reasons nobody could
// see from the terminal.

import Foundation
import ScanLink
import Security

enum PairingStore {
    private static let service = "dev.spiffcs.hoard.scan.ios"
    private static let account = "pairing-code"

    static func load() -> PairingCode? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data,
              let text = String(data: data, encoding: .utf8)
        else { return nil }
        return PairingCode(text)
    }

    /// save replaces any existing code. Delete-then-add rather than an update,
    /// because `SecItemUpdate` on an absent item fails and the two-call dance to
    /// find out is longer than just doing this.
    static func save(_ code: PairingCode) {
        let base: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(base as CFDictionary)
        var add = base
        add[kSecValueData as String] = Data(code.digits.utf8)
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }
}
