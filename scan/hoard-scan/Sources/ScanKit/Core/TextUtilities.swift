// Small string helpers shared by the parsers. Nothing here knows about cards.

import Foundation

/// confusables maps the non-ASCII lookalikes Vision returns for this text to the
/// ASCII the patterns expect. With language correction off and glyphs barely 1% of
/// the frame tall, it will happily report a set code as "MHЗ" with a Cyrillic З, or
/// a Greek Ο for a zero — which then fails an [0-9A-Z] match for reasons invisible
/// in the emitted bottomLines.
let confusables: [Character: Character] = [
    // Cyrillic
    "А": "A", "В": "B", "Е": "E", "З": "3", "К": "K", "М": "M", "Н": "H", "О": "O",
    "Р": "P", "С": "C", "Т": "T", "У": "Y", "Х": "X", "І": "I", "Ѕ": "S", "Ј": "J",
    // Greek
    "Α": "A", "Β": "B", "Ε": "E", "Ζ": "Z", "Η": "H", "Ι": "I", "Κ": "K", "Μ": "M",
    "Ν": "N", "Ο": "O", "Ρ": "P", "Τ": "T", "Υ": "Y", "Χ": "X",
    // Fullwidth / typographic digits and slashes
    "０": "0", "１": "1", "２": "2", "３": "3", "４": "4", "５": "5", "６": "6",
    "７": "7", "８": "8", "９": "9", "⁄": "/", "∕": "/", "／": "/",
]

/// asciify folds lookalike glyphs to ASCII and uppercases the result, so the
/// patterns can stay strict about shape without also being strict about which
/// codepoint Vision happened to pick. Uppercasing is what lets a lowercase read
/// ("mh3 • en") still yield a set code.
func asciify(_ s: String) -> String {
    String(s.uppercased().map { confusables[$0] ?? $0 })
}

/// group returns a capture group of the first match, if any.
func group(_ re: NSRegularExpression, _ s: String, _ n: Int = 1) -> String? {
    let full = NSRange(s.startIndex..., in: s)
    guard let m = re.firstMatch(in: s, range: full), m.numberOfRanges > n,
          let r = Range(m.range(at: n), in: s) else { return nil }
    return String(s[r])
}

/// normalizeNumber drops the zero padding cards are printed with ("0123/0281"),
/// since Scryfall stores collector numbers unpadded.
func normalizeNumber(_ s: String) -> String {
    let trimmed = s.drop(while: { $0 == "0" })
    return trimmed.isEmpty ? "0" : String(trimmed)
}

/// normTitle reduces a read title to the characters worth comparing.
func normTitle(_ s: String) -> String {
    String(s.lowercased().unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) })
}

/// editDistance is plain Levenshtein — titles are short and there are at most
/// a handful of comparisons per capture, so the simple table is plenty.
func editDistance(_ a: String, _ b: String) -> Int {
    let x = Array(a), y = Array(b)
    if x.isEmpty { return y.count }
    if y.isEmpty { return x.count }
    var prev = Array(0...y.count)
    var cur = [Int](repeating: 0, count: y.count + 1)
    for i in 1...x.count {
        cur[0] = i
        for j in 1...y.count {
            let sub = prev[j - 1] + (x[i - 1] == y[j - 1] ? 0 : 1)
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, sub)
        }
        swap(&prev, &cur)
    }
    return prev[y.count]
}
