// The collector block printed along the card's bottom border: where to look
// for it, and how to read a number, set code and finish marker out of it.

import BorderKit
import CoreGraphics
import Foundation

/// collectorBandFraction is how far up the *card* the collector band reaches, as a
/// fraction of the card's own height.
///
/// The card's own height is the only sane reference. An earlier version measured
/// the band against the frame instead, which fails the moment the card doesn't
/// reach the bottom of the frame: a card resting mid-frame with desk below it puts
/// its collector block ~38% up, well outside any plausible frame-relative band, and
/// the read comes back completely empty. See findCard.
///
/// 0.15 covers the collector block, which lives in the bottom ~6%, with enough room
/// left over for the tilt of a hand-held card. It does not reliably exclude the
/// lowest line of rules text or a creature's power/toughness — past roughly 8° of
/// turn those come along too — so parseCollectorInfo, not this band, is what has to
/// tell a collector number from a "2/2" printed above it.
let collectorBandFraction: CGFloat = 0.15

/// frameBandFallback is the frame-relative band used only when no card rectangle
/// could be found. It is deliberately the whole lower half: without card bounds
/// there is no way to know where the border sits, and a band that is too tall
/// merely adds lines for the patterns below to reject, while one that is too short
/// silently reads nothing at all.
let frameBandFallback: CGFloat = 0.5

// Collector info as printed. "123/264" is the common form; the solo form covers
// cards that print the number without a set total, optionally with a trailing
// rarity letter. The set/language pair is the M15-frame second line, e.g. "MH3 • EN".
//
// The separator in the set/language line is matched loosely: at this glyph size
// Vision reports the printed bullet as "-", ".", "|" and friends about as often as
// "•", so requiring a real bullet drops perfectly good reads. A bare space counts
// too.
//
// What keeps that looseness safe is the trailing token: it must be one of the
// language codes Magic actually prints. Accepting any two letters there matches
// ordinary prose — "cards equal to the sacrificed" uppercases to a tidy
// "EQUAL TO" and yields a set code of "EQUAL".
let cardLanguages = "EN|DE|FR|IT|ES|PT|JA|JP|KO|RU|ZH|ZHS|ZHT|CS|CT|HE|LA|AR|SA|PH"
// The rarity letter may trail the number (M15 frames: "330 R") or lead it
// (Marvel frames: "R 0330", where mythic's M also arrives as Cyrillic М until
// asciify folds it).
let collectorSoloRE = try! NSRegularExpression(pattern: #"^[A-Z]?\s*#?\s*(\d{1,5})\s*[A-Z]?$"#)
// The separator is captured, not skipped: modern frames print it as the foil
// marker — a star (★) on foil printings, a plain bullet (•) on nonfoil ones.
// Vision renders the star as "*", "+", and at this glyph size even as a
// letter (K, X, A, T — all observed live). Letter misreads get their own
// alternation that REQUIRES leading whitespace: "MSH KEN" is a starred
// border, but "KRAKEN" and "MOLTEN" contain the same letter-EN shape with no
// space, and matching those would boilerplate-kill real card titles. Older
// frames carry no marker; a bare-space match leaves both groups empty and
// the finish unknown.
let setLangRE = try! NSRegularExpression(
    pattern: #"\b([0-9A-Z]{3,5})(?:\s*([•·∙*★✦✧✶+.,:;|/\\―—–-])\s*|\s+([KXAT])\s*|\s+)(?:"#
        + cardLanguages + #")\b"#)

/// finishFromSeparator classifies a set/language separator glyph: star-shaped
/// reads (including the letter misreads) mean the printed foil marker,
/// dot-shaped ones the nonfoil bullet, and anything else (or nothing) stays
/// unknown rather than guessed.
func finishFromSeparator(_ sep: String) -> String {
    if sep.isEmpty { return "" }
    if "★✦✧✶*+KXAT".contains(sep) { return "foil" }
    if "•·∙.,:;|/\\―—–-".contains(sep) { return "nonfoil" }
    return ""
}

/// looksLikeAYear reports whether a bare number is really a printing year. Every
/// card carries a copyright year in the same block as the collector number, and on
/// its own line it is indistinguishable from a collector number by shape alone.
/// Magic has no four-digit collector numbers in this range, so the range is safe
/// to exclude outright.
func looksLikeAYear(_ s: String) -> Bool {
    guard s.count == 4, let n = Int(s) else { return false }
    return n >= 1993 && n <= 2035
}

/// lowercaseCount is how many lowercase letters a line holds, which is how the
/// collector block is told apart from rules text. The border block is set in caps,
/// digits and small caps, so it reads as all-uppercase; prose does not. It matters
/// because a card's rules text can carry a collector-number shape of its own —
/// "Create a 2/2 white Cat creature token" — and the band cannot always exclude it.
func lowercaseCount(_ s: String) -> Int {
    s.filter { $0.isLowercase }.count
}

/// setLangFurniture gates which lines may yield a set code. The set/language
/// line is border print — "MSC ★ EN", "MH3 • EN I TOMAS HONZ" — set in caps,
/// so it carries almost no lowercase. Rules text does, and because setLangRE
/// tolerates a bare space between the code and the language, ordinary prose
/// matches it constantly once asciify uppercases everything: "…and put it into
/// your hand" yields set PUT, language Italian. Three captures of Eternal
/// Dragon shipped `PUT` that way in one live session, and a wrong set code is
/// the failure that stays invisible until valuation.
///
/// Only the extraction is gated. boilerplate still uses the same regex to kill
/// lines, where a generous match is the safe direction.
func setLangFurniture(_ s: String) -> Bool {
    let letters = s.filter { $0.isLetter }.count
    guard letters > 0 else { return true }
    return lowercaseCount(s) * 4 <= letters
}


/// parseCollectorInfo pulls every collector-number candidate out of the bottom
/// band's text. That covers both places the number appears — the bottom-left
/// block on M15-frame cards (2014 onward) and the bottom centre on older ones —
/// and, crucially, more than one card's border at once: a card scanned off the
/// top of a stack shows a sliver of the card beneath it, whose block parses
/// exactly as well as the target's (observed live). Reporting all candidates
/// lets the caller keep the one that matches a real printing.
///
/// `lines` should arrive bottom-most first. Candidates are ranked by how little
/// prose they contain, falling back to that bottom-up order for ties, so real
/// border blocks outrank rules text that merely looks like them. Each number is
/// paired with the set code read nearest it in the band — the two lines of a
/// border block arrive adjacent.
func parseCollectorInfo(_ lines: [String]) -> [CollectorRead] {
    let ranked = lines.enumerated()
        .sorted { a, b in
            let (la, lb) = (lowercaseCount(a.element), lowercaseCount(b.element))
            return la == lb ? a.offset < b.offset : la < lb
        }
        .map { (offset: $0.offset, text: asciify($0.element), raw: $0.element) }

    var sets: [(offset: Int, code: String, finish: String)] = []
    for l in ranked {
        // asciify has already folded the case away, so the prose test has to
        // run against what Vision actually read.
        guard setLangFurniture(l.raw) else { continue }
        if let s = group(setLangRE, l.text) {
            // The separator lands in group 2 (symbols) or 3 (letter misreads
            // of the star); whichever matched carries the finish.
            let sep = group(setLangRE, l.text, 2) ?? group(setLangRE, l.text, 3) ?? ""
            sets.append((l.offset, s, finishFromSeparator(sep)))
        }
    }
    func setNear(_ offset: Int) -> (code: String, finish: String) {
        // The number line and its set line print adjacently, but the band's
        // bottom-up ordering interleaves them with the right column's
        // copyright lines — so "near" has to reach a few indices, with
        // nearest-wins keeping a stacked neighbour's set from stealing in.
        let nearest = sets.min { abs($0.offset - offset) < abs($1.offset - offset) }
        guard let nearest, abs(nearest.offset - offset) <= 4 else { return ("", "") }
        return (nearest.code, nearest.finish)
    }

    var reads: [CollectorRead] = []
    var seen = Set<String>()
    func add(_ offset: Int, _ n: String, pair: Bool) {
        let near = setNear(offset)
        let read = CollectorRead(number: normalizeNumber(n), set: near.code,
                                 finish: near.finish, pair: pair)
        let key = read.number + "/" + read.set
        if !seen.contains(key) {
            seen.insert(key)
            reads.append(read)
        }
    }
    // Pair form first: it is the harder shape to fake. A creature's
    // power/toughness reads exactly like a collector pair and shares the band
    // on frames whose stat box sits low (observed live: "2/2" became collector
    // number 2); what separates them is the total — no printed set counts
    // fewer than 20 cards — with the numerator width of zero-padded prints
    // ("0087/0383") as a second signal.
    for l in ranked {
        if let n = group(collectorPairRE, l.text), let total = group(collectorPairRE, l.text, 2),
           (Int(total) ?? 0) >= 20 || n.count >= 3 {
            add(l.offset, n, pair: true)
        }
    }
    // Bare numbers after: a lone number is much easier to confuse with a
    // planeswalker's loyalty or a copyright year.
    for l in ranked {
        let t = l.text.trimmingCharacters(in: .whitespaces)
        if let n = group(collectorSoloRE, t), !looksLikeAYear(n) {
            add(l.offset, n, pair: false)
        }
    }
    return Array(reads.prefix(4))
}
