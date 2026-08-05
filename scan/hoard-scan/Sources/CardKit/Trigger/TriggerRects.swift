// The cheap per-sample rectangle pass.
//
// Not the same detector the read uses. `locateCard` runs document segmentation
// on a full-resolution still and is worth its cost once per card; this runs ten
// times a second on a preview-sized buffer and only has to answer "is there
// something card-shaped, and is it where it was last time".
//
// Deliberately loose about what counts. The trigger's job is to notice a thing
// being put down, and the background filter, the fragment rules and the scene
// signature do the discriminating. A detector tuned tight enough to only ever
// see cards would drop the card on every sample where it is half-shadowed,
// which is most of them while a hand is withdrawing.

import CoreVideo
import Foundation
import Vision

/// triggerRects finds candidate rectangles in a preview buffer.
///
/// Returns them largest first, with boxes that mostly swallow other boxes
/// removed — a detector routinely reports a card and a piece of the same card,
/// and the trigger should not treat those as two things.
public func triggerRects(_ buffer: CVPixelBuffer) -> [CGRect] {
    let request = VNDetectRectanglesRequest()
    // A card is 63x88mm — 0.716. The range is wide because perspective, a
    // partly covered card and a hand's shadow all shift the ratio, and a miss
    // here costs a whole sample.
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

    // Containment dedup, largest first: a box more than 70% inside one already
    // kept is the same object seen twice.
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
