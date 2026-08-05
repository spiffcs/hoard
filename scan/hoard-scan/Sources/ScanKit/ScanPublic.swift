// ScanKit's public surface — the second entry point, alongside CLI.swift.
//
// The macOS helper reaches the pipeline through runCLI() and a pipe. The iPhone
// capture head is a separate Xcode target, so it needs a real API, and this is
// deliberately the whole of it.
//
// Keeping it this small is the point. Making FrameScan, CardEntry and Event
// public would export the internal shapes of the read pipeline across a module
// boundary and freeze them there — and those shapes still move. The wire format
// is already the contract with the Go side, so it is the contract here too: this
// returns the same NDJSON line the helper writes to stdout, and the phone
// forwards it unread. One format, one place it is defined (Core/Wire.swift), and
// the iOS side stays a transport rather than a second interpretation of a card.
//
// Everything else in ScanKit stays `internal` and reachable from the tests with
// @testable.

import CoreGraphics
import Foundation

/// HoardScan is the namespace the iPhone app links against.
public enum HoardScan {
    /// scanEventJSON reads one already-upright frame and returns the `scan`
    /// event for it, encoded exactly as the macOS helper would emit it.
    ///
    /// The image must already have its EXIF orientation baked into the pixels
    /// and its rotation applied — same contract readCard has always had, and for
    /// the same reason: applying an orientation here as well lands the title at
    /// the bottom and makes the ranking pick rules text instead. `rotation` is
    /// reported back on the event, not applied.
    ///
    /// Returns an empty Data only if encoding itself fails, which it does not.
    public static func scanEventJSON(_ image: CGImage, rotation: Int) -> Data {
        let scan = scanFrame(image)
        return (try? JSONEncoder().encode(Event.scan(scan, rotation: rotation))) ?? Data()
    }

    /// scanFileJSON reads an image file and returns its `scan` event, taking the
    /// byte-identical path `hoard-scan --image` takes.
    ///
    /// This exists rather than leaving callers to load their own CGImage because
    /// the loading *is* part of the pipeline. `--image` decodes through
    /// CGImageSource, bakes the EXIF orientation into the pixels with
    /// `uprighted`, then applies the rotation — and a caller reaching for
    /// UIImage instead skips the uprighting, silently feeds Vision a differently
    /// oriented frame, and produces differences that look like OCR drift and are
    /// not. Comparing a phone read against a Mac golden is only meaningful if
    /// both sides load identically, so both sides call this.
    ///
    /// Returns nil when the file cannot be decoded.
    public static func scanFileJSON(path: String, rotation: Int) -> Data? {
        guard let (cg, orientation) = cgImage(fromFile: path) else { return nil }
        let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: rotation)
        return scanEventJSON(forOCR, rotation: rotation)
    }

    /// capabilityReport is the `--probe` dump, verbatim, so the phone can print
    /// the same text the macOS helper does and the two can be read side by side.
    /// That comparison is the entire reason the probe exists.
    public static func capabilityReport() -> String { probeReport() }

    /// visionRevision is the algorithm generation both text passes are pinned
    /// to. Worth surfacing because the goldens are only comparable across two
    /// machines if this number matches on both — see docs/scanner-tuning.md.
    public static var visionRevision: Int { textRecognitionRevision }
}
