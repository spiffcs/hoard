import CoreGraphics
import Foundation

// MARK: - Border colour

// Before 1998 a card printed no collector number, so the copyright year is the
// only printing evidence it carries, and the year alone pins 24% of the
// pre-1998 printings that have a sibling. Border colour is the era's other
// discriminator and the catalog already stores it: year plus border pins 47%,
// and 4th Edition goes from 0% to 95%, since 4ED (white, 1995) and 4BB (black,
// 1995) differ in nothing else a camera can see.
//
// A pixel classifier over the perspective crop was built and removed once
// already — see docs/scanner-tuning.md. It failed because the crop does not
// reliably contain the border: Vision locks onto whichever edge has contrast,
// sometimes the card's outer cut and sometimes the printed frame just inside
// it, so an 8th Edition Gaea's Herald came back black off its gold-brown inner
// frame. Saturation could not separate the cases (0.40 for a real white border
// under tungsten against 0.43 for that frame). The missing signal was never in
// the pixels; it was knowing whether what we were looking at is the card.
//
// So nothing here reads the crop. Two changes make it work:
//
//  1. The geometry is anchored on text whose identity is already proven by its
//     *content* — the copyright line and the artist credit, which
//     copyrightFurniture and artistCredit recognize by what they say. Those sit
//     at a fixed fraction of card height, so two of them recover the card's own
//     scale, and the ring is sampled on the full-resolution frame from there.
//     No amount of edge contrast can fake "this line says Wizards of the Coast".
//
//  2. The ring is judged against the card's own two tones, never an absolute
//     threshold. On an old frame the footer is printed *on the border* — black
//     ink on a white one, white ink on a black one — so an Otsu split of that
//     single line yields the paper point and the ink point under whatever light
//     the desk happened to have. That is the direct answer to the tungsten
//     problem, and it is self-checking: if the reconstruction has drifted onto
//     an inner frame, the ring lands between the two tones instead of on one of
//     them, and we abstain rather than guess.

/// What the reader sampled. Kept together so the verdict can be a separate
/// question asked of the numbers, rather than a decision tangled through the
/// measuring — every field here is reported on the BorderReading whether or
/// not a verdict follows it.
private struct BorderMeasurements {
    let geometry: CardGeometry
    let tones: FooterTones
    let bottom: RingStats
    /// The opposite edge, when the card's top is in shot at all.
    let top: RingStats?
    /// The frame just inside the border, which is what a drifted ring lands on.
    let inner: RingStats
    /// Where the ring sits in the range the card prints its own footer with:
    /// 0 is that line's ink, 1 is the surface it is printed on.
    let tone: CGFloat
    /// How far the ring stands off the frame just inside it.
    let standoff: CGFloat
    let scaleDisagreement: CGFloat
    let frameH: CGFloat
}

/// readBorder runs the whole chain and returns what it saw. It answers only
/// when every gate passes; `abstain` always says which one did not.
public func readBorder(_ cg: CGImage, lines: [Line], bandLines: [Line],
                       copyrightYear: Int) -> BorderReading {
    var out = BorderReading()
    guard let m = measureBorder(cg, lines: lines, bandLines: bandLines,
                                copyrightYear: copyrightYear, into: &out)
    else { return out }
    judgeBorder(m, into: &out)
    return out
}

/// measureBorder reconstructs the card and samples it, recording every number
/// on `out` as it goes. Returns nil — having said which step could not be
/// taken — when the chain cannot continue.
private func measureBorder(_ cg: CGImage, lines: [Line], bandLines: [Line],
                           copyrightYear: Int,
                           into out: inout BorderReading) -> BorderMeasurements? {
    // Both passes are offered. The whole-frame pass usually has the title too,
    // which is what makes the scale checkable; the band pass is aimed at the
    // footer and often the only one that read it at all.
    guard let anchor = footerAnchor(lines + bandLines) else {
        out.abstain = "no footer anchor"
        return nil
    }
    let footer = anchor.line
    out.footerText = footer.text
    out.anchorKind = anchor.kind.rawValue
    out.footerVMeasured = Double(1 - footer.box.midY)
    out.footerLeftU = Double(footer.box.minX)
    out.footerRightU = Double(footer.box.maxX)
    if let c = positionalCredit(lines + bandLines) {
        out.creditCandidateLeftU = Double(c.box.minX)
    }

    let frameW = CGFloat(cg.width), frameH = CGFloat(cg.height)
    // The title is whichever plausible line sits highest above the footer.
    let title = lines.filter { $0.box.midY > footer.box.midY + 0.2 }
        .max { $0.box.midY < $1.box.midY }
    if let t = title {
        out.titleText = t.text
        out.titleVMeasured = Double(1 - t.box.midY)
        out.titleLeftU = Double(t.box.minX)
    }
    guard let g = cardGeometry(footer: footer, kind: anchor.kind, title: title,
                               year: copyrightYear,
                               frameW: frameW, frameH: frameH) else {
        out.abstain = "no geometry"
        return nil
    }
    // Without a title there is only one scale estimate, and nothing to check
    // it against — which is how one card reconstructed 50% too tall while
    // reporting perfect agreement with itself. An unchecked estimate is not
    // evidence, so this abstains rather than trusting it.
    if title == nil {
        out.abstain = "no title anchor"
        return nil
    }
    out.cardHeightPx = Double(g.heightPx)
    out.thetaDegrees = Double(g.theta * 180 / .pi)
    out.footerGlyphVMeasured = Double(footer.box.height * frameH / max(g.heightPx, 1))
    // The glyph estimate is allowed to be either one text row or two, because
    // Vision frequently returns the credit and copyright rows as a single
    // observation — "Illus: © Jeff A: Menges" is both of them at once — and
    // that box is twice as tall. Measured over the corpus, a merged anchor
    // read 0.032 of card height against a single row's 0.0176. Every card lost
    // to this check was a merged row whose baseline estimate was fine (median
    // error −1.6%), so scoring it as one row was rejecting good geometry for
    // having been read in a shape we had not accounted for.
    let ratio = g.scaleFromGlyph / max(g.scaleFromBaseline, 1)
    let disagreement = min(abs(ratio - 1), abs(ratio / 2 - 1))
    out.scaleAgreement = Double(1 - disagreement)

    guard let px = PixelReader(cg) else {
        out.abstain = "no pixels"
        return nil
    }
    // The footer's two tones are the card's own printed black and white point,
    // measured under whatever light the desk had — see FooterTones for why
    // they are emphatically not the border.
    guard let patch = footerPatch(px, g) else {
        out.abstain = "footer not bimodal"
        return nil
    }
    out.patchDark = Double(patch.dark)
    out.patchBright = Double(patch.bright)
    out.patchSeparation = Double(patch.range)
    out.patchDarkFraction = Double(patch.darkFraction)
    out.patchChroma = Double(patch.chroma)
    out.clipHigh = Double(patch.clipHigh)
    out.clipLow = Double(patch.clipLow)

    let bottomV = 1 - CardLayout.borderV * CardLayout.ringDepth
    let topV = CardLayout.borderV * CardLayout.ringDepth
    guard let bottom = borderRing(px, g, edgeV: bottomV) else {
        out.abstain = "no bottom ring"
        return nil
    }
    out.ringBottom = Double(bottom.median)
    out.ringMAD = Double(bottom.mad)
    out.ringChroma = Double(bottom.chroma)
    let top = borderRing(px, g, edgeV: topV)
    if let top = top { out.ringTop = Double(top.median) }
    out.horizontalAnchor = g.anchorLeft != nil
    if let sym = symbolInk(px, g) {
        out.symbolCoverage = Double(sym.coverage)
        out.symbolContrast = Double(sym.contrast)
    }
    let innerStats = borderRing(px, g, edgeV: CardLayout.innerV)
    if let inner = innerStats {
        out.innerBottom = Double(inner.median)
        out.innerMAD = Double(inner.mad)
    }

    guard let inner = innerStats else {
        out.abstain = "no inner reference"
        return nil
    }
    let delta = bottom.median - inner.median
    guard patch.range >= BorderGate.minToneRange else {
        out.abstain = "footer tones too close"
        return nil
    }
    let tone = (bottom.median - patch.dark) / patch.range
    out.t = Double(tone)
    out.standoff = Double(delta)

    return BorderMeasurements(geometry: g, tones: patch, bottom: bottom, top: top,
                              inner: inner, tone: tone, standoff: delta,
                              scaleDisagreement: disagreement, frameH: frameH)
}

/// judgeBorder turns the measurements into a verdict, or into the name of the
/// gate that refused one.
///
/// Everything measureBorder produced is reported whatever happens. From here
/// down it is a claim, so every gate has to pass — a wrong border silently
/// picks the wrong printing, and unlike a stray collector number it will always
/// match *something*.
private func judgeBorder(_ m: BorderMeasurements, into out: inout BorderReading) {
    if m.geometry.heightPx < BorderGate.minCardHeightPx { out.abstain = "card too small"; return }
    // A card cannot be taller than the picture of it. Cheap, always available,
    // and it is what catches a reconstruction that has gone badly wrong rather
    // than slightly wrong — one corpus card came back 50% too tall while its
    // two scale estimates agreed with each other perfectly, because with no
    // title there was only ever one of them.
    if m.geometry.heightPx > m.frameH * 1.05 { out.abstain = "card larger than frame"; return }
    if abs(out.thetaDegrees) > Double(BorderGate.maxThetaDegrees) { out.abstain = "too tilted"; return }
    if m.scaleDisagreement > BorderGate.maxScaleDisagreement { out.abstain = "scales disagree"; return }
    if m.bottom.mad > BorderGate.maxRingMAD { out.abstain = "ring not uniform"; return }

    // Two signals, and both have to say the same thing. The tone position asks
    // whether the border falls outside the range the card prints itself with;
    // the standoff asks whether the ring is even a different surface from the
    // frame beside it. Requiring both is what makes the drift failure — a ring
    // that slid onto the inner frame — abstain rather than answer confidently
    // about a surface it never sampled.
    let verdict: String
    if m.tone >= BorderGate.whiteTone { verdict = "white" }
    else if m.tone <= BorderGate.blackTone { verdict = "black" }
    else { out.abstain = "between tones"; return }

    if abs(m.standoff) < BorderGate.minInnerDelta {
        out.abstain = "ring matches inner frame"
        return
    }
    if (m.standoff > 0) != (verdict == "white") {
        out.abstain = "tone and frame standoff disagree"
        return
    }

    // A second edge is not required — a card resting low in frame can have its
    // top out of shot — but when it is there it has to agree, because a ring
    // that disagrees with the opposite ring is measuring something that is not
    // the border.
    // The opposite edge corroborates when it can, and vetoes only when it
    // actively says the other thing. It is not held to clearing the same bar
    // independently, because a card lying on a desk is not lit evenly: the
    // far edge is systematically dimmer, and the reference tones come from the
    // footer at the near edge. Measured on a live session of white-bordered
    // cards, the top ring ran 0.10–0.15 darker than the bottom, which was
    // enough to fail a strict check on three of six cards that were plainly
    // white. An indeterminate second opinion is not a contradiction — it just
    // does not earn the stronger source label.
    if let top = m.top {
        let topTone = (top.median - m.tones.dark) / m.tones.range
        let opposite = verdict == "white"
            ? topTone <= BorderGate.blackTone
            : topTone >= BorderGate.whiteTone
        if opposite {
            out.abstain = "edges disagree"
            return
        }
        let agrees = verdict == "white"
            ? topTone >= BorderGate.whiteTone
            : topTone <= BorderGate.blackTone
        out.source = agrees ? "footer+ring" : "footer"
    } else {
        out.source = "footer"
    }
    out.color = verdict
}
