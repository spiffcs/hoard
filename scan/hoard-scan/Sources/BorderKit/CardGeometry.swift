import CoreGraphics
import Foundation

struct CardGeometry {
    let heightPx: CGFloat
    let origin: CGPoint
    let theta: CGFloat
    let halfSpanPx: CGFloat
    let anchorV: CGFloat
    let anchorLeft: CGPoint?
    let anchorLeftU: CGFloat
    let scaleFromBaseline: CGFloat
    let scaleFromGlyph: CGFloat
}

func cardGeometry(footer: Line, kind: AnchorKind, title: Line?, frame: FrameEvidence,
                  frameW: CGFloat, frameH: CGFloat) -> CardGeometry? {
    let anchorV = kind == .copyright ? CardLayout.footerV : CardLayout.creditV
    guard let quad = footer.quad else { return nil }
    func px(_ p: CGPoint) -> CGPoint { CGPoint(x: p.x * frameW, y: (1 - p.y) * frameH) }
    let tl = px(quad.topLeft), tr = px(quad.topRight)
    let bl = px(quad.bottomLeft)
    let theta = atan2(tr.y - tl.y, tr.x - tl.x)
    let lengthPx = hypot(tr.x - tl.x, tr.y - tl.y)
    let glyphPx = hypot(bl.x - tl.x, bl.y - tl.y)
    guard lengthPx > 1, glyphPx > 0.5 else { return nil }

    let origin = CGPoint(x: (tl.x + tr.x + bl.x + px(quad.bottomRight).x) / 4,
                         y: (tl.y + tr.y + bl.y + px(quad.bottomRight).y) / 4)

    let leftU = lineOpener(footer.text, kind: kind)
        .flatMap { CardLayout.leftU(kind: kind, prefix: $0, frame: frame) }
    let anchorLeft = leftU != nil
        ? CGPoint(x: (tl.x + bl.x) / 2, y: (tl.y + bl.y) / 2) : nil
    let anchorLeftU = leftU ?? 0

    let fromGlyph = glyphPx / CardLayout.footerGlyphV
    var fromBaseline = fromGlyph
    if let title = title {
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

enum LinePrefix {
    case trademark
    case copyrightGlyph
    case year
    case illus
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
        if token.prefix(5).filter({ $0.isNumber }).count >= 4 { return .year }
        let letters = token.filter { $0.isLetter }
        if letters.count <= 2 && letters.count == token.count { return .trademark }
        return nil
    }
}

extension CardGeometry {
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
