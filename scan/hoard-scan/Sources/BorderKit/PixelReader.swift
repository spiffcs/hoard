import CoreGraphics
import Foundation

final class PixelReader {
    let width: Int
    let height: Int
    private let data: UnsafeMutablePointer<UInt8>
    private let bytesPerRow: Int

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

    func chroma(_ x: CGFloat, _ y: CGFloat) -> CGFloat? {
        guard let c = rgb(x, y) else { return nil }
        return max(c.r, max(c.g, c.b)) - min(c.r, min(c.g, c.b))
    }

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

func medianAbsoluteDeviation(_ xs: [CGFloat]) -> CGFloat {
    let m = medianOf(xs)
    return medianOf(xs.map { abs($0 - m) })
}

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
