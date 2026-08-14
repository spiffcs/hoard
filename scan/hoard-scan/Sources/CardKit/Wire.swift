import Foundation
import ScanWire

extension CardReading {
    public func scanEvent(
        rotation: Int, auto: Bool? = nil, fireReason: String? = nil,
        holdDelta: Double? = nil, faceDelta: Double? = nil
    ) -> Event {
        Event(
            event: "scan",
            name: title,
            candidates: Array(lines.prefix(3)),
            rotation: rotation,
            collectorNumber: printing.number,
            setCode: printing.setCode,
            bottomLines: bandLines,
            cards: cardEntry.map { [$0] } ?? [],
            bandAnchored: located,
            auto: auto,
            fireReason: fireReason,
            holdDelta: holdDelta,
            faceDelta: faceDelta,
            collectorAlts: nil,
            finishHint: printing.finish.isEmpty ? nil : printing.finish,
            language: scryfallLanguage(printing.language))
    }

    private var cardEntry: CardEntry? {
        guard !bandLines.isEmpty
            || plausibleTitle(title)
            || lines.contains(where: plausibleTitle)
        else { return nil }
        let retroFooter = retroFrameFooter(bandLines + lines)
        return CardEntry(
            name: title,
            candidates: Array(lines.prefix(3)),
            collectorNumber: printing.number,
            setCode: printing.setCode,
            source: located ? "crop" : "frame",
            finishHint: printing.finish,
            language: scryfallLanguage(printing.language),
            finishSource: printing.finishSource.isEmpty ? nil : printing.finishSource,
            sparkleScore: sparkle?.luma.map { Double($0.score) },
            sparkleOffsetU: sparkle?.luma.map { Double($0.offsetU) },
            sparkleOffsetV: sparkle?.luma.map { Double($0.offsetV) },
            sparkleContrast: sparkle?.luma.map { Double($0.contrast) },
            sparkleChromaScore: sparkle?.chroma.map { Double($0.score) },
            sparkleChromaContrast: sparkle?.chroma.map { Double($0.contrast) },
            numberSource: printing.numberSource == .copyrightRow ? "copyright" : nil,
            copyrightYear: printing.year,
            borderColor: border.color,
            borderSource: border.source,
            frameStyle: retroFooter ? "retro" : nil)
    }
}

public func scryfallLanguage(_ printed: String) -> String? {
    let code = printed.uppercased()
    if code.isEmpty { return nil }
    switch code {
    case "CS": return "zhs"
    case "CT": return "zht"
    default: return code.lowercased()
    }
}
