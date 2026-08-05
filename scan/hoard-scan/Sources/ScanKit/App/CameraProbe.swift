// What the camera will actually admit to, as opposed to what the documentation
// implies.
//
// The scanner's whole tuning ledger rests on one measured number — Continuity
// Camera stills are 1920x1080 — and on a list of controls believed to be
// iOS-only. Both are recorded in comments across App/ and in
// docs/scanner-tuning.md, and both were established by being surprised. This
// mode exists so the next person changing the capture path can re-establish
// them in one command instead of another afternoon.
//
// It is deliberately verbose and deliberately plain text: the output is meant to
// be pasted into the capability ledger in docs/scanner-tuning.md, next to the
// tuning ledger it explains.
//
// The interesting part is the session experiment at the bottom. activeFormat
// before startRunning is not the format that will be used, which is the trap
// CaptureController documents at length — pinning maxPhotoDimensions to the
// pre-session format once capped a whole session at 640x480. The probe reports
// the format at three moments, and then tries the one path that has never been
// tried: assigning activeFormat *after* the session is live.

import AVFoundation
import CoreMedia
import Foundation
import Vision

// MARK: - Device enumeration

/// Every camera the platform will name, not just the ones the scanner uses.
/// availableCameras() is deliberately Continuity-only; a probe that shared that
/// filter could not answer "is there something better attached".
private func allCameras() -> [AVCaptureDevice] {
    #if os(macOS)
        let types: [AVCaptureDevice.DeviceType] = [
            .continuityCamera, .deskViewCamera, .external, .builtInWideAngleCamera,
        ]
    #else
        let types: [AVCaptureDevice.DeviceType] = [
            .builtInWideAngleCamera, .builtInUltraWideCamera, .builtInTelephotoCamera,
            .builtInDualCamera, .builtInDualWideCamera, .builtInTripleCamera,
            .builtInLiDARDepthCamera,
        ]
    #endif
    return AVCaptureDevice.DiscoverySession(
        deviceTypes: types, mediaType: .video, position: .unspecified
    ).devices
}

// MARK: - Formatting helpers

private func dims(_ d: CMVideoDimensions) -> String { "\(d.width)x\(d.height)" }

private func dims(_ d: CMVideoDimensions?) -> String { d.map(dims) ?? "—" }

/// The largest still a format will produce, which is the number this whole
/// exercise is about.
func maxPhoto(_ format: AVCaptureDevice.Format) -> CMVideoDimensions? {
    format.supportedMaxPhotoDimensions
        .max { Int($0.width) * Int($0.height) < Int($1.width) * Int($1.height) }
}

/// The format whose stills carry the most pixels. Shared with the live capture
/// path, which raises the device onto it once the session is running — see
/// CaptureController.raiseToBestFormat for why that has to happen after
/// startRunning rather than before.
func bestPhotoFormat(_ device: AVCaptureDevice) -> AVCaptureDevice.Format? {
    func pixels(_ f: AVCaptureDevice.Format) -> Int {
        guard let m = maxPhoto(f) else { return 0 }
        return Int(m.width) * Int(m.height)
    }
    return device.formats.max { pixels($0) < pixels($1) }
}

private func yn(_ b: Bool) -> String { b ? "yes" : "no" }

/// Controls that exist on one platform and not the other are reported as
/// "n/a (iOS-only)" rather than omitted — the absence is the finding.
private let notHere = "n/a (iOS-only)"

private func rates(_ format: AVCaptureDevice.Format) -> String {
    format.videoSupportedFrameRateRanges
        .map { "\(Int($0.minFrameRate))–\(Int($0.maxFrameRate))" }
        .joined(separator: ",")
}

// MARK: - Static capability dump

private func describe(_ device: AVCaptureDevice) -> String {
    var out = ""
    out += "\n### \(device.localizedName)\n\n"
    out += "    deviceType   \(device.deviceType.rawValue)\n"
    out += "    modelID      \(device.modelID)\n"
    out += "    uniqueID     \(device.uniqueID)\n"
    out += "    manufacturer \(device.manufacturer)\n"
    out += "    connected    \(yn(device.isConnected))\n"

    out += "\n    formats (\(device.formats.count)):\n"
    for (i, f) in device.formats.enumerated() {
        let d = CMVideoFormatDescriptionGetDimensions(f.formatDescription)
        var line = "      [\(i)] video=\(dims(d)) photo=\(dims(maxPhoto(f))) fps=\(rates(f))"
        line += " centerStage=\(yn(f.isCenterStageSupported))"
        #if os(iOS)
            line += " maxZoom=\(String(format: "%.1f", f.videoMaxZoomFactor))"
            line += " iso=\(Int(f.minISO))–\(Int(f.maxISO))"
            line += " hdr=\(yn(f.isVideoHDRSupported))"
        #endif
        out += line + "\n"
    }

    out += "\n    focus:\n"
    out += "      continuousAutoFocus   \(yn(device.isFocusModeSupported(.continuousAutoFocus)))\n"
    out += "      locked                \(yn(device.isFocusModeSupported(.locked)))\n"
    out += "      pointOfInterest       \(yn(device.isFocusPointOfInterestSupported))\n"
    #if os(iOS)
        out += "      customLensPosition    \(yn(device.isLockingFocusWithCustomLensPositionSupported))\n"
        out += "      minimumFocusDistance  \(device.minimumFocusDistance) mm\n"
        out += "      rangeRestriction      yes (.near/.far)\n"
    #else
        out += "      customLensPosition    \(notHere)\n"
        out += "      minimumFocusDistance  \(notHere)\n"
        out += "      rangeRestriction      \(notHere)\n"
    #endif

    out += "\n    exposure:\n"
    out += "      continuousAutoExposure \(yn(device.isExposureModeSupported(.continuousAutoExposure)))\n"
    out += "      locked                 \(yn(device.isExposureModeSupported(.locked)))\n"
    #if os(iOS)
        out += "      custom (duration/ISO)  \(yn(device.isExposureModeSupported(.custom)))\n"
        out += "      targetBias range       \(device.minExposureTargetBias)–\(device.maxExposureTargetBias)\n"
    #else
        out += "      custom (duration/ISO)  \(notHere)\n"
        out += "      targetBias range       \(notHere)\n"
    #endif

    out += "\n    white balance:\n"
    out += "      locked        \(yn(device.isWhiteBalanceModeSupported(.locked)))\n"
    #if os(iOS)
        out += "      custom gains  \(yn(device.isLockingWhiteBalanceWithCustomDeviceGainsSupported))\n"
        out += "      maxGain       \(device.maxWhiteBalanceGain)\n"
    #else
        out += "      custom gains  \(notHere)\n"
    #endif

    out += "\n    zoom:\n"
    #if os(iOS)
        out += "      videoZoomFactor  \(device.videoZoomFactor)\n"
        out += "      switchOverFactors \(device.virtualDeviceSwitchOverVideoZoomFactors)\n"
        out += "      constituents      \(device.constituentDevices.map(\.localizedName))\n"
    #else
        out += "      \(notHere). Center Stage is the only framing lever\n"
    #endif

    out += "\n    light:\n"
    out += "      hasTorch      \(yn(device.hasTorch))\n"
    out += "      torchAvailable \(yn(device.isTorchAvailable))\n"
    out += "      hasFlash      \(yn(device.hasFlash))\n"

    return out
}

// MARK: - The session experiment

/// The three-moment activeFormat report, plus the untried post-startRunning
/// assignment. Everything here needs a live session, which on macOS needs a
/// pumping run loop before Continuity Camera will even appear — hence the
/// spinRunLoop calls, same as the live path.
private func sessionExperiment(on device: AVCaptureDevice) -> String {
    var out = "\n## Session experiment: \(device.localizedName)\n\n"

    guard let input = try? AVCaptureDeviceInput(device: device) else {
        return out + "    could not open the device for input\n"
    }
    let session = AVCaptureSession()
    let photoOutput = AVCapturePhotoOutput()
    guard session.canAddInput(input), session.canAddOutput(photoOutput) else {
        return out + "    could not configure a session with a photo output\n"
    }

    out += "    activeFormat before any configuration:\n"
    out += "      video=\(dims(CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)))"
    out += " photo=\(dims(maxPhoto(device.activeFormat)))\n"

    if session.canSetSessionPreset(.photo) {
        session.sessionPreset = .photo
        out += "    sessionPreset = .photo (accepted)\n"
    } else {
        out += "    sessionPreset = .photo REFUSED. This is new, and worth chasing\n"
    }
    session.addInput(input)
    session.addOutput(photoOutput)

    out += "    activeFormat after preset, before startRunning:\n"
    out += "      video=\(dims(CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)))"
    out += " photo=\(dims(maxPhoto(device.activeFormat)))\n"
    out += "      photoOutput.maxPhotoDimensions=\(dims(photoOutput.maxPhotoDimensions))\n"

    session.startRunning()
    // startRunning() returning is not the same as being able to take a picture;
    // the live path waits for the connection the same way.
    spinRunLoop(seconds: 5) { photoOutput.connection(with: .video)?.isActive == true }
    spinRunLoop(seconds: 0.75) { false }

    out += "    activeFormat after startRunning (the one that will actually be used):\n"
    out += "      video=\(dims(CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)))"
    out += " photo=\(dims(maxPhoto(device.activeFormat)))\n"
    out += "      photoOutput.maxPhotoDimensions=\(dims(photoOutput.maxPhotoDimensions))\n"

    // The untried path. CaptureController's comment records that setting
    // maxPhotoDimensions *before* the session runs backfires, because
    // activeFormat is still the low-res default at that point. Nobody has tried
    // raising activeFormat itself once the session is live.
    let best = bestPhotoFormat(device)
    if let best, best != device.activeFormat {
        out += "\n    experiment: assigning the highest-photo format post-startRunning\n"
        out += "      candidate: video="
        out += "\(dims(CMVideoFormatDescriptionGetDimensions(best.formatDescription)))"
        out += " photo=\(dims(maxPhoto(best)))\n"
        do {
            try device.lockForConfiguration()
            device.activeFormat = best
            device.unlockForConfiguration()
            spinRunLoop(seconds: 1.0) { false }
            out += "      accepted. activeFormat now: video="
            out += "\(dims(CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)))"
            out += " photo=\(dims(maxPhoto(device.activeFormat)))\n"
            if let m = maxPhoto(device.activeFormat) {
                photoOutput.maxPhotoDimensions = m
                out += "      photoOutput.maxPhotoDimensions set to \(dims(m)), reads back "
                out += "\(dims(photoOutput.maxPhotoDimensions))\n"
            }
        } catch {
            out += "      REFUSED: \(error.localizedDescription)\n"
        }
    } else {
        out += "\n    experiment skipped: the active format already has the largest still\n"
    }

    out += "\n    photo output:\n"
    #if os(iOS)
        out += "      zeroShutterLag       \(yn(photoOutput.isZeroShutterLagSupported))\n"
        out += "      responsiveCapture    \(yn(photoOutput.isResponsiveCaptureSupported))\n"
        out += "      fastCapturePriority  \(yn(photoOutput.isFastCapturePrioritizationSupported))\n"
        out += "      maxPhotoQualityPrio  \(photoOutput.maxPhotoQualityPrioritization.rawValue)\n"
    #else
        out += "      zeroShutterLag       \(notHere)\n"
        out += "      responsiveCapture    \(notHere)\n"
        out += "      fastCapturePriority  \(notHere)\n"
    #endif

    session.stopRunning()
    return out
}

// MARK: - Entry point

/// probeReport is the whole dump as one string, so the iOS app can render the
/// identical text on screen that the macOS helper prints to stdout. Comparing
/// the two side by side is the entire point of the exercise.
func probeReport(deviceID: String? = nil) -> String {
    var out = "# Camera capability probe\n"
    #if os(macOS)
        out += "\nplatform: macOS \(ProcessInfo.processInfo.operatingSystemVersionString)\n"
    #else
        out += "\nplatform: iOS \(ProcessInfo.processInfo.operatingSystemVersionString)\n"
    #endif

    // Vision's revision is the other half of "what will this machine do to my
    // pixels". ReadCard pins it; this is where the pin is checked against what
    // the OS actually offers, and it is the number to compare across platforms
    // before trusting a golden anywhere.
    out += "\nVision text recognition:\n"
    out += "    supportedRevisions \(VNRecognizeTextRequest.supportedRevisions.sorted())\n"
    out += "    currentRevision    \(VNRecognizeTextRequest.currentRevision)\n"
    out += "    defaultRevision    \(VNRecognizeTextRequest.defaultRevision)\n"
    out += "    pinned by ReadCard \(textRecognitionRevision)\n"

    let cameras = allCameras()
    if cameras.isEmpty {
        #if os(macOS)
            return out + "\nno cameras found.\n\n" + noPhoneMessage + "\n"
        #else
            return out + "\nno cameras found. This build expects a physical device;"
                + " the simulator has none.\n"
        #endif
    }
    out += "cameras found: \(cameras.count)\n"
    for device in cameras { out += describe(device) }

    // The session experiment runs against one device: the requested one, else
    // the first Continuity Camera, else whatever is first.
    let subject = deviceID.flatMap { id in cameras.first { $0.uniqueID == id } }
        ?? cameras.first { $0.deviceType == .continuityCamera }
        ?? cameras[0]
    out += sessionExperiment(on: subject)
    return out
}
