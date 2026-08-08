// Sampling raw pixels, and the two statistics the ring is judged with.

import CoreGraphics
import Foundation

/// PixelReader is one RGBA8 copy of a frame. Made only once an anchor has been
/// found, so a frame with no readable footer costs nothing.
final class PixelReader {
    let width: Int
    let height: Int
    private let data: UnsafeMutablePointer<UInt8>
    private let bytesPerRow: Int

    /// The largest frame worth copying, in pixels. Live captures are 12 MP;
    /// the offline probe reads arbitrary files, and before this ceiling a
    /// 20000×20000 PNG was a 1.6 GB allocation and an unhandled abort inside
    /// `allocate` — nil is an answerable failure, a trap is not.
    private static let maxPixels = 64 * 1024 * 1024

    init?(_ cg: CGImage) {
        let w = cg.width, h = cg.height
        guard w > 0, h > 0, w * h <= Self.maxPixels else { return nil }
        let stride = w * 4
        let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: stride * h)
        guard let ctx = CGContext(
            data: buf, width: w, height: h, bitsPerComponent: 8, bytesPerRow: stride,
            space: CGColorSpaceCreateDeviceRGB(),
            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        else {
            buf.deallocate()
            return nil
        }
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        width = w; height = h; bytesPerRow = stride; data = buf
    }

    deinit { data.deallocate() }

    /// Sample at a pixel position, with y measured downward from the top — the
    /// bitmap's own order, and the convention the geometry below works in,
    /// since Vision's bottom-up y only makes the tilt maths harder to read.
    func rgb(_ x: CGFloat, _ y: CGFloat) -> (r: CGFloat, g: CGFloat, b: CGFloat)? {
        let px = Int(x.rounded(.down)), py = Int(y.rounded(.down))
        guard px >= 0, px < width, py >= 0, py < height else { return nil }
        let o = py * bytesPerRow + px * 4
        return (CGFloat(data[o]) / 255, CGFloat(data[o + 1]) / 255, CGFloat(data[o + 2]) / 255)
    }

    func luma(_ x: CGFloat, _ y: CGFloat) -> CGFloat? {
        guard let c = rgb(x, y) else { return nil }
        return 0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b
    }

    /// Chroma as the channel spread. Enough to tell a neutral border from a
    /// gold or silver one without dragging in a colour space, and those are the
    /// only cases it has to reject.
    func chroma(_ x: CGFloat, _ y: CGFloat) -> CGFloat? {
        guard let c = rgb(x, y) else { return nil }
        return max(c.r, max(c.g, c.b)) - min(c.r, min(c.g, c.b))
    }

    /// warmCool is blue minus red: negative on the retro text box's tan, positive
    /// on the cool silver a foil's diffraction throws back. Range -1…1.
    ///
    /// One subtraction rather than a colour space, and that is the whole idea —
    /// the sparkle reader needs an axis on which the marker and the box differ,
    /// not a perceptually uniform one. Measured on live captures where the luma
    /// reader scored zero, this axis carries 5-13x the structure luma does; see
    /// `sparkleWarmCool`.
    func warmCool(_ x: CGFloat, _ y: CGFloat) -> CGFloat? {
        guard let c = rgb(x, y) else { return nil }
        return c.b - c.r
    }
}

func medianOf(_ xs: [CGFloat]) -> CGFloat {
    guard !xs.isEmpty else { return 0 }
    let s = xs.sorted()
    return s.count % 2 == 1 ? s[s.count / 2] : (s[s.count / 2 - 1] + s[s.count / 2]) / 2
}

/// medianAbsoluteDeviation is the spread measure the ring wants: a ring that
/// has slipped half off the card straddles two very different surfaces, and
/// says so here, where a mean would just land somewhere in between.
func medianAbsoluteDeviation(_ xs: [CGFloat]) -> CGFloat {
    let m = medianOf(xs)
    return medianOf(xs.map { abs($0 - m) })
}

/// otsu splits samples in two at the threshold minimizing within-class
/// variance. On a footer line the two class means are the border's paper tone
/// and the credit's ink — the card's own black and white points, measured in
/// the light it was actually photographed under.
/// ToneSplit is a bimodal sample cut in two: the two class means, and how much
/// of the sample fell in the dark one.
struct ToneSplit {
    let dark: CGFloat
    let bright: CGFloat
    let darkFraction: CGFloat
}

func otsu(_ samples: [CGFloat]) -> ToneSplit? {
    guard samples.count >= 64 else { return nil }
    var hist = [Int](repeating: 0, count: 256)
    for s in samples { hist[max(0, min(255, Int(s * 255)))] += 1 }
    let total = samples.count
    var sum: CGFloat = 0
    for i in 0..<256 { sum += CGFloat(i) * CGFloat(hist[i]) }
    var sumB: CGFloat = 0, weightB = 0, best: CGFloat = -1, threshold = 0
    for i in 0..<256 {
        weightB += hist[i]
        if weightB == 0 { continue }
        let weightF = total - weightB
        if weightF == 0 { break }
        sumB += CGFloat(i) * CGFloat(hist[i])
        let meanB = sumB / CGFloat(weightB)
        let meanF = (sum - sumB) / CGFloat(weightF)
        let between = CGFloat(weightB) * CGFloat(weightF) * (meanB - meanF) * (meanB - meanF)
        if between > best { best = between; threshold = i }
    }
    guard best > 0 else { return nil }
    var dark: [CGFloat] = [], bright: [CGFloat] = []
    for s in samples {
        if Int(s * 255) <= threshold { dark.append(s) } else { bright.append(s) }
    }
    guard !dark.isEmpty, !bright.isEmpty else { return nil }
    return ToneSplit(dark: dark.reduce(0, +) / CGFloat(dark.count),
                     bright: bright.reduce(0, +) / CGFloat(bright.count),
                     darkFraction: CGFloat(dark.count) / CGFloat(total))
}
