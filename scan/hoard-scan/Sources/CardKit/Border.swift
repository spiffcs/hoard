// Reading the printed border off a flattened card.
//
// This is the evidence that turns a whole stratum of old reprints from a review
// queue into auto-commits, and it is worth being precise about why. A 1995
// copyright line narrows Prodigal Sorcerer's 23 printings to exactly two —
// `4ed/94` and `4bb/94` — which share a set, a collector number and a year and
// differ only in that one is white-bordered and the other black. The year alone
// cannot choose, so the card queues. The border can, and it is the only thing
// printed on the card that can.
//
// Written for the iPhone head rather than ported from the macOS reader, because
// the two are solving different problems. That one (ScanKit/Core/Border, ~960
// lines) spends most of itself establishing where the card *is* inside a
// photograph — a footer anchor, a title anchor, tilt, scale agreement between
// the two — and then reads a luma ring under drifting ambient light. Here:
//
//   - `locateCard` has already returned a perspective-corrected rectangle, so
//     "the outer 3% of the card" is a fact, not an estimate, and both edges are
//     always in frame;
//   - exposure and white balance are locked for the session, so tone means the
//     same thing from one capture to the next;
//   - the border is ~100px wide at 4032x3024 rather than a handful.
//
// The consequential difference is colour. The macOS reader normalises to luma
// and records the cost in its own comments: gold and silver "read as white
// often enough to matter — eight of them across the corpus — and no photometry
// fixes it, because a silver border simply is light grey". That is true of
// luma. It is not true of chroma: gold is strongly saturated and white, silver
// and black are not, so the colour the old reader had to discard is exactly the
// axis that separates the case it could not.
//
// What survives from its design is the shape of the argument, which is sound:
// two independent signals must agree rather than one; abstaining is a
// first-class answer that always names the gate it failed; and the reference
// tones come from the card itself, because absolute brightness means nothing.

import CoreGraphics
import Foundation

/// What the border reader saw, including when it refuses to answer.
///
/// The numbers ride along whatever the verdict, because they are what the
/// thresholds get fitted from; only `color` is a claim.
public struct BorderReading: Sendable {
    /// "white" or "black" — absent when abstaining.
    ///
    /// Deliberately only those two. Gold and silver were attempted and measured
    /// wrong: an absolute chroma gate called three white-bordered cards gold,
    /// because white card stock under a warm lamp genuinely is yellow in RGB.
    /// They are a separate question and mixing them in produced confident wrong
    /// answers on the one that matters.
    public var color: String?
    /// "ring" when one edge answered, "ring+ring" when both agreed.
    public var source: String?
    /// Why there is no colour, when there is none.
    public var abstain = ""
    /// Where the ring sits in the card's own tone range: 1 is as bright as the
    /// card's brightest paper, 0 as dark as its deepest ink.
    public var tone = 0.0
    /// The ring's colourfulness, 0 for neutral. The axis the macOS reader had
    /// to throw away, and the one that separates gold from white.
    public var chroma = 0.0
    /// Ring minus the frame just inside it. The corroborating signal — it asks
    /// whether the ring is a different surface at all, which is what catches a
    /// sample that slid off the border onto the art.
    public var standoff = 0.0

    public init() {}
}

/// Where the border and its reference sit, as fractions of the card's height.
///
/// A Magic card is 88mm tall with a border of roughly 3mm, so the printed
/// border is about the outer 3.4%. The ring sample stays well inside that; the
/// inner sample sits clear of it, in the card's own frame.
private enum BorderGeometry {
    /// The border itself, inset from the very edge — the outermost pixels catch
    /// the cut line and whatever the card is resting on.
    static let ringNear = 0.008, ringFar = 0.026
    /// The card's own frame, just inside the border. Same surface, same light,
    /// always present — which is what makes it the honest reference.
    static let innerNear = 0.048, innerFar = 0.076
    /// Corners are rounded and often lifted, so the sample stays in the middle
    /// half of the edge.
    static let left = 0.25, right = 0.75
}

enum BorderGate {
    /// The tone thresholds, fitted on 16 real phone captures (2026-08-04).
    ///
    /// `tone` is where the outer ring sits in the card's own ink-to-paper
    /// range, so 0 is as dark as the card's deepest ink and 1 as bright as its
    /// paper. On those captures the two classes did not overlap:
    ///
    ///     black  0.02  0.07  -0.15  -0.05  0.12  0.21  -0.15   (max 0.21)
    ///     white  0.46  0.49   0.25   1.06  1.01  0.97   1.05
    ///            0.40  1.03                                    (min 0.25)
    ///
    /// The previous values asked for 0.80 to call something white, which threw
    /// away every white border between 0.25 and 0.80 — six of the nine here.
    /// They were guessed rather than fitted, and nothing had ever measured a
    /// real capture to contradict them.
    ///
    /// The gap between the classes is only 0.04 wide, so the band between these
    /// two numbers abstains rather than splitting the difference.
    static let blackTone = 0.22
    static let whiteTone = 0.30
    /// How far the ring must sit from the card's own frame just inside it,
    /// as a fraction of the tone range.
    ///
    /// This is the "is it even a different surface" test, and dropping it cost
    /// a wrong commit. The refit that fitted the tone gates rewrote this whole
    /// function and kept only the *sign* of the standoff, which on a value of
    /// 0.01 is a coin flip: Mana Leak read white at tone 0.41 and standoff
    /// 0.01, against confident whites at 0.97-1.03 and 0.19-0.74. The card is
    /// black-bordered.
    ///
    /// 0.14 is the original value and the captures support it: every correct
    /// read this session sat at 0.16 or beyond, and the only two below it were
    /// 0.01 and 0.03 — one wrong, one right for a card the year had already
    /// settled on its own.
    static let minStandoff = 0.14
    /// A border is flat colour. A sample this uneven is reading art.
    static let maxRingMAD = 0.085
    /// Below this the outer 2% of the card is not enough real pixels to sample.
    static let minCardHeightPx = 400
}
/// readBorder decides what colour border a flattened card is printed with.
///
/// `margin` is how far past the card's own edge the crop was pushed, as a
/// fraction of the card, for callers that widen the crop. Zero is the card
/// itself, which is what `readCard` passes: measured on the corpus, widening
/// the crop made the reader worse, not better, because a card that already
/// fills its frame has nothing outside it to widen into and the sample lands
/// on the padding a perspective correction leaves behind.
public func readBorder(_ card: CGImage, margin: Double = 0) -> BorderReading {
    var out = BorderReading()
    // Card space inside an expanded crop: the card starts `margin` in and is
    // `1 / (1 + 2 * margin)` of the crop tall.
    let scale = 1 / (1 + 2 * margin)
    let toCrop = { (v: Double) in (margin + v) * scale }
    guard card.height >= BorderGate.minCardHeightPx else {
        out.abstain = "card too small"
        return out
    }
    guard let grid = ColorGrid(card) else {
        out.abstain = "no pixels"
        return out
    }
    // The card's own ink and paper, as percentiles rather than min and max: one
    // specular highlight would otherwise set the scale for everything.
    guard let (ink, paper) = grid.toneRange(), paper - ink > 0.12 else {
        out.abstain = "card tones too close"
        return out
    }
    let norm = { (v: Double) in (v - ink) / (paper - ink) }

    // Both edges are read independently. A card is symmetrical, so a reading
    // that is not is measuring something other than the border.
    let edges = [true, false].compactMap { readEdge(grid, top: $0, norm: norm, at: toCrop) }

    guard let first = edges.first else {
        if let m = sample(grid, top: false, norm: norm, at: toCrop) {
            out.tone = m.tone
            out.chroma = m.chroma
            out.standoff = m.standoff
            out.abstain = m.mad > BorderGate.maxRingMAD ? "ring not uniform" : "between tones"
        } else {
            out.abstain = "no usable edge"
        }
        return out
    }
    out.tone = first.tone
    out.chroma = first.chroma
    out.standoff = first.standoff

    if edges.count == 2 {
        guard edges[0].color == edges[1].color else {
            out.abstain = "edges disagree"
            return out
        }
        out.source = "ring+ring"
    } else {
        out.source = "ring"
    }
    out.color = first.color
    return out
}

/// One edge's raw measurements, before any verdict.
private struct EdgeSample {
    var tone = 0.0, chroma = 0.0, standoff = 0.0, mad = 0.0
}

/// One edge's verdict, when it clears every gate.
private struct EdgeVerdict {
    var color: String
    var tone: Double, chroma: Double, standoff: Double
}

private func sample(_ grid: ColorGrid, top: Bool,
                    norm: (Double) -> Double,
                    at toCrop: (Double) -> Double) -> EdgeSample? {
    let g = BorderGeometry.self
    func band(_ near: Double, _ far: Double) -> Patch? {
        let (a, b) = top ? (toCrop(near), toCrop(far))
                         : (toCrop(1 - far), toCrop(1 - near))
        return grid.patch(yFrom: a, yTo: b, xFrom: g.left, xTo: g.right)
    }
    guard let ring = band(g.ringNear, g.ringFar),
          let inner = band(g.innerNear, g.innerFar)
    else { return nil }
    return EdgeSample(tone: norm(ring.luma), chroma: ring.chroma,
                      standoff: norm(ring.luma) - norm(inner.luma), mad: ring.mad)
}

/// readEdge reads one edge and applies every gate. nil means that edge cannot
/// answer, which is not itself a failure — one good edge is enough.
private func readEdge(_ grid: ColorGrid, top: Bool,
                      norm: (Double) -> Double,
                      at toCrop: (Double) -> Double) -> EdgeVerdict? {
    guard let m = sample(grid, top: top, norm: norm, at: toCrop) else { return nil }
    // A border is flat colour. Anything this uneven is art, a sleeve edge, or
    // the desk showing past a card that did not fill its own rectangle.
    guard m.mad <= BorderGate.maxRingMAD else { return nil }

    let color: String
    if m.tone <= BorderGate.blackTone { color = "black" }
    else if m.tone >= BorderGate.whiteTone { color = "white" }
    else { return nil }

    // The second signal, and the reason a thin tone margin is safe to use.
    //
    // `standoff` is the ring minus the card's own frame just inside it, so it
    // asks a purely local question — is the outer edge lighter or darker than
    // the card beside it — and answers it without reference to any absolute
    // brightness. On the 16 captures its *sign* alone agreed with the truth on
    // 14, and on both cards where it disagreed with tone the honest answer was
    // to abstain: one sat at -0.03, which is no difference at all.
    //
    // Requiring both is what converts a 0.04-wide tone margin fitted on 16
    // cards into a decision that fails closed when the two disagree.
    // Magnitude first, then direction. A ring that merely leans the right way
    // has not established that it is a different surface from the frame beside
    // it, and without that the tone gate is deciding alone.
    guard abs(m.standoff) >= BorderGate.minStandoff else { return nil }
    guard (m.standoff > 0) == (color == "white") else { return nil }
    return EdgeVerdict(color: color, tone: m.tone, chroma: m.chroma,
                       standoff: m.standoff)
}

// MARK: - Pixels

/// One sampled rectangle.
struct Patch {
    var luma = 0.0
    /// Distance from neutral, after the card's own colour cast is divided out.
    ///
    /// Raw chroma does not work and the fixtures proved it: white card stock
    /// under warm desk light is genuinely yellow in RGB, so an absolute
    /// threshold called two white-bordered cards gold and abstained on every
    /// other white. The corpus could not show this — those are neutrally
    /// balanced scans — which is exactly why border.sh says to fit on the
    /// corpus and *confirm on the photographs*.
    ///
    /// So the cast is measured from the whole card and removed before the ring
    /// is asked whether it is colourful. A gold border is saturated relative to
    /// the card it is printed on; a white one is not, whatever the lamp did to
    /// both of them.
    var chroma = 0.0
    /// Mean absolute deviation of luma — flatness.
    var mad = 0.0
}

/// A small RGB copy of the card, for sampling.
///
/// Rendered once at a bounded width: border sampling asks about broad flat
/// regions, and doing it against 4032x3024 would cost more than the entire read
/// it is part of while answering the same question.
struct ColorGrid {
    let w: Int, h: Int
    let px: [UInt8]   // RGBA, 4 bytes per pixel
    /// The card's colour cast, computed once — every patch is corrected by it.
    private(set) var cast: (Double, Double, Double) = (1, 1, 1)

    init?(_ image: CGImage) {
        let width = min(320, image.width)
        let height = max(1, Int((Double(image.height) / Double(image.width)
            * Double(width)).rounded()))
        var buf = [UInt8](repeating: 0, count: width * height * 4)
        guard let ctx = buf.withUnsafeMutableBytes({ raw -> CGContext? in
            CGContext(data: raw.baseAddress, width: width, height: height,
                      bitsPerComponent: 8, bytesPerRow: width * 4,
                      space: CGColorSpaceCreateDeviceRGB(),
                      bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        }) else { return nil }
        ctx.interpolationQuality = .medium
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
        self.w = width
        self.h = height
        self.px = buf
        self.cast = gains
    }

    /// Per-channel gains that neutralise the card's own colour cast.
    ///
    /// Grey-world rather than white-patch: the brightest pixels on a card are
    /// often a saturated art highlight, while the average of a whole card is
    /// reliably close to neutral across every frame era tried.
    private var gains: (Double, Double, Double) {
        var sum = (0.0, 0.0, 0.0)
        let n = Double(w * h)
        for i in stride(from: 0, to: w * h * 4, by: 4) {
            sum.0 += Double(px[i]); sum.1 += Double(px[i + 1]); sum.2 += Double(px[i + 2])
        }
        let (r, g, b) = (max(1, sum.0 / n), max(1, sum.1 / n), max(1, sum.2 / n))
        let grey = (r + g + b) / 3
        return (grey / r, grey / g, grey / b)
    }

    func luma(_ i: Int) -> Double {
        // Rec. 601 weights. The exact coefficients matter less than using the
        // same ones for the ring and its reference, since every comparison here
        // is between two patches of this same grid.
        (0.299 * Double(px[i]) + 0.587 * Double(px[i + 1])
            + 0.114 * Double(px[i + 2])) / 255
    }

    /// The card's ink and paper tones, as the 5th and 95th percentiles.
    func toneRange() -> (Double, Double)? {
        guard w * h > 0 else { return nil }
        var vals = [Double]()
        vals.reserveCapacity(w * h)
        for i in stride(from: 0, to: w * h * 4, by: 4) { vals.append(luma(i)) }
        vals.sort()
        let lo = vals[vals.count / 20], hi = vals[vals.count * 19 / 20]
        return hi > lo ? (lo, hi) : nil
    }

    /// Every luma value in one rectangle of card space, unsummarised — the
    /// polarity reader needs the distribution, not a median.
    func lumaValues(yFrom: Double, yTo: Double,
                    xFrom: Double, xTo: Double) -> [Double]? {
        let y0 = max(0, Int(yFrom * Double(h)))
        let y1 = min(h, Int(yTo * Double(h)))
        let x0 = max(0, Int(xFrom * Double(w)))
        let x1 = min(w, Int(xTo * Double(w)))
        guard y1 > y0, x1 > x0 else { return nil }
        var out = [Double]()
        out.reserveCapacity((y1 - y0) * (x1 - x0))
        for y in y0..<y1 {
            for x in x0..<x1 { out.append(luma((y * w + x) * 4)) }
        }
        return out
    }

    /// One rectangle of card space, summarised.
    func patch(yFrom: Double, yTo: Double, xFrom: Double, xTo: Double) -> Patch? {
        // No flip. A CGBitmapContext's buffer starts at the image's top row,
        // which is the same way card space runs and the same way cropCard
        // reads it — and cropCard is demonstrably right, since the band at
        // y 0.82 finds the collector row at the bottom of real cards.
        //
        // The flip that used to be here inverted every reading: black-bordered
        // cards read light and white-bordered ones read dark, across a whole
        // live session. It produced tidy plausible numbers the entire time,
        // which is why BorderGridTests pins the orientation against a
        // synthetic card rather than trusting the reasoning.
        let y0 = max(0, Int(yFrom * Double(h)))
        let y1 = min(h, Int(yTo * Double(h)))
        let x0 = max(0, Int(xFrom * Double(w)))
        let x1 = min(w, Int(xTo * Double(w)))
        guard y1 > y0, x1 > x0 else { return nil }

        var lumas = [Double](), chromas = [Double]()
        lumas.reserveCapacity((y1 - y0) * (x1 - x0))
        let (gr, gg, gb) = cast
        for y in y0..<y1 {
            for x in x0..<x1 {
                let i = (y * w + x) * 4
                lumas.append(luma(i))
                let r = Double(px[i]) * gr, g = Double(px[i + 1]) * gg,
                    b = Double(px[i + 2]) * gb
                chromas.append((max(r, g, b) - min(r, g, b)) / 255)
            }
        }
        guard lumas.count >= 24 else { return nil }
        lumas.sort(); chromas.sort()
        // Medians throughout: a single glyph or a speck crossing the sample
        // drags an average and does not move a median.
        let med = lumas[lumas.count / 2]
        return Patch(luma: med, chroma: chromas[chromas.count / 2],
                     mad: lumas.reduce(0) { $0 + abs($1 - med) } / Double(lumas.count))
    }
}

// MARK: - What the footer's polarity measured, and why it is not here
//
// An earlier attempt read the bottom sixth of the card and took the majority
// tone to be the border, on the reasoning that the copyright and artist lines
// are printed on it. The captures show why that fails: the border is only the
// outer tenth of that strip, and the other nine tenths are rules-text box and
// the card's own coloured frame. So the majority is the frame, and the reader
// confidently called cream-bordered cards black.
//
// Kept as a note rather than as code because the reasoning is seductive and
// would otherwise be reinvented.


