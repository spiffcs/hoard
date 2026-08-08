// What the person chose on the Settings tab: where the price tiers begin, and
// which voice each one speaks with.
//
// This is also where the phone stopped taking the Mac's tier verdict at face
// value. The Go side still decides its own three tiers for its terminal HUD,
// but it sends the dollar amount alongside — so a phone with its own thresholds
// can draw its own lines without a wire change, and an older hoard keeps
// working untouched. Review and unpriced results carry no amount and pass
// through unchanged; they are outcomes, not prices.
//
// Four price tiers here against the Mac's three: the phone splits the top of
// the range into a big win and a jackpot, because that is the distinction a
// person sorting a box by ear actually wants — "set it aside" versus "stop and
// look".

import Foundation
import SwiftUI

@MainActor
final class TierSettings: ObservableObject {
    /// One instance, shared by the link (which resolves tiers) and the Settings
    /// tab (which edits them). App-wide state, like the pairing code.
    static let shared = TierSettings()

    /// The factory lines and voices, also what Reset restores.
    enum Default {
        static let winAt = 1.0
        static let bigAt = 25.0
        static let jackpotAt = 100.0
        static let voices = [
            "bulk": "knock", "win": "bell", "big": "bells", "jackpot": "harp",
        ]
    }

    /// Where each tier begins, in dollars. Bulk has no knob: it is everything
    /// under the win line.
    @Published var winAt: Double { didSet { store.set(winAt, forKey: "tiers.winAt") } }
    @Published var bigAt: Double { didSet { store.set(bigAt, forKey: "tiers.bigAt") } }
    @Published var jackpotAt: Double { didSet { store.set(jackpotAt, forKey: "tiers.jackpotAt") } }

    @Published var bulkVoice: String { didSet { store.set(bulkVoice, forKey: "tiers.bulkVoice") } }
    @Published var winVoice: String { didSet { store.set(winVoice, forKey: "tiers.winVoice") } }
    @Published var bigVoice: String { didSet { store.set(bigVoice, forKey: "tiers.bigVoice") } }
    @Published var jackpotVoice: String { didSet { store.set(jackpotVoice, forKey: "tiers.jackpotVoice") } }

    private let store: UserDefaults

    init(store: UserDefaults = .standard) {
        self.store = store
        winAt = store.object(forKey: "tiers.winAt") as? Double ?? Default.winAt
        bigAt = store.object(forKey: "tiers.bigAt") as? Double ?? Default.bigAt
        jackpotAt = store.object(forKey: "tiers.jackpotAt") as? Double ?? Default.jackpotAt
        bulkVoice = store.string(forKey: "tiers.bulkVoice") ?? Default.voices["bulk"]!
        winVoice = store.string(forKey: "tiers.winVoice") ?? Default.voices["win"]!
        bigVoice = store.string(forKey: "tiers.bigVoice") ?? Default.voices["big"]!
        jackpotVoice = store.string(forKey: "tiers.jackpotVoice") ?? Default.voices["jackpot"]!
    }

    func reset() {
        winAt = Default.winAt
        bigAt = Default.bigAt
        jackpotAt = Default.jackpotAt
        bulkVoice = Default.voices["bulk"]!
        winVoice = Default.voices["win"]!
        bigVoice = Default.voices["big"]!
        jackpotVoice = Default.voices["jackpot"]!
    }

    /// tier maps a result to the celebration it gets here.
    ///
    /// A priced card is re-tiered from its amount against the phone's own
    /// lines. Checked top-down, so even thresholds a person has typed out of
    /// order stay deterministic: the highest line the price clears wins.
    /// Anything without an amount — review, unpriced, an old parent sending
    /// tier-only results — keeps the wire's word.
    func tier(wire: String?, amount: Double?) -> String? {
        guard wire != "review", let amount else { return wire }
        if amount >= jackpotAt { return "jackpot" }
        if amount >= bigAt { return "big" }
        if amount >= winAt { return "win" }
        return "bulk"
    }

    /// voice says which sound a tier speaks with, or nil for silence.
    ///
    /// Review keeps its fixed question — a queued card is a request, not a
    /// price outcome, and its sound is not a preference. Unpriced stays silent,
    /// as it always has: a shrug is not worth a noise.
    func voice(forTier tier: String?) -> String? {
        switch tier {
        case "bulk": return bulkVoice
        case "win": return winVoice
        case "big": return bigVoice
        case "jackpot": return jackpotVoice
        case "review": return "question"
        default: return nil
        }
    }
}
