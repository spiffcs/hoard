import CoreGraphics
import Foundation

private struct BorderMeasurements {
    let geometry: CardGeometry
    let tones: FooterTones
    let bottom: RingStats
    let top: RingStats?
    let inner: RingStats
    let tone: CGFloat
    let standoff: CGFloat
    let scaleDisagreement: CGFloat
    let frameH: CGFloat
}

public func readBorder(_ cg: CGImage, lines: [Line], bandLines: [Line],
                       frame: FrameEvidence) -> BorderReading {
    var out = BorderReading()
    guard let m = measureBorder(cg, lines: lines, bandLines: bandLines,
                                frame: frame, into: &out)
    else { return out }
    judgeBorder(m, into: &out)
    return out
}

private func measureBorder(_ cg: CGImage, lines: [Line], bandLines: [Line],
                           frame: FrameEvidence,
                           into out: inout BorderReading) -> BorderMeasurements? {
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
    let title = lines.filter { $0.box.midY > footer.box.midY + 0.2 }
        .max { $0.box.midY < $1.box.midY }
    if let t = title {
        out.titleText = t.text
        out.titleVMeasured = Double(1 - t.box.midY)
        out.titleLeftU = Double(t.box.minX)
    }
    guard let g = cardGeometry(footer: footer, kind: anchor.kind, title: title,
                               frame: frame,
                               frameW: frameW, frameH: frameH) else {
        out.abstain = "no geometry"
        return nil
    }
    if title == nil {
        out.abstain = "no title anchor"
        return nil
    }
    out.cardHeightPx = Double(g.heightPx)
    out.thetaDegrees = Double(g.theta * 180 / .pi)
    out.footerGlyphVMeasured = Double(footer.box.height * frameH / max(g.heightPx, 1))
    let ratio = g.scaleFromGlyph / max(g.scaleFromBaseline, 1)
    let disagreement = min(abs(ratio - 1), abs(ratio / 2 - 1))
    out.scaleAgreement = Double(1 - disagreement)

    guard let px = PixelReader(cg) else {
        out.abstain = "no pixels"
        return nil
    }
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

private func judgeBorder(_ m: BorderMeasurements, into out: inout BorderReading) {
    if m.geometry.heightPx < BorderGate.minCardHeightPx { out.abstain = "card too small"; return }
    if m.geometry.heightPx > m.frameH * 1.05 { out.abstain = "card larger than frame"; return }
    if abs(out.thetaDegrees) > Double(BorderGate.maxThetaDegrees) { out.abstain = "too tilted"; return }
    if m.scaleDisagreement > BorderGate.maxScaleDisagreement { out.abstain = "scales disagree"; return }
    if m.bottom.mad > BorderGate.maxRingMAD { out.abstain = "ring not uniform"; return }

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
