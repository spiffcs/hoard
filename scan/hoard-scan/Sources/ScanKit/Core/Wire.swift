// The newline-delimited JSON protocol spoken to the Go side (internal/scan).
// Field names and their optional-vs-empty distinctions are a wire contract:
// an older hoard binary must keep parsing what a newer helper emits.

import Foundation

// MARK: - JSON output

/// Event is one newline-delimited JSON message on stdout. The live session emits
/// a stream of these rather than a single object, because the window stays open
/// across many captures.
///
///   {"event":"ready","device":"…","rotation":90}   window is up and previewing
///   {"event":"scan","name":"…","candidates":[…]}   a capture was read
///   {"event":"rotation","rotation":180}            user turned the preview
///   {"event":"framing","state":"auto"}             auto-framing toggled; see state
///   {"event":"torch","state":"on"}                 phone torch toggled; see state
///   {"event":"error","message":"…"}                capture failed; session lives
///   {"event":"closed","rotation":90}               window closed; process exits
struct Event: Encodable {
    var event: String
    var name: String = ""
    var candidates: [String] = []
    /// The manual rotation currently in effect, so the caller can remember it
    /// and hand it back via --rotate next time.
    var rotation: Int = 0
    var message: String = ""
    var device: String = ""
    /// Read off the card's bottom border when present. Cards printed before
    /// Exodus (1998) carry no collector number at all, and the set code only
    /// became reliably printed with the M15 frame, so both are routinely empty
    /// and an empty read is ordinary rather than a failure.
    var collectorNumber: String = ""
    var setCode: String = ""
    /// Raw text of the bottom band, for tuning the read via --image.
    var bottomLines: [String] = []
    /// Every card the capture found, in reading order — a fanned spread yields
    /// one entry per card. Optional so events that carry no cards keep their
    /// old wire shape byte-for-byte; the flat fields above stay populated from
    /// the frame-wide read, so an older hoard keeps working against this
    /// helper.
    var cards: [CardEntry]? = nil
    /// Vision's confidence (0–1) in the line chosen as the title. Optional so
    /// events that carry no read keep their old wire shape; an older hoard
    /// ignores it.
    var confidence: Float? = nil
    /// Whether the collector band was anchored to a detected card rectangle
    /// (true) or fell back to the frame's lower half (false). An anchored band
    /// is the only one whose collector read deserves trust.
    var bandAnchored: Bool? = nil
    /// True when this scan was fired by the auto trigger rather than a capture
    /// command or the space key.
    var auto: Bool? = nil
    /// Capabilities this helper supports, advertised on the ready event so the
    /// parent can feature-detect instead of probing with commands.
    var features: [String]? = nil
    /// Auto-trigger state, carried by "auto" events only.
    var state: String? = nil
    /// Collector blocks beyond the primary flat fields — see CardEntry.
    var collectorAlts: [CollectorRead]? = nil
    /// The primary block's printed finish marker; see CollectorRead.finish.
    var finishHint: String? = nil
}

/// HUDCommand is the payload of the `result` stdin verb — the Go side's
/// scan.HUDResult. A tier means flash it with the tier's styling and sound;
/// a total means update the persistent session counter, always silently.
/// Amount is absent on an unpriced card, and always absent on the review
/// tier, which renders as "Needs Review" rather than a price.
struct HUDCommand: Decodable {
    var amount: Double?
    var tier: String? // bulk | win | jackpot | unpriced | review
    var total: Double?
}

/// Device is one camera the helper can capture from, as listed by --list-devices.
struct Device: Encodable {
    var id: String
    var name: String
    var kind: String
}

struct DeviceList: Encodable {
    var devices: [Device]
}

/// emit writes one JSON line to stdout, unbuffered. It must go straight to the
/// file handle rather than through print(): stdout is a pipe here, so buffered
/// writes would sit unflushed and the parent would block waiting for an event
/// that has already happened.
func emit<T: Encodable>(_ out: T) {
    guard let data = try? JSONEncoder().encode(out) else { return }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data("\n".utf8))
}

func fail(_ message: String, code: Int32 = 1) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(code)
}
