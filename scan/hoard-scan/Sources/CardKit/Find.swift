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
    /// The same card flattened again with `wideMargin` of slack on every side.
    ///
    /// This exists because `image` cannot be trusted to contain the border.
    /// Vision locks onto whichever edge has contrast, and on a white-bordered
    /// card against a light desk the strongest edge in the frame is
    /// border-to-art, not card-to-desk — so the quad settles *inside* the
    /// card's cut edge and the white border is not in these pixels at all.
    /// Measured live: Control Magic and Phantasmal Terrain, both white, both
    /// sampled at the card's own ink point off the blue inner frame.
    ///
    /// Widening does not by itself say where the card is; it only guarantees
    /// the border is somewhere in shot. Finding it is the border reader's job,
    /// and it anchors on text to do it.
    public var wide: CGImage?
    /// The margin `wide` was actually built with, which is `wideMargin` only
    /// when the frame had that much to give. Anything mapping coordinates into
    /// `wide` has to use this number and not the constant.
    public var wideMarginUsed: CGFloat = 0

    /// How far past the quad the wide flatten reaches, as a fraction of the
    /// card. A tenth covers every inset observed — the worst had the quad on
    /// the inner frame, a few percent in — without reaching the next card on a
    /// tightly packed desk.
    public static let wideMargin: CGFloat = 0.10
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

    // The same quad, pushed out about its own centre. Scaling by 1 + 2m puts m
    // of slack on each side, in the card's own frame rather than the image's,
    // so a tilted card widens along its own edges.
    let centre = CGPoint(x: (tl.x + tr.x + bl.x + br.x) / 4,
                         y: (tl.y + tr.y + bl.y + br.y) / 4)
    // Only as far as the frame can actually supply.
    //
    // CIPerspectiveCorrection fills anything outside its input with transparent
    // black, and black is a border colour — widening past the frame edge would
    // manufacture a confident "black" out of nothing. A card photographed on a
    // desk has room; a scan cropped to the card itself has none, and gets no
    // wide flatten rather than a fabricated one.
    let corners = [tl, tr, bl, br]
    var k = 1 + 2 * LocatedCard.wideMargin
    for p in corners {
        // The largest scale about the centre that keeps this corner in frame.
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
    // Below a couple of percent there is nothing to gain and the reader is
    // better off saying so: nil abstains rather than failing the read.
    let wide = k >= 1.02
        ? flatten(cg, tl: out(tl), tr: out(tr), bl: out(bl), br: out(br))
        : nil

    // Check the aspect *after* flattening, not before. A tilted card has a
    // bounding box of the wrong shape while the card itself is fine, and
    // rejecting on the box would throw away exactly the captures perspective
    // correction exists to rescue.
    let ratio = Double(flat.width) / Double(flat.height)
    guard abs(ratio - cardAspect) < aspectTolerance else { return nil }

    return LocatedCard(bounds: box, image: flat, wide: wide,
                       wideMarginUsed: wide == nil ? 0 : (k - 1) / 2)
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
