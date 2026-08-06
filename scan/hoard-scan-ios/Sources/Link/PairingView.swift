// Setting the phone up with a Mac.
//
// Its own screen rather than a corner of the scanning one. The code is needed
// once per Mac and then never again, so keeping it over the camera would put a
// six-digit secret on screen for every card scanned afterwards — and the
// scanning screen has exactly one job, which is showing a price.
//
// One tap from the root, though. Pairing is the first thing anyone does with
// this app, and burying it two levels down would make the common first
// experience a hunt.

import SwiftUI

struct PairingView: View {
    @ObservedObject var link: LinkController
    @State private var confirmingNewCode = false
    @AppStorage(DevMode.key) private var developerMode = false

    var body: some View {
        NavigationStack {
            List {
                Section {
                    if link.connected {
                        Label("Connected to hoard", systemImage: "checkmark.circle.fill")
                            .foregroundStyle(.green)
                    } else {
                        Label(link.status, systemImage: "antenna.radiowaves.left.and.right")
                            .foregroundStyle(.secondary)
                    }
                } header: {
                    Text("Status")
                }

                Section {
                    Text(link.code.display)
                        .font(.system(size: 46, weight: .heavy, design: .monospaced))
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 4)
                        .textSelection(.enabled)
                } header: {
                    Text("Pairing code")
                } footer: {
                    Text("In hoard, press ctrl+p at the card prompt and type this code. "
                        + "It is only needed once per Mac.")
                }

                Section {
                    Button("Generate a new code", role: .destructive) {
                        confirmingNewCode = true
                    }
                } footer: {
                    Text("Stops every Mac that is already paired from connecting. "
                        + "Use this to revoke one.")
                }

                Section {
                    Toggle("Developer mode", isOn: $developerMode)
                    // Inside the toggle rather than beside it. The log is for
                    // the same person the readouts are for, and a share sheet
                    // on the setup screen of an app that scans cards is one
                    // more row to read past for everyone else.
                    if developerMode {
                        ShareLink("Share session log", item: SessionLog.fileURL)
                    }
                } header: {
                    Text("Developer")
                } footer: {
                    Text("Shows the trigger's state and the last card read on the "
                        + "scanning screen. Off, that screen shows a price and "
                        + "nothing else.")
                }

                Section {
                    Label("Keep this app open and on screen while scanning.",
                          systemImage: "iphone")
                    Label("Both devices need the same Wi-Fi, or a cable.",
                          systemImage: "wifi")
                } header: {
                    Text("If hoard cannot find this phone")
                } footer: {
                    // The failure this actually causes, stated plainly: iOS
                    // suspends background apps, and a suspended app stops
                    // advertising, so a phone switched away from is
                    // indistinguishable from a phone that is not there.
                    Text("iOS stops background apps from being discovered, so a phone "
                        + "you have switched away from looks the same as one that is off.")
                }
            }
            .navigationTitle("Pair")
            .confirmationDialog(
                "Generate a new code?", isPresented: $confirmingNewCode, titleVisibility: .visible
            ) {
                Button("Generate", role: .destructive) { link.newCode() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("Any Mac already paired with this phone will stop working "
                    + "until you pair it again.")
            }
        }
    }
}
