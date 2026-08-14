import Foundation
import SwiftUI

enum Tier: String, CaseIterable, Identifiable {
    case bulk, win, big, jackpot, review

    var id: String { rawValue }

    var label: String {
        switch self {
        case .bulk: return "Bulk"
        case .win: return "Rare"
        case .big: return "Mythic"
        case .jackpot: return "Staple"
        case .review: return "Review"
        }
    }

    var defaultVoice: String {
        switch self {
        case .bulk: return "knock"
        case .win: return "bell"
        case .big: return "bells"
        case .jackpot: return "harp"
        case .review: return "question"
        }
    }

    var storageKey: String { "tiers.\(rawValue)Voice" }
}

@MainActor
final class TierSettings: ObservableObject {
    static let shared = TierSettings()

    enum Default {
        static let winAt = 1.0
        static let bigAt = 25.0
        static let jackpotAt = 100.0
    }

    @Published var winAt: Double { didSet { store.set(winAt, forKey: "tiers.winAt") } }
    @Published var bigAt: Double { didSet { store.set(bigAt, forKey: "tiers.bigAt") } }
    @Published var jackpotAt: Double { didSet { store.set(jackpotAt, forKey: "tiers.jackpotAt") } }

    @Published private(set) var voices: [Tier: String] = [:]

    private let store: UserDefaults

    init(store: UserDefaults = .standard) {
        self.store = store
        winAt = store.object(forKey: "tiers.winAt") as? Double ?? Default.winAt
        bigAt = store.object(forKey: "tiers.bigAt") as? Double ?? Default.bigAt
        jackpotAt = store.object(forKey: "tiers.jackpotAt") as? Double ?? Default.jackpotAt
        for tier in Tier.allCases {
            let palette = Set(Sounds.voices(for: tier).map(\.id))
            let stored = store.string(forKey: tier.storageKey)
            voices[tier] = stored.flatMap { palette.contains($0) ? $0 : nil }
                ?? tier.defaultVoice
        }
    }

    func voice(for tier: Tier) -> String {
        voices[tier] ?? tier.defaultVoice
    }

    func setVoice(_ voice: String, for tier: Tier) {
        voices[tier] = voice
        store.set(voice, forKey: tier.storageKey)
    }

    var bulkVoice: String? { sounding(voice(for: .bulk)) }

    private func sounding(_ voice: String) -> String? {
        voice == Sounds.silence ? nil : voice
    }

    func reset() {
        winAt = Default.winAt
        bigAt = Default.bigAt
        jackpotAt = Default.jackpotAt
        for tier in Tier.allCases { setVoice(tier.defaultVoice, for: tier) }
    }

    func tier(wire: String?, amount: Double?) -> String? {
        guard wire != "review", let amount else { return wire }
        if amount >= jackpotAt { return "jackpot" }
        if amount >= bigAt { return "big" }
        if amount >= winAt { return "win" }
        return "bulk"
    }

    func voice(forTier tier: String?) -> String? {
        guard let tier, let known = Tier(rawValue: tier) else { return nil }
        return sounding(voice(for: known))
    }
}
