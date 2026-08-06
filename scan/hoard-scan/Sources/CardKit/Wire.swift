// Turning a CardReading into the line the Go side already understands.
//
// CardKit shares no code with ScanKit, but it must not invent a second dialect
// of the protocol: the Go side is one parser, and "an older hoard binary must
// keep parsing what a newer helper emits" applies whichever pipeline emitted it.
// So the shapes come from ScanWire and only the mapping lives here.
//
// The mapping is deliberately narrow. CardKit's own types carry more than the
// wire does — where a number came from, how confident the segmentation was —
// and the temptation is to widen the contract to fit. Anything added here is
// added to a format two binaries and three years of goldens depend on, so the
// rule is that a field crosses only when the Go side has a use for it.

import Foundation
import ScanWire

extension CardReading {
    /// scanEvent is this reading as the `scan` event the parent expects.
    ///
    /// `rotation` is reported back rather than applied — the caller has already
    /// baked orientation into the pixels, and an event that claimed to have
    /// rotated them would invite a second turn downstream.
    public func scanEvent(
        rotation: Int, auto: Bool? = nil, fireReason: String? = nil
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
            // `located` maps onto bandAnchored rather than getting a field of
            // its own: the wire's word for it is already "was the band anchored
            // to a detected card, or did it fall back to a fraction of the
            // frame" — which is exactly the distinction, and the Go side
            // already declines to trust an unanchored read.
            bandAnchored: located,
            auto: auto,
            fireReason: fireReason,
            collectorAlts: nil,
            // The printed foil marker, when the set row carried one. Left nil
            // rather than "nonfoil" when it did not: the Go side's
            // finishFromEvidence tells a read finish from a defaulted one, and
            // flattening that distinction is how a foil records silently as
            // nonfoil.
            finishHint: printing.finish.isEmpty ? nil : printing.finish)
    }

    /// The single-card entry, or nothing when the capture found nothing.
    ///
    /// Nil is the important case and it was missing. This built an entry
    /// unconditionally, so a capture of bare desk — no card located, no title,
    /// no band — still crossed the wire as a card whose every field was empty,
    /// and the parent dutifully queued it as "nothing readable". Seven of ten
    /// review cases in one live session were that: five fired 1.6-2.0s *before*
    /// the real card committed, as the operator's hand was still moving, and
    /// the rest during idle gaps of a minute or more with nothing on the desk.
    ///
    /// The macOS helper has never done this. Its `empty-desk` fixture pins the
    /// contract in one line — a frame with no cards yields an empty list, not
    /// junk entries — and its golden is literally `[]`. This is CardKit
    /// catching up to a rule that already existed.
    ///
    /// The test is "did the capture learn anything a card would tell it", not
    /// "was a card located". A card the segmenter missed but whose title read
    /// anyway is a real card and a legitimate review case.
    ///
    /// Two ways to qualify, and the first is why "any text at all" is not the
    /// bar. `chooseTitle` falls back to the first line whatever it looks like,
    /// so a capture that caught only a power/toughness box arrives with a
    /// title of "0/1" — which crossed the wire as a card and queued live as
    /// `couldn't identify "0/1"`. A scrap is not a name. `plausibleTitle`
    /// already knows this and already rejects it; it just was not being asked.
    ///
    /// A band read qualifies on its own, even with no plausible title. That is
    /// the glare-blown-title case, and it is the one the review queue exists
    /// for: the footer names the printing, so a human can finish the job. Live,
    /// a capture whose title read "2" carried a perfect `1993-2002 ... 46/350`
    /// and was rightly kept.
    private var cardEntry: CardEntry? {
        // The title is checked as well as the lines it came from, so this does
        // not depend on a caller having populated both. In the live path
        // `chooseTitle` always draws from `lines`, but a reading assembled any
        // other way should answer the same question the same way.
        guard !bandLines.isEmpty
            || plausibleTitle(title)
            || lines.contains(where: plausibleTitle)
        else { return nil }
        return CardEntry(
            name: title,
            candidates: Array(lines.prefix(3)),
            collectorNumber: printing.number,
            setCode: printing.setCode,
            source: located ? "crop" : "frame",
            // On the *card entry*, which is what the parent actually reads.
            //
            // The first attempt at this set the Event's top-level finishHint
            // instead, and the difference is invisible until it matters: the
            // top-level fields are the compatibility path for a helper too old
            // to send a card list, so `CardList()` falls back to them only when
            // `cards` is empty. A capture that sends both puts the finish in
            // one place and has it read from the other — which is exactly how a
            // foil Deserted Temple committed as nonfoil twice.
            finishHint: printing.finish,
            // A number lifted out of a copyright row is upgrade-only evidence
            // on the Go side, and mislabelling it as a band read is how a
            // guessed printing gets treated as a confirmed one.
            numberSource: printing.numberSource == .copyrightRow ? "copyright" : nil,
            copyrightYear: printing.year,
            // Sent. Fitted and scored on 16 stills from the real rig: 13
            // answered, 13 correct, none wrong, 3 abstained. The Go side still
            // only reorders a printing list with this — it cannot auto-commit
            // on a border — so the cost of the three abstentions is a card that
            // queues exactly as it does today.
            borderColor: border.color,
            borderSource: border.source)
    }
}
