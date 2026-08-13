// The Settings tab: the tier lines and their voices.
//
// One section per tier, top down the way the money reads — jackpot first would
// be the casino's order, but a person setting this up thinks in "everything
// under a dollar" first, so bulk leads. Review comes last, after the prices,
// because it is not one.
//
// Each picker offers only the voices built for that tier, so the choice is
// between three sounds of the right weight rather than fifteen of every weight.
// That scoping lives on the voices themselves (Sounds.swift); this file just
// asks for the tier's own.
//
// Every voice change plays itself. A sound picked silently is a sound
// discovered mid-box, and the whole point of the tiers is knowing them by ear
// before the box starts.
//
// Every picker also ends in Silent, which is the one choice that does not
// audition — there is nothing to hear, so the Play button says so and goes
// dim rather than sitting there doing nothing when pressed. Five silent tiers
// is a mute app, and a supported one.

import SwiftUI

struct SettingsView: View {
    @ObservedObject private var settings = TierSettings.shared
    /// The link's player, borrowed for previews so a picked voice sounds
    /// exactly as it will in a session — same engine, same buffers.
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

    /// The editable line where a tier begins, or nil where a tier has none —
    /// bulk is everything under the win line, and review is not a price.
    private func threshold(for tier: Tier) -> Binding<Double>? {
        switch tier {
        case .win: return $settings.winAt
        case .big: return $settings.bigAt
        case .jackpot: return $settings.jackpotAt
        case .bulk, .review: return nil
        }
    }
}

/// One tier's card: where it starts, and what it sounds like.
private struct TierSection: View {
    let tier: Tier
    @Binding var voice: String
    /// Nil for the tiers with no line of their own.
    let threshold: Binding<Double>?
    let sounds: Sounds

    /// Silent is a choice like any other in the picker, and the only one with
    /// nothing to audition.
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
            // This tier's voices only, plus Silent. A sounding voice belongs to
            // exactly one tier, so nothing here overlaps with any other section
            // except that last row, which every tier offers.
            Picker("Sound", selection: $voice) {
                ForEach(Sounds.voices(for: tier)) { v in
                    Text(v.label).tag(v.id)
                }
            }
            // The audition. Also fires on every picker change below, but a
            // button that replays the current choice on demand costs nothing
            // and saves re-picking a voice just to hear it again.
            //
            // On Silent it says what it would do instead of doing nothing.
            // `play` already refuses silence, so an enabled button here would
            // be a control that responds to a press with no sound and no
            // explanation — indistinguishable from audio being broken, which
            // is the reading this tab must never invite.
            Button {
                sounds.play(voice: voice)
            } label: {
                isSilent
                    ? Label("No sound", systemImage: "speaker.slash.fill")
                    : Label("Play", systemImage: "speaker.wave.2.fill")
            }
            .disabled(isSilent)
        } header: {
            // The name alone. The header used to carry the tier's range beside
            // it — "Win · $1.00 and up" — which was the only place the line was
            // visible before "Starts at" became editable. Now it is stated once,
            // in the field that sets it, and a header repeating it is a second
            // copy that can disagree with the first.
            Text(tier.label)
        }
        .onChange(of: voice) { _, picked in
            // Picking Silent makes no sound, and needs no test for it here:
            // `play` refuses silence by name. Keeping that check in one place
            // is what makes "silent" mean the same thing to the audition and
            // to a card landing mid-session.
            sounds.play(voice: picked)
        }
    }
}
