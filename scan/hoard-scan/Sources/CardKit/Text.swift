// Making OCR output comparable without making it lie.
//
// Everything here exists because of what real captures actually came back as.
// The iPhone head reads the bottom of a card cleanly — the pixels are no longer
// the problem — but "cleanly" still means the trademark glyph arrives as IM, TN,
// rM or T, "Illus." as fllus, or Ius., "Inc." as Inr., and one 1993 as 1093.
//
// The temptation is to fold aggressively so everything matches. That is how a
// parser starts inventing cards: fold O to 0 everywhere and a set code becomes a
// number, fold l to 1 and an illustrator becomes a collector number. So the
// folding here is narrow and positional — applied where the grammar already says
// what kind of character belongs, and nowhere else.

import Foundation

/// Characters that OCR confuses in *digit* positions. Applied only where the
/// surrounding structure has already established that digits are expected — a
/// year, a collector number — never to free text.
private let digitLookalikes: [Character: Character] = [
    "O": "0", "o": "0", "D": "0", "Q": "0",
    "l": "1", "I": "1", "i": "1", "|": "1",
    "S": "5", "s": "5",
    "B": "8",
    "Z": "2", "z": "2",
    "G": "6",
]

/// digitsOnly rewrites a run that ought to be numeric, folding the lookalikes
/// above. Returns nil if what is left is not plausibly a number, so a caller
/// gets a refusal rather than a guess.
///
/// **Folding repairs a number; it must never manufacture one.** A token has to
/// be already mostly digits to qualify. Without that rule the World
/// Championship frame's sideboard marker `SB` folds to `58` and `GB` to `68`,
/// and cards that print no collector number at all acquire one — measured, on
/// 18% of the corpus's pre-1998 gold-bordered stratum, by this function before
/// the rule existed. An invented printing is the worst thing this pipeline can
/// produce, and it is worth refusing a real number occasionally to avoid it.
func digitsOnly(_ s: some StringProtocol) -> String? {
    var out = ""
    var real = 0
    var dropped = 0
    var considered = 0
    for ch in s {
        if ch == " " || ch == "," || ch == "." { continue }
        considered += 1
        if ch.isNumber {
            out.append(ch)
            real += 1
        } else if let folded = digitLookalikes[ch] {
            out.append(folded)
        } else {
            // A stray character is dropped rather than fatal. Aborting on one
            // threw away collector rows that had been read almost perfectly —
            // `n0322` and `R DA62` both reached the parser intact and were
            // refused over a single junk letter, which was most of a 27%
            // review-queue rate on a live session.
            dropped += 1
        }
    }
    // Both guards measure against what came *in*, not what came out — and that
    // distinction is the whole safety property. An earlier version compared
    // real digits to the *output* length, which meant dropping more junk made a
    // token look more numeric: `a7KA2` shed three letters and its two survivors
    // read as a clean "72", inventing a collector number on a card that prints
    // none. Repair may fix a number; it may not assemble one out of debris.
    guard !out.isEmpty, dropped <= 1, real * 2 >= considered else { return nil }
    return out
}

/// squashed is the aggressive form, for *comparison only* — never for anything
/// that ends up in a field. Lowercased, non-alphanumerics dropped.
func squashed(_ s: some StringProtocol) -> String {
    String(s.lowercased().filter { $0.isLetter || $0.isNumber })
}

/// looksLikeCompanyRow reports whether a line is the printer's credit line, by
/// content rather than by exact spelling.
///
/// Matching on "Wizards of the Coast" verbatim fails constantly — observed:
/// `Wizacd of dox Coкel`, `Wizards of the Coast,inc.`, `Wizard of dox`. Matching
/// on a couple of durable fragments does not. `coast` survives nearly every
/// mangling seen so far; `wizard` survives most; the year is the strongest
/// signal of all and is checked separately.
func looksLikeCompanyRow(_ line: String) -> Bool {
    let s = squashed(line)
    return s.contains("coast") || s.contains("wizard") || s.contains("wizard5")
}

/// looksLikeIllustratorRow spots the artist credit. "Illus." is the durable part
/// and it is also the part OCR mangles most, so this matches a prefix of the
/// squashed form rather than the token.
func looksLikeIllustratorRow(_ line: String) -> Bool {
    let s = squashed(line)
    return s.hasPrefix("illus") || s.hasPrefix("ilus") || s.hasPrefix("fllus")
        || s.hasPrefix("flus") || s.hasPrefix("lllus")
}

/// The window of years a Magic card can carry. Anything outside it is a misread
/// or a page number, and treating it as a year is how a card acquires a printing
/// it never had.
let plausibleYears = 1993...2035

/// year extracts a four-digit year from a token, folding digit lookalikes, and
/// refuses anything outside the plausible window.
func year(from token: some StringProtocol) -> Int? {
    guard let digits = digitsOnly(token), digits.count == 4,
          let n = Int(digits), plausibleYears.contains(n)
    else { return nil }
    return n
}
