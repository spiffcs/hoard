// Reading pixels at reconstructed positions: the border ring, the footer
// patch that supplies the card's own black and white points, and the type
// line's right margin where the expansion symbol sits.

import CoreGraphics
import Foundation

/// RingStats is one sampled border ring.
struct RingStats {
    let median: CGFloat
    let mad: CGFloat
    let chroma: CGFloat
    let count: Int
}

/// borderRing samples one edge's border ring, walking along the card's own
/// axis rather than the frame's so a tilted card is sampled inside its border
/// rather than diagonally out of it. `edgeV` is the ring centre in card space.
func borderRing(_ px: PixelReader, _ g: CardGeometry, edgeV: CGFloat, samples: Int = 256) -> RingStats? {
    let along = CGPoint(x: cos(g.theta), y: sin(g.theta))
    let down = CGPoint(x: -sin(g.theta), y: cos(g.theta))
    // Signed distance from the footer row to the ring, down the card.
    let offset = (edgeV - g.anchorV) * g.heightPx
    var lumas: [CGFloat] = [], chromas: [CGFloat] = []
    lumas.reserveCapacity(samples)
    for i in 0..<samples {
        let t = (CGFloat(i) / CGFloat(samples - 1) - 0.5) * 2 * (g.halfSpanPx * 0.9)
        let x = g.origin.x + along.x * t + down.x * offset
        let y = g.origin.y + along.y * t + down.y * offset
        guard let l = px.luma(x, y), let c = px.chroma(x, y) else { continue }
        lumas.append(l); chromas.append(c)
    }
    guard lumas.count >= samples / 2 else { return nil }
    return RingStats(median: medianOf(lumas), mad: medianAbsoluteDeviation(lumas),
                     chroma: medianOf(chromas), count: lumas.count)
}

/// symbolInk measures how much of the type line's right margin is covered by
/// something other than the frame behind it — the expansion symbol, when the
/// set prints one.
///
/// Presence is the whole question here, and it is a far easier one than shape:
/// before Exodus the symbol is monochrome black with no rarity colour, so this
/// is "is there a glyph in that box" rather than "which glyph". It already
/// separates cases neither the year nor the border can — 4BB against Ice Age,
/// both black and both 1995, where 4th Edition's right margin is bare and Ice
/// Age's carries a snowflake.
///
/// The background is estimated from the same band just left of the symbol,
/// which is frame and nothing else, so this is a local contrast measure and
/// carries over from a scan to a desk lamp the way the border ratio does.
/// SymbolInk is how much of the type line's right margin is covered by ink, and
/// how hard that ink stands off the frame around it. Nothing consumes these yet
/// — they are the groundwork the expansion-symbol reader needs, kept reachable
/// on purpose (see docs/scanner-symbol-plan.md).
struct SymbolInk {
    let coverage: CGFloat
    let contrast: CGFloat
}

func symbolInk(_ px: PixelReader, _ g: CardGeometry) -> SymbolInk? {
    let boxU: CGFloat = 0.055, boxV: CGFloat = 0.026
    func sample(_ centreU: CGFloat) -> [CGFloat] {
        var out: [CGFloat] = []
        for i in 0..<24 {
            for j in 0..<16 {
                let u = centreU + (CGFloat(i) / 23 - 0.5) * boxU
                let v = CardLayout.symbolV + (CGFloat(j) / 15 - 0.5) * boxV
                guard let p = g.point(u: u, v: v), let l = px.luma(p.x, p.y) else { continue }
                out.append(l)
            }
        }
        return out
    }
    let patch = sample(CardLayout.symbolU)
    // The reference sits a symbol's width to the left: still the type-line
    // band, never the type text itself, which ends well before it.
    let reference = sample(CardLayout.symbolU - boxU * 1.6)
    guard patch.count >= 200, reference.count >= 200 else { return nil }
    let base = medianOf(reference)
    let spread = medianAbsoluteDeviation(reference)
    // "Different from the frame" has to mean different by more than the frame
    // varies on its own, or every mottled old-frame background reads as ink.
    let threshold = max(0.10, spread * 4)
    let differing = patch.filter { abs($0 - base) > threshold }
    return SymbolInk(coverage: CGFloat(differing.count) / CGFloat(patch.count),
                     contrast: abs(medianOf(patch) - base))
}

/// footerPatch samples the footer line's own box, giving Otsu both the ink and
/// the surface it is printed on.
/// FooterTones is the card's own printed black and white point, read off the
/// footer line under whatever light the desk happened to have — plus how much
/// of the patch is clipped, which is how a blown highlight announces itself.
///
/// Note what these are *not*: the border. On an old frame the footer is
/// printed on the coloured frame inside the border, so a white-bordered card's
/// footer background reads 0.72 where its border reads 0.93. That is exactly
/// why the ratio classifies rather than normalizes — the border is the thing
/// that sits outside the range the card prints its own footer with.
struct FooterTones {
    let dark: CGFloat
    let bright: CGFloat
    let darkFraction: CGFloat
    let chroma: CGFloat
    let clipHigh: CGFloat
    let clipLow: CGFloat

    /// The span the tone scale divides by. Too narrow and the line is not
    /// carrying legible ink, so the ratio is noise.
    var range: CGFloat { bright - dark }
}

func footerPatch(_ px: PixelReader, _ g: CardGeometry) -> FooterTones? {
    let along = CGPoint(x: cos(g.theta), y: sin(g.theta))
    let down = CGPoint(x: -sin(g.theta), y: cos(g.theta))
    let glyphPx = CardLayout.footerGlyphV * g.heightPx
    var lumas: [CGFloat] = [], chromas: [CGFloat] = []
    for i in 0..<192 {
        let t = (CGFloat(i) / 191 - 0.5) * 2 * (g.halfSpanPx * 0.98)
        for j in -3...3 {
            let d = CGFloat(j) / 3 * glyphPx * 0.75
            let x = g.origin.x + along.x * t + down.x * d
            let y = g.origin.y + along.y * t + down.y * d
            guard let l = px.luma(x, y), let c = px.chroma(x, y) else { continue }
            lumas.append(l); chromas.append(c)
        }
    }
    guard let split = otsu(lumas) else { return nil }
    let hi = CGFloat(lumas.filter { $0 > 0.99 }.count) / CGFloat(lumas.count)
    let lo = CGFloat(lumas.filter { $0 < 0.01 }.count) / CGFloat(lumas.count)
    return FooterTones(dark: split.dark, bright: split.bright,
                       darkFraction: split.darkFraction, chroma: medianOf(chromas),
                       clipHigh: hi, clipLow: lo)
}
