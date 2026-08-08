// The two patterns that say "this line is a card's footer furniture".
//
// Here rather than with the parsers that also use them, because
// copyrightFurniture is what anchors the border reader's geometry and BorderKit
// cannot depend on the pipelines that consume it. ScanKit imports them back.

import Foundation

/// The copyright row's year. A reprint prints a range — "© 1993-2003 Wizards of
/// the Coast, Inc. 95/350" — and the second year is the one that dates the
/// printing. The dash arrives as "-", "–", "—" or, at this glyph size, a bare
/// space, hence the optional separator.
public let copyrightYearRE = footerPattern(
    #"(?:19|20)\d{2}\s*[-–—]?\s*((?:19|20)\d{2})"#)
/// "123/264" — a collector number over its set total.
public let collectorPairRE = footerPattern(
    #"(\d{1,5})\s*/\s*(\d{1,5})"#)
/// A year on its own, for the older frames that print no range.
public let copyrightLoneYearRE = footerPattern(
    #"\b((?:19|20)\d{2})\b"#)

/// footerPattern compiles one of the literals above without `try!` — these
/// were the codebase's only three, and a regex is a poor place for its only
/// crash. The literals have compiled on every build since they were written,
/// so the fallback is unreachable today; its job is the future edit that
/// breaks one. That edit now degrades to `(?!)` — a lookahead that can never
/// succeed — so the furniture is simply never found and the card goes to
/// review, instead of the first scan of a session taking the app down.
/// (`(?!)` is itself a constant known to compile; the final chained fallback
/// exists only to satisfy the type checker.)
func footerPattern(_ pattern: String) -> NSRegularExpression {
    (try? NSRegularExpression(pattern: pattern))
        ?? (try? NSRegularExpression(pattern: "(?!)"))
        ?? NSRegularExpression()
}
