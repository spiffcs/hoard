// Reducing a frame to a coarse luma grid, which answers "did anything move"
// and "is there detail here" without needing a rectangle.

import CoreVideo
import Foundation

/// sceneGridW/H is the resolution of the frame signature — coarse on purpose.
/// It has to answer "did anything move" and "is there detail here", not "what
/// is this", and a coarse grid is both cheap and immune to sensor noise.
let sceneGridW = 16
let sceneGridH = 24

/// sceneSignature reduces a video frame to a small luma grid.
///
/// It exists because the fire decision cannot keep depending on Vision finding
/// a rectangle. A borderless card has no border: the art runs to the edge, so
/// the only edge is card-against-desk, and the detector loses it constantly —
/// measured at 93 stabilization passes started against 21 fired in one live
/// session. The user's experience of that is a scanner that will not fire, and
/// the user's response is to nudge the card, which restarts the cycle.
///
/// Pixels do not have that problem. Whether the scene is *moving* is answerable
/// without knowing what is in it.
///
/// Deliberately cheap: this runs on every sample beside triggerRects, on the
/// analysis queue, at twice the old rate.
func sceneSignature(_ buffer: CVPixelBuffer) -> [UInt8] {
    CVPixelBufferLockBaseAddress(buffer, .readOnly)
    defer { CVPixelBufferUnlockBaseAddress(buffer, .readOnly) }
    let w = CVPixelBufferGetWidth(buffer), h = CVPixelBufferGetHeight(buffer)
    guard w >= sceneGridW, h >= sceneGridH else { return [] }

    // 420f/420v carry luma in plane 0; a packed BGRA buffer has no planes and
    // its bytes are interleaved. Both are handled, and anything else declines.
    let planar = CVPixelBufferGetPlaneCount(buffer) > 0
    guard let base = planar
        ? CVPixelBufferGetBaseAddressOfPlane(buffer, 0)
        : CVPixelBufferGetBaseAddress(buffer) else { return [] }
    let stride = planar
        ? CVPixelBufferGetBytesPerRowOfPlane(buffer, 0)
        : CVPixelBufferGetBytesPerRow(buffer)
    let step = planar ? 1 : 4
    let px = base.assumingMemoryBound(to: UInt8.self)

    var grid = [UInt8](repeating: 0, count: sceneGridW * sceneGridH)
    for gy in 0..<sceneGridH {
        let y = min(h - 1, (gy * h) / sceneGridH + h / (sceneGridH * 2))
        for gx in 0..<sceneGridW {
            let x = min(w - 1, (gx * w) / sceneGridW + w / (sceneGridW * 2))
            if planar {
                grid[gy * sceneGridW + gx] = px[y * stride + x]
            } else {
                // BGRA: a green-weighted approximation is close enough to luma
                // for a motion test and avoids three multiplies per cell.
                let o = y * stride + x * step
                grid[gy * sceneGridW + gx] =
                    UInt8((Int(px[o]) + 2 * Int(px[o + 1]) + Int(px[o + 2])) / 4)
            }
        }
    }
    return grid
}

/// sceneDelta is the mean absolute difference between two signatures, or a
/// large number when they cannot be compared.
func sceneDelta(_ a: [UInt8], _ b: [UInt8]) -> Double {
    guard !a.isEmpty, a.count == b.count else { return 255 }
    var sum = 0
    for i in 0..<a.count { sum += abs(Int(a[i]) - Int(b[i])) }
    return Double(sum) / Double(a.count)
}

/// sceneDetail is the spread of the middle of the frame — high where a card
/// with art and text sits, low on bare desk. It is what stops the stillness
/// path firing at an empty surface, which is otherwise both changed and still
/// the moment a card is lifted away.
func sceneDetail(_ g: [UInt8]) -> Double {
    guard !g.isEmpty else { return 0 }
    var v: [Double] = []
    for gy in (sceneGridH / 4)..<(3 * sceneGridH / 4) {
        for gx in (sceneGridW / 4)..<(3 * sceneGridW / 4) {
            v.append(Double(g[gy * sceneGridW + gx]))
        }
    }
    guard v.count > 1 else { return 0 }
    let m = v.reduce(0, +) / Double(v.count)
    return (v.map { ($0 - m) * ($0 - m) }.reduce(0, +) / Double(v.count)).squareRoot()
}
