// The newline-delimited JSON protocol spoken to the Go side (internal/scan).
// Field names and their optional-vs-empty distinctions are a wire contract:
// an older hoard binary must keep parsing what a newer helper emits.
//
// This target exists so that contract has exactly one definition. Two scan
// pipelines now speak it — ScanKit's Continuity Camera path and CardKit's
// iPhone head, which share no other code by design — and the moment each
// declares its own event struct they can drift apart silently, which is a
// contract nobody is checking. The types moved here verbatim; the mapping from
// each pipeline's own internals into them stays with that pipeline.
//
// Nothing in this file may import a camera, a window or a read pipeline. It is
// Foundation and the shape of a JSON line.

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
public struct Event: Encodable {
    public var event: String
    public var name: String = ""
    public var candidates: [String] = []
    /// The manual rotation currently in effect, so the caller can remember it
    /// and hand it back via --rotate next time.
    public var rotation: Int = 0
    public var message: String = ""
    public var device: String = ""
    /// The sending app's build stamp, carried on the ready event so a session
    /// log proves which build produced it. One live session was spent tuning
    /// against a phone silently running a stale install; this is the field
    /// that makes that impossible to miss. Optional so old helpers keep their
    /// wire shape.
    public var appVersion: String? = nil
    /// Read off the card's bottom border when present. Cards printed before
    /// Exodus (1998) carry no collector number at all, and the set code only
    /// became reliably printed with the M15 frame, so both are routinely empty
    /// and an empty read is ordinary rather than a failure.
    public var collectorNumber: String = ""
    public var setCode: String = ""
    /// Raw text of the bottom band, for tuning the read via --image.
    public var bottomLines: [String] = []
    /// Every card the capture found, in reading order — a fanned spread yields
    /// one entry per card. Optional so events that carry no cards keep their
    /// old wire shape byte-for-byte; the flat fields above stay populated from
    /// the frame-wide read, so an older hoard keeps working against this
    /// helper.
    public var cards: [CardEntry]? = nil
    /// Vision's confidence (0–1) in the line chosen as the title. Optional so
    /// events that carry no read keep their old wire shape; an older hoard
    /// ignores it.
    public var confidence: Float? = nil
    /// Whether the collector band was anchored to a detected card rectangle
    /// (true) or fell back to the frame's lower half (false). An anchored band
    /// is the only one whose collector read deserves trust.
    public var bandAnchored: Bool? = nil
    /// Why the trigger fired: "removed", "replaced", "moved" or "nudged".
    /// Absent on a manual shutter, and absent from helpers older than this
    /// field.
    ///
    /// The distinction the parent could not make. A capture following a card
    /// leaving the frame, or a card being laid over the last one, is a new
    /// placement *as a fact*; a capture following the parent's own nudge is a
    /// second look at something already handled. Both arrive as `auto: true`,
    /// so the parent was reconstructing the difference from a four-second
    /// window — and a real scan can race a nudge onto the wire.
    public var fireReason: String? = nil
    /// How far the picture inside the pinned captured window had drifted from
    /// the card that was shot through it, on the sample that fired. Compared
    /// against the trigger's `cardChanged`.
    ///
    /// Carried for the parent's telemetry rather than for a decision: it is
    /// the number that separates "the scene churned" from "the card changed",
    /// and it existed only in a stderr trace line, which no golden and no log
    /// analysis can read.
    public var holdDelta: Double? = nil
    /// How closely the nearest card still in frame resembled the one that was
    /// shot, on the sample that fired. Compared against `movedFaceMax`.
    ///
    /// **Only meaningful when `fireReason` is "replaced" or "moved".** Those
    /// are the two causes decided by measuring it; "removed" means nothing was
    /// on the watched rect to measure, and "nudged" means no card comparison
    /// ran at all. The trigger sends nil in both cases rather than the last
    /// value it happened to hold — see the `.removed` branch of `hold(_:)`.
    ///
    /// This is what lets the parent tell a decisive replacement from a
    /// marginal one, instead of taking a boolean's word for it.
    public var faceDelta: Double? = nil
    /// True when this scan was fired by the auto trigger rather than a capture
    /// command or the space key.
    public var auto: Bool? = nil
    /// Capabilities this helper supports, advertised on the ready event so the
    /// parent can feature-detect instead of probing with commands.
    public var features: [String]? = nil
    /// Auto-trigger state, carried by "auto" events only.
    public var state: String? = nil
    /// Collector blocks beyond the primary flat fields — see CardEntry.
    public var collectorAlts: [CollectorRead]? = nil
    /// The primary block's printed finish marker; see CollectorRead.finish.
    public var finishHint: String? = nil
    /// The primary block's printed language code; see CollectorRead.language.
    public var language: String? = nil

    /// Spelled out rather than left to the compiler because making these types
    /// public to share them costs the implicit memberwise init. Argument order
    /// and defaults match the properties above exactly, so every existing call
    /// site is unaffected — and a field added above without a matching
    /// parameter here is a compile error rather than a silent wire change.
    public init(
        event: String,
        name: String = "",
        candidates: [String] = [],
        rotation: Int = 0,
        message: String = "",
        device: String = "",
        appVersion: String? = nil,
        collectorNumber: String = "",
        setCode: String = "",
        bottomLines: [String] = [],
        cards: [CardEntry]? = nil,
        confidence: Float? = nil,
        bandAnchored: Bool? = nil,
        auto: Bool? = nil,
        fireReason: String? = nil,
        holdDelta: Double? = nil,
        faceDelta: Double? = nil,
        features: [String]? = nil,
        state: String? = nil,
        collectorAlts: [CollectorRead]? = nil,
        finishHint: String? = nil,
        language: String? = nil
    ) {
        self.event = event
        self.name = name
        self.candidates = candidates
        self.rotation = rotation
        self.message = message
        self.device = device
        self.appVersion = appVersion
        self.collectorNumber = collectorNumber
        self.setCode = setCode
        self.bottomLines = bottomLines
        self.cards = cards
        self.confidence = confidence
        self.bandAnchored = bandAnchored
        self.auto = auto
        self.fireReason = fireReason
        self.holdDelta = holdDelta
        self.faceDelta = faceDelta
        self.features = features
        self.state = state
        self.collectorAlts = collectorAlts
        self.finishHint = finishHint
        self.language = language
    }
}

/// CardEntry is one card found in a capture.
public struct CardEntry: Encodable {
    public var name: String = ""
    public var candidates: [String] = []
    public var collectorNumber: String = ""
    public var setCode: String = ""
    /// Vision's confidence in this entry's title read.
    public var confidence: Float = 0
    /// Which channel produced the entry: "crop" (a perspective-corrected card
    /// rectangle — collector info, when present, is card-anchored by
    /// construction) or "frame" (the frame-wide title pass, which never carries
    /// collector info).
    public var source: String = ""
    /// Collector blocks beyond the primary one, so the caller can keep the
    /// number that actually matches a printing when a stacked card's border
    /// shares the band.
    public var collectorAlts: [CollectorRead]? = nil
    /// The primary block's printed finish marker; see CollectorRead.finish.
    public var finishHint: String = ""
    /// The printed language code; see CollectorRead.language. Optional so a
    /// capture on a frame that prints none keeps the old wire shape.
    public var language: String? = nil
    /// Which signal produced finishHint: "separator" is the modern set row's
    /// glyph, "sparkle" the starburst a retro-frame foil prints at the text
    /// box's corner. Absent when no finish was read.
    ///
    /// Telemetry only — the Go side treats a present finishHint as evidence
    /// whatever produced it. It is on the wire so a session's log can say which
    /// signal is carrying the answer, because the failure mode this feature
    /// exists to fix was precisely a signal that went quiet without anyone
    /// noticing. Optional so a capture that reads no finish keeps the old wire
    /// shape byte-for-byte.
    public var finishSource: String? = nil
    /// What the foil-sparkle reader measured, whatever verdict followed.
    ///
    /// The score is on the wire because the feature it belongs to shipped
    /// without it, and one live session was enough to show the cost: four
    /// retro-frame foils read as nonfoil and the log could not say whether they
    /// were near misses or the marker was never found. Offline on the same
    /// stills they were 0.020, 0.339, 0.425 and 0.496 against a bar of 0.52 —
    /// two failure modes wearing one symptom, and no way to tell them apart
    /// from a session log.
    ///
    /// The offsets are here for the same reason and are the sharper signal: a
    /// score taken at the edge of the search window means the marker is
    /// somewhere outside it, which is a registration problem and not a
    /// threshold one. Nil when the reader was never asked.
    public var sparkleScore: Double? = nil
    public var sparkleOffsetU: Double? = nil
    public var sparkleOffsetV: Double? = nil
    public var sparkleContrast: Double? = nil
    /// The same three, measured on the warm-cool channel — see SparkleVerdict.
    ///
    /// Reported alongside rather than instead of, and never merged into one
    /// "best" number. The reason the second channel exists is that the two fail
    /// on different cards; a session that could only see the winning score
    /// could not tell whether the new channel was earning its place or whether
    /// luma was still doing all the work.
    public var sparkleChromaScore: Double? = nil
    public var sparkleChromaContrast: Double? = nil
    /// Where collectorNumber came from: nil/"" is the trusted collector band,
    /// "copyright" the old-frame copyright-line tail (upgrade-only evidence —
    /// see CardRead.copyrightNumber). Optional so events keep their old wire
    /// shape when no copyright read happened.
    public var numberSource: String? = nil
    /// End year of the copyright range, when one was read; see
    /// CardRead.copyrightYear.
    public var copyrightYear: Int? = nil
    /// The printed border, "white" or "black", when it could be read off the
    /// card's own edge. Absent means the reader declined — never "" and never
    /// "unknown", because absence is the honest shape for "no evidence" and an
    /// empty string invites a downstream `!= ""` that treats it as one.
    ///
    /// Optional so a capture that reads no border keeps the old wire shape
    /// byte-for-byte. Deliberately not mirrored onto the flat Event fields,
    /// for the reason numberSource exists: a parent that predates provenance
    /// treats anything flat as trusted.
    public var borderColor: String? = nil
    /// How the border was established: "footer+ring" when the opposite edge
    /// agreed, "footer" when only one edge was in shot. The Go side may hold
    /// the weaker one to a lower bar.
    public var borderSource: String? = nil
    /// The frame family the footer's shape claims: "retro" when the artist is
    /// credited on a row of its own ("Illus. …" — the 1993-2003 frames and
    /// their modern retro-frame reprints), absent when the footer read as a
    /// modern frame or read too badly to say. Absent rather than "modern" on
    /// silence, for the same reason borderColor is: no evidence must never
    /// downstream-compare equal to evidence. A same-set regular/retro twin
    /// pair differs in nothing else the wire carries.
    public var frameStyle: String? = nil

    public init(
        name: String = "",
        candidates: [String] = [],
        collectorNumber: String = "",
        setCode: String = "",
        confidence: Float = 0,
        source: String = "",
        collectorAlts: [CollectorRead]? = nil,
        finishHint: String = "",
        language: String? = nil,
        finishSource: String? = nil,
        sparkleScore: Double? = nil,
        sparkleOffsetU: Double? = nil,
        sparkleOffsetV: Double? = nil,
        sparkleContrast: Double? = nil,
        sparkleChromaScore: Double? = nil,
        sparkleChromaContrast: Double? = nil,
        numberSource: String? = nil,
        copyrightYear: Int? = nil,
        borderColor: String? = nil,
        borderSource: String? = nil,
        frameStyle: String? = nil
    ) {
        self.name = name
        self.candidates = candidates
        self.collectorNumber = collectorNumber
        self.setCode = setCode
        self.confidence = confidence
        self.source = source
        self.collectorAlts = collectorAlts
        self.finishHint = finishHint
        self.language = language
        self.finishSource = finishSource
        self.sparkleScore = sparkleScore
        self.sparkleOffsetU = sparkleOffsetU
        self.sparkleOffsetV = sparkleOffsetV
        self.sparkleContrast = sparkleContrast
        self.sparkleChromaScore = sparkleChromaScore
        self.sparkleChromaContrast = sparkleChromaContrast
        self.numberSource = numberSource
        self.copyrightYear = copyrightYear
        self.borderColor = borderColor
        self.borderSource = borderSource
        self.frameStyle = frameStyle
    }
}

/// CollectorRead is one collector block read off a card's bottom border.
public struct CollectorRead: Encodable {
    public var number = ""
    public var set = ""
    public var finish = ""
    /// The language code printed beside the set code ("en", "ja"), lower-cased
    /// to Scryfall's spelling. Empty on frames that print none.
    ///
    /// It crosses because the Go side has a use for it that nothing else can
    /// serve: a printing published only in another language shares a set and a
    /// collector number with its English namesake, separated by a marker the
    /// card does not print, so the number alone always picks the English one.
    public var language = ""
    /// Whether the number was read in "n/total" form. A bare number shares its
    /// shape with a mana cost and a power box; a pair with a plausible total
    /// does not, so the crop channel can trust one and not the other. Local
    /// only — the wire shape stays what parent binaries already parse.
    public var pair = false

    enum CodingKeys: String, CodingKey {
        case number, set, finish, language
    }

    public init(
        number: String = "", set: String = "", finish: String = "",
        language: String = "", pair: Bool = false
    ) {
        self.number = number
        self.set = set
        self.finish = finish
        self.language = language
        self.pair = pair
    }
}

/// HUDCommand is the payload of the `result` stdin verb — the Go side's
/// scan.HUDResult. A tier means flash it with the tier's styling and sound;
/// a total means update the persistent session counter, always silently.
/// Amount is absent on an unpriced card, and always absent on the review
/// tier, which renders as "Needs Review" rather than a price.
public struct HUDCommand: Decodable {
    public var amount: Double?
    public var tier: String?  // bulk | win | jackpot | unpriced | review
    public var total: Double?
    /// The committed card's name, so the phone can say what registered and
    /// the operator's eyes never leave the pile for the terminal.
    public var name: String?
    /// What was actually written: "foil" or "nonfoil", absent on older parents.
    ///
    /// The committed finish rather than what the camera thought it saw. A
    /// source can only report whether a star appeared between the set code and
    /// the language; the parent has the catalog, so it knows whether the
    /// printing comes in foil at all and what it therefore recorded.
    public var finish: String?
    /// A short instruction for the screen, sent when the parent's duplicate
    /// rules suppressed a sighting the operator may overrule. Carries no
    /// tier and no sound: the suppression itself was deliberately silent.
    public var note: String?
    /// Marks the note as the second-copy offer — show a button that answers
    /// it with a `promote` event, the parent's `+` key made tappable.
    public var promote: Bool?
}

/// Device is one camera the helper can capture from, as listed by --list-devices.
public struct Device: Encodable {
    public var id: String
    public var name: String
    public var kind: String

    public init(id: String, name: String, kind: String) {
        self.id = id
        self.name = name
        self.kind = kind
    }
}

public struct DeviceList: Encodable {
    public var devices: [Device]

    public init(devices: [Device]) { self.devices = devices }
}

/// emit writes one JSON line to stdout, unbuffered. It must go straight to the
/// file handle rather than through print(): stdout is a pipe here, so buffered
/// writes would sit unflushed and the parent would block waiting for an event
/// that has already happened.
public func emit(_ out: some Encodable) {
    guard let data = try? JSONEncoder().encode(out) else { return }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data("\n".utf8))
}

public func fail(_ message: String, code: Int32 = 1) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(code)
}
