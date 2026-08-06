// Where the card's furniture sits in card space. Fitted against scan/corpus,
// where the card fills the frame so its rect is known exactly; every constant
// carries the sample it came from.

import CoreGraphics

/// Magic cards are 63×88 mm.
let cardAspect: CGFloat = 63.0 / 88.0

/// Where the card's furniture sits in card space, with v running 0…1 down from
/// the card's top edge. Fitted on scan/corpus, where the card *is* the image so
/// the card rect is known exactly — see scan/corpus/border.sh.
enum CardLayout {
    /// Centre of the copyright row, the lowest text on the card.
    static let footerV: CGFloat = 0.9375
    /// Centre of the illustrator credit, one row above it — or of the two rows
    /// together, since Vision often returns them as a single observation.
    /// Anchoring on the wrong one of these costs ~1.5% of scale, which is most
    /// of the border's own thickness, so they are kept apart.
    static let creditV: CGFloat = 0.9212
    /// Centre of the title row.
    static let titleV: CGFloat = 0.0625
    /// Printed border thickness top and bottom, as a fraction of card height.
    static let borderV: CGFloat = 0.039
    /// How deep into that border to sample, as a fraction of its thickness —
    /// clear of both the card's cut edge and the printed frame inside it.
    static let ringDepth: CGFloat = 0.45
    /// A footer line's glyph-box height as a fraction of card height. The local
    /// scale estimate, which exists only to disagree with the long one.
    static let footerGlyphV: CGFloat = 0.0174
    /// Where to sample the card's own frame, just inside the border. The
    /// footer text is printed here — on old frames it sits on the coloured
    /// frame, not on the border, which is worth knowing because it means the
    /// footer's own two tones are *not* the card's paper and ink.
    static let innerV: CGFloat = 0.950
    /// Left edge of each footer row, as a fraction of card *width*. Measured
    /// across 120 pre-1998 corpus cards: the copyright row starts at 0.086
    /// (p10 0.083, p90 0.102) and the credit row at 0.097 (p10 0.089, p90
    /// 0.099) — the © glyph's box reaches a little further left than a letter.
    ///
    /// This is the card's horizontal landmark, and it has to be the *left*
    /// edge because the right one is wherever the sentence happened to end:
    /// across the same cards it ranges 0.42 to 0.62. Worth recording that
    /// docs/scanner-limits.md called this footer "two centred rows" — it is
    /// left-aligned, and the measurement is what says so.
    ///
    /// **These two are the pre-1998 values only** — kept because they are what
    /// the era was originally fitted to, and superseded for every other frame by
    /// `leftU` below, which is keyed per era because the row moves across the
    /// card as the frame is redesigned: 0.086 before 1998, 0.233 from
    /// 1998-2002, 0.079 on the 8th Edition frame and 0.593 on M15.
    static let copyrightLeftU: CGFloat = 0.086
    static let creditLeftU: CGFloat = 0.097

    /// leftU is the horizontal landmark: where the anchor line's left end sits
    /// as a fraction of card width. It is keyed on the *frame era* and on what
    /// the line actually starts with, because both move it — and it returns nil
    /// wherever that combination has never been measured.
    ///
    /// The prefix matters as much as the era, which is not obvious until you
    /// measure it. A line read as "™ & © 1993-2003 Wizards…" starts where the
    /// row starts; the same row read as "© 1993-2003 Wizards…" or "1993-2003
    /// Wizards…" has lost its opening and starts further right. Keyed only on
    /// era, the 2003-2014 stratum looked hopelessly bimodal — median 0.080 with
    /// a p90 of 0.214. Split by prefix, the trademark reads are 0.080 with a
    /// p10 of 0.075 and a p90 of 0.083, and the noise was entirely the lines
    /// that began late.
    ///
    /// nil is the important part. Guessing does not degrade gracefully: the
    /// landmark is at u≈0.08 and the expansion symbol at u≈0.87, so the lever
    /// is most of the card's width and a wrong offset throws the predicted
    /// symbol clean off the image — which is exactly what a 7th Edition card
    /// did while this was a single constant. Nothing but the symbol reader
    /// consumes it, and it would rather have no answer than a wrong one.
    static func leftU(kind: AnchorKind, prefix: LinePrefix,
                      frame: FrameEvidence) -> CGFloat? {
        let year = frame.year
        // A lone year, or none at all, is the pre-collector-number era: those
        // frames print no range, so there is nothing else it could be.
        //
        // Reading "no year" as pre-1998 is checked rather than assumed: over
        // scan/corpus the cards whose year does not read anchor on their credit
        // row at 0.097 (n=27, IQR 0.008), which is the pre-1998 credit value
        // exactly.
        if year == 0 || year < 1998 {
            switch prefix {
            case .copyrightGlyph, .trademark: return 0.086   // n=80, p10 .083 p90 .099
            case .illus: return 0.097                        // n=30, p10 .091 p90 .102
            case .year: return 0.102                         // n=3
            }
        }
        if year <= 2002 {
            switch prefix {
            case .trademark: return 0.233                    // n=6, IQR .006
            case .year: return 0.260                         // n=9, IQR .005
            // "© 1993-2001…" still spreads .214 to .274 — too loose to be a
            // landmark when the lever to the card's far side is 0.8 of a width.
            case .copyrightGlyph, .illus: return nil
            }
        }
        // From here there are *two* frames, not one, and reading them as one is
        // what this function got wrong for as long as it existed.
        //
        // The M15 frame moved the copyright row from the card's left edge to
        // under its centre: measured over scan/corpus it sits at **0.593**
        // (n=35, IQR 0.004) where the 8th Edition frame sits at 0.079 (n=16,
        // IQR 0.004). Both are tight; they are simply different places. Until
        // this branch existed every M15 card was told 0.080 and every position
        // derived from it landed half a card too far left.
        //
        // Nothing consumed `point()` yet, which is the only reason this was
        // survivable — see the note on symbolU below.
        if frame.isM15 {
            switch prefix {
            case .trademark: return kind == .copyright ? 0.593 : nil      // n=35, IQR .004
            case .copyrightGlyph: return kind == .copyright ? 0.596 : nil // n=3, IQR .004
            case .year, .illus: return nil
            }
        }
        // The 8th Edition frame. Only the trademark read is tight enough to be
        // a landmark, and it is measured on clean scans; note that this is
        // precisely the line a 1080p desk photograph of this frame fails to
        // read at all, so live captures mostly anchor on the credit row
        // instead — whose left edge here is still too loose to use and is
        // deliberately left nil until it is measured.
        switch prefix {
        case .trademark: return kind == .copyright ? 0.079 : nil   // n=16, IQR .004
        case .copyrightGlyph, .year, .illus: return nil
        }
    }
    /// The expansion symbol, at the right end of the type line. The old frame
    /// (Ice Age's snowflake, 7th Edition's "7") puts it at 0.877/0.578; the
    /// redesigned 8th Edition frame at 0.867/0.590. One patch covers both.
    /// Core sets before 6th Edition print nothing here — 4th Edition's right
    /// margin is empty — and that absence is the signal.
    static let symbolU: CGFloat = 0.872
    static let symbolV: CGFloat = 0.584

    /// The foil sparkle, at the text box's lower-left corner — the eight-point
    /// starburst with a comet trail that a retro-frame foil prints and a
    /// nonfoil does not. See Sparkle.swift.
    ///
    /// Measured on 27 live foils across two sessions, spanning the 2003 frame
    /// and the 2024 retro frame: the marker sits in the same place on both,
    /// which is why the reader is not keyed on era. It is emphatically *not*
    /// centred by eye — these came from aligning every capture against a
    /// running average and taking where they settled.
    ///
    /// Only two eras are actually fitted. The 2001, 2004 and 2006 cards in the
    /// corpus are all nonfoils, so no foil between the two fitted points has
    /// ever been measured; frames in that gap rely on the search window
    /// absorbing the difference rather than on a constant that describes them.
    static let sparkleU: CGFloat = 0.205
    static let sparkleV: CGFloat = 0.889
}

/// AnchorMeasurement is where a footer anchor actually sat, reported so
/// `CardLayout.leftU`'s table can be fitted from ground truth rather than
/// argued about.
///
/// `leftU` returns a *card-space* fraction, so fitting it needs images where
/// card space is known exactly — the clean scans in scan/corpus, where the card
/// is the whole image. That is how the table was originally built, and
/// measuring it any other way is what let it drift: taken through the wide
/// flatten instead, the same pre-1998 anchors read 0.045 against a true 0.086,
/// because the located quad does not reliably bound the printed card.
public struct AnchorMeasurement: Sendable {
    public let kind: String       // "copyright" | "credit"
    public let prefix: String     // "trademark" | "copyrightGlyph" | "year" | "illus"
    /// The anchor line's left edge, as a fraction of the image's width. On a
    /// clean scan that is card space directly.
    public let leftU: CGFloat
    public let text: String
}

/// measureAnchor picks the anchor the border reader would pick and reports
/// where it sat. Corpus tooling; nothing in a live read calls it.
public func measureAnchor(_ lines: [Line], year: Int) -> AnchorMeasurement? {
    guard let anchor = footerAnchor(lines) else { return nil }
    guard let prefix = lineOpener(anchor.line.text, kind: anchor.kind) else { return nil }
    let name: String
    switch prefix {
    case .trademark: name = "trademark"
    case .copyrightGlyph: name = "copyrightGlyph"
    case .year: name = "year"
    case .illus: name = "illus"
    }
    return AnchorMeasurement(kind: anchor.kind.rawValue, prefix: name,
                             leftU: anchor.line.box.minX, text: anchor.line.text)
}

/// What the footer says about which frame the card is printed with.
///
/// The copyright year alone cannot answer it, and that is not a subtlety — the
/// M15 frame shipped with Magic 2015 in **July 2014**, so 2014 has cards of both
/// designs and every year after it has only the new one. A table keyed on the
/// year alone therefore has one bucket that must hold two frames whose
/// copyright rows sit half a card apart.
///
/// So 2014 is decided on evidence rather than assumed: the M15 frame is the
/// first to print a set/language row, and the first to print the collector
/// number on a line of its own rather than in the tail of the copyright row.
/// Either one is enough. Measured over scan/corpus, requiring *both* misses
/// three real M15 cards (a Snakeskin Veil whose set code did not read, two
/// Unsanctioned cards whose number did not), and requiring *either* one alone
/// without the year would mislabel four older cards whose joke-set "code" is a
/// misparse — `S.N.O.T.` reading as set `CYRIL`. The year does the work
/// everywhere except the one year it cannot.
public struct FrameEvidence: Sendable {
    public let year: Int
    public let hasSetCode: Bool
    public let numberOnOwnRow: Bool

    public init(year: Int, hasSetCode: Bool = false, numberOnOwnRow: Bool = false) {
        self.year = year
        self.hasSetCode = hasSetCode
        self.numberOnOwnRow = numberOnOwnRow
    }

    /// Whether this is the M15 frame or later.
    public var isM15: Bool {
        if year >= 2015 { return true }
        if year == 2014 { return hasSetCode || numberOnOwnRow }
        return false
    }
}
