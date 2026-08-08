// The Settings tab: the tier lines and their voices.
//
// One section per price tier, top down the way the money reads — jackpot
// first would be the casino's order, but a person setting this up thinks in
// "everything under a dollar" first, so bulk leads.
//
// Every voice change plays itself. A sound picked silently is a sound
// discovered mid-box, and the whole point of the tiers is knowing them by ear
// before the box starts.

import SwiftUI

struct SettingsView: View {
    @ObservedObject private var settings = TierSettings.shared
    /// The link's player, borrowed for previews so a picked voice sounds
    /// exactly as it will in a session — same engine, same buffers.
    let sounds: Sounds

    var body: some View {
        NavigationStack {
            Form {
                TierSection(
                    title: "Bulk", subtitle: "under \(dollars(settings.winAt))",
                    voice: $settings.bulkVoice, threshold: nil, sounds: sounds)
                TierSection(
                    title: "Win", subtitle: "\(dollars(settings.winAt)) and up",
                    voice: $settings.winVoice, threshold: $settings.winAt,
                    sounds: sounds)
                TierSection(
                    title: "Big Win", subtitle: "\(dollars(settings.bigAt)) and up",
                    voice: $settings.bigVoice, threshold: $settings.bigAt,
                    sounds: sounds)
                TierSection(
                    title: "Jackpot", subtitle: "\(dollars(settings.jackpotAt)) and up",
                    voice: $settings.jackpotVoice, threshold: $settings.jackpotAt,
                    sounds: sounds)

                Section {
                    Button("Reset to Defaults", role: .destructive) {
                        settings.reset()
                    }
                } footer: {
                    Text("Prices come from the Mac; which sound plays is decided here.")
                }
            }
            .navigationTitle("Settings")
        }
    }

    private func dollars(_ amount: Double) -> String {
        String(format: "$%.2f", amount)
    }
}

/// One tier's card: where it starts, and what it sounds like.
private struct TierSection: View {
    let title: String
    let subtitle: String
    @Binding var voice: String
    /// Nil for bulk, which has no line of its own — it is everything under the
    /// win threshold.
    let threshold: Binding<Double>?
    let sounds: Sounds

    var body: some View {
        Section {
            if let threshold {
                LabeledContent("Starts at") {
                    TextField("Amount", value: threshold,
                              format: .currency(code: "USD"))
                        .keyboardType(.decimalPad)
                        .multilineTextAlignment(.trailing)
                }
            }
            Picker("Sound", selection: $voice) {
                ForEach(Sounds.voices) { v in
                    Text(v.label).tag(v.id)
                }
            }
            // The audition. Also fires on every picker change below, but a
            // button that replays the current choice on demand costs nothing
            // and saves re-picking a voice just to hear it again.
            Button {
                sounds.play(voice: voice)
            } label: {
                Label("Play", systemImage: "speaker.wave.2.fill")
            }
        } header: {
            Text("\(title) · \(subtitle)")
        }
        .onChange(of: voice) { _, picked in
            sounds.play(voice: picked)
        }
    }
}
