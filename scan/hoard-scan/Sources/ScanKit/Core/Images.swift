// Loading, uprighting and rotating frames. --image replays a file through the
// identical path a live capture takes, which is what makes scan/fixtures mean
// anything.

import CoreGraphics
import CoreImage
import Foundation
import ImageIO

/// sharedCIContext is the one CIContext for the whole process. Constructing a
/// CIContext spins up a Metal device, and doing that per capture (twice — here
/// and in scanFrame) was measurable per-card cost. CIContext is thread-safe.
let sharedCIContext = CIContext()

/// uprighted bakes an EXIF orientation into the pixels, returning an image that
/// reads correctly with no orientation tag. Normalizing once here is what keeps
/// the tag and the manual rotation from both being applied.
func uprighted(_ cg: CGImage, _ orientation: CGImagePropertyOrientation) -> CGImage {
    if orientation == .up { return cg }
    let ci = CIImage(cgImage: cg).oriented(orientation)
    return sharedCIContext.createCGImage(ci, from: ci.extent) ?? cg
}

/// rotatedImage returns a copy of the image turned clockwise by a multiple of
/// 90°, so OCR sees exactly the framing the corrected preview showed. Doing this
/// in pixels rather than via an EXIF orientation tag keeps the direction
/// unambiguous. The input must already be upright — see uprighted.
func rotatedImage(_ cg: CGImage, clockwiseDegrees deg: Int) -> CGImage {
    let steps = ((deg / 90) % 4 + 4) % 4
    if steps == 0 { return cg }

    let w = cg.width, h = cg.height
    let sideways = steps % 2 == 1
    let outW = sideways ? h : w
    let outH = sideways ? w : h
    guard let ctx = CGContext(
        data: nil, width: outW, height: outH,
        bitsPerComponent: 8, bytesPerRow: 0,
        space: cg.colorSpace ?? CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue)
    else { return cg }

    ctx.translateBy(x: CGFloat(outW) / 2, y: CGFloat(outH) / 2)
    // CoreGraphics rotates counter-clockwise for a positive angle.
    ctx.rotate(by: -CGFloat(steps) * .pi / 2)
    ctx.translateBy(x: -CGFloat(w) / 2, y: -CGFloat(h) / 2)
    ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
    return ctx.makeImage() ?? cg
}

/// cgImage loads an image file through CGImageSource — the same decode a live
/// capture goes through — so --image exercises the real pipeline, including the
/// pixel formats NSImage would quietly normalize away.
func cgImage(fromFile path: String) -> (CGImage, CGImagePropertyOrientation)? {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { return nil }
    let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any]
    let raw = props?[kCGImagePropertyOrientation] as? UInt32 ?? 1
    return (cg, CGImagePropertyOrientation(rawValue: raw) ?? .up)
}
