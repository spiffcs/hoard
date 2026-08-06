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

    for line in lines {
        // The copyright row carries the year, and on 1998-2003 frames the
        // collector number too. It is found by content — a plausible year plus
        // either the company name or a slash pair — because its exact spelling
        // does not survive OCR.
        if let (from, to) = years(in: line) {
            // Keep the widest range seen; a card prints one copyright row, but
            // the band pass sometimes splits it across two observations.
            if out.year == nil || to > out.year! {
                out.year = to
                out.yearFrom = from
            }
            if out.numberSource != .ownRow, let pair = collectorPair(in: line) {
                out.number = pair.number
                out.total = pair.total
                out.numberSource = .copyrightRow
            } else if out.numberSource == .none, let n = trailingNumber(in: line) {
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
            }
            continue
        }

        // The modern set row: code, a separator of some kind, a language, then
        // the artist. Checked before the number row because the code itself can
        // look like a bare token.
        if let set = setRow(in: line) {
            out.setCode = set.code
            out.language = set.language
            out.finish = set.finish
            if !set.finish.isEmpty { out.finishSource = "separator" }
            continue
        }

        // The modern number row, alone: "R 0338", "0338", "M 0087".
        if let n = ownNumberRow(line) {
            out.number = n.number
            out.rarity = n.rarity
            if let total = n.total { out.total = total }
            out.numberSource = .ownRow
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
    guard let last = line.split(separator: " ").last, let digits = digitsOnly(last)
    else { return nil }
    guard (1...4).contains(digits.count) else { return nil }
    if let n = Int(digits), plausibleYears.contains(n) { return nil }
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
    for tok in tokens.dropFirst().prefix(2) {
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
func ownNumberRow(_ line: String) -> (number: String, rarity: String, total: Int?)? {
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
        return (stripLeadingZeros(n), hasRarity ? trailing : "", total)
    }

    switch tokens.count {
    case 1:
        guard let d = digitsOnly(tokens[0]), (2...4).contains(d.count) else { return nil }
        return (stripLeadingZeros(d), "", nil)
    case 2:
        // "R 0338" — the rarity, then the number.
        let lead = tokens[0].uppercased().filter { $0.isLetter }
        guard lead.count == 1, knownRarities.contains(lead),
              let d = digitsOnly(tokens[1]), (2...4).contains(d.count)
        else { return nil }
        return (stripLeadingZeros(d), lead, nil)
    default:
        return nil
    }
}

/// Rarity letters the modern frame prints beside the collector number.
let knownRarities: Set<String> = ["C", "U", "R", "M", "S", "T", "L", "P"]

/// stripLeadingZeros turns the frame's zero-padded "0338" into the "338" the
/// catalog is keyed on, without turning "0" into "".
func stripLeadingZeros(_ s: String) -> String {
    let trimmed = String(s.drop { $0 == "0" })
    return trimmed.isEmpty ? "0" : trimmed
}
