import CoreGraphics
import CoreVideo
import Vision

/// triggerRects runs the rectangle detector the trigger samples with. Only the
/// boxes matter here, never the text — but which boxes matters a great deal:
/// the raw detector also returns the rectangles *inside* a card (the art frame
/// and the text box) and any speck of desk clutter, and since the stability
/// check compares whole rectangle sets between samples, one flickering speck
/// resets the stillness streak forever. So this filters harder than cardRects:
/// a real size floor, a containment pass that keeps only outermost boxes, and
/// a cap at the few largest — the cards, not their furniture.
///
/// No orientation is passed on purpose: Vision's aspect-ratio bounds are
/// shorter-dimension over longer-dimension, so a sideways card passes the same
/// filter and the trigger doesn't care which way up the sensor is.

func triggerRects(_ buffer: CVPixelBuffer) -> [CGRect] {
    let req = VNDetectRectanglesRequest()
    req.minimumAspectRatio = 0.3
    req.maximumAspectRatio = 1.0
    req.minimumSize = 0.1
    // Low enough to keep seeing the hard cards — foils and borderless frames
    // flicker at higher bars — while the size floor, the containment pass and
    // the background baseline absorb what a lower bar lets through.
    req.minimumConfidence = 0.35
    req.quadratureTolerance = 25
    req.maximumObservations = 8
    do {
        try VNImageRequestHandler(cvPixelBuffer: buffer, options: [:]).perform([req])
    } catch {
        return []
    }
    let boxes = (req.results ?? []).map { $0.boundingBox }
        .sorted { $0.width * $0.height > $1.width * $1.height }
    var kept: [CGRect] = []
    for b in boxes {
        let swallowed = kept.contains { k in
            let inter = k.intersection(b)
            return !inter.isNull && inter.width * inter.height > 0.7 * b.width * b.height
        }
        if !swallowed { kept.append(b) }
    }
    // The largest few are the cards; anything past that is noise whose coming
    // and going would only reset the stillness streak.
    return Array(kept.prefix(4))
}
