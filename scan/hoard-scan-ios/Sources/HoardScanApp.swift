import SwiftUI

@main
struct HoardScanApp: App {
    @StateObject private var link = LinkController()

    @State private var tab = Tab.scan

    @Environment(\.scenePhase) private var scenePhase

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
            .onChange(of: link.connected) { _, connected in
                if connected { tab = .scan }
            }
            .task { link.start() }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active { link.start() }
            }
        }
    }
}
