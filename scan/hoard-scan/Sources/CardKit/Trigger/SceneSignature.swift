import Accelerate
import CoreGraphics
import CoreVideo
import Foundation

public struct SceneSignature: Sendable, Equatable {
    public static let columns = 16
    public static let rows = 24

    public let cells: [UInt8]

    public init(cells: [UInt8]) {
        self.cells = cells
    }

    public static let unknown = SceneSignature(cells: [])

    public func delta(to other: SceneSignature) -> Double {
        guard !cells.isEmpty, cells.count == other.cells.count else { return .greatestFiniteMagnitude }
        var total = 0
        for i in cells.indices {
            total += abs(Int(cells[i]) - Int(other.cells[i]))
        }
        return Double(total) / Double(cells.count)
    }

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
        guard format == kCVPixelFormatType_32BGRA
            || format == kCVPixelFormatType_32ARGB
            || format == kCVPixelFormatType_32RGBA,
            bytesPerRow >= width * 4
        else { return .unknown }
        read = { x, y in Int(bytes[y * bytesPerRow + x * 4 + 1]) }
    }
    return SceneSignature(cells: sampleGrid(
        read: read, win: win, width: width, height: height))
}

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
