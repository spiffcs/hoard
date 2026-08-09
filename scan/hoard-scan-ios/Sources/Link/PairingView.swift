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

                // The code is shown only while pairing is actually open.
                //
                // It used to be permanent and always on screen, because it was
                // the only thing identifying a Mac. Now a paired Mac is
                // recognised by the certificate it pinned, so the code is a
                // one-time introduction token: generated per launch, closed
                // the moment a Mac pairs, and replaced whenever this window is
                // reopened. Leaving a used code on screen would suggest it
                // still does something, which is exactly the belief that makes
                // a six-digit secret dangerous.
                if link.pairingOpen {
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
                            + "It is used once, to introduce a Mac — after that the two "
                            + "recognise each other and the code is replaced.")
                    }
                } else {
                    Section {
                        Label(
                            link.pairedCount == 1
                                ? "1 Mac paired"
                                : "\(link.pairedCount) Macs paired",
                            systemImage: "checkmark.shield.fill")
                            .foregroundStyle(.green)
                        Button("Add a Mac") { link.newCode() }
                    } header: {
                        Text("Pairing")
                    } footer: {
                        Text("Paired Macs connect on their own — no code needed. "
                            + "Adding one shows a fresh code for as long as it takes to use it.")
                    }
                }

                if !link.encrypted {
                    // Never silent. A downgrade that nobody is told about is
                    // the failure this whole design exists to remove, and the
                    // one case that can reach it — the keychain unavailable
                    // before first unlock — is recoverable by the person
                    // holding the phone.
                    Section {
                        Label("Link is not encrypted", systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                    } footer: {
                        Text("This phone could not reach its keychain, so it could not present "
                            + "its certificate. Unlock the phone and reopen Hoardling.")
                    }
                }

                Section {
                    Button("Forget all Macs", role: .destructive) {
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
                    // indistinguishable from a phone that is not there. The
                    // app restarts its advertising on every return to the
                    // foreground, so the second sentence is the fix.
                    Text("iOS stops background apps from being discovered, so a phone "
                        + "you have switched away from looks the same as one that is off. "
                        + "Bringing Hoardling back on screen puts it back on the network.")
                }
            }
            .navigationTitle("Pair")
            .confirmationDialog(
                "Forget all Macs?", isPresented: $confirmingNewCode, titleVisibility: .visible
            ) {
                Button("Forget", role: .destructive) { link.forgetMacs() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("Any Mac already paired with this phone will stop working "
                    + "until you pair it again.")
            }
        }
    }
}
