// Picking the title out of everything the card says.
//
// A card is mostly text that is not its name: rules, flavour, a type line, an
// artist credit, a copyright row. The title is one line among twenty, and the
// only reliable thing about it is where it sits — the top of the card.
//
// This leans on that harder than the macOS pipeline can, because that one reads
// a photograph in which the card might be anywhere, while this reads a card that
// has already been located and flattened. "The top of the card" is a fact here
// rather than an estimate.

import Foundation

/// Words that mark a line as structural rather than a name. A type line is the
/// most common wrong answer — it sits high, it is short, and it is capitalised
/// exactly like a title.
private let typeLineWords: Set<String> = [
    "creature", "instant", "sorcery", "land", "artifact", "enchantment",
    "planeswalker", "battle", "kindred", "tribal", "summon", "legendary",
    "basic", "snow", "world", "scheme", "plane", "phenomenon", "vanguard",
    "conspiracy", "token", "emblem",
]

/// chooseTitle picks the most likely name from lines in reading order.
///
/// Reading order is load-bearing: Vision returns observations top-to-bottom for
/// an upright card, and the title is the first line of a card that has been
/// flattened. The filtering below exists to skip the cases where it is not —
/// a mana cost read as text, a set symbol read as a stray letter.
public func chooseTitle(from lines: [String]) -> String {
    for line in lines {
        if plausibleTitle(line) { return line }
    }
    return lines.first ?? ""
}

/// plausibleTitle rejects the lines that are reliably not names.
///
/// Deliberately permissive about what a name looks like. Magic names include
/// "Ach! Hans, Run!", "Yawgmoth's Will", "_____ Goblin" and single words — so
/// any rule about word count or capitalisation throws away real cards. The rules
/// here only exclude things that are structurally something else.
func plausibleTitle(_ line: String) -> Bool {
    let trimmed = line.trimmingCharacters(in: .whitespaces)
    guard trimmed.count >= 3 else { return false }

    // A type line. Checked on the first word: "Creature - Cephalid" is a type
    // line, but "Creature Guy" could be a card name, and the em-dash form is
    // what actually distinguishes them.
    let words = trimmed.lowercased().split(whereSeparator: { !$0.isLetter })
    if let first = words.first, typeLineWords.contains(String(first)) {
        // "Summon Merfolk" and "Legendary Creature - Human" are type lines.
        // A name starting with one of these words is rare enough, and wrong
        // here is cheaper than wrong the other way: the caller gets alternates.
        return false
    }

    // Rules text. A sentence ends in a full stop; a card name essentially never
    // does. This is the single most effective filter on a frame-wide read.
    if trimmed.hasSuffix(".") { return false }

    // Mostly digits or punctuation — a collector row, a power/toughness box, a
    // mana cost that read as text.
    let letters = trimmed.filter(\.isLetter).count
    guard letters >= 3, Double(letters) / Double(trimmed.count) > 0.5 else { return false }

    return true
}
