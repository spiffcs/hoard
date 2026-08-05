// Which end of the card the sampler is actually looking at.
//
// This test exists because getting it wrong is invisible. A flipped sampler
// still returns tidy, plausible numbers — it just answers about the opposite
// edge — and it cost a whole live session: black-bordered cards read light,
// white-bordered ones read dark, a clean inversion that looked like a
// threshold problem and was not.

import CoreGraphics
import Testing

@testable import CardKit

/// A card-shaped image whose top third is white and bottom third is black, so
/// "which end did you sample" has an unambiguous answer.
private func splitImage() -> CGImage {
    let w = 200, h = 600
    let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8,
                        bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    // CGContext draws bottom-up, so filling the *upper* part of the image means
    // filling the high-y end of the context.
    ctx.setFillColor(gray: 0.5, alpha: 1)
    ctx.fill(CGRect(x: 0, y: 0, width: w, height: h))
    ctx.setFillColor(gray: 1.0, alpha: 1)          // white: image top
    ctx.fill(CGRect(x: 0, y: h * 2 / 3, width: w, height: h / 3))
    ctx.setFillColor(gray: 0.0, alpha: 1)          // black: image bottom
    ctx.fill(CGRect(x: 0, y: 0, width: w, height: h / 3))
    return ctx.makeImage()!
}

@Test("card space runs top-down, the same way cropCard reads it")
func samplerOrientation() {
    let img = splitImage()
    // cropCard is the reference: the band at y 0.82 demonstrably reads the
    // collector row at the *bottom* of a real card, so y grows downward.
    let bottom = cropCard(img, CGRect(x: 0, y: 0.85, width: 1, height: 0.1))!
    let top = cropCard(img, CGRect(x: 0, y: 0.05, width: 1, height: 0.1))!
    #expect(meanLuma(bottom) < 0.2, "cropCard y=0.85 should be the black end")
    #expect(meanLuma(top) > 0.8, "cropCard y=0.05 should be the white end")

    // The border sampler must agree with it.
    let grid = ColorGrid(img)!
    let lowStrip = grid.lumaValues(yFrom: 0.85, yTo: 0.95, xFrom: 0.2, xTo: 0.8)!
    let highStrip = grid.lumaValues(yFrom: 0.05, yTo: 0.15, xFrom: 0.2, xTo: 0.8)!
    let lowMean = lowStrip.reduce(0, +) / Double(lowStrip.count)
    let highMean = highStrip.reduce(0, +) / Double(highStrip.count)
    #expect(lowMean < 0.2, "sampler y=0.85 read \(lowMean), should be the black end")
    #expect(highMean > 0.8, "sampler y=0.05 read \(highMean), should be the white end")
}

/// meanLuma of a whole image, for the reference assertions.
private func meanLuma(_ img: CGImage) -> Double {
    let grid = ColorGrid(img)!
    let all = grid.lumaValues(yFrom: 0, yTo: 1, xFrom: 0, xTo: 1)!
    return all.reduce(0, +) / Double(all.count)
}
