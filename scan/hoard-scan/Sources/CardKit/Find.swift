// Finding the card, and flattening it.
//
// The macOS pipeline uses VNDetectRectanglesRequest and spends a 300-line merge
// ladder surviving it: its own notes record that on a fanned spread the detector
// "returns quads that span several cards", so a crop cannot be trusted and the
// frame-wide text pass has to remain the primary channel.
//
// Document segmentation is trained on the opposite assumption — one rectangular
// printed thing against a background — which is exactly the scanning rig. It
// returns a quad rather than a bounding box, which is what makes perspective
// correction possible, and perspective correction is what turns "somewhere near
// the bottom of the photograph" into "82% of the way down the card".
//
// It finds one document, not several. Multi-card frames are a later problem and
// deliberately not solved here: a pipeline that reads one card correctly is
// worth more than one that reads a fan approximately.

import CoreImage
import CoreGraphics
import Foundation
import Vision

/// A located card: the quad it occupied in the frame, and its pixels flattened
/// to a rectangle.
public struct LocatedCard {
    /// The card's bounding box in frame pixels, origin top left.
    public var bounds: CGRect
    /// The card, perspective-corrected and upright.
    public var image: CGImage
}


/// Shared across the file set: a CIContext carries GPU state worth reusing,
/// and building one per call showed up as its own line in the stage timings.
let ciContext = CIContext()

/// uprighted bakes an EXIF orientation into the pixels, returning an image that
/// reads correctly with no orientation tag. Normalising once is what keeps the
/// tag and any manual rotation from both being applied.
public func uprighted(_ cg: CGImage, _ orientation: CGImagePropertyOrientation) -> CGImage {
    guard orientation != .up else { return cg }
    let ci = CIImage(cgImage: cg).oriented(orientation)
    return ciContext.createCGImage(ci, from: ci.extent) ?? cg
}

/// A Magic card is 63x88mm — 0.716 on its long axis. Anything far off that is
/// the desk, a hand, or the whole frame, and a confidently wrong card rect is
/// worse than none: every card-space measurement downstream inherits the error.
private let cardAspect = 63.0 / 88.0
private let aspectTolerance = 0.14

/// locateCard finds the card and returns it flattened.
public func locateCard(_ cg: CGImage) -> LocatedCard? {
    let request = VNDetectDocumentSegmentationRequest()
    let handler = VNImageRequestHandler(cgImage: cg, options: [:])
    guard (try? handler.perform([request])) != nil,
          let obs = request.results?.first
    else { return nil }

    let w = CGFloat(cg.width), h = CGFloat(cg.height)
    // Vision normalises with the origin at the bottom left; everything here
    // works top-left, so the corners flip on the way in.
    func point(_ p: CGPoint) -> CGPoint { CGPoint(x: p.x * w, y: (1 - p.y) * h) }
    let tl = point(obs.topLeft), tr = point(obs.topRight)
    let bl = point(obs.bottomLeft), br = point(obs.bottomRight)

    let box = CGRect(x: min(tl.x, bl.x), y: min(tl.y, tr.y),
                     width: max(tr.x, br.x) - min(tl.x, bl.x),
                     height: max(bl.y, br.y) - min(tl.y, tr.y))
    guard box.width > w * 0.08, box.height > h * 0.08 else { return nil }

    guard let flat = flatten(cg, tl: tl, tr: tr, bl: bl, br: br) else { return nil }

    // Check the aspect *after* flattening, not before. A tilted card has a
    // bounding box of the wrong shape while the card itself is fine, and
    // rejecting on the box would throw away exactly the captures perspective
    // correction exists to rescue.
    let ratio = Double(flat.width) / Double(flat.height)
    guard abs(ratio - cardAspect) < aspectTolerance else { return nil }

    return LocatedCard(bounds: box, image: flat)
}

/// flatten perspective-corrects the quad into an upright rectangle.
///
/// The output is sized from the quad's own edges rather than to a fixed
/// resolution, so a capture that filled the frame keeps its pixels and one taken
/// from further away is not upscaled into implied detail it does not have.
private func flatten(_ cg: CGImage,
                     tl: CGPoint, tr: CGPoint, bl: CGPoint, br: CGPoint) -> CGImage? {
    guard let filter = CIFilter(name: "CIPerspectiveCorrection") else { return nil }
    let ci = CIImage(cgImage: cg)
    // CIPerspectiveCorrection works bottom-left-origin, so the corners flip back.
    let flip = { (p: CGPoint) in CIVector(x: p.x, y: CGFloat(cg.height) - p.y) }
    filter.setValue(ci, forKey: kCIInputImageKey)
    filter.setValue(flip(tl), forKey: "inputTopLeft")
    filter.setValue(flip(tr), forKey: "inputTopRight")
    filter.setValue(flip(bl), forKey: "inputBottomLeft")
    filter.setValue(flip(br), forKey: "inputBottomRight")
    guard let out = filter.outputImage else { return nil }
    return ciContext.createCGImage(out, from: out.extent)
}
