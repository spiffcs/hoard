import CoreGraphics
import Foundation

struct RingStats {
    let median: CGFloat
    let mad: CGFloat
    let chroma: CGFloat
    let count: Int
}

func borderRing(_ px: PixelReader, _ g: CardGeometry, edgeV: CGFloat, samples: Int = 256) -> RingStats? {
    let along = CGPoint(x: cos(g.theta), y: sin(g.theta))
    let down = CGPoint(x: -sin(g.theta), y: cos(g.theta))
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
    let reference = sample(CardLayout.symbolU - boxU * 1.6)
    guard patch.count >= 200, reference.count >= 200 else { return nil }
    let base = medianOf(reference)
    let spread = medianAbsoluteDeviation(reference)
    let threshold = max(0.10, spread * 4)
    let differing = patch.filter { abs($0 - base) > threshold }
    return SymbolInk(coverage: CGFloat(differing.count) / CGFloat(patch.count),
                     contrast: abs(medianOf(patch) - base))
}

struct FooterTones {
    let dark: CGFloat
    let bright: CGFloat
    let darkFraction: CGFloat
    let chroma: CGFloat
    let clipHigh: CGFloat
    let clipLow: CGFloat

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
