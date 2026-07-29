// hoard-scan — capture a Magic card image and OCR its title.
//
// Modes:
//   hoard-scan --list-devices   Print the available cameras as JSON, exit.
//   hoard-scan --image <path>   Headless: OCR an existing image file, print JSON, exit.
//   hoard-scan [--device <id>] [--rotate <deg>]
//                               Live session: open a camera preview window and keep
//                               it open, emitting one JSON event per line on stdout
//                               and reading commands (capture / rotate-left /
//                               rotate-right / quit) as lines on stdin. Space, ←/→
//                               and Esc do the same things in the window itself.
//
// The window persisting across captures is the point: relaunching the helper per
// card costs a camera warm-up each time, and forces the user back to a keystroke
// in the terminal to reopen it.
//
// Capture is Continuity Camera (iPhone) only — webcams are never used; see
// availableCameras() for why.
//
// Output is newline-delimited JSON; see Event for the shapes.
// For --list-devices: {"devices": [{"id": "...", "name": "...", "kind": "..."}]}
//
// The Go side (internal/scan) manages this process and parses those events.

import AVFoundation
import AppKit
import CoreImage
import ImageIO
import Vision

// MARK: - JSON output

/// Event is one newline-delimited JSON message on stdout. The live session emits
/// a stream of these rather than a single object, because the window stays open
/// across many captures.
///
///   {"event":"ready","device":"…","rotation":90}   window is up and previewing
///   {"event":"scan","name":"…","candidates":[…]}   a capture was read
///   {"event":"rotation","rotation":180}            user turned the preview
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

// MARK: - OCR

/// A recognized text line with its vertical position (Vision origin is bottom-left,
/// so a larger `top` is higher on the card) and confidence.
struct Line {
    let text: String
    let top: CGFloat
    let width: CGFloat
    let confidence: Float
}

/// CardRead is everything one capture yielded: the title guess and its alternates,
/// plus whatever the bottom border gave up.
struct CardRead {
    var name: String = ""
    var candidates: [String] = []
    var collectorNumber: String = ""
    var setCode: String = ""
    var bottomLines: [String] = []
}

/// collectorBandFraction is how far up the *card* the collector band reaches, as a
/// fraction of the card's own height.
///
/// The card's own height is the only sane reference. An earlier version measured
/// the band against the frame instead, which fails the moment the card doesn't
/// reach the bottom of the frame: a card resting mid-frame with desk below it puts
/// its collector block ~38% up, well outside any plausible frame-relative band, and
/// the read comes back completely empty. See findCard.
///
/// 0.15 covers the collector block, which lives in the bottom ~6%, with enough room
/// left over for the tilt of a hand-held card. It does not reliably exclude the
/// lowest line of rules text or a creature's power/toughness — past roughly 8° of
/// turn those come along too — so parseCollectorInfo, not this band, is what has to
/// tell a collector number from a "2/2" printed above it.
let collectorBandFraction: CGFloat = 0.15

/// frameBandFallback is the frame-relative band used only when no card rectangle
/// could be found. It is deliberately the whole lower half: without card bounds
/// there is no way to know where the border sits, and a band that is too tall
/// merely adds lines for the patterns below to reject, while one that is too short
/// silently reads nothing at all.
let frameBandFallback: CGFloat = 0.5

// Collector info as printed. "123/264" is the common form; the solo form covers
// cards that print the number without a set total, optionally with a trailing
// rarity letter. The set/language pair is the M15-frame second line, e.g. "MH3 • EN".
//
// The separator in the set/language line is matched loosely: at this glyph size
// Vision reports the printed bullet as "-", ".", "|" and friends about as often as
// "•", so requiring a real bullet drops perfectly good reads. A bare space counts
// too.
//
// What keeps that looseness safe is the trailing token: it must be one of the
// language codes Magic actually prints. Accepting any two letters there matches
// ordinary prose — "cards equal to the sacrificed" uppercases to a tidy
// "EQUAL TO" and yields a set code of "EQUAL".
let cardLanguages = "EN|DE|FR|IT|ES|PT|JA|JP|KO|RU|ZH|ZHS|ZHT|CS|CT|HE|LA|AR|SA|PH"
let collectorPairRE = try! NSRegularExpression(pattern: #"(\d{1,5})\s*/\s*\d{1,5}"#)
let collectorSoloRE = try! NSRegularExpression(pattern: #"^#?\s*(\d{1,5})\s*[A-Z]?$"#)
let setLangRE = try! NSRegularExpression(
    pattern: #"\b([0-9A-Z]{3,5})(?:\s*[•·∙*★+.,:;|/\\―—–-]\s*|\s+)(?:"# + cardLanguages + #")\b"#)

/// confusables maps the non-ASCII lookalikes Vision returns for this text to the
/// ASCII the patterns expect. With language correction off and glyphs barely 1% of
/// the frame tall, it will happily report a set code as "MHЗ" with a Cyrillic З, or
/// a Greek Ο for a zero — which then fails an [0-9A-Z] match for reasons invisible
/// in the emitted bottomLines.
let confusables: [Character: Character] = [
    // Cyrillic
    "А": "A", "В": "B", "Е": "E", "З": "3", "К": "K", "М": "M", "Н": "H", "О": "O",
    "Р": "P", "С": "C", "Т": "T", "У": "Y", "Х": "X", "І": "I", "Ѕ": "S", "Ј": "J",
    // Greek
    "Α": "A", "Β": "B", "Ε": "E", "Ζ": "Z", "Η": "H", "Ι": "I", "Κ": "K", "Μ": "M",
    "Ν": "N", "Ο": "O", "Ρ": "P", "Τ": "T", "Υ": "Y", "Χ": "X",
    // Fullwidth / typographic digits and slashes
    "０": "0", "１": "1", "２": "2", "３": "3", "４": "4", "５": "5", "６": "6",
    "７": "7", "８": "8", "９": "9", "⁄": "/", "∕": "/", "／": "/",
]

/// asciify folds lookalike glyphs to ASCII and uppercases the result, so the
/// patterns can stay strict about shape without also being strict about which
/// codepoint Vision happened to pick. Uppercasing is what lets a lowercase read
/// ("mh3 • en") still yield a set code.
func asciify(_ s: String) -> String {
    String(s.uppercased().map { confusables[$0] ?? $0 })
}

/// group returns a capture group of the first match, if any.
func group(_ re: NSRegularExpression, _ s: String, _ n: Int = 1) -> String? {
    let full = NSRange(s.startIndex..., in: s)
    guard let m = re.firstMatch(in: s, range: full), m.numberOfRanges > n,
          let r = Range(m.range(at: n), in: s) else { return nil }
    return String(s[r])
}

/// normalizeNumber drops the zero padding cards are printed with ("0123/0281"),
/// since Scryfall stores collector numbers unpadded.
func normalizeNumber(_ s: String) -> String {
    let trimmed = s.drop(while: { $0 == "0" })
    return trimmed.isEmpty ? "0" : String(trimmed)
}

/// looksLikeAYear reports whether a bare number is really a printing year. Every
/// card carries a copyright year in the same block as the collector number, and on
/// its own line it is indistinguishable from a collector number by shape alone.
/// Magic has no four-digit collector numbers in this range, so the range is safe
/// to exclude outright.
func looksLikeAYear(_ s: String) -> Bool {
    guard s.count == 4, let n = Int(s) else { return false }
    return n >= 1993 && n <= 2035
}

/// lowercaseCount is how many lowercase letters a line holds, which is how the
/// collector block is told apart from rules text. The border block is set in caps,
/// digits and small caps, so it reads as all-uppercase; prose does not. It matters
/// because a card's rules text can carry a collector-number shape of its own —
/// "Create a 2/2 white Cat creature token" — and the band cannot always exclude it.
func lowercaseCount(_ s: String) -> Int {
    s.filter { $0.isLowercase }.count
}

/// parseCollectorInfo pulls a collector number and set code out of the bottom
/// band's text. That covers both places the number appears: the bottom-left block
/// on M15-frame cards (2014 onward) and the bottom centre on older ones.
///
/// `lines` should arrive bottom-most first. Candidates are then tried in order of
/// how little prose they contain, falling back to that bottom-up order for ties, so
/// the real border block always outranks rules text that merely looks like it. This
/// is a preference and not a filter: when the only line on offer is a messy one, it
/// is still used rather than nothing.
func parseCollectorInfo(_ lines: [String]) -> (number: String, set: String) {
    let ranked = lines.enumerated()
        .sorted { a, b in
            let (la, lb) = (lowercaseCount(a.element), lowercaseCount(b.element))
            return la == lb ? a.offset < b.offset : la < lb
        }
        .map { asciify($0.element) }

    var number = "", set = ""
    for line in ranked {
        if number.isEmpty, let n = group(collectorPairRE, line) {
            number = normalizeNumber(n)
        }
        if set.isEmpty, let s = group(setLangRE, line) {
            set = s
        }
    }
    // Only fall back to a bare number once the "x/y" form has been ruled out —
    // a lone number is much easier to confuse with a planeswalker's loyalty or a
    // copyright year.
    if number.isEmpty {
        for line in ranked {
            let t = line.trimmingCharacters(in: .whitespaces)
            if let n = group(collectorSoloRE, t), !looksLikeAYear(n) {
                number = normalizeNumber(n)
                break
            }
        }
    }
    return (number, set)
}

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
/// frame's lower half when no card could be located.
func collectorBand(_ cg: CGImage) -> CGRect {
    guard let card = findCard(cg) else {
        return CGRect(x: 0, y: 0, width: 1, height: frameBandFallback)
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
    return CGRect(x: 0, y: 0, width: 1, height: min(1, height))
}

/// readCard runs Vision text recognition on a CGImage and returns the best guess
/// at the card's title, a few alternate lines, and the collector info printed
/// along the bottom border.
///
/// The image must already be upright: callers bake any EXIF orientation into the
/// pixels (see uprighted) before applying their own rotation. Passing an
/// orientation here as well would apply two rotations, which lands the title at
/// the bottom and makes the ranking below pick rules text instead.
func readCard(_ cg: CGImage) -> CardRead {
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = true

    // A second pass over the card's bottom border only. Language correction is off
    // here and that is not incidental: with it on, Vision "corrects" "123/264" and
    // set codes like "MH3" into dictionary words, which is the quietest possible
    // way for this to stop working.
    let bottom = VNRecognizeTextRequest()
    bottom.recognitionLevel = .accurate
    bottom.usesLanguageCorrection = false
    bottom.recognitionLanguages = ["en-US"]
    // Normalized, origin bottom-left. The frame is already upright and
    // rotation-normalized by this point, so the band is stable across orientations.
    bottom.regionOfInterest = collectorBand(cg)

    let handler = VNImageRequestHandler(cgImage: cg, options: [:])
    do {
        try handler.perform([request, bottom])
    } catch {
        return CardRead()
    }

    var read = CardRead()

    // Bottom band first: it stands alone, so a title failure doesn't cost us the
    // collector number. Sorted bottom-most first, which is what parseCollectorInfo
    // relies on to prefer the border block over anything printed above it.
    let bottomLines = (bottom.results ?? [])
        .compactMap { obs -> (CGFloat, String)? in
            guard let cand = obs.topCandidates(1).first else { return nil }
            let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
            return t.isEmpty ? nil : (obs.boundingBox.minY, t)
        }
        .sorted { $0.0 < $1.0 }
        .map { $0.1 }
    read.bottomLines = bottomLines
    (read.collectorNumber, read.setCode) = parseCollectorInfo(bottomLines)

    var lines: [Line] = []
    for obs in request.results ?? [] {
        guard let cand = obs.topCandidates(1).first else { continue }
        let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { continue }
        lines.append(Line(text: t, top: obs.boundingBox.maxY,
                          width: obs.boundingBox.width, confidence: cand.confidence))
    }
    if lines.isEmpty {
        return read
    }

    // The card name is the top-most reasonably-wide, confident line. Rank the
    // upper portion of the card by position, preferring wider/high-confidence text.
    let ranked = lines.sorted { a, b in
        // Primary: higher on the card first.
        if abs(a.top - b.top) > 0.04 { return a.top > b.top }
        // Tie-break: wider (titles span most of the card) then more confident.
        if abs(a.width - b.width) > 0.05 { return a.width > b.width }
        return a.confidence > b.confidence
    }

    // Filter out obvious non-title noise (very short tokens, pure numbers/symbols).
    func plausibleName(_ s: String) -> Bool {
        let letters = s.filter { $0.isLetter }
        return letters.count >= 3
    }
    let names = ranked.map { $0.text }.filter(plausibleName)
    let primary = names.first ?? ranked.first!.text
    // Report several lines, best-guess first. The caller tries each against
    // Scryfall, so a card still resolves when the top-line guess is wrong —
    // which happens whenever the capture reaches Vision at an odd angle.
    var candidates = Array(names.prefix(8))
    if candidates.first != primary { candidates.insert(primary, at: 0) }

    read.name = primary
    read.candidates = candidates
    return read
}

/// decodePhoto turns a captured photo into a CGImage plus the orientation Vision
/// should read it at. macOS doesn't expose `AVCapturePhoto.metadata`, so the
/// orientation is recovered from the encoded file representation; if that isn't
/// available the raw representation is assumed upright.
func decodePhoto(_ photo: AVCapturePhoto) -> (CGImage, CGImagePropertyOrientation)? {
    if let data = photo.fileDataRepresentation(),
       let src = CGImageSourceCreateWithData(data as CFData, nil),
       let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) {
        let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any]
        let raw = props?[kCGImagePropertyOrientation] as? UInt32 ?? 1
        return (cg, CGImagePropertyOrientation(rawValue: raw) ?? .up)
    }
    if let cg = photo.cgImageRepresentation() { return (cg, .up) }
    return nil
}

/// saveDebugImage writes an image to $HOARD_SCAN_DEBUG_DIR when that's set, so a
/// scan that reads the wrong line can be reproduced offline with --image.
func saveDebugImage(_ cg: CGImage, _ filename: String) {
    guard let dir = ProcessInfo.processInfo.environment["HOARD_SCAN_DEBUG_DIR"],
          !dir.isEmpty else { return }
    let url = URL(fileURLWithPath: dir).appendingPathComponent(filename)
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil)
    else { return }
    CGImageDestinationAddImage(dest, cg, nil)
    CGImageDestinationFinalize(dest)
    FileHandle.standardError.write(Data("hoard-scan: wrote \(url.path)\n".utf8))
}

/// uprighted bakes an EXIF orientation into the pixels, returning an image that
/// reads correctly with no orientation tag. Normalizing once here is what keeps
/// the tag and the manual rotation from both being applied.
func uprighted(_ cg: CGImage, _ orientation: CGImagePropertyOrientation) -> CGImage {
    if orientation == .up { return cg }
    let ci = CIImage(cgImage: cg).oriented(orientation)
    return CIContext().createCGImage(ci, from: ci.extent) ?? cg
}

/// rotatedImage returns a copy of the image turned clockwise by a multiple of
/// 90°, so OCR sees exactly the framing the corrected preview showed. Doing this
/// in pixels rather than via an EXIF orientation tag keeps the direction
/// unambiguous. The input must already be upright — see uprighted.
func rotatedImage(_ cg: CGImage, clockwiseDegrees deg: Int) -> CGImage {
    let steps = ((deg / 90) % 4 + 4) % 4
    if steps == 0 { return cg }

    let w = cg.width, h = cg.height
    let sideways = steps % 2 == 1
    let outW = sideways ? h : w
    let outH = sideways ? w : h
    guard let ctx = CGContext(
        data: nil, width: outW, height: outH,
        bitsPerComponent: 8, bytesPerRow: 0,
        space: cg.colorSpace ?? CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue)
    else { return cg }

    ctx.translateBy(x: CGFloat(outW) / 2, y: CGFloat(outH) / 2)
    // CoreGraphics rotates counter-clockwise for a positive angle.
    ctx.rotate(by: -CGFloat(steps) * .pi / 2)
    ctx.translateBy(x: -CGFloat(w) / 2, y: -CGFloat(h) / 2)
    ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
    return ctx.makeImage() ?? cg
}

/// cgImage loads an image file through CGImageSource — the same decode a live
/// capture goes through — so --image exercises the real pipeline, including the
/// pixel formats NSImage would quietly normalize away.
func cgImage(fromFile path: String) -> (CGImage, CGImagePropertyOrientation)? {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { return nil }
    let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any]
    let raw = props?[kCGImagePropertyOrientation] as? UInt32 ?? 1
    return (cg, CGImagePropertyOrientation(rawValue: raw) ?? .up)
}

// MARK: - Camera discovery

/// How long to wait for a Continuity Camera to publish itself before deciding
/// there isn't one. Override with HOARD_SCAN_WAIT (seconds) when a phone is slow
/// to appear.
let continuityWait = ProcessInfo.processInfo.environment["HOARD_SCAN_WAIT"]
    .flatMap(Double.init) ?? 2.5

/// availableCameras lists the iPhones offered via Continuity Camera.
///
/// This is deliberately iPhone-only. Built-in and USB webcams are fixed,
/// user-facing, and can't be aimed at a card on the desk, so falling back to one
/// yields a capture the OCR can't read — and the failure looks like bad OCR
/// rather than the wrong camera. Better to say no iPhone is connected.
func availableCameras() -> [AVCaptureDevice] {
    AVCaptureDevice.DiscoverySession(
        deviceTypes: [.continuityCamera], mediaType: .video, position: .unspecified
    ).devices
}

/// noPhoneMessage is the one place the "connect an iPhone" guidance is worded.
let noPhoneMessage = """
no iPhone found — hoard scans with Continuity Camera only, not a webcam. \
Connect an iPhone by USB, or unlock it nearby with Continuity Camera enabled \
(Settings › General › AirPlay & Continuity). If you tapped Disconnect on the \
phone, toggle that setting off and on to re-offer it.
"""

/// spinRunLoop pumps the main run loop for up to `seconds`, returning as soon as
/// `ready()` is true. Continuity Camera is published to AVFoundation
/// asynchronously and only to a process that is pumping its run loop, so a bare
/// enumeration on a blocked main thread reports "no iPhone" even when one is
/// connected. Anything that needs a complete device list has to wait like this.
func spinRunLoop(seconds: Double, until ready: () -> Bool) {
    let deadline = Date().addingTimeInterval(seconds)
    while !ready(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
    }
}

/// hasContinuityCamera reports whether an iPhone has shown up yet.
func hasContinuityCamera() -> Bool { !availableCameras().isEmpty }

/// kindLabel is a short human tag shown next to a camera's name in the picker.
/// Everything discoverable is a Continuity Camera, so this is only interesting
/// when someone has two phones paired.
func kindLabel(_ d: AVCaptureDevice) -> String {
    d.deviceType == .continuityCamera ? "iPhone" : "camera"
}

// MARK: - Live capture (AppKit window + AVFoundation)

final class CaptureController: NSObject, AVCapturePhotoCaptureDelegate {
    /// The uniqueID of the camera to use; nil takes the highest-ranked available one.
    private let deviceID: String?
    /// Extra clockwise rotation, in degrees, applied on top of whatever the
    /// rotation coordinator reports. The coordinator returns 0° for some
    /// Continuity Camera setups — it can't always tell how the phone is being
    /// held — which leaves a portrait-held phone previewing sideways. The user
    /// corrects it once with ←/→ and the caller remembers the result.
    private var manualRotation: Int
    let session = AVCaptureSession()
    fileprivate let photoOutput = AVCapturePhotoOutput()
    private var window: NSWindow?
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private var deviceName = "camera"
    private var rotationCoordinator: AVCaptureDevice.RotationCoordinator?
    private var rotationObservation: NSKeyValueObservation?

    init(deviceID: String?, rotation: Int) {
        self.deviceID = deviceID
        self.manualRotation = ((rotation / 90) % 4 + 4) % 4 * 90
        super.init()
    }

    func start() {
        // Wait for the requested phone (or any phone) to publish itself rather
        // than giving up on a device that's a beat slow to appear.
        spinRunLoop(seconds: continuityWait) {
            guard let id = deviceID else { return hasContinuityCamera() }
            return availableCameras().contains { $0.uniqueID == id }
        }

        // An explicitly requested phone wins; a stale id (that phone walked away)
        // falls back to another paired one. There is deliberately no webcam
        // fallback — see availableCameras().
        let cameras = availableCameras()
        guard let device = deviceID.flatMap({ id in cameras.first { $0.uniqueID == id } })
            ?? cameras.first
        else {
            fail(noPhoneMessage)
        }
        guard let input = try? AVCaptureDeviceInput(device: device) else {
            fail("could not open \(device.localizedName)")
        }
        deviceName = device.localizedName
        guard session.canAddInput(input), session.canAddOutput(photoOutput) else {
            fail("could not configure capture session")
        }
        // Ask for full-resolution stills. The default preset is .high, which caps
        // the capture at video resolution (1080p on Continuity Camera) and leaves
        // the collector number under 1% of the frame height — right at the edge of
        // what Vision can resolve. .photo gives the sensor's full frame instead.
        if session.canSetSessionPreset(.photo) {
            session.sessionPreset = .photo
        }
        session.addInput(input)
        session.addOutput(photoOutput)

        buildWindow()
        trackRotation(of: device)
        DispatchQueue.global(qos: .userInitiated).async { [session] in
            session.startRunning()
            // Re-apply once the session is live: before it starts, the preview
            // layer's connection may not exist yet, so the initial apply is a
            // silent no-op and the KVO observer only fires if the angle later
            // changes.
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                self.applyRotation()
                // Don't announce readiness until the photo output has an active
                // connection and the stream has had a moment to settle:
                // startRunning() returning isn't the same as being able to take a
                // picture, and a capture in that gap fails with an opaque
                // "operation could not be completed".
                spinRunLoop(seconds: 5) {
                    self.photoOutput.connection(with: .video)?.isActive == true
                }
                spinRunLoop(seconds: 0.75) { false }
                emit(Event(event: "ready", rotation: self.manualRotation,
                           device: self.deviceName))
            }
        }
    }

    /// trackRotation keeps the preview upright as the phone is turned. A camera
    /// delivers frames in its own fixed orientation, so without this a
    /// portrait-held iPhone previews rotated 90°. The coordinator reports the
    /// angle that levels the horizon, and is KVO-observable so turning the phone
    /// mid-session re-levels the preview live.
    private func trackRotation(of device: AVCaptureDevice) {
        let coordinator = AVCaptureDevice.RotationCoordinator(device: device, previewLayer: previewLayer)
        rotationCoordinator = coordinator
        applyRotation()
        rotationObservation = coordinator.observe(
            \.videoRotationAngleForHorizonLevelPreview, options: [.new]
        ) { [weak self] _, _ in
            DispatchQueue.main.async { self?.applyRotation() }
        }
    }

    /// autoPreviewAngle is what the coordinator thinks levels the preview; 0 when
    /// it can't tell how the phone is held.
    private var autoPreviewAngle: CGFloat {
        rotationCoordinator?.videoRotationAngleForHorizonLevelPreview ?? 0
    }

    /// effectiveRotation is the total turn the user is actually looking at: what
    /// the coordinator contributes to the preview plus their manual correction.
    /// The captured pixels get this same total, so OCR reads exactly the framing
    /// that was confirmed on screen.
    private var effectiveRotation: Int {
        (Int(autoPreviewAngle) + manualRotation) % 360
    }

    /// applyRotation pushes the effective angle onto the preview connection and
    /// refreshes the title.
    ///
    /// The still is deliberately left unrotated. The coordinator's *capture*
    /// angle can differ from its *preview* angle — here by a full 180° — so
    /// letting it rotate the photo turns the capture by a different amount than
    /// the preview showed, and OCR reads an upside-down card no matter what the
    /// user picks. Instead the whole turn is applied to the pixels afterwards
    /// from effectiveRotation, which keeps the two paths identical by
    /// construction.
    private func applyRotation() {
        if let conn = previewLayer?.connection {
            let angle = CGFloat(effectiveRotation)
            if conn.isVideoRotationAngleSupported(angle) { conn.videoRotationAngle = angle }
        }
        if let conn = photoOutput.connection(with: .video), conn.isVideoRotationAngleSupported(0) {
            conn.videoRotationAngle = 0
        }
        updateTitle()
    }

    /// rotate turns the preview a quarter-turn and remembers the choice. The
    /// parent is told so it can persist the correction without waiting for the
    /// window to close.
    private func rotate(clockwise: Bool) {
        manualRotation = (manualRotation + (clockwise ? 90 : 270)) % 360
        applyRotation()
        emit(Event(event: "rotation", rotation: manualRotation))
    }

    /// updateTitle surfaces the current rotation — including what the automatic
    /// angle contributed — so a wrong orientation is diagnosable at a glance.
    private func updateTitle() {
        let total = Int(autoPreviewAngle) + manualRotation
        window?.title = "hoard — \(deviceName) · \(total % 360)° "
            + "(auto \(Int(autoPreviewAngle))°) · Space capture · ←/→ rotate · Esc cancel"
    }

    private func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 560),
            styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        win.title = "hoard — \(deviceName) · Space to capture · Esc to cancel"
        win.center()

        let view = PreviewView(frame: win.contentLayoutRect)
        view.autoresizingMask = [.width, .height]
        let preview = AVCaptureVideoPreviewLayer(session: session)
        preview.videoGravity = .resizeAspect
        view.previewLayer = preview
        view.wantsLayer = true
        view.layer = preview
        self.previewLayer = preview
        view.onKey = { [weak self] key in
            switch key {
            case .space: self?.capture()
            case .escape: self?.shutdown()
            case .rotateLeft: self?.rotate(clockwise: false)
            case .rotateRight: self?.rotate(clockwise: true)
            }
        }
        win.contentView = view
        win.makeKeyAndOrderFront(nil)
        win.makeFirstResponder(view)
        NSApp.activate(ignoringOtherApps: true)
        self.window = win
    }

    private func capture() {
        // Re-level right before the shutter in case the phone moved since the
        // last KVO notification.
        applyRotation()
        photoOutput.capturePhoto(with: AVCapturePhotoSettings(), delegate: self)
    }

    /// shutdown closes the window and ends the process. The rotation rides along
    /// so a correction made just before closing isn't thrown away.
    func shutdown() {
        emit(Event(event: "closed", rotation: manualRotation))
        session.stopRunning()
        exit(0)
    }

    /// handle runs one command from the parent. These mirror the in-window keys,
    /// so the user can drive the camera from the terminal without switching
    /// windows — which is the point of keeping the session open.
    func handle(command: String) {
        switch command {
        case "capture": capture()
        case "rotate-left": rotate(clockwise: false)
        case "rotate-right": rotate(clockwise: true)
        case "quit": shutdown()
        default: emit(Event(event: "error", message: "unknown command: \(command)"))
        }
    }

    /// A failed capture reports an error but keeps the window open — one bad
    /// frame shouldn't tear down a session the user is mid-way through.
    func photoOutput(_ output: AVCapturePhotoOutput,
                     didFinishProcessingPhoto photo: AVCapturePhoto, error: Error?) {
        if let error {
            emit(Event(event: "error", message: "capture failed: \(error.localizedDescription)"))
            return
        }
        guard let (cg, orientation) = decodePhoto(photo) else {
            emit(Event(event: "error", message: "no image from capture"))
            return
        }
        // Normalize the capture's own orientation first, then match the framing
        // the user corrected in the preview. Exactly one rotation each.
        saveDebugImage(cg, "capture-raw.png")
        let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: effectiveRotation)
        saveDebugImage(forOCR, "capture-ocr.png")
        let read = readCard(forOCR)
        // Emit and stay live: the window persists so the next card can be framed
        // and captured without relaunching the camera.
        emit(Event(event: "scan", name: read.name, candidates: read.candidates,
                   rotation: manualRotation,
                   collectorNumber: read.collectorNumber, setCode: read.setCode,
                   bottomLines: read.bottomLines))
    }
}

/// PreviewView hosts the camera preview layer and forwards key presses.
final class PreviewView: NSView {
    enum Key { case space, escape, rotateLeft, rotateRight }
    var previewLayer: AVCaptureVideoPreviewLayer?
    var onKey: ((Key) -> Void)?
    override var acceptsFirstResponder: Bool { true }
    override func keyDown(with event: NSEvent) {
        switch event.keyCode {
        case 49: onKey?(.space)        // space
        case 53: onKey?(.escape)       // esc
        case 123: onKey?(.rotateLeft)  // left arrow
        case 124: onKey?(.rotateRight) // right arrow
        default: super.keyDown(with: event)
        }
    }
}

// MARK: - App lifecycle

/// Supplies the Dock menu and routes every quit path through the controller.
///
/// Quitting must not go straight to `exit()`: hoard is waiting on stdout for a
/// `closed` event to know the camera window is gone. `CaptureController.shutdown`
/// emits that first, so closing from the Dock leaves the add session in exactly
/// the state it would be in after pressing esc.
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let controller: CaptureController

    init(controller: CaptureController) {
        self.controller = controller
    }

    /// Right-click (or click-and-hold) on the Dock icon.
    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? {
        let menu = NSMenu()
        // A nil target sends the action up the responder chain to NSApp, so this
        // lands in applicationShouldTerminate below just like ⌘Q does.
        menu.addItem(withTitle: "Quit hoard scan",
                     action: #selector(NSApplication.terminate(_:)),
                     keyEquivalent: "")
        return menu
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        controller.shutdown() // emits "closed", stops the session, exits 0
        return .terminateNow
    }

    /// Closing the capture window should end the helper, not leave a menu-bar
    /// ghost with no way back to the camera.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }
}

/// Builds the minimal menu bar an activation-policy `.regular` app needs.
///
/// Without a main menu AppKit gives the app no ⌘Q at all, which is how the
/// helper ended up only closable from the terminal.
func installMainMenu() {
    let appMenu = NSMenu()
    appMenu.addItem(withTitle: "Quit hoard scan",
                    action: #selector(NSApplication.terminate(_:)),
                    keyEquivalent: "q")

    let appItem = NSMenuItem()
    appItem.submenu = appMenu

    let main = NSMenu()
    main.addItem(appItem)
    NSApp.mainMenu = main
}

// MARK: - Entry point

let args = Array(CommandLine.arguments.dropFirst())

if args.contains("--list-devices") {
    // Become a real (if dock-less) app: AVFoundation only advertises Continuity
    // Camera to a GUI process with a running run loop.
    NSApplication.shared.setActivationPolicy(.accessory)

    // Ask for camera access first: device names are only populated once granted,
    // and it puts the permission prompt at picker time rather than mid-capture.
    var granted = false
    var answered = false
    AVCaptureDevice.requestAccess(for: .video) { g in
        granted = g
        answered = true
    }
    spinRunLoop(seconds: 120) { answered }
    if !granted {
        fail("camera access denied — grant it in System Settings › Privacy & Security › Camera")
    }

    // Give a nearby iPhone a moment to publish itself before concluding it isn't
    // there; returns as soon as one appears.
    spinRunLoop(seconds: continuityWait, until: hasContinuityCamera)

    let devices = availableCameras().map {
        Device(id: $0.uniqueID, name: $0.localizedName, kind: kindLabel($0))
    }
    if devices.isEmpty {
        fail(noPhoneMessage, code: 4)
    }
    emit(DeviceList(devices: devices))
    exit(0)
}

// Default to a quarter-turn clockwise: a portrait-held iPhone hands over a
// landscape frame, so the capture arrives turned 90° counter-clockwise. hoard
// passes --rotate explicitly (its own saved value), so this default only applies
// when the helper is run by hand.
var requestedRotation = 90
if let i = args.firstIndex(of: "--rotate"), i + 1 < args.count {
    requestedRotation = Int(args[i + 1]) ?? 90
}

if let i = args.firstIndex(of: "--image") {
    guard i + 1 < args.count else { fail("--image requires a file path") }
    guard let (cg, orientation) = cgImage(fromFile: args[i + 1]) else {
        fail("could not read image: \(args[i + 1])")
    }
    // Byte-for-byte the live pipeline, so this mode reproduces real scans.
    let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: requestedRotation)
    // Write the same debug bitmap the live path does, so HOARD_SCAN_DEBUG_DIR can
    // be used to tune the bottom band against a still photo.
    saveDebugImage(forOCR, "capture-ocr.png")
    let read = readCard(forOCR)
    emit(Event(event: "scan", name: read.name, candidates: read.candidates,
               rotation: requestedRotation,
               collectorNumber: read.collectorNumber, setCode: read.setCode,
               bottomLines: read.bottomLines))
    exit(read.name.isEmpty ? 3 : 0)
}

// Live mode: request camera access, then run the AppKit event loop.
var requestedDevice: String?
if let i = args.firstIndex(of: "--device"), i + 1 < args.count {
    requestedDevice = args[i + 1]
}
let app = NSApplication.shared
app.setActivationPolicy(.regular)
let controller = CaptureController(deviceID: requestedDevice, rotation: requestedRotation)

// Held in a top-level binding: NSApplication does not retain its delegate.
let appDelegate = AppDelegate(controller: controller)
app.delegate = appDelegate
installMainMenu()

// Commands arrive as bare lines on stdin (capture / rotate-left / rotate-right /
// quit) so the parent can drive the camera while the terminal keeps focus. A
// closed stdin means the parent is gone, so shut down rather than linger with an
// orphaned window.
Thread.detachNewThread {
    while let line = readLine(strippingNewline: true) {
        let cmd = line.trimmingCharacters(in: .whitespaces)
        if cmd.isEmpty { continue }
        DispatchQueue.main.async { controller.handle(command: cmd) }
    }
    DispatchQueue.main.async { controller.shutdown() }
}

AVCaptureDevice.requestAccess(for: .video) { granted in
    DispatchQueue.main.async {
        if !granted { fail("camera access denied — grant it in System Settings › Privacy › Camera") }
        controller.start()
    }
}
app.run()
