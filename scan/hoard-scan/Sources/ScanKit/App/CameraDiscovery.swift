// Which cameras the helper will use, and the waiting Continuity Camera needs
// before it will admit to existing.

// macOS only. This is the camera, window and HUD half of ScanKit; the read
// pipeline under Core/ is what compiles for iOS. See Package.swift.
#if os(macOS)

import AVFoundation
import Foundation

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
    // Desk View (.deskViewCamera) is deliberately excluded too: the top-down
    // dewarped feed reads nicely but sits well below the sensor's full photo
    // resolution, so the collector number — already at the edge of what
    // Vision resolves — becomes a coin flip and the review queue fills up.
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

/// hasContinuityCamera reports whether an iPhone has shown up yet.
func hasContinuityCamera() -> Bool { !availableCameras().isEmpty }

/// kindLabel is a short human tag shown next to a camera's name in the picker.
/// Everything discoverable is a Continuity Camera, so this is only interesting
/// when someone has two phones paired.
func kindLabel(_ d: AVCaptureDevice) -> String {
    d.deviceType == .continuityCamera ? "Continuity Camera" : "camera"
}

/// deviceLabel is the device's name with macOS's trailing " Camera" removed.
///
/// The kind now says what it is, and "Billionaires are Parasites Camera ·
/// Continuity Camera" reads like a stutter. Only the suffix goes; a phone
/// actually named "… Camera" keeps its name plus one dropped word, which is a
/// better trade than the stutter on every row.
func deviceLabel(_ d: AVCaptureDevice) -> String {
    let name = d.localizedName
    guard d.deviceType == .continuityCamera, name.hasSuffix(" Camera") else { return name }
    return String(name.dropLast(" Camera".count))
}

#endif
