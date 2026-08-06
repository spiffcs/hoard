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
    /// **These hold for pre-1998 only, and the symbol reader cannot ship until
    /// that is fixed.** Measured per era, the copyright row's left edge sits at
    /// 0.086 before 1998, 0.260 from 1998–2002, and 0.594 on the M15 frame —
    /// the line moves across the card as the frame is redesigned. Using the
    /// pre-1998 value on a 7th Edition card threw the predicted symbol
    /// position clean off the image. The era is recoverable from the same line
    /// that anchors on it (parseCopyrightCollector already reads its year, and
    /// a lone year means pre-1998 while a range means later), so the fix is a
    /// lookup keyed on that rather than a single constant.
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
    static func leftU(kind: AnchorKind, prefix: LinePrefix, year: Int) -> CGFloat? {
        // A lone year, or none at all, is the pre-collector-number era: those
        // frames print no range, so there is nothing else it could be.
        if year == 0 || year < 1998 {
            switch prefix {
            case .copyrightGlyph, .trademark: return 0.086   // n=79, p10 .083 p90 .102
            case .illus: return 0.097                        // n=23, p10 .091 p90 .099
            case .year: return 0.102                         // n=8
            }
        }
        if year <= 2002 {
            switch prefix {
            case .trademark: return 0.231                    // n=5, p10 .228 p90 .236
            case .year: return 0.271                         // n=7, p10 .263 p90 .325
            // "© 1993-2001…" spread 0.099 to 0.274 in this era — unusable.
            case .copyrightGlyph, .illus: return nil
            }
        }
        // The 8th Edition frame. Only the trademark read is tight enough to be
        // a landmark, and it is measured on clean scans; note that this is
        // precisely the line a 1080p desk photograph of this frame fails to
        // read at all, so live captures mostly anchor on the credit row
        // instead — whose left edge here is still too loose to use (n=15,
        // IQR 0.067) and is deliberately left nil until it is measured.
        switch prefix {
        case .trademark: return kind == .copyright ? 0.080 : nil   // n=21, p10 .075 p90 .083
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
}
