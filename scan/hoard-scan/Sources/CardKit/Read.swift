@_exported import BorderKit
import CoreGraphics
import CoreImage
import Foundation
import Vision

public struct CardReading: Sendable {
    public var title = ""
    public var lines: [String] = []
    public var printing = Printing()
    public var cardBox: CGRect?
    public var located = false
    public var bandLines: [String] = []
    public var border = BorderReading()
    public var sparkle: SparkleVerdict? = nil
    public var footerRecovered = false
    public var timings = ReadTimings()

    public init() {}
}

public struct ReadTimings: Sendable {
    public var locate = 0.0
    public var whole = 0.0
    public var band = 0.0
    public var border = 0.0
    public var sparkle = 0.0
    public var total = 0.0
    public init() {}
    public var line: String {
        String(format: "locate=%.0f whole=%.0f band=%.0f border=%.0f sparkle=%.0f",
               locate, whole, band, border, sparkle)
    }
}

@inline(__always) func millis(since t: DispatchTime) -> Double {
    Double(DispatchTime.now().uptimeNanoseconds - t.uptimeNanoseconds) / 1e6
}

@available(macOS 15, iOS 18, *)
public func readCard(_ image: CGImage) async -> CardReading {
    var out = CardReading()
    let began = DispatchTime.now()
    let locateStart = DispatchTime.now()
    let found = locateCard(image)
    out.timings.locate = millis(since: locateStart)

    guard let card = found else {
        let t = DispatchTime.now()
        out.lines = await recognizeText(image, correctLanguage: true)
        out.timings.whole = millis(since: t)
        out.title = chooseTitle(from: out.lines)
        out.timings.total = millis(since: began)
        return out
    }

    out.located = true
    out.cardBox = normalize(card.bounds, in: image)

    let upright = card.image
    let wholeStart = DispatchTime.now()
    let headRect = cropCard(upright, CardGeometry.head) != nil
        ? CardGeometry.head : CGRect(x: 0, y: 0, width: 1, height: 1)
    async let whole = recognizeLines(
        cropCard(upright, CardGeometry.head) ?? upright, correctLanguage: true)

    var bandRead: [Line] = []
    let bandStart = DispatchTime.now()
    if let band = cropCard(upright, CardGeometry.band) {
        bandRead = await recognizeLines(band, correctLanguage: false)
    }
    out.timings.band = millis(since: bandStart)

    let headRead = await whole
    out.lines = headRead.map(\.text)
    out.timings.whole = millis(since: wholeStart)

    var bandLines: [String] = bandRead.map(\.text)

    var printing = readPrinting(bandLines: bandLines)
    if printing.isEmpty, let recovered = await recoverFooter(image, box: card.bounds) {
        bandLines += recovered.lines
        printing = recovered.printing
        out.footerRecovered = true
    }

    if let wide = card.wide {
        let borderStart = DispatchTime.now()
        out.border = readBorder(
            wide,
            lines: headRead.map {
                intoWide($0, from: headRect, margin: card.wideMarginUsed)
            },
            bandLines: bandRead.map {
                intoWide($0, from: CardGeometry.band, margin: card.wideMarginUsed)
            },
            frame: FrameEvidence(year: printing.year ?? 0,
                                 hasSetCode: !printing.setCode.isEmpty,
                                 numberOnOwnRow: printing.numberSource == .ownRow))
        out.timings.border = millis(since: borderStart)
    } else {
        out.border.abstain = "no wide crop"
    }

    if printing.finish != "foil", (printing.year ?? 9999) >= SparkleGate.firstFoilYear {
        let sparkleStart = DispatchTime.now()
        out.sparkle = sparkleInCard(
            upright, anchorShiftV: companyAnchorShiftV(bandRead))
        out.timings.sparkle = millis(since: sparkleStart)
        if let s = out.sparkle, s.isFoil {
            printing.finish = "foil"
            printing.finishSource = "sparkle-" + s.channel
        }
    }

    out.bandLines = bandLines
    out.printing = printing
    out.title = chooseTitle(from: out.lines)
    out.timings.total = millis(since: began)
    return out
}

func companyAnchorShiftV(_ bandRead: [Line]) -> CGFloat {
    let companyRowToMarkerV: CGFloat = -0.0671
    let maxShift: CGFloat = 0.03
    guard let company = bandRead.first(where: { looksLikeCompanyRow($0.text) })
    else { return 0 }
    let vMid = 0.82 + (1 - company.box.midY) * 0.18
    let shift = vMid + companyRowToMarkerV - CardLayout.sparkleV
    guard abs(shift) <= maxShift else { return 0 }
    return shift
}

@available(macOS 15, iOS 18, *)
func recoverFooter(
    _ frame: CGImage, box: CGRect
) async -> (lines: [String], printing: Printing)? {
    let strip = CGRect(
        x: box.minX,
        y: box.maxY - box.height * FooterRecovery.overlap,
        width: box.width,
        height: box.height * (FooterRecovery.overlap + FooterRecovery.reach)
    ).integral.intersection(
        CGRect(x: 0, y: 0, width: frame.width, height: frame.height))

    guard strip.width >= 8, strip.height >= 8,
          let crop = frame.cropping(to: strip)
    else { return nil }

    let lines = await recognizeText(crop, correctLanguage: false)
    let printing = readPrinting(bandLines: lines)
    return printing.isEmpty ? nil : (lines, printing)
}

enum FooterRecovery {
    static let overlap = 0.06
    static let reach = 0.33
}

public enum CardGeometry {
    public static let band = CGRect(x: 0, y: 0.82, width: 1, height: 0.18)

    public static let head = CGRect(x: 0, y: 0, width: 1, height: 0.30)

    public static let symbol = CGRect(x: 0.78, y: 0.53, width: 0.19, height: 0.11)
}

func intoWide(_ line: Line, from crop: CGRect, margin m: CGFloat) -> Line {
    let k = 1 + 2 * m
    let cardTop = crop.minY + (1 - line.box.maxY) * crop.height
    let cardBottom = crop.minY + (1 - line.box.minY) * crop.height
    let cardLeft = crop.minX + line.box.minX * crop.width
    let cardRight = crop.minX + line.box.maxX * crop.width

    let wideTop = (m + cardTop) / k
    let wideBottom = (m + cardBottom) / k
    let box = CGRect(x: (m + cardLeft) / k, y: 1 - wideBottom,
                     width: (cardRight - cardLeft) / k,
                     height: wideBottom - wideTop)

    func point(_ p: CGPoint) -> CGPoint {
        let cardX = crop.minX + p.x * crop.width
        let cardY = crop.minY + (1 - p.y) * crop.height
        return CGPoint(x: (m + cardX) / k, y: 1 - (m + cardY) / k)
    }
    let quad = line.quad.map {
        Quad(topLeft: point($0.topLeft), topRight: point($0.topRight),
             bottomLeft: point($0.bottomLeft), bottomRight: point($0.bottomRight))
    }
    return Line(text: line.text, box: box, confidence: line.confidence, quad: quad)
}

public func cropCard(_ card: CGImage, _ rect: CGRect) -> CGImage? {
    let px = CGRect(
        x: rect.minX * CGFloat(card.width), y: rect.minY * CGFloat(card.height),
        width: rect.width * CGFloat(card.width), height: rect.height * CGFloat(card.height)
    ).integral.intersection(CGRect(x: 0, y: 0, width: card.width, height: card.height))
    guard px.width >= 8, px.height >= 8 else { return nil }
    return card.cropping(to: px)
}

private func normalize(_ r: CGRect, in image: CGImage) -> CGRect {
    CGRect(x: r.minX / CGFloat(image.width), y: r.minY / CGFloat(image.height),
           width: r.width / CGFloat(image.width), height: r.height / CGFloat(image.height))
}

@available(macOS 15, iOS 18, *)
func recognizeText(_ cg: CGImage, correctLanguage: Bool) async -> [String] {
    await recognizeLines(cg, correctLanguage: correctLanguage).map(\.text)
}

@available(macOS 15, iOS 18, *)
public func recognizeLines(_ cg: CGImage, correctLanguage: Bool) async -> [Line] {
    var request = RecognizeTextRequest(.revision3)
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = correctLanguage
    request.recognitionLanguages = [Locale.Language(identifier: "en-US")]
    guard let observations = try? await request.perform(on: cg) else { return [] }
    return observations.compactMap { o -> Line? in
        guard let c = o.topCandidates(1).first else { return nil }
        let text = c.string.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }
        let b = o.boundingBox.cgRect
        return Line(text: text, box: b, confidence: c.confidence,
                    quad: Quad(topLeft: CGPoint(x: b.minX, y: b.maxY),
                               topRight: CGPoint(x: b.maxX, y: b.maxY),
                               bottomLeft: CGPoint(x: b.minX, y: b.minY),
                               bottomRight: CGPoint(x: b.maxX, y: b.minY)))
    }
}

@available(macOS 15, iOS 18, *)
public func measureAnchorOnFlatCard(_ image: CGImage) async -> AnchorFit? {
    var lines = await recognizeLines(image, correctLanguage: false)
    var bandText: [String] = []
    if let band = cropCard(image, CardGeometry.band) {
        let read = await recognizeLines(band, correctLanguage: false)
        bandText = read.map(\.text)
        lines += read.map { intoWhole($0, from: CardGeometry.band) }
    }
    let printing = readPrinting(bandLines: bandText)
    guard let m = measureAnchor(lines, year: printing.year ?? 0) else { return nil }
    return AnchorFit(anchor: m, year: printing.year ?? 0,
                     setCode: printing.setCode,
                     numberSource: printing.numberSource.rawValue)
}

public struct AnchorFit: Sendable {
    public let anchor: AnchorMeasurement
    public let year: Int
    public let setCode: String
    public let numberSource: String
}

func intoWhole(_ line: Line, from crop: CGRect) -> Line {
    let top = crop.minY + (1 - line.box.maxY) * crop.height
    let bottom = crop.minY + (1 - line.box.minY) * crop.height
    let box = CGRect(x: crop.minX + line.box.minX * crop.width,
                     y: 1 - bottom,
                     width: line.box.width * crop.width,
                     height: bottom - top)
    return Line(text: line.text, box: box, confidence: line.confidence, quad: nil)
}
