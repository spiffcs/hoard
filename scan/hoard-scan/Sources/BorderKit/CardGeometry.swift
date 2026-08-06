// Recovering the card's own scale and orientation from anchored text, so the
// ring can be sampled on the full-resolution frame rather than the crop.

import CoreGraphics
import Foundation

/// CardGeometry is the card's own scale and orientation, recovered from where
/// two known lines sit rather than from any detected edge.
struct CardGeometry {
    /// Card height in pixels.
    let heightPx: CGFloat
    /// Footer centre in pixels, y measured downward.
    let origin: CGPoint
    /// Text tilt, radians, in the same y-down frame.
    let theta: CGFloat
    /// Half the footer line's length in pixels — the span along the card that
    /// is provably printed on it, and therefore safe to sample across.
    let halfSpanPx: CGFloat
    /// Where in card space the anchor row sits — every offset is measured from
    /// here, so it depends on which of the two footer rows we latched onto.
    let anchorV: CGFloat
    /// The anchor line's left end in pixels, and where that sits in card space.
    /// nil when the line did not demonstrably start at its beginning, since a
    /// truncated read's left edge is wherever OCR gave up rather than a
    /// landmark. Only the symbol reader needs this; the border ring is sampled
    /// along the anchor's own span and never needs to know the card's width.
    let anchorLeft: CGPoint?
    let anchorLeftU: CGFloat
    /// The two independent estimates, kept so the caller can make them agree.
    let scaleFromBaseline: CGFloat
    let scaleFromGlyph: CGFloat
}

/// cardGeometry reconstructs the card from a footer line and, when there is
/// one, a title line above it.
///
/// Two estimates of the card's height, deliberately: the title-to-footer
/// baseline spans 86% of the card, so it is precise but assumes both anchors
/// are the lines we think they are; the footer's own glyph height assumes
/// nothing but is coarse. Neither is trusted alone. They have to agree, and
/// when they do not the frame is not what it looks like and we stop.
func cardGeometry(footer: Line, kind: AnchorKind, title: Line?, frame: FrameEvidence,
                  frameW: CGFloat, frameH: CGFloat) -> CardGeometry? {
    let anchorV = kind == .copyright ? CardLayout.footerV : CardLayout.creditV
    guard let quad = footer.quad else { return nil }
    // Into pixels, y downward.
    func px(_ p: CGPoint) -> CGPoint { CGPoint(x: p.x * frameW, y: (1 - p.y) * frameH) }
    let tl = px(quad.topLeft), tr = px(quad.topRight)
    let bl = px(quad.bottomLeft)
    let theta = atan2(tr.y - tl.y, tr.x - tl.x)
    let lengthPx = hypot(tr.x - tl.x, tr.y - tl.y)
    let glyphPx = hypot(bl.x - tl.x, bl.y - tl.y)
    guard lengthPx > 1, glyphPx > 0.5 else { return nil }

    let origin = CGPoint(x: (tl.x + tr.x + bl.x + px(quad.bottomRight).x) / 4,
                         y: (tl.y + tr.y + bl.y + px(quad.bottomRight).y) / 4)

    // The line's left end is a card landmark only when the line demonstrably
    // begins at its own beginning. A read that lost its opening — "the Coast,
    // inc. All rights reserved.", observed on a real photograph — starts
    // wherever OCR gave up, which is not a landmark at all. Requiring the
    // opener is the same provenance-by-content rule the vertical anchor uses.
    let leftU = lineOpener(footer.text, kind: kind)
        .flatMap { CardLayout.leftU(kind: kind, prefix: $0, frame: frame) }
    let anchorLeft = leftU != nil
        ? CGPoint(x: (tl.x + bl.x) / 2, y: (tl.y + bl.y) / 2) : nil
    let anchorLeftU = leftU ?? 0

    let fromGlyph = glyphPx / CardLayout.footerGlyphV
    var fromBaseline = fromGlyph
    if let title = title {
        // Distance between the two rows along the card's own downward axis,
        // so a tilted card measures the same as a square one.
        let titleMid = CGPoint(x: title.box.midX * frameW, y: (1 - title.box.midY) * frameH)
        let down = CGPoint(x: -sin(theta), y: cos(theta))
        let gap = (origin.x - titleMid.x) * down.x + (origin.y - titleMid.y) * down.y
        let span = anchorV - CardLayout.titleV
        if gap > 0, span > 0 { fromBaseline = gap / span }
    }
    return CardGeometry(heightPx: fromBaseline, origin: origin, theta: theta,
                        halfSpanPx: lengthPx / 2, anchorV: anchorV,
                        anchorLeft: anchorLeft, anchorLeftU: anchorLeftU,
                        scaleFromBaseline: fromBaseline, scaleFromGlyph: fromGlyph)
}

/// lineOpener reports whether a footer line starts where the printed row does.
/// The copyright row opens with © or ™ or its year; the credit row opens with
/// the illustrator abbreviation, which illusToken has already matched at the
/// front. Anything else is a line OCR joined partway through.
/// What the anchor line begins with. The landmark's position depends on this
/// as much as on the frame era, because a line that lost its opening starts
/// somewhere else entirely — see CardLayout.leftU.
enum LinePrefix {
    case trademark       // "™ &" — the row's true start from 1998 on
    case copyrightGlyph  // "©" or "™" alone
    case year            // the range or year, opening lost
    case illus           // the illustrator abbreviation
}

func lineOpener(_ s: String, kind: AnchorKind) -> LinePrefix? {
    let t = s.trimmingCharacters(in: .whitespaces)
    guard let token = t.split(whereSeparator: { $0.isWhitespace }).first else { return nil }
    switch kind {
    case .credit:
        return (illusToken(t) || artistCredit(t)) ? .illus : nil
    case .copyright:
        if token.contains("©") || token.contains("™") || token.hasPrefix("(") {
            return .copyrightGlyph
        }
        // "©1995" often loses its symbol and arrives as "01995" or "1995".
        if token.prefix(5).filter({ $0.isNumber }).count >= 4 { return .year }
        // From 1998 the line opens "™ & © 1993-2001", and the trademark mark
        // is what this glyph size mangles most: "TM", "TH", "IM", "Iм" have
        // all been observed. Two letters at the head of a line already known
        // to be a copyright line is the preamble, not prose — every way this
        // line can start *late* begins with a real word ("the Coast, inc.").
        let letters = token.filter { $0.isLetter }
        if letters.count <= 2 && letters.count == token.count { return .trademark }
        return nil
    }
}

extension CardGeometry {
    /// point maps a position in card space — u across the width, v down the
    /// height, both 0…1 from the card's top-left — into frame pixels. nil when
    /// no horizontal landmark was established, which is most of what makes
    /// this safe: without it there is no honest way to say where the card's
    /// right-hand side is, and the symbol lives there.
    func point(u: CGFloat, v: CGFloat) -> CGPoint? {
        guard let left = anchorLeft else { return nil }
        let widthPx = heightPx * cardAspect
        let along = CGPoint(x: cos(theta), y: sin(theta))
        let down = CGPoint(x: -sin(theta), y: cos(theta))
        let du = (u - anchorLeftU) * widthPx
        let dv = (v - anchorV) * heightPx
        return CGPoint(x: left.x + along.x * du + down.x * dv,
                       y: left.y + along.y * du + down.y * dv)
    }
}
