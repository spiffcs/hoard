// A cheap picture of the frame, for telling "still" from "moved".
//
// The rectangle detector is the primary signal and it is unreliable in a very
// specific way: it drops a motionless card on roughly two samples in five. So
// the trigger needs a second opinion that does not depend on finding anything —
// something that answers "is this the same picture as last time" from the pixels
// alone.
//
// A coarse luma grid does that for almost nothing. It is deliberately blunt: at
// this resolution a card being placed moves many cells and a sensor's noise
// floor moves none, which is the entire discrimination required.

import Accelerate
import CoreGraphics
import CoreVideo
import Foundation

/// A coarse luma grid of one frame.
public struct SceneSignature: Sendable, Equatable {
    public static let columns = 16
    public static let rows = 24

    /// Row-major, one byte per cell.
    public let cells: [UInt8]

    public init(cells: [UInt8]) {
        self.cells = cells
    }

    /// An empty signature, which compares as maximally different to everything.
    /// Used before the first sample, where "unknown" must never read as "still".
    public static let unknown = SceneSignature(cells: [])

    /// delta is the mean absolute per-cell change between two frames.
    ///
    /// Signatures that cannot be compared read as maximally different, so a
    /// missing or malformed frame can never be mistaken for stillness — which
    /// would fire the shutter at nothing.
    public func delta(to other: SceneSignature) -> Double {
        guard !cells.isEmpty, cells.count == other.cells.count else { return .greatestFiniteMagnitude }
        var total = 0
        for i in cells.indices {
            total += abs(Int(cells[i]) - Int(other.cells[i]))
        }
        return Double(total) / Double(cells.count)
    }

    /// detail is the standard deviation of the middle half of the frame.
    ///
    /// The middle half, not the whole thing: the edges carry the desk and the
    /// operator's hands, and a bare mat with a busy border would otherwise read
    /// as having something worth photographing in it.
    public var detail: Double {
        guard !cells.isEmpty else { return 0 }
        var middle: [Double] = []
        middle.reserveCapacity(cells.count / 4)
        let colRange = (Self.columns / 4)..<(Self.columns * 3 / 4)
        let rowRange = (Self.rows / 4)..<(Self.rows * 3 / 4)
        for r in rowRange {
            for c in colRange {
                let i = r * Self.columns + c
                if i < cells.count { middle.append(Double(cells[i])) }
            }
        }
        guard middle.count > 1 else { return 0 }
        let mean = middle.reduce(0, +) / Double(middle.count)
        let variance = middle.reduce(0) { $0 + ($1 - mean) * ($1 - mean) } / Double(middle.count)
        return variance.squareRoot()
    }
}

/// sceneSignature samples a pixel buffer into the grid.
///
/// Reads the luma plane directly for the biplanar formats a capture session
/// actually delivers, and approximates it for packed BGRA. Sampling rather than
/// averaging: this runs ten times a second beside a Vision pass, and one pixel
/// per cell is enough to answer the only question being asked.
/// sceneSignature samples a coarse luma grid, over the whole frame or inside a
/// normalized sub-rect.
///
/// The sub-rect form is what lets the trigger tell one card from another. The
/// whole-frame signature answers "did the picture change", and a card occupies
/// a fraction of the frame, so swapping one card for a completely different one
/// barely moves a mean taken over 384 cells spread across the desk. Sampling
/// the same grid *inside the card* asks about the card instead — a different
/// card is a different picture, unmistakably.
///
/// It costs the same either way: the grid was always 384 point samples at
/// computed coordinates, and this only changes which coordinates.
public func sceneSignature(
    _ buffer: CVPixelBuffer, in rect: CGRect? = nil
) -> SceneSignature {
    CVPixelBufferLockBaseAddress(buffer, .readOnly)
    defer { CVPixelBufferUnlockBaseAddress(buffer, .readOnly) }

    let width = CVPixelBufferGetWidth(buffer)
    let height = CVPixelBufferGetHeight(buffer)
    guard width > 0, height > 0 else { return .unknown }

    let format = CVPixelBufferGetPixelFormatType(buffer)
    let planar = format == kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange
        || format == kCVPixelFormatType_420YpCbCr8BiPlanarFullRange

    let base: UnsafeMutableRawPointer?
    let bytesPerRow: Int
    if planar {
        base = CVPixelBufferGetBaseAddressOfPlane(buffer, 0)
        bytesPerRow = CVPixelBufferGetBytesPerRowOfPlane(buffer, 0)
    } else {
        base = CVPixelBufferGetBaseAddress(buffer)
        bytesPerRow = CVPixelBufferGetBytesPerRow(buffer)
    }
    guard let base else { return .unknown }
    let bytes = base.assumingMemoryBound(to: UInt8.self)

    // The window to sample, in pixels. Vision's boxes are y-up and the buffer
    // is y-down, so the rect flips on the way in — getting this wrong samples
    // the opposite end of the card and still returns a plausible signature,
    // which is the failure mode that costs a session before anyone notices.
    let win: (x: Int, y: Int, w: Int, h: Int)
    if let rect, rect.width > 0.01, rect.height > 0.01 {
        let x0 = max(0, Int(rect.minX * CGFloat(width)))
        let y0 = max(0, Int((1 - rect.maxY) * CGFloat(height)))
        win = (x0, y0,
               max(1, min(width - x0, Int(rect.width * CGFloat(width)))),
               max(1, min(height - y0, Int(rect.height * CGFloat(height)))))
    } else {
        win = (0, 0, width, height)
    }

    let read: (Int, Int) -> Int
    if planar {
        read = { x, y in Int(bytes[y * bytesPerRow + x]) }
    } else {
        // BGRA. Green-weighted rather than a true luma: it is within a few
        // percent and this number is only ever compared with itself.
        read = { x, y in Int(bytes[y * bytesPerRow + x * 4 + 1]) }
    }
    return SceneSignature(cells: sampleGrid(
        read: read, win: win, width: width, height: height))
}

/// sampleGrid fills the cell grid, one point sample per cell.
///
/// The aliasing this produces — a card that moved a pixel reads as changed —
/// is load-bearing for the trigger. (An area-averaged variant and a chroma
/// twin lived here briefly for the temporal-shimmer experiment; three
/// labelled runs showed the channel does not separate in-hand, and
/// docs/sprint-foil-recognition.md Stage B holds the record.)
func sampleGrid(
    read: (Int, Int) -> Int, win: (x: Int, y: Int, w: Int, h: Int),
    width: Int, height: Int
) -> [UInt8] {
    var cells = [UInt8](repeating: 0, count: SceneSignature.columns * SceneSignature.rows)
    for r in 0..<SceneSignature.rows {
        for c in 0..<SceneSignature.columns {
            let y = min(height - 1,
                        win.y + (r * win.h) / SceneSignature.rows
                            + win.h / (SceneSignature.rows * 2))
            let x = min(width - 1,
                        win.x + (c * win.w) / SceneSignature.columns
                            + win.w / (SceneSignature.columns * 2))
            cells[r * SceneSignature.columns + c] =
                UInt8(max(0, min(255, read(x, y))))
        }
    }
    return cells
}

