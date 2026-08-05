// hoard-scan — capture a Magic card image and OCR its title.
//
// Modes:
//   hoard-scan --list-devices   Print the available cameras as JSON, exit.
//   hoard-scan --image <path>   Headless: OCR an existing image file, print JSON, exit.
//   hoard-scan [--device <id>] [--rotate <deg>]
//                               Live session: open a camera preview window and keep
//                               it open, emitting one JSON event per line on stdout
//                               and reading commands (capture / rotate-left /
//                               rotate-right / frame-on / frame-off / torch-on /
//                               torch-off / effects / result {json} / quit) as
//                               lines on stdin. Space, ←/→, Z, T, V and Esc do the
//                               same things in the window itself.
//   hoard-scan --hud-demo       Live window with no camera, for eyeballing the
//                               price HUD: pipe `result {...}` lines on stdin.
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

/// runCLI is the helper's whole entry point, called by the executable target's
/// main.swift. It lives in the library rather than in top-level code so the
/// tests can link everything here; the executable is a three-line shell.
///
/// Every mode either exits or blocks in `app.run()`, so this never returns
/// normally — which is also what keeps `controller` and `appDelegate` alive,
/// NSApplication not retaining its delegate.
public func runCLI() {
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

    // --border-probe reads one image and reports everything the border reader saw,
    // verdict or not. It exists to fit the constants in CardLayout and BorderGate
    // against scan/corpus, where the card fills the frame and the card rect is
    // therefore known exactly — which is the one thing that corpus can say about
    // the border, and the reason it cannot say anything about the *crop*.
    if let i = args.firstIndex(of: "--border-probe") {
        guard i + 1 < args.count else { fail("--border-probe requires a file path") }
        let path = args[i + 1]
        guard let (cg, orientation) = cgImage(fromFile: path) else {
            fail("could not read image: \(path)")
        }
        let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: requestedRotation)
        let read = readCard(forOCR)
        var reading = readBorder(forOCR, read)
        reading.footerText = reading.footerText.isEmpty ? "" : reading.footerText
        emit(reading)
        exit(reading.color == nil ? 3 : 0)
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
        let scan = scanFrame(forOCR)
        emit(Event.scan(scan, rotation: requestedRotation))
        exit(Event.readAnything(scan) ? 0 : 3)
    }

    // Live mode: request camera access, then run the AppKit event loop.
    var requestedDevice: String?
    if let i = args.firstIndex(of: "--device"), i + 1 < args.count {
        requestedDevice = args[i + 1]
    }
    let app = NSApplication.shared
    app.setActivationPolicy(.regular)
    let controller = CaptureController(deviceID: requestedDevice, rotation: requestedRotation,
                                       auto: args.contains("--auto"))

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

    if args.contains("--hud-demo") {
        // No camera, no permission prompt: the demo exists to see the HUD.
        DispatchQueue.main.async { controller.startDemo() }
    } else {
        AVCaptureDevice.requestAccess(for: .video) { granted in
            DispatchQueue.main.async {
                if !granted { fail("camera access denied — grant it in System Settings › Privacy › Camera") }
                controller.start()
            }
        }
    }
    app.run()
}
