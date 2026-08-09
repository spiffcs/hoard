// What the person chose on the Settings tab: where the price tiers begin, and
// which voice each one speaks with.
//
// This is also where the phone stopped taking the Mac's tier verdict at face
// value. The Go side still decides its own three tiers for its terminal HUD,
// but it sends the dollar amount alongside — so a phone with its own thresholds
// can draw its own lines without a wire change, and an older hoard keeps
// working untouched. Unpriced results carry no amount and pass through
// unchanged; a shrug is not a price.
//
// Four price tiers here against the Mac's three: the phone splits the top of
// the range into a big win and a jackpot, because that is the distinction a
// person sorting a box by ear actually wants — "set it aside" versus "stop and
// look". Review is a fifth tier here and is not a price at all: it is the Mac
// asking a question, and it now picks its own voice like the rest. It used to
// be fixed on the argument that a queued card "is not a preference", but the
// sound a person most needs to recognize is the one that interrupts them, and
// which sound does that best depends on the room.
//
// Each tier's voices are its own — see Sounds.swift. Nothing here can assign
// across tiers, because the picker is derived from the voices' own tier.

import Foundation
import SwiftUI

/// The five things a resolved card can be, in the order Settings shows them.
///
/// Deliberately not on the wire: `big` has never existed on the Go side, and
/// `tier(wire:amount:)` keeps trading in plain strings so the link and the
/// overlays stay untouched by this type.
enum Tier: String, CaseIterable, Identifiable {
    case bulk, win, big, jackpot, review

    var id: String { rawValue }

    var label: String {
        switch self {
        case .bulk: return "Bulk"
        case .win: return "Win"
        case .big: return "Big Win"
        case .jackpot: return "Jackpot"
        case .review: return "Review"
        }
    }

    /// The factory voice, and what Reset restores. Each is the voice its tier
    /// was originally designed around.
    var defaultVoice: String {
        switch self {
        case .bulk: return "knock"
        case .win: return "bell"
        case .big: return "bells"
        case .jackpot: return "harp"
        case .review: return "question"
        }
    }

    /// Unchanged from when these were four separate properties, so an install
    /// that already picked its voices keeps them across this update.
    var storageKey: String { "tiers.\(rawValue)Voice" }
}

@MainActor
final class TierSettings: ObservableObject {
    /// One instance, shared by the link (which resolves tiers) and the Settings
    /// tab (which edits them). App-wide state, like the pairing code.
    static let shared = TierSettings()

    /// The factory lines, also what Reset restores. The factory *voices* live
    /// on `Tier` beside the tier they belong to.
    enum Default {
        static let winAt = 1.0
        static let bigAt = 25.0
        static let jackpotAt = 100.0
    }

    /// Where each tier begins, in dollars. Bulk has no knob: it is everything
    /// under the win line. Neither does review, which is not a price.
    @Published var winAt: Double { didSet { store.set(winAt, forKey: "tiers.winAt") } }
    @Published var bigAt: Double { didSet { store.set(bigAt, forKey: "tiers.bigAt") } }
    @Published var jackpotAt: Double { didSet { store.set(jackpotAt, forKey: "tiers.jackpotAt") } }

    /// Written only through `setVoice`, so persistence cannot be bypassed by a
    /// caller that mutates the dictionary directly.
    @Published private(set) var voices: [Tier: String] = [:]

    private let store: UserDefaults

    init(store: UserDefaults = .standard) {
        self.store = store
        winAt = store.object(forKey: "tiers.winAt") as? Double ?? Default.winAt
        bigAt = store.object(forKey: "tiers.bigAt") as? Double ?? Default.bigAt
        jackpotAt = store.object(forKey: "tiers.jackpotAt") as? Double ?? Default.jackpotAt
        // Validated against the voices *this tier* offers, not merely against
        // the voices that exist. Two things land here. A stored id no voice
        // answers to — one renamed or retired across an update — would silently
        // mute its tier: play() finds no buffer and returns, and the picker
        // shows a blank row. And an earlier build let any tier pick any voice,
        // so an install can be carrying a harp run on bulk, which the tier's
        // own palette no longer contains. Both fall back to the factory voice,
        // which is audible and re-pickable.
        for tier in Tier.allCases {
            let palette = Set(Sounds.voices(for: tier).map(\.id))
            let stored = store.string(forKey: tier.storageKey)
            voices[tier] = stored.flatMap { palette.contains($0) ? $0 : nil }
                ?? tier.defaultVoice
        }
    }

    /// voice says which sound a tier speaks with.
    func voice(for tier: Tier) -> String {
        voices[tier] ?? tier.defaultVoice
    }

    func setVoice(_ voice: String, for tier: Tier) {
        voices[tier] = voice
        store.set(voice, forKey: tier.storageKey)
    }

    /// The bulk voice by name, for the legacy `chime` verb — a parent too old
    /// to know about tiers gets the sound of the tier most of its cards are in.
    var bulkVoice: String { voice(for: .bulk) }

    func reset() {
        winAt = Default.winAt
        bigAt = Default.bigAt
        jackpotAt = Default.jackpotAt
        for tier in Tier.allCases { setVoice(tier.defaultVoice, for: tier) }
    }

    /// tier maps a result to the celebration it gets here.
    ///
    /// A priced card is re-tiered from its amount against the phone's own
    /// lines. Checked top-down, so even thresholds a person has typed out of
    /// order stay deterministic: the highest line the price clears wins.
    /// Anything without an amount — review, unpriced, an old parent sending
    /// tier-only results — keeps the wire's word.
    ///
    /// Still strings in and strings out: the overlays and the link controller
    /// speak the wire's vocabulary, and `Tier` is this file's business.
    func tier(wire: String?, amount: Double?) -> String? {
        guard wire != "review", let amount else { return wire }
        if amount >= jackpotAt { return "jackpot" }
        if amount >= bigAt { return "big" }
        if amount >= winAt { return "win" }
        return "bulk"
    }

    /// voice says which sound a wire tier speaks with, or nil for silence.
    ///
    /// Unpriced stays silent, as it always has: it is not a `Tier`, so it falls
    /// out here along with anything a newer parent invents.
    func voice(forTier tier: String?) -> String? {
        guard let tier, let known = Tier(rawValue: tier) else { return nil }
        return voice(for: known)
    }
}
