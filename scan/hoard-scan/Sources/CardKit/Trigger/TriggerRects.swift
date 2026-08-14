import CoreVideo
import Foundation
import Vision

public func triggerRects(_ buffer: CVPixelBuffer) -> [CGRect] {
    let request = VNDetectRectanglesRequest()
    request.minimumAspectRatio = 0.3
    request.maximumAspectRatio = 1.0
    request.minimumSize = 0.1
    request.minimumConfidence = 0.35
    request.quadratureTolerance = 25
    request.maximumObservations = 8

    let handler = VNImageRequestHandler(cvPixelBuffer: buffer, options: [:])
    guard (try? handler.perform([request])) != nil else { return [] }
    let boxes = (request.results ?? [])
        .map(\.boundingBox)
        .sorted { $0.width * $0.height > $1.width * $1.height }

    var kept: [CGRect] = []
    for box in boxes {
        let swallowed = kept.contains { keep in
            let i = keep.intersection(box)
            guard !i.isNull, box.width > 0, box.height > 0 else { return false }
            return Double(i.width * i.height) / Double(box.width * box.height) > 0.7
        }
        if !swallowed { kept.append(box) }
    }
    return Array(kept.prefix(4))
}
