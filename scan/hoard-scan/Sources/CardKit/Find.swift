import CoreImage
import CoreGraphics
import Foundation
import Vision

public struct LocatedCard {
    public var bounds: CGRect
    public var image: CGImage
    public var wide: CGImage?
    public var wideMarginUsed: CGFloat = 0

    public static let wideMargin: CGFloat = 0.10
}

let ciContext = CIContext()

public func uprighted(_ cg: CGImage, _ orientation: CGImagePropertyOrientation) -> CGImage {
    guard orientation != .up else { return cg }
    let ci = CIImage(cgImage: cg).oriented(orientation)
    return ciContext.createCGImage(ci, from: ci.extent) ?? cg
}

private let cardAspect = 63.0 / 88.0
private let aspectTolerance = 0.14

public func locateCard(_ cg: CGImage) -> LocatedCard? {
    let request = VNDetectDocumentSegmentationRequest()
    let handler = VNImageRequestHandler(cgImage: cg, options: [:])
    guard (try? handler.perform([request])) != nil,
          let obs = request.results?.first
    else { return nil }

    let w = CGFloat(cg.width), h = CGFloat(cg.height)
    func point(_ p: CGPoint) -> CGPoint { CGPoint(x: p.x * w, y: (1 - p.y) * h) }
    let tl = point(obs.topLeft), tr = point(obs.topRight)
    let bl = point(obs.bottomLeft), br = point(obs.bottomRight)

    let box = CGRect(x: min(tl.x, bl.x), y: min(tl.y, tr.y),
                     width: max(tr.x, br.x) - min(tl.x, bl.x),
                     height: max(bl.y, br.y) - min(tl.y, tr.y))
    guard box.width > w * 0.08, box.height > h * 0.08 else { return nil }

    guard let flat = flatten(cg, tl: tl, tr: tr, bl: bl, br: br) else { return nil }

    let centre = CGPoint(x: (tl.x + tr.x + bl.x + br.x) / 4,
                         y: (tl.y + tr.y + bl.y + br.y) / 4)
    let corners = [tl, tr, bl, br]
    var k = 1 + 2 * LocatedCard.wideMargin
    for p in corners {
        if p.x != centre.x {
            let limit = (p.x > centre.x ? w - centre.x : centre.x) / abs(p.x - centre.x)
            k = min(k, limit)
        }
        if p.y != centre.y {
            let limit = (p.y > centre.y ? h - centre.y : centre.y) / abs(p.y - centre.y)
            k = min(k, limit)
        }
    }
    func out(_ p: CGPoint) -> CGPoint {
        CGPoint(x: centre.x + (p.x - centre.x) * k, y: centre.y + (p.y - centre.y) * k)
    }
    let wide = k >= 1.02
        ? flatten(cg, tl: out(tl), tr: out(tr), bl: out(bl), br: out(br))
        : nil

    let ratio = Double(flat.width) / Double(flat.height)
    guard abs(ratio - cardAspect) < aspectTolerance else { return nil }

    return LocatedCard(bounds: box, image: flat, wide: wide,
                       wideMarginUsed: wide == nil ? 0 : (k - 1) / 2)
}

private func flatten(_ cg: CGImage,
                     tl: CGPoint, tr: CGPoint, bl: CGPoint, br: CGPoint) -> CGImage? {
    guard let filter = CIFilter(name: "CIPerspectiveCorrection") else { return nil }
    let ci = CIImage(cgImage: cg)
    let flip = { (p: CGPoint) in CIVector(x: p.x, y: CGFloat(cg.height) - p.y) }
    filter.setValue(ci, forKey: kCIInputImageKey)
    filter.setValue(flip(tl), forKey: "inputTopLeft")
    filter.setValue(flip(tr), forKey: "inputTopRight")
    filter.setValue(flip(bl), forKey: "inputBottomLeft")
    filter.setValue(flip(br), forKey: "inputBottomRight")
    guard let out = filter.outputImage else { return nil }
    return ciContext.createCGImage(out, from: out.extent)
}
