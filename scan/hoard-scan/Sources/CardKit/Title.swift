import Foundation

private let typeLineWords: Set<String> = [
    "creature", "instant", "sorcery", "land", "artifact", "enchantment",
    "planeswalker", "battle", "kindred", "tribal", "summon", "legendary",
    "basic", "snow", "world", "scheme", "plane", "phenomenon", "vanguard",
    "conspiracy", "token", "emblem",
]

public func chooseTitle(from lines: [String]) -> String {
    for line in lines {
        if plausibleTitle(line) { return line }
    }
    return lines.first ?? ""
}

func plausibleTitle(_ line: String) -> Bool {
    let trimmed = line.trimmingCharacters(in: .whitespaces)
    guard trimmed.count >= 3 else { return false }

    let words = trimmed.lowercased().split(whereSeparator: { !$0.isLetter })
    if let first = words.first, typeLineWords.contains(String(first)) {
        return false
    }

    if trimmed.hasSuffix(".") { return false }

    let letters = trimmed.filter(\.isLetter).count
    guard letters >= 3, Double(letters) / Double(trimmed.count) > 0.5 else { return false }

    return true
}
