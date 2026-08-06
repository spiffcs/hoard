// The furniture a card prints at its foot, recognized by what it says.
//
// This is the whole reason the border reader can be trusted: the geometry is
// anchored on lines whose identity is proven by their *content*, not by edge
// contrast. No amount of glare can fake "this line says Wizards of the Coast".
//
// Moved here with the reader so BorderKit is self-contained. ScanKit still uses
// all three for its own title selection and imports them back.

import Foundation

/// editDistance is plain Levenshtein — titles are short and there are at most
/// a handful of comparisons per capture, so the simple table is plenty.
public func editDistance(_ a: String, _ b: String) -> Int {
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

/// copyrightFurniture reports whether a line is (a fragment of) the bottom
/// copyright line — "™ & © 1993-2003 Wizards of the Coast, Inc. 95/350" and
/// the OCR manglings thereof ("te Coast, Inc", "Coast, Ine: 30/1", "1993-2003
/// Wizar"). Two signals are required: a brand token (coast, or a wizar…
/// prefix — Vision truncates the word) plus corroboration (©/™, an inc-shaped
/// token, a year range, or a collector pair), so that real titles sharing a
/// token — "Coast Watcher", "Wizard's Retort" — survive.
public func copyrightFurniture(_ s: String) -> Bool {
    let tokens = s.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber })
    let brand = tokens.contains("coast") || tokens.contains(where: { $0.hasPrefix("wizar") })
    guard brand else { return false }
    if s.contains("©") || s.contains("™") { return true }
    if tokens.contains(where: { ["inc", "ine", "in", "ir", "lnc"].contains($0) }) { return true }
    let ascii = asciify(s)
    if group(copyrightYearRE, ascii) != nil { return true }
    if group(collectorPairRE, ascii) != nil { return true }
    // Old frames print a range ("1993-2003"); modern ones print a single year
    // ("™ & © 2024 Wizards of the Coast 418"). Requiring the range rejected
    // every modern copyright line outright, which cost both the release year
    // and the collector number printed beside it — and the ™/© that would
    // otherwise vouch for the line comes back as "Iм & C" at this glyph size.
    if let y = group(copyrightLoneYearRE, ascii), let n = Int(y), n >= 1993, n <= 2035 {
        return true
    }
    return false
}

/// artistCredit matches the "Illus. <Name>" line even when OCR has mangled the
/// credit word. On old frames it is set in the same small serif as the
/// copyright, and the exact `illus` prefix missed "Tins. Liz Danforth" — which
/// then read as a perfectly good Title Case name and won a live capture's
/// merge, burying Dwarven Ruins.
///
/// The credit word is only allowed to be wrong by a letter or two, and only
/// when what follows looks like a person: a mangled word alone is not enough,
/// or real two-word titles would start dying.
public func artistCredit(_ s: String) -> Bool {
    let words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    // "Illus. Liz Danforth" — the credit word, then a two-word personal name.
    // Artists are credited first-and-last, and holding the count to exactly
    // two is what lets the credit word itself be read loosely.
    guard words.count == 3 else { return false }
    // The abbreviation's trailing period is the load-bearing signal. Magic
    // titles use commas for epithets ("Jaya Ballard, Task Mage"), never a
    // period after their first word, so this is the shape no real name has.
    // OCR turns the period into a comma often enough to accept both.
    guard let last = words[0].last, last == "." || last == "," else { return false }
    let head = words[0].lowercased().filter { $0.isLetter }
    guard head.count >= 3, head.count <= 6 else { return false }
    // Wide enough for the observed mangles — "Illus." came back as "Tins."
    // and "Tims.", both four edits out — and safe only because the period and
    // the two-word name have already narrowed the field this far.
    guard editDistance(head, "illus") <= 4 else { return false }
    return words.dropFirst().allSatisfy { $0.first?.isUppercase == true }
}
