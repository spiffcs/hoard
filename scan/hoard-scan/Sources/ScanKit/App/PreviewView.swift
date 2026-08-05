// macOS only. This is the camera, window and HUD half of ScanKit; the read
// pipeline under Core/ is what compiles for iOS. See Package.swift.
#if os(macOS)

import AVFoundation
import AppKit

/// PreviewView hosts the camera preview layer and forwards key presses.
final class PreviewView: NSView {
    enum Key {
        case space, escape, rotateLeft, rotateRight, framingToggle, torchToggle,
             effectsPanel, autoToggle
    }
    var previewLayer: AVCaptureVideoPreviewLayer?
    /// The auto-trigger's cue, kept sized to the view by layout().
    var outlines: OutlineOverlay?
    /// The price HUD, kept sized to the view like the outline.
    var hud: PriceHUD?
    var onKey: ((Key) -> Void)?
    override var acceptsFirstResponder: Bool { true }
    override func keyDown(with event: NSEvent) {
        switch event.keyCode {
        case 49: onKey?(.space)        // space
        case 53: onKey?(.escape)       // esc
        case 123: onKey?(.rotateLeft)  // left arrow
        case 124: onKey?(.rotateRight) // right arrow
        case 6: onKey?(.framingToggle) // z
        case 17: onKey?(.torchToggle)  // t
        case 9: onKey?(.effectsPanel)  // v
        case 0: onKey?(.autoToggle)    // a
        default: super.keyDown(with: event)
        }
    }

    override func layout() {
        super.layout()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        outlines?.layout(bounds: bounds)
        hud?.layout(bounds: bounds)
        CATransaction.commit()
    }

    /// A window dragged to a different-density display re-rasterizes the HUD
    /// text, or it renders blurry there.
    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        if let scale = window?.backingScaleFactor { hud?.setScale(scale) }
    }
}

#endif
