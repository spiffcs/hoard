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
public func sceneSignature(_ buffer: CVPixelBuffer) -> SceneSignature {
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

    var cells = [UInt8](repeating: 0, count: SceneSignature.columns * SceneSignature.rows)
    for r in 0..<SceneSignature.rows {
        let y = min(height - 1, (r * height) / SceneSignature.rows + height / (SceneSignature.rows * 2))
        for c in 0..<SceneSignature.columns {
            let x = min(width - 1, (c * width) / SceneSignature.columns + width / (SceneSignature.columns * 2))
            let value: UInt8
            if planar {
                value = bytes[y * bytesPerRow + x]
            } else {
                // BGRA. Green-weighted rather than a true luma: it is within a
                // few percent and this number is only ever compared with itself.
                let p = y * bytesPerRow + x * 4
                value = bytes[p + 1]
            }
            cells[r * SceneSignature.columns + c] = value
        }
    }
    return SceneSignature(cells: cells)
}
