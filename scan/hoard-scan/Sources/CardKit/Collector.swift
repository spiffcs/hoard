// Reading the printing off the bottom of a card.
//
// Three grammars, one per frame era, established from real captures rather than
// from the layout documentation:
//
//   modern (M15 frame and later)
//       R 0338
//       MH3 • EN OLENA RICHARDS
//       ™ & © 2024 Wizards of the Coast
//
//   1998-2003 — the number rides inside the copyright row
//       TM & © 1993-2003 Wizards of the Coast, Inc. 15/145
//       ©1993-1999 Wizards of the Coast, Inc, 36/143
//
//   pre-1998 — there is no number, and saying so is the correct answer
//       Illus. Amy Weber
//       ©1994 Wizards of the Coast, Inc. All rights reserved
//
// The rule that shapes everything else: a wrong collector number does not rank a
// card badly, it invents a printing that was never held. So every field here is
// refused unless its structure is unambiguous, and "nothing printed" is a first
// class answer rather than a failure.

import Foundation

/// What the bottom of a card said about which printing it is.
public struct Printing: Equatable, Sendable {
    /// The collector number as printed, leading zeros stripped ("0338" -> "338").
    public var number = ""
    /// The denominator of a printed pair, when there is one ("15/145" -> 145).
    public var total: Int?
    /// The set code, upper-cased. Only the M15 frame and later print one.
    public var setCode = ""
    /// R, C, U, M, S, T, L — printed beside the number on modern frames.
    public var rarity = ""
    /// The two-letter language from the set row ("EN").
    public var language = ""
    /// "foil" or "nonfoil" when the set row's separator said so, empty when the
    /// frame prints no marker or the glyph was unreadable.
    ///
    /// Empty is not "nonfoil". Old frames carry no marker at all, so claiming
    /// nonfoil from silence would state as read what was never printed — and
    /// foil is worth a multiple of nonfoil.
    public var finish = ""
    /// Where `finish` came from. "separator" is the modern set row's glyph;
    /// "sparkle" is the printed starburst a retro-frame foil carries, read by
    /// BorderKit. Empty when nothing said.
    ///
    /// Reported rather than acted on: the Go side treats any present finish as
    /// evidence. It is here so a session's telemetry can say which signal is
    /// carrying the answer, which is the only way to notice one of them
    /// quietly stopping.
    public var finishSource = ""
    /// The single year, or the later end of a printed range.
    public var year: Int?
    /// The earlier end of a printed range, when one was printed.
    public var yearFrom: Int?
    /// Which row the number came from. A number lifted out of a copyright row
    /// deserves less trust than one printed on its own line, and the caller
    /// needs to be able to tell.
    public var numberSource: NumberSource = .none

    public enum NumberSource: String, Equatable, Sendable {
        case none, ownRow, copyrightRow
    }

    public var isEmpty: Bool {
        number.isEmpty && setCode.isEmpty && year == nil
    }
}

/// readPrinting parses the band's lines, bottom-most first.
public func readPrinting(bandLines lines: [String]) -> Printing {
    var out = Printing()
    // Whether the current `.ownRow` number came from a bare form. Local rather
    // than on `Printing`, because it is a fact about this parse and not about
    // the printing — nothing downstream should be able to ask.
    var ownRowIsBare = false

    for line in lines {
        // The copyright row carries the year, and on 1998-2003 frames the
        // collector number too. It is found by content — the company name, or
        // the copyright glyph — because its exact spelling does not survive OCR.
        //
        // The year and the number are read from it *independently*, and that
        // separation is the point. They used to share a gate: the number was
        // read inside `if let years(in: line)`, so a row whose four small
        // italic digits failed OCR dropped a collector number that was sitting
        // in plain text at the end of the same line. Live, on one session, that
        // cost four cards — `wards of the Coast 399`, `zards of the Coast 14`,
        // `Wizards of the Coast 413`, `4 Wizards of the Coast 407` — every one
        // of them queued as "printing unverified" holding no number at all.
        //
        // Nothing about `trailingNumber`'s safety came from the year. Its three
        // guards are the tail position, the plausible-year refusal, and the
        // four-digit ceiling; what makes a lone number at the end of this line
        // safe to read is that the *company name* has already said what the
        // line is. That guard is still here, unchanged. The year was never
        // doing this job — it only happened to be standing in the doorway.
        if looksLikeCompanyRow(line) || line.contains("©") {
            var consumed = false
            if let (from, to) = years(in: line) {
                consumed = true
                // Keep the widest range seen; a card prints one copyright row,
                // but the band pass sometimes splits it across two observations.
                if out.year == nil || to > out.year! {
                    out.year = to
                    out.yearFrom = from
                }
            }
            // A bare own-row number does not hold the floor against this line.
            // Live: Brainsurge's footer read `T 89` on one line and
            // `izards of the Coast 399` on the next; 89 claimed `.ownRow`, the
            // real 399 was refused for want of a free slot, and the card queued
            // against a number no printing of it has. The row identified by the
            // company's own name outranks a row identified only by its shape.
            let ownRowHolds = out.numberSource == .ownRow && !ownRowIsBare
            if !ownRowHolds, let pair = collectorPair(in: line) {
                out.number = pair.number
                out.total = pair.total
                out.numberSource = .copyrightRow
                ownRowIsBare = false
                consumed = true
            } else if out.numberSource == .none || ownRowIsBare,
                      let n = trailingNumber(in: line) {
                // A bare number at the tail of the copyright row, with no
                // total beside it: "™M & © 2024 Wizards of the Coast 410".
                //
                // The pair form was the only one handled, so this layout read
                // its year and dropped its number on the floor — a live
                // Marionette Apprentice queued twice holding a perfectly
                // legible 410. The number is right there at the end of the
                // line; nothing but the missing slash made it invisible.
                out.number = n
                out.numberSource = .copyrightRow
                ownRowIsBare = false
                consumed = true
            }
            // Only a row this branch could actually use is consumed. The
            // fingerprint is loose on purpose — a bare `©` passes it — and the
            // modern set row prints its artist credit with one:
            // `MH3*EN © ROB ALEXANDER` is a set row, not a copyright row, and
            // swallowing it here cost the star that says the card is foil.
            if consumed { continue }
        }

        // The modern set row: code, a separator of some kind, a language, then
        // the artist. Checked before the number row because the code itself can
        // look like a bare token.
        if let set = setRow(in: line) {
            out.setCode = set.code
            out.language = set.language
            // First verdict wins. This used to assign unconditionally, which
            // let a second set-row-shaped line later in the band clear an
            // earlier foil to "" — leaving finishSource claiming a separator
            // verdict that no longer existed — or flip it outright.
            if out.finish.isEmpty, !set.finish.isEmpty {
                out.finish = set.finish
                out.finishSource = "separator"
            }
            continue
        }

        // The modern number row, alone: "R 0338", "0338", "M 0087".
        //
        // The same precedence from the other direction — lines arrive
        // bottom-most first, so the company row is as likely to have been seen
        // already as not, and a bare hit must not overwrite what it found.
        if let n = ownNumberRow(line) {
            if n.bare && out.numberSource == .copyrightRow {
                // Still worth its rarity, which the copyright row never prints.
                if out.rarity.isEmpty { out.rarity = n.rarity }
                continue
            }
            out.number = n.number
            out.rarity = n.rarity
            if let total = n.total { out.total = total }
            out.numberSource = .ownRow
            ownRowIsBare = n.bare
        }
    }
    return out
}

// MARK: - Rows

/// years pulls a single year or a printed range out of a line.
///
/// Requires the company row's fingerprint alongside, because a bare four-digit
/// number in the plausible window is also what a collector number looks like on
/// a large set — and reading "1999" off a numbered row as a copyright year would
/// silently date a card wrongly.
func years(in line: String) -> (from: Int?, to: Int)? {
    guard looksLikeCompanyRow(line) || line.contains("©") else { return nil }
    let tokens = line.split { !$0.isLetter && !$0.isNumber }
    let found = tokens.compactMap { year(from: $0) }
    guard let last = found.last else { return nil }
    return (found.count > 1 ? found.first : nil, last)
}

/// collectorPair pulls "15/145" out of a copyright row.
///
/// Deliberately only called on a line already identified as the copyright row.
/// The same `N/M` shape is the power/toughness box, which sits in the same strip
/// of the card — measured across thirteen captures, power/toughness always
/// stands alone on its own line and the collector pair never does. Requiring the
/// company row is what keeps `0/1` on a Fallen Empires creature from being
/// read as a printing.
func collectorPair(in line: String) -> (number: String, total: Int)? {
    let parts = line.split(separator: "/")
    guard parts.count == 2 else { return nil }
    // The number is the trailing run of the left half, the total the leading run
    // of the right — "Wizards of the Coast, Inc. 15" / "145".
    guard let numTok = parts[0].split(separator: " ").last,
          let number = digitsOnly(numTok), number.count <= 4,
          let totalTok = parts[1].split(separator: " ").first,
          let totalStr = digitsOnly(totalTok), let total = Int(totalStr),
          total > 0, total < 10000
    else { return nil }
    return (stripLeadingZeros(number), total)
}

/// finishFromSeparator classifies the glyph printed between the set code and
/// the language, which is the only place a modern frame says "foil".
///
/// A star means foil, a bullet means nonfoil, and anything else stays unknown
/// rather than guessed — a wrong finish is a wrong price, and the nonfoil
/// default is at least a knowable default.
///
/// The vocabulary is wider than the two real glyphs because Vision does not
/// render a star at this size reliably. Observed live on the macOS path: "*",
/// "+", and at this glyph size even bare letters — K, X, A, T. That knowledge
/// was hard-won there and is duplicated rather than shared, because CardKit
/// deliberately shares no code with ScanKit; what travels between them is the
/// finding, not the file.
func finishFromSeparator(_ sep: String) -> String {
    if sep.isEmpty { return "" }
    if sep.contains(where: { "★✦✧✶*+".contains($0) }) { return "foil" }
    if sep.contains(where: { "•·∙.,:;|/\\―—–-".contains($0) }) { return "nonfoil" }
    return ""
}

/// tokens, with where each one sat, so the separator between two of them can be
/// recovered. `split` discards it, which is how the foil star went missing.
func alphanumericTokens(_ line: String) -> [(text: String, start: String.Index, end: String.Index)] {
    var out: [(String, String.Index, String.Index)] = []
    var i = line.startIndex
    while i < line.endIndex {
        guard line[i].isLetter || line[i].isNumber else {
            i = line.index(after: i)
            continue
        }
        let start = i
        while i < line.endIndex, line[i].isLetter || line[i].isNumber {
            i = line.index(after: i)
        }
        out.append((String(line[start..<i]), start, i))
    }
    return out
}

/// trailingNumber pulls a bare collector number off the end of a copyright row.
///
/// Only ever called on a line already established as the copyright row, which
/// is what makes a lone number at the end safe to read: the company name and
/// the year have already identified what this line is, so the digits after them
/// are the collector number and not a page number or a stray from the art.
///
/// Three guards, and the middle one is the load-bearing one:
///
///   - the token has to be at the very end, because that is where the number is
///     printed and a number anywhere else in this row is part of the date;
///   - it must not be a plausible year, or `© 2024 Wizards of the Coast` would
///     read its own copyright date as the card's collector number;
///   - and it must be short, because collector numbers are at most four digits
///     and a longer run is a misread of something else entirely.
func trailingNumber(in line: String) -> String? {
    let words = line.split(separator: " ")
    guard let last = words.last, let digits = digitsOnly(last) else { return nil }
    guard (1...4).contains(digits.count) else { return nil }
    if let n = Int(digits), plausibleYears.contains(n) { return nil }
    // Fourth guard, and the newest: the digits must sit *against* the company
    // name. This used to be free — the row also had to carry a legible year, so
    // rules text could never be offered here at all. Reading the number without
    // the year gave that up, and `looksLikeCompanyRow` matches on substrings:
    // `beasts of the coastal plain 12` holds "coast" and ends in a number, and
    // read as printing 12 the moment the year stopped standing guard.
    //
    // What separates the credit line from a sentence that happens to mention
    // the coast is where the number is. A copyright row prints it immediately
    // after the company — "Wizards of the Coast 413", "of the Coast, Inc. 15" —
    // and prose does not. Close range, because these are the very words the
    // lamp mangles: "Coust", "Coasp" and "Coast:" are all this token, live.
    guard words.count >= 2 else { return nil }
    let prev = String(words[words.count - 2]).lowercased().filter { $0.isLetter }
    guard editDistance(prev, "coast") <= 1 || editDistance(prev, "inc") <= 1
            || editDistance(prev, "wizards") <= 2
    else { return nil }
    let stripped = String(digits.drop(while: { $0 == "0" }))
    return stripped.isEmpty ? nil : stripped
}

/// setRow matches "MH3 • EN OLENA RICHARDS".
///
/// The bullet reads as any of several glyphs depending on the frame and the
/// light — observed as •, *, ·, a stray I, and sometimes nothing at all — so the
/// separator is permissive. The code and the language are not: three to five
/// alphanumerics and exactly two letters, or this is prose.
func setRow(in line: String) -> (code: String, language: String, finish: String)? {
    // Tokens with their positions, rather than a plain split. The separator is
    // frequently glued to the code with no space at all — observed `MSC•EN •
    // ALEXANDER`, `MAR• EN OBRIAN`, `MH3*EN © ROB ALEXANDER` — and a plain
    // split both merges those into one unmatched token *and* discards the very
    // glyph that says whether the card is foil.
    let tokens = alphanumericTokens(line)
    guard tokens.count >= 2 else { return nil }
    let code = tokens[0].text.uppercased()
    guard (3...5).contains(code.count), code.allSatisfy({ $0.isLetter || $0.isNumber }),
          code.contains(where: { $0.isLetter })
    else { return nil }
    // The language sits next, possibly with the separator glued to it.
    for (offset, tok) in tokens.dropFirst().prefix(2).enumerated() {
        // The frame prints the language in capitals and prose does not, and
        // that case difference is the whole defence against a sentence that
        // happens to scan: "card, put it onto the battlefield" tokenises as
        // code CARD, language IT, separator ", put " — and the comma then
        // asserted a *nonfoil* separator verdict on a live foil (Charitable
        // Levy, 2026-08-06, twice). "IT" printed on a card qualifies; "it" in
        // rules text never does.
        guard tok.text.filter({ $0.isLetter }).allSatisfy({ $0.isUppercase })
        else { continue }
        // A token standing between the code and the language can only be the
        // separator misread as a glyph or two — a stray I for the bullet is
        // observed live. A word there means this is prose, whatever follows.
        if offset == 1, tokens[1].text.count > 2 { return nil }
        let lang = tok.text.uppercased().filter { $0.isLetter }
        if lang.count == 2, knownLanguages.contains(lang) {
            let sep = String(line[tokens[0].end..<tok.start])
                .trimmingCharacters(in: .whitespaces)
            return (code, lang, finishFromSeparator(sep))
        }
        // Junk glued to the language: "DOM•ENIO" is `DOM • EN` with two
        // characters of the artist-credit glyph fused onto the code. Live, that
        // cost a Zahid its set code and sent it to review holding a perfect
        // 076/269 — the number matched three printings and only the set could
        // separate them.
        //
        // The trailing run is capped at two characters, which is what keeps
        // this from eating names. OCR fuses a glyph or two onto "EN"; an
        // artist called Enrico does not become a two-letter language with a
        // short tail. "Illus Enrico" is the shape this must never match.
        if (3...4).contains(lang.count) {
            let head = String(lang.prefix(2))
            if knownLanguages.contains(head) {
                let sep = String(line[tokens[0].end..<tok.start])
                    .trimmingCharacters(in: .whitespaces)
                return (code, head, finishFromSeparator(sep))
            }
        }
        // A star misread as a letter and glued to the language: "MSH KEN" is a
        // starred border, not a word. Only the four letters Vision actually
        // produces for a star qualify, and only immediately before a language
        // — "KRAKEN" and "MOLTEN" carry the same letter-EN shape and must not
        // be read as foil set rows.
        if lang.count == 3, let first = lang.first, "KXAT".contains(first),
           knownLanguages.contains(String(lang.dropFirst())) {
            return (code, String(lang.dropFirst()), "foil")
        }
    }
    return nil
}

/// The language codes the frame prints. A closed list, because the whole point
/// of this token is to confirm that the row *is* the set row — an open two-letter
/// match would accept the first two letters of any artist's name.
let knownLanguages: Set<String> = [
    "EN", "DE", "FR", "IT", "ES", "PT", "JA", "KO", "RU", "ZH", "CS", "CT", "PH",
]

/// ownNumberRow matches a number printed on its own line.
///
/// There are two modern layouts, not one, and missing the second cost most of a
/// corpus stratum before it was noticed:
///
///     U 0247        rarity first, zero-padded number   (M15 era)
///     130/287 M     number over total, rarity last     (newer frames)
///     228           bare, no rarity printed
///
/// The second form is why the power/toughness guard cannot simply reject every
/// `N/M` standing on its own line. The discriminator is the rarity letter: a
/// collector pair is printed with one, and a power/toughness box never is.
/// `2/2` has no letter beside it and stays rejected; `130/287 M` does and is
/// read.
///
/// `bare` separates the pair form from the other two, because they are not
/// equally believable and `readPrinting` has to be able to tell. A pair carries
/// its own corroboration — a number, a denominator, usually a rarity — and is
/// the strongest printing evidence the footer holds. The bare forms are a shape
/// and nothing more: any two-to-four digit run, or a single letter that happens
/// to be a rarity followed by one. On a retro frame there is no own number row
/// at all, so a bare hit on a card that also prints a company row is far more
/// likely to be footer debris than a printing.
func ownNumberRow(_ line: String) -> (number: String, rarity: String, total: Int?, bare: Bool)? {
    let tokens = line.split(separator: " ").map(String.init)
        .filter { !$0.isEmpty }
    guard let first = tokens.first else { return nil }

    // A pair leading the line: "130/287 M", "012/216", "002/004 P HASCON 2017".
    if first.contains("/") {
        let halves = first.split(separator: "/")
        guard halves.count == 2,
              let n = digitsOnly(halves[0]), n.count <= 4,
              let t = digitsOnly(halves[1]), let total = Int(t), total > 0
        else { return nil }
        let trailing = tokens.count > 1
            ? tokens[1].uppercased().filter { $0.isLetter } : ""
        let hasRarity = trailing.count == 1 && knownRarities.contains(trailing)
        // Two independent reasons to believe this is a printing rather than a
        // creature's body. Either suffices, and both are needed in practice:
        // some frames print the rarity on the following line instead of this
        // one, leaving "012/216" standing entirely alone.
        //
        // A set of a hundred cards or more is the tell. No printed power and
        // toughness has a three-digit denominator, and every collector
        // denominator seen so far is well above it.
        guard hasRarity || total >= 100 else { return nil }
        // The denominator comes back too. It is the set's size, and the row
        // has already been parsed for it — the copyright-row form has always
        // reported it and this one silently did not, which left one field of
        // the footer populated or empty depending purely on which layout the
        // card happened to print.
        return (stripLeadingZeros(n), hasRarity ? trailing : "", total, false)
    }

    switch tokens.count {
    case 1:
        // A lone run of digits, which is also what a price sticker looks like.
        // Live, a `$18` beside the card read as collector number 18, matched no
        // printing of Meltdown, and took the card's perfectly good 2024 down
        // with it — a number that matches nothing is not neutral, it outranks
        // the copyright row and then fails the ranking outright.
        guard !tokens[0].contains(where: { currencyGlyphs.contains($0) }),
              let d = digitsOnly(tokens[0]), (2...4).contains(d.count)
        else { return nil }
        return (stripLeadingZeros(d), "", nil, true)
    case 2:
        // "R 0338" — the rarity, then the number.
        //
        // Believable in proportion to its width. This frame pads its number to
        // the set's size, so a real one is `0338`, `0247`, `0087` — three or
        // four characters, effectively always four. Two digits beside a single
        // letter is a different thing wearing the same shape, and `knownRarities`
        // holds eight of the commonest letters in English, so debris matches it
        // often: live, `T 89` fell out of a mangled `Illus.` credit and took
        // precedence over the `399` printed on the copyright row below it.
        let lead = tokens[0].uppercased().filter { $0.isLetter }
        guard lead.count == 1, knownRarities.contains(lead),
              let d = digitsOnly(tokens[1]), (2...4).contains(d.count)
        else { return nil }
        return (stripLeadingZeros(d), lead, nil, d.count < 3)
    default:
        return nil
    }
}

/// Glyphs that mean the digits beside them are a price and not a printing.
/// A card photographed on a desk is often photographed beside its price tag.
let currencyGlyphs: Set<Character> = ["$", "£", "€", "¥", "₹"]

/// Rarity letters the modern frame prints beside the collector number.
let knownRarities: Set<String> = ["C", "U", "R", "M", "S", "T", "L", "P"]

/// stripLeadingZeros turns the frame's zero-padded "0338" into the "338" the
/// catalog is keyed on, without turning "0" into "".
func stripLeadingZeros(_ s: String) -> String {
    let trimmed = String(s.drop { $0 == "0" })
    return trimmed.isEmpty ? "0" : trimmed
}
