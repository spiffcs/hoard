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
public let copyrightYearRE = try! NSRegularExpression(
    pattern: #"(?:19|20)\d{2}\s*[-–—]?\s*((?:19|20)\d{2})"#)
/// "123/264" — a collector number over its set total.
public let collectorPairRE = try! NSRegularExpression(
    pattern: #"(\d{1,5})\s*/\s*(\d{1,5})"#)
/// A year on its own, for the older frames that print no range.
public let copyrightLoneYearRE = try! NSRegularExpression(
    pattern: #"\b((?:19|20)\d{2})\b"#)
