import CoreGraphics
import Foundation

public struct SparkleReading: Sendable {
    public let score: CGFloat
    public let offsetU: CGFloat
    public let offsetV: CGFloat
    public let contrast: CGFloat
    public let samples: Int
}

public typealias CardSampler = (CGFloat, CGFloat) -> CGFloat?

public enum SparkleTemplate {
    public static let cols = 52
    public static let rows = 34
    public static let spanU: CGFloat = CGFloat(cols) / 630
    public static let spanV: CGFloat = CGFloat(rows) / 880
}

public func retroFrameFooter(_ lines: [String]) -> Bool {
    lines.contains { illusToken($0) || artistCredit($0) }
}

public func sparkleInCard(_ card: CGImage,
                          window: SparkleWindow = .fitted,
                          anchorShiftU: CGFloat = 0,
                          anchorShiftV: CGFloat = 0) -> SparkleVerdict? {
    guard let px = PixelReader(card) else { return nil }
    let w = CGFloat(card.width), h = CGFloat(card.height)
    return SparkleVerdict(
        luma: sparkleScan({ u, v in px.luma((u + anchorShiftU) * w, (v + anchorShiftV) * h) },
                          window: window),
        chroma: sparkleScan({ u, v in px.warmCool((u + anchorShiftU) * w, (v + anchorShiftV) * h) },
                            window: window, template: sparkleChromaTemplate))
}

public struct SparkleVerdict: Sendable {
    public let luma: SparkleReading?
    public let chroma: SparkleReading?

    public var isFoil: Bool {
        if (luma?.score ?? -1) >= SparkleGate.accept,
           (luma?.contrast ?? -1) >= SparkleGate.acceptLumaContrast { return true }
        return (chroma?.contrast ?? -1) >= SparkleGate.acceptChromaContrast
            && (luma?.contrast ?? -1) >= SparkleGate.chromaVoteLumaFloor
    }

    public var channel: String {
        if (luma?.score ?? -1) >= SparkleGate.accept,
           (luma?.contrast ?? -1) >= SparkleGate.acceptLumaContrast { return "luma" }
        if (chroma?.contrast ?? -1) >= SparkleGate.acceptChromaContrast,
           (luma?.contrast ?? -1) >= SparkleGate.chromaVoteLumaFloor { return "chroma" }
        if (chroma?.score ?? -1) >= SparkleGate.acceptChroma { return "chroma-only" }
        return ""
    }

    public var score: CGFloat { luma?.score ?? 0 }
}

public func sparklePatch(du: CGFloat, dv: CGFloat, step: Int,
                         _ sample: CardSampler) -> [CGFloat]? {
    var out: [CGFloat] = []
    out.reserveCapacity((SparkleTemplate.rows / step + 1) * (SparkleTemplate.cols / step + 1))
    var j = 0
    while j < SparkleTemplate.rows {
        let v = CardLayout.sparkleV + dv
            + (CGFloat(j) / CGFloat(SparkleTemplate.rows - 1) - 0.5) * SparkleTemplate.spanV
        var i = 0
        while i < SparkleTemplate.cols {
            let u = CardLayout.sparkleU + du
                + (CGFloat(i) / CGFloat(SparkleTemplate.cols - 1) - 0.5) * SparkleTemplate.spanU
            guard let l = sample(u, v) else { return nil }
            out.append(l)
            i += step
        }
        j += step
    }
    return out
}

public func sparkleNormalise(_ p: [CGFloat]) -> [CGFloat]? {
    guard !p.isEmpty else { return nil }
    let mean = p.reduce(0, +) / CGFloat(p.count)
    var out = p.map { $0 - mean }
    let energy = sqrt(out.reduce(0) { $0 + $1 * $1 })
    guard energy > 1e-6 else { return nil }
    for i in out.indices { out[i] /= energy }
    return out
}

public func sparkleCorrelate(_ a: [CGFloat], _ b: [CGFloat]) -> CGFloat {
    var sum: CGFloat = 0
    for i in a.indices where i < b.count { sum += a[i] * b[i] }
    return sum
}

public struct SparkleWindow: Sendable {
    public let u: CGFloat, v: CGFloat
    public let cellsU: Int, cellsV: Int
    public init(u: CGFloat, v: CGFloat, cellsU: Int, cellsV: Int) {
        self.u = u; self.v = v; self.cellsU = cellsU; self.cellsV = cellsV
    }
    public static let fitted = SparkleWindow(
        u: SparkleGate.searchU, v: SparkleGate.searchV,
        cellsU: SparkleGate.searchCellsU, cellsV: SparkleGate.searchCellsV)
}

public func sparkleScan(_ sample: CardSampler,
                        window: SparkleWindow = .fitted,
                        template: [CGFloat] = sparkleTemplate) -> SparkleReading? {
    var reads = 0

    func patch(du: CGFloat, dv: CGFloat, step: Int) -> [CGFloat]? {
        guard let p = sparklePatch(du: du, dv: dv, step: step, sample) else { return nil }
        reads += p.count
        return p
    }
    let normalise = sparkleNormalise
    let correlate = sparkleCorrelate

    let cellU = window.u / CGFloat(window.cellsU)
    let cellV = window.v / CGFloat(window.cellsV)

    var coarse: [(score: CGFloat, i: Int, j: Int)] = []
    let t4 = sparkleTemplateDecimated(4, template)
    var i = -window.cellsU
    while i <= window.cellsU {
        var j = -window.cellsV
        while j <= window.cellsV {
            if let p = patch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV, step: 4),
               let n = normalise(p) {
                coarse.append((correlate(n, t4), i, j))
            }
            j += SparkleGate.coarseStride
        }
        i += SparkleGate.coarseStride
    }
    guard !coarse.isEmpty else { return nil }
    coarse.sort { $0.score > $1.score }

    let t2 = sparkleTemplateDecimated(2, template)
    var bestScore = -CGFloat.infinity
    var bestI = 0, bestJ = 0
    for candidate in coarse.prefix(SparkleGate.refineCandidates) {
        for di in -SparkleGate.refineRadius...SparkleGate.refineRadius {
            for dj in -SparkleGate.refineRadius...SparkleGate.refineRadius {
                let i = candidate.i + di, j = candidate.j + dj
                guard abs(i) <= window.cellsU, abs(j) <= window.cellsV else { continue }
                guard let p = patch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV, step: 2),
                      let n = normalise(p) else { continue }
                let s = correlate(n, t2)
                if s > bestScore { bestScore = s; bestI = i; bestJ = j }
            }
        }
    }
    guard bestScore > -CGFloat.infinity else { return nil }
    let bestDU = CGFloat(bestI) * cellU, bestDV = CGFloat(bestJ) * cellV

    guard let final = patch(du: bestDU, dv: bestDV, step: 1) else { return nil }
    let spread = medianAbsoluteDeviation(final)
    guard let n = normalise(final) else { return nil }
    guard spread >= SparkleGate.minContrast else {
        return SparkleReading(score: 0, offsetU: bestDU, offsetV: bestDV,
                              contrast: spread, samples: reads)
    }
    return SparkleReading(score: correlate(n, template),
                          offsetU: bestDU, offsetV: bestDV,
                          contrast: spread, samples: reads)
}

func sparkleTemplateDecimated(_ step: Int, _ source: [CGFloat] = sparkleTemplate) -> [CGFloat] {
    guard source.count == SparkleTemplate.rows * SparkleTemplate.cols else { return [] }
    var out: [CGFloat] = []
    var j = 0
    while j < SparkleTemplate.rows {
        var i = 0
        while i < SparkleTemplate.cols {
            out.append(source[j * SparkleTemplate.cols + i])
            i += step
        }
        j += step
    }
    let mean = out.reduce(0, +) / CGFloat(out.count)
    for i in out.indices { out[i] -= mean }
    let energy = sqrt(out.reduce(0) { $0 + $1 * $1 })
    guard energy > 1e-6 else { return out }
    for i in out.indices { out[i] /= energy }
    return out
}
