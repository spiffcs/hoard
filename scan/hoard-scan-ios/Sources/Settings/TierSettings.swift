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
// the range into Mythic and Staple, because that is the distinction a
// person sorting a box by ear actually wants — "set it aside" versus "stop and
// look". Review is a fifth tier here and is not a price at all: it is the Mac
// asking a question, and it now picks its own voice like the rest. It used to
// be fixed on the argument that a queued card "is not a preference", but the
// sound a person most needs to recognize is the one that interrupts them, and
// which sound does that best depends on the room.
//
// Each tier's voices are its own — see Sounds.swift. Nothing here can assign
// across tiers, because the picker is derived from the voices' own tier.
//
// Any tier can also be set to no sound, and all five at once makes a silent
// app. Silence is carried as a voice every tier offers rather than as a missing
// value, because the validation in init repairs ids it does not recognise: a
// tier that was turned off has to be something this file can read back, not a
// hole where a voice used to be.

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

    /// What the Settings tab calls this tier.
    ///
    /// Labels only, and deliberately not matched by a rename of the cases. A
    /// `rawValue` here is load-bearing in three places that have nothing to do
    /// with what is on screen: it builds `storageKey`, so moving it would
    /// silently reset every voice already picked; it is the vocabulary the Mac
    /// sends (`tierJackpot` in `internal/tui/hud.go`); and `PriceOverlay` and
    /// `CardOutline` switch on the same strings. So `.jackpot` reads "Staple"
    /// to a person and stays `jackpot` everywhere a string crosses a boundary.
    ///
    /// The names are the collector's ladder rather than the casino's, which is
    /// the vocabulary someone sorting a box is already thinking in.
    var label: String {
        switch self {
        case .bulk: return "Bulk"
        case .win: return "Rare"
        case .big: return "Mythic"
        case .jackpot: return "Staple"
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
        // answers to — one renamed or retired across an update — would mute its
        // tier for a reason nobody chose: play() finds no buffer and returns,
        // and the picker shows a blank row. And an earlier build let any tier
        // pick any voice, so an install can be carrying a harp run on bulk,
        // which the tier's own palette no longer contains. Both fall back to
        // the factory voice, which is audible and re-pickable.
        //
        // A tier deliberately turned off is neither of those, and this loop
        // already tells the difference without a special case: `Sounds.silence`
        // is in every tier's palette, so it is a voice that matches and is kept.
        // That is exactly why silence is a palette member and not an empty
        // string or a homemade "none" — an id this loop does not recognise is
        // *restored to the default*, so a sentinel outside the vocabulary would
        // give a setting that appears to work, reverts on the next launch, and
        // produces no build error on the way.
        for tier in Tier.allCases {
            let palette = Set(Sounds.voices(for: tier).map(\.id))
            let stored = store.string(forKey: tier.storageKey)
            voices[tier] = stored.flatMap { palette.contains($0) ? $0 : nil }
                ?? tier.defaultVoice
        }
    }

    /// voice says which sound a tier speaks with, or `Sounds.silence` if it has
    /// been turned off. Still a plain String because this is what the Settings
    /// picker binds to, and a picker needs a selection for every row including
    /// the silent one; the nil-for-nothing-to-play shape belongs at the play
    /// sites below.
    func voice(for tier: Tier) -> String {
        voices[tier] ?? tier.defaultVoice
    }

    func setVoice(_ voice: String, for tier: Tier) {
        voices[tier] = voice
        store.set(voice, forKey: tier.storageKey)
    }

    /// The bulk voice by name, for the legacy `chime` verb — a parent too old
    /// to know about tiers gets the sound of the tier most of its cards are in.
    ///
    /// Nil when bulk has been turned off. The verb borrows bulk's voice, so it
    /// has to borrow bulk's silence too: bulk is the tier a person is likeliest
    /// to mute, and an old parent that went on knocking through it would look
    /// like the setting had not taken.
    var bulkVoice: String? { sounding(voice(for: .bulk)) }

    /// The voice to hand `play`, or nil where there is nothing to play. Kept in
    /// one place so both play sites read as "if there is a sound, make it"
    /// rather than each testing for silence in its own words.
    private func sounding(_ voice: String) -> String? {
        voice == Sounds.silence ? nil : voice
    }

    /// Reset restores the factory lines and the factory voices — including on a
    /// tier that was turned off, which is deliberate. This is the button for "I
    /// have lost track of what I changed", and a tier that stayed silent
    /// through it would be the one setting the reset did not reach.
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
    /// Two ways to get nil now, and the caller does not have to tell them
    /// apart. Unpriced stays silent as it always has: it is not a `Tier`, so it
    /// falls out of the guard along with anything a newer parent invents. And a
    /// tier turned off in Settings is answered here rather than handed to
    /// `play` — `play` would refuse it anyway, but a play site should not be
    /// asked to play something that is not a sound.
    func voice(forTier tier: String?) -> String? {
        guard let tier, let known = Tier(rawValue: tier) else { return nil }
        return sounding(voice(for: known))
    }
}
