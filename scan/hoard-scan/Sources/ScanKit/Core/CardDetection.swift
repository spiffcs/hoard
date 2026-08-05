// Finding card-shaped quads in a frame and straightening them. Two detectors
// with different appetites: findCard anchors the collector band, cardRects
// feeds the multi-card channel.

import CoreGraphics
import CoreImage
import Vision

/// findCard locates the card in the frame, so the collector band can be anchored
/// to the card's own bottom edge instead of the frame's.
///
/// Returns nil when nothing card-shaped stands out — a card on a same-coloured
/// surface, or one held at too steep an angle — which is the cue to fall back to
/// frameBandFallback.
func findCard(_ cg: CGImage) -> VNRectangleObservation? {
    let req = VNDetectRectanglesRequest()
    // A Magic card is 63x88mm, so 0.716 dead on. The tolerance either side absorbs
    // the perspective foreshortening of a hand-held phone; the helper never asks
    // the user to square the card up.
    req.minimumAspectRatio = 0.55
    req.maximumAspectRatio = 0.9
    // A framed card dominates the shot. This rejects specks and, more usefully,
    // the rectangles inside the card — the art box and the text box.
    req.minimumSize = 0.15
    req.minimumConfidence = 0.5
    req.quadratureTolerance = 25
    req.maximumObservations = 10

    do {
        try VNImageRequestHandler(cgImage: cg, options: [:]).perform([req])
    } catch {
        return nil
    }
    // The tallest candidate is the card itself rather than one of its inner boxes.
    return (req.results ?? []).max { $0.boundingBox.height < $1.boundingBox.height }
}

/// collectorBand returns the region of interest to search for collector info: the
/// frame up to a ceiling set just above the detected card's bottom border, or the
/// frame's lower half when no card could be located. anchored reports which of
/// the two it was — a card-anchored band is the only one whose collector read
/// deserves trust downstream.
/// CollectorBand is where to look for the collector block, and whether that
/// region was anchored to a detected card rectangle. An anchored band is the
/// only one whose collector read deserves trust — the fallback is just the
/// frame's lower half, which may contain anything.
struct CollectorBand {
    let region: CGRect
    let anchored: Bool
}

func collectorBand(_ cg: CGImage) -> CollectorBand {
    guard let card = findCard(cg) else {
        return CollectorBand(
            region: CGRect(x: 0, y: 0, width: 1, height: frameBandFallback), anchored: false)
    }
    // Work from the corner points, not the axis-aligned bounding box. A card is
    // never perfectly square to the camera, and for a tilted one the bounding box
    // bottom is its lowest *corner* — below the collector text, which runs on the
    // same tilt. The band therefore has to span the tilt as well as reach up the
    // card, or it misses the text entirely at around 8° of turn.
    let top = max(card.bottomLeft.y, card.bottomRight.y)
    // The card's height along its own edge, so the fraction stays a fraction of
    // the card however it is turned.
    let edge = hypot(card.topLeft.x - card.bottomLeft.x,
                     card.topLeft.y - card.bottomLeft.y)
    // Pad a little: the detected edge can sit just inside the printed border, and
    // the collector text runs very close to that border.
    let pad: CGFloat = 0.01

    // Only the *top* of the band is anchored to the card. It runs to the frame's
    // bottom edge and full width because whatever lies below and beside the card is
    // desk, which costs nothing to include and keeps this a superset of the region
    // a frame-relative band would have covered. Vision's recognition of text this
    // small is sensitive to the shape of the region it is given, and the wider strip
    // reads marginal borders more reliably than a tight crop does.
    let height = top + edge * collectorBandFraction + pad
    return CollectorBand(
        region: CGRect(x: 0, y: 0, width: 1, height: min(1, height)), anchored: true)
}

/// cardRects finds every card-shaped quad in the frame. The detector runs with
/// looser thresholds than findCard's — a card at the edge of a fan shows less
/// of its outline than a lone framed card — and the containment pass keeps
/// only the outermost quads, dropping near-duplicate detections and the boxes
/// *inside* a card (the art frame and the rules box, which OCR to rules text
/// rather than titles).
func cardRects(_ cg: CGImage) -> [VNRectangleObservation] {
    let req = VNDetectRectanglesRequest()
    req.minimumAspectRatio = 0.15
    req.maximumAspectRatio = 1.0
    req.minimumSize = 0.08
    req.minimumConfidence = 0.3
    req.quadratureTolerance = 25
    req.maximumObservations = 16
    do {
        try VNImageRequestHandler(cgImage: cg, options: [:]).perform([req])
    } catch {
        multiDebug("rect detection failed: \(error.localizedDescription)")
        return []
    }
    let rects = req.results ?? []

    func area(_ o: VNRectangleObservation) -> CGFloat {
        o.boundingBox.width * o.boundingBox.height
    }
    var kept: [VNRectangleObservation] = []
    for r in rects.sorted(by: { area($0) > area($1) }) {
        let bb = r.boundingBox
        let swallowed = kept.contains { k in
            let inter = k.boundingBox.intersection(bb)
            return !inter.isNull && inter.width * inter.height > 0.7 * bb.width * bb.height
        }
        if !swallowed { kept.append(r) }
    }
    multiDebug("\(rects.count) rects, \(kept.count) kept after containment dedup")
    return kept
}

/// perspectiveCrop straightens one detected quad into an upright card image —
/// the step the single-card path never needed, since it reads the whole frame.
func perspectiveCrop(_ cg: CGImage, _ r: VNRectangleObservation, _ ctx: CIContext) -> CGImage? {
    let w = CGFloat(cg.width), h = CGFloat(cg.height)
    let ci = CIImage(cgImage: cg)
    func pt(_ p: CGPoint) -> CIVector { CIVector(x: p.x * w, y: p.y * h) }
    guard let f = CIFilter(name: "CIPerspectiveCorrection") else { return nil }
    f.setValue(ci, forKey: kCIInputImageKey)
    f.setValue(pt(r.topLeft), forKey: "inputTopLeft")
    f.setValue(pt(r.topRight), forKey: "inputTopRight")
    f.setValue(pt(r.bottomLeft), forKey: "inputBottomLeft")
    f.setValue(pt(r.bottomRight), forKey: "inputBottomRight")
    guard let out = f.outputImage else { return nil }
    return ctx.createCGImage(out, from: out.extent)
}
