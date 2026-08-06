// The developer-mode switch, and the one place its key is spelled.
//
// Two screens read it — the Pair tab owns the toggle, the scanning screen obeys
// it — and an `@AppStorage` key is a string, so spelling it twice is a silent
// way for the toggle to stop toggling anything.
//
// UserDefaults rather than the keychain, which is where the pairing code lives:
// this is a preference, not a secret. Nothing here changes what the app is
// allowed to do, only what it says while doing it.

import Foundation

enum DevMode {
    /// The `@AppStorage` key. Off unless it has been turned on: the scanning
    /// screen's job is a price, and everything this reveals is a readout for
    /// someone tuning the rig rather than working through a box.
    static let key = "developerMode"
}
