import SwiftUI

struct SettingsView: View {
    @ObservedObject private var settings = TierSettings.shared
    let sounds: Sounds

    var body: some View {
        NavigationStack {
            Form {
                ForEach(Tier.allCases) { tier in
                    TierSection(
                        tier: tier,
                        voice: Binding(
                            get: { settings.voice(for: tier) },
                            set: { settings.setVoice($0, for: tier) }),
                        threshold: threshold(for: tier),
                        sounds: sounds)
                }

                Section {
                    Button("Reset to Defaults", role: .destructive) {
                        settings.reset()
                    }
                }
            }
            .navigationTitle("Settings")
        }
    }

    private func threshold(for tier: Tier) -> Binding<Double>? {
        switch tier {
        case .win: return $settings.winAt
        case .big: return $settings.bigAt
        case .jackpot: return $settings.jackpotAt
        case .bulk, .review: return nil
        }
    }
}

private struct TierSection: View {
    let tier: Tier
    @Binding var voice: String
    let threshold: Binding<Double>?
    let sounds: Sounds

    private var isSilent: Bool { voice == Sounds.silence }

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
                ForEach(Sounds.voices(for: tier)) { v in
                    Text(v.label).tag(v.id)
                }
            }
            Button {
                sounds.play(voice: voice)
            } label: {
                isSilent
                    ? Label("No sound", systemImage: "speaker.slash.fill")
                    : Label("Play", systemImage: "speaker.wave.2.fill")
            }
            .disabled(isSilent)
        } header: {
            Text(tier.label)
        }
        .onChange(of: voice) { _, picked in
            sounds.play(voice: picked)
        }
    }
}
