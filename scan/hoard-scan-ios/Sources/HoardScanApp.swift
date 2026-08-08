// The iPhone capture head.
//
// The iOS read pipeline is a rewrite rather than a port of the macOS one. The
// two share only the wire contract with the Go side; everything else — capture,
// card finding, parsing, the trigger — is being built against what the phone can
// actually do, rather than carried over from a pipeline fitted to a 1080p webcam
// with no focus, exposure or white balance control.
//
// At this stage the app is still an instrument, and each tab is a question:
//
// Three screens, and no more than three. The instruments that answered the
// earlier questions — does a 24 MP frame read a collector number, what will
// this hardware let me do, does iOS Vision read the fixtures differently —
// have all been answered and removed. What is left is what someone scanning a
// box of cards needs: the camera, the one-time pairing, and the knobs for the
// sounds they will spend a session listening to.
//
//   Scan       the camera, the price, and the trigger
//   Pair       the six digits, once per Mac
//   Settings   the tier lines and their voices

import SwiftUI

@main
struct HoardScanApp: App {
    /// Owned by the app, not by the Scan tab.
    ///
    /// It lived on the view first, and that was wrong in a way that looked like
    /// a network fault: the listener started and stopped with the tab, so
    /// switching to Camera or Fixtures took the phone off the network, and the
    /// Mac's picker silently showed one device instead of two. Advertising is a
    /// property of the app being open, not of which screen is showing.
    @StateObject private var link = LinkController()

    /// Which tab is showing. Bound rather than left to the TabView because a
    /// connection is a reason to change screens: pairing ends on the Pair tab,
    /// and without this you sit looking at a code you no longer need while the
    /// camera runs somewhere you cannot see.
    @State private var tab = Tab.scan

    private enum Tab: Hashable { case scan, pair, settings }

    var body: some Scene {
        WindowGroup {
            TabView(selection: $tab) {
                SessionView(link: link)
                    .tabItem { Label("Scan", systemImage: "wave.3.right") }
                    .tag(Tab.scan)
                PairingView(link: link)
                    .tabItem { Label("Pair", systemImage: "link") }
                    .tag(Tab.pair)
                SettingsView(sounds: link.sounds)
                    .tabItem { Label("Settings", systemImage: "gearshape") }
                    .tag(Tab.settings)
            }
            // Only on connect, never on disconnect. Being thrown out of the
            // scanning screen because the link blipped mid-box would be worse
            // than staying put — a dropped link recovers on its own, and the
            // Scan tab says so.
            .onChange(of: link.connected) { _, connected in
                if connected { tab = .scan }
            }
            // Advertise from launch. The Mac's picker only offers a choice when
            // it can see more than one source, so a phone that has not started
            // listening yet is indistinguishable from no phone at all.
            .task { link.start() }
        }
    }
}
