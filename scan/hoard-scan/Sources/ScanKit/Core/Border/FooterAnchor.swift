// Picking the line to reconstruct the card from. The anchor must be proven by
// content — no amount of edge contrast can fake "this line says Wizards of
// the Coast" — with a positional fallback for frames that draw the credit.

import Foundation

/// illusToken matches a line that opens with the illustrator abbreviation,
/// however mangled — "Illus.", "Illus:", "Tins.". Deliberately looser than
/// artistCredit, which demands the whole "Illus. First Last" shape because a
/// false positive *there* eats a card's name. Here a false positive only
/// yields geometry that then fails its own agreement and ring checks, so the
/// trade runs the other way: the strict form missed "Illus: © Jeff A: Menges"
/// (four words) and a bare "Illus." on its own line, and those are footers.
func illusToken(_ s: String) -> Bool {
    guard let first = s.split(whereSeparator: { $0.isWhitespace }).first else { return false }
    guard let last = first.last, last == "." || last == ":" || last == "," else { return false }
    let head = first.lowercased().filter { $0.isLetter }
    guard head.count >= 3, head.count <= 6 else { return false }
    return editDistance(head, "illus") <= 2
}

/// Which row of the footer an anchor landed on. They sit at different heights,
/// so the reconstruction has to know which one it is looking at.
enum AnchorKind: String {
    case copyright
    case credit
}

/// personalNameLine matches a bare "First Last" or "First M. Last" with nothing
/// else on the line. On its own this says almost nothing — it is exactly the
/// shape of a card name, which is why it may never identify a *title* — but at
/// the foot of a card it is the illustrator, and that is a position no title
/// occupies.
func personalNameLine(_ s: String) -> Bool {
    let words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    guard words.count == 2 || words.count == 3 else { return false }
    return words.allSatisfy { w in
        guard let f = w.first, f.isUppercase else { return false }
        // Allow a middle initial ("Monte M. Moore") but nothing else odd.
        return w.dropFirst().allSatisfy { $0.isLetter || $0 == "." || $0 == "'" || $0 == "-" }
    }
}

/// footerAnchor picks the line to reconstruct the card from: the lowest one
/// that says what it is. Recognition is by *content* — this line claims to be
/// the copyright or the illustrator credit — which is exactly the provenance
/// the deleted classifier lacked when it tried to read a border off whichever
/// edge happened to have contrast.
///
/// The 8th Edition frame forces one exception, and it is worth stating plainly
/// because it is the only anchor here not proven by what the line says. That
/// frame draws a paintbrush where every earlier one writes "Illus.", so its
/// credit arrives as a bare name and no content rule can ever match it — a
/// whole 8th Edition pile produced zero anchors for exactly this reason. What
/// identifies it instead is *where it is*: the bottom-most line on a card that
/// has several, which is a position a title never occupies. It is taken only
/// when no content-proven anchor exists, and everything downstream still has
/// to agree — the two scale estimates, the card fitting its frame, the ring
/// being one uniform surface, the tone landing outside the card's own range.
func footerAnchor(_ lines: [Line]) -> (line: Line, kind: AnchorKind)? {
    let proven = lines.compactMap { line -> (Line, AnchorKind)? in
        if copyrightFurniture(line.text) { return (line, .copyright) }
        if artistCredit(line.text) || illusToken(line.text) { return (line, .credit) }
        return nil
    }
    // Lowest on the card. Vision's y grows upward, so that is the minimum.
    if let best = proven.min(by: { $0.0.box.midY < $1.0.box.midY }) { return best }

    // Positional fallback: a bare personal name, lowest of at least three
    // lines, with real text above it. Fewer lines than that and "lowest" means
    // nothing — a lone credit-shaped line could as easily be the card's name.
    return positionalCredit(lines).map { ($0, .credit) }
}

/// positionalCredit finds the illustrator credit by where it sits rather than
/// what it says: the bottom-most bare personal name on a card that read several
/// lines. Split out from footerAnchor so the measurement can be reported even
/// when a content-proven anchor won.
func positionalCredit(_ lines: [Line]) -> Line? {
    guard lines.count >= 3 else { return nil }
    let ys = lines.map { $0.box.midY }
    guard let low = ys.min(), let high = ys.max(), high - low > 0.05 else { return nil }
    // "At the foot" is the bottom quarter of whatever was read, not literally
    // the lowest line: the power/toughness box and stray flavour fragments sit
    // down there too and are usually read *below* the credit.
    let foot = low + (high - low) * 0.25
    let candidates = lines.filter { $0.box.midY <= foot && personalNameLine($0.text) }
    guard let credit = candidates.min(by: { $0.box.midY < $1.box.midY }) else { return nil }
    let above = lines.filter { $0.box.midY > credit.box.midY + 0.02 }
    guard above.count >= 2 else { return nil }
    return credit
}
